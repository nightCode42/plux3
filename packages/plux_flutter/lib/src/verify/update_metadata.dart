// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Verification of the update metadata (SEC-050, SEC-051, SEC-056,
/// SEC-122, ADR-0004, ADR-0054): the `root`, `timestamp` and `snapshot`
/// documents and the chain they form with the manifest, the `targets` role.
///
/// The chain is `timestamp`, which names the newest snapshot and its hash;
/// `snapshot`, which pins the version of the targets; and the manifest,
/// which carries that version. Each link must meet its role's signature
/// threshold under the keys of the trusted root, be unexpired, and not go
/// back in version. A new root is accepted only when the previous root's
/// threshold and its own both signed it.
///
/// Expiry is judged against the clock the caller passes, which is the
/// device clock at sync time: a running app is never stopped because
/// metadata expired (B11). The rules are those of the server's verifier
/// (`backend/internal/updatemeta`), which the tests share vectors with.
/// Signatures are checked before the fields they cover are used (SEC-052).
/// Everything here runs on the sync isolate; nothing touches the UI one.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart' as crypto;
import 'package:cryptography/cryptography.dart'
    show KeyPairType, Signature, SimplePublicKey;
import 'package:cryptography/dart.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/security/p256.dart';
import 'package:plux_flutter/src/security/pins.dart';
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// The roles of the update metadata (SEC-050).
const List<String> metadataRoles = ['root', 'targets', 'snapshot', 'timestamp'];

/// The signature algorithm names of SEC-122, as the schema spells them.
const String algEd25519 = 'Ed25519';

/// ECDSA over P-256 with SHA-256; signatures are the 64 bytes `r || s`.
const String algES256 = 'ES256';

/// Reserved for the post-quantum transition: it can be named in a root, and
/// nothing here verifies with it.
const String algMlDsa65 = 'ML-DSA-65';

/// The environment type of a production key (SEC-056).
const String environmentProduction = 'production';

/// The environment type of a development key (SEC-056).
const String environmentDevelopment = 'development';

/// The largest metadata document accepted, in bytes: the limit
/// `updateMetadata.bytes` of the registry (LIM-001).
final int metadataMaxBytes = PluxLimit.updateMetadataBytes.defaultValue;

/// The most hosts a root pins, and the fewest and most pins of a host
/// (`schema/update/root.schema.json`).
const int maxPinHosts = 16, maxPinsPerHost = 8;

/// The deepest nesting a metadata document has (the root's).
const int _maxDepth = 9;

/// The identifiers of the ways metadata fails, carried in the `fault`
/// detail of the [PluxException].
abstract final class MetadataFault {
  /// A malformed document, or one that breaks its schema.
  static const format = 'format';

  /// Too few valid signatures from the role's keys.
  static const threshold = 'threshold';

  /// A role past its expiry.
  static const expired = 'expired';

  /// A version below the one already trusted, or not the successor a root
  /// update must be.
  static const rollback = 'rollback';

  /// A hash or version the link above did not name.
  static const mismatch = 'mismatch';

  /// Production metadata signed by a key that is not a production key.
  static const developmentKey = 'development-key';
}

PluxException _fail(String fault, String message) => PluxException(
  switch (fault) {
    MetadataFault.rollback => PluxErrorCode.rollbackRejected,
    MetadataFault.developmentKey => PluxErrorCode.developmentKeyInProduction,
    _ => PluxErrorCode.updateMetadataInvalid,
  },
  message,
  details: {'fault': fault},
);

/// The fault of a [PluxException] raised by this library, or null.
String? metadataFault(PluxException e) => e.details['fault'];

/// The identifier the schema spells [name] with: the manifest's historical
/// `ed25519` and the `ecdsa-p256-sha256` form read as [algEd25519] and
/// [algES256]. An unknown name is returned unchanged.
String canonicalAlgorithm(String name) => switch (name.toLowerCase()) {
  'ed25519' => algEd25519,
  'es256' || 'ecdsa-p256-sha256' => algES256,
  'ml-dsa-65' => algMlDsa65,
  _ => name,
};

/// Whether this runtime verifies with [name].
bool isSupportedAlgorithm(String name) {
  final a = canonicalAlgorithm(name);
  return a == algEd25519 || a == algES256;
}

/// The identifier of a public key: the first sixteen bytes of its SHA-256
/// in lower-case hexadecimal.
String metadataKeyId(List<int> publicKey) => hexEncode(
  Uint8List.fromList(crypto.sha256.convert(publicKey).bytes.sublist(0, 16)),
);

/// A public key a root lists.
final class MetadataKey {
  /// Creates a key.
  const MetadataKey(this.algorithm, this.publicKey);

  /// The algorithm name as the root spells it.
  final String algorithm;

  /// 32 raw bytes for Ed25519, the 65-byte uncompressed point for ES256.
  final Uint8List publicKey;
}

/// The keys that may sign a role and how many must.
final class RoleKeys {
  /// Creates the keys of a role.
  const RoleKeys(this.keyIds, this.threshold);

  /// The keys, by identifier.
  final List<String> keyIds;

  /// How many distinct keys must have signed.
  final int threshold;
}

/// The signed part of a root: the keys and thresholds of every role.
final class RootMetadata {
  /// Creates a root.
  const RootMetadata({
    required this.version,
    required this.expires,
    required this.keys,
    required this.roles,
    this.pins = const {},
  });

  /// The root's version; the first is 1.
  final int version;

  /// When the root stops being valid; null for the root made from the
  /// keys an app embeds, which carries no expiry.
  final DateTime? expires;

  /// Every key, by identifier.
  final Map<String, MetadataKey> keys;

  /// Each role's keys, by role name.
  final Map<String, RoleKeys> roles;

  /// The certificate pins of the servers the app talks to, per host name:
  /// at least [minPins] distinct ones each (SEC-041). Empty when the root
  /// pins nothing, which leaves the pins the device holds as they are.
  final Map<String, List<String>> pins;

  /// The keys of [role].
  RoleKeys role(String role) => roles[role] ?? const RoleKeys([], 1);
}

/// The signed part of a timestamp: the newest snapshot and its hash.
final class TimestampMetadata {
  const TimestampMetadata._(
    this.version,
    this.expires,
    this.snapshotVersion,
    this.snapshotSha256,
  );

  /// The timestamp's version.
  final int version;

  /// When it stops being valid.
  final DateTime expires;

  /// The version of the snapshot it names.
  final int snapshotVersion;

  /// The SHA-256 of that snapshot's file, in lower-case hexadecimal.
  final String snapshotSha256;
}

/// The signed part of a snapshot: the pinned targets version.
final class SnapshotMetadata {
  const SnapshotMetadata._(this.version, this.expires, this.targetsVersion);

  /// The snapshot's version.
  final int version;

  /// When it stops being valid.
  final DateTime expires;

  /// The version of the manifest it pins.
  final int targetsVersion;
}

/// What a verifier reads of a manifest, the targets role.
final class TargetsMetadata {
  const TargetsMetadata._(this.version, this.expires);

  /// The manifest's version, which the snapshot pins.
  final int version;

  /// When the manifest stops being valid.
  final DateTime expires;
}

/// The highest version trusted so far per role; a document below its
/// role's floor is refused (anti-rollback).
final class MetadataFloor {
  /// Creates a floor.
  const MetadataFloor({
    this.timestamp = 0,
    this.snapshot = 0,
    this.targets = 0,
  });

  /// The timestamp's.
  final int timestamp;

  /// The snapshot's.
  final int snapshot;

  /// The targets', that is the manifest's.
  final int targets;

  /// The higher of each role's version here and in [other].
  MetadataFloor raisedTo(MetadataFloor other) => MetadataFloor(
    timestamp: other.timestamp > timestamp ? other.timestamp : timestamp,
    snapshot: other.snapshot > snapshot ? other.snapshot : snapshot,
    targets: other.targets > targets ? other.targets : targets,
  );
}

/// A metadata file: the canonical bytes its signatures cover, the fields
/// of that signed part, and the signatures.
final class MetadataDocument {
  const MetadataDocument._(this.signed, this._fields, this.signatures);

  /// The canonical JSON of the signed part.
  final Uint8List signed;
  final Map<String, Object?> _fields;

  /// The signatures over [signed].
  final List<DocumentSignature> signatures;
}

final _keyIdPattern = RegExp(r'^[0-9a-f]{32}$');
final _base64Pattern = RegExp(r'^[A-Za-z0-9+/]+={0,2}$');
final _specPattern = RegExp(
  r'^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$',
);
final _hostPattern = RegExp(r'^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$');
final _sha256Pattern = RegExp(r'^[0-9a-f]{64}$');
final _algNamePattern = RegExp(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$');
final _rfc3339 = RegExp(
  r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$',
);

PluxException _format(String message) => _fail(MetadataFault.format, message);

/// Reads a metadata file. It must be UTF-8 JSON in RFC 8785 canonical form,
/// as the server writes it, so that the bytes a timestamp hashes and the
/// bytes a signature covers cannot differ from what is read, and its
/// objects must have exactly the members the schemas allow.
MetadataDocument parseMetadataDocument(Uint8List raw) {
  if (raw.isEmpty || raw.length > metadataMaxBytes) {
    throw _format('a metadata file is empty or too large');
  }
  final Object? json;
  final String text;
  try {
    text = utf8.decode(raw);
    json = jsonDecode(text);
  } on FormatException {
    throw _format('a metadata file is not JSON');
  }
  try {
    if (canonicalJson(json) != text) {
      throw _format('a metadata file is not in RFC 8785 canonical form');
    }
  } on ArgumentError {
    throw _format('a metadata file is not in RFC 8785 canonical form');
  }
  final file = _object(json, 'the file', const {'signed', 'signatures'});
  final signed = _map(file['signed'], 'signed');
  _checkDepth(signed, 0);
  final signatures = file['signatures'];
  if (signatures is! List<Object?>) throw _format('signatures is not a list');
  return MetadataDocument._(
    Uint8List.fromList(utf8.encode(canonicalJson(signed))),
    signed,
    [for (final s in signatures) _signature(s)],
  );
}

void _checkDepth(Object? v, int depth) {
  if (depth > _maxDepth) throw _format('a metadata file is nested too deeply');
  if (v is Map<String, Object?>) {
    for (final c in v.values) {
      _checkDepth(c, depth + 1);
    }
  } else if (v is List<Object?>) {
    for (final c in v) {
      _checkDepth(c, depth + 1);
    }
  }
}

DocumentSignature _signature(Object? v) {
  final s = _object(v, 'a signature', const {'keyid', 'alg', 'sig'});
  final keyId = s['keyid'];
  final alg = s['alg'];
  final sig = s['sig'];
  if (keyId is! String ||
      !_keyIdPattern.hasMatch(keyId) ||
      alg is! String ||
      alg.isEmpty ||
      sig is! String ||
      _base64(sig) == null) {
    throw _format('a signature is malformed');
  }
  return DocumentSignature(
    keyId: keyId,
    algorithm: alg,
    signature: _base64(sig)!,
  );
}

/// The bytes of standard, padded base64, or null.
Uint8List? _base64(String s) {
  if (!_base64Pattern.hasMatch(s) || s.length % 4 != 0) return null;
  try {
    return base64.decode(s);
  } on FormatException {
    return null;
  }
}

Map<String, Object?> _map(Object? v, String what) {
  if (v is! Map<String, Object?>) throw _format('$what is not an object');
  return v;
}

/// [v] as an object whose members are exactly [names]: the schemas close
/// every object and require every member.
Map<String, Object?> _object(Object? v, String what, Set<String> names) {
  final m = _map(v, what);
  if (m.length != names.length || !names.every(m.containsKey)) {
    throw _format('$what does not have exactly ${names.join(', ')}');
  }
  return m;
}

int _version(Object? v, String what) {
  if (v is! int || v < 1) throw _format('$what is not a version');
  return v;
}

DateTime _time(Object? v, String what) {
  final m = v is String ? _rfc3339.firstMatch(v) : null;
  if (m == null) throw _format('$what is not an RFC 3339 time');
  final y = int.parse(m[1]!);
  final mo = int.parse(m[2]!);
  final d = int.parse(m[3]!);
  final calendar = DateTime.utc(y, mo, d);
  if (calendar.month != mo ||
      calendar.day != d ||
      int.parse(m[4]!) > 23 ||
      int.parse(m[5]!) > 59 ||
      int.parse(m[6]!) > 60) {
    throw _format('$what is not an RFC 3339 time');
  }
  try {
    return DateTime.parse(v! as String);
  } on FormatException {
    throw _format('$what is not an RFC 3339 time');
  }
}

/// The properties every signed part carries.
({int version, DateTime expires}) _header(
  Map<String, Object?> signed,
  String role,
) {
  if (signed['_type'] != role) {
    throw _format('_type is ${signed['_type']}, want $role');
  }
  final spec = signed['specVersion'];
  if (spec is! String || !_specPattern.hasMatch(spec)) {
    throw _format('specVersion is malformed');
  }
  return (
    version: _version(signed['version'], 'version'),
    expires: _time(signed['expires'], 'expires'),
  );
}

/// Reads the signed part of a root and checks it against the schema and
/// the rules of the server: every listed key hashes to its identifier, and
/// each role names listed, distinct keys, enough of them usable to meet its
/// threshold.
RootMetadata parseRoot(MetadataDocument document) {
  final signed = _object(document._fields, 'the root', {
    '_type',
    'version',
    'expires',
    'specVersion',
    'keys',
    'roles',
    if (document._fields.containsKey('pins')) 'pins',
  });
  final h = _header(signed, 'root');
  final keys = <String, MetadataKey>{};
  for (final MapEntry(:key, :value) in _map(signed['keys'], 'keys').entries) {
    final k = _object(value, 'a key', const {'alg', 'public'});
    final alg = k['alg'];
    final pub = k['public'] is String ? _base64(k['public']! as String) : null;
    if (!_keyIdPattern.hasMatch(key) ||
        alg is! String ||
        !_algNamePattern.hasMatch(alg) ||
        pub == null ||
        pub.isEmpty) {
      throw _format('key $key is malformed');
    }
    if (isSupportedAlgorithm(alg) && metadataKeyId(pub) != key) {
      throw _format('key $key is not the hash of its public key');
    }
    keys[key] = MetadataKey(alg, pub);
  }
  final rolesJson = _object(signed['roles'], 'roles', metadataRoles.toSet());
  final roles = <String, RoleKeys>{};
  for (final role in metadataRoles) {
    final r = _object(rolesJson[role], 'role $role', const {
      'keyids',
      'threshold',
    });
    final ids = r['keyids'];
    final threshold = r['threshold'];
    if (ids is! List<Object?> || ids.any((i) => i is! String)) {
      throw _format('role $role has no list of key identifiers');
    }
    if (threshold is! int) throw _format('role $role has no threshold');
    final named = ids.cast<String>();
    var usable = 0;
    final seen = <String>{};
    for (final id in named) {
      final k = keys[id];
      if (k == null) {
        throw _format('role $role names key $id, which the root does not list');
      }
      if (!seen.add(id)) throw _format('role $role names key $id twice');
      if (isSupportedAlgorithm(k.algorithm)) usable++;
    }
    if (threshold < 1 || threshold > usable) {
      throw _format(
        'role $role has threshold $threshold with $usable usable keys',
      );
    }
    roles[role] = RoleKeys(List.unmodifiable(named), threshold);
  }
  return RootMetadata(
    version: h.version,
    expires: h.expires,
    keys: Map.unmodifiable(keys),
    roles: Map.unmodifiable(roles),
    pins: _pins(signed['pins']),
  );
}

/// The pins a root carries: per host name between [minPins] and
/// [maxPinsPerHost] distinct, canonical SPKI SHA-256 pins (SEC-041).
Map<String, List<String>> _pins(Object? v) {
  if (v == null) return const {};
  final hosts = _map(v, 'pins');
  if (hosts.length > maxPinHosts) throw _format('the root pins too many hosts');
  final out = <String, List<String>>{};
  for (final MapEntry(:key, :value) in hosts.entries) {
    if (key.length > 253 || !_hostPattern.hasMatch(key)) {
      throw _format('$key is not a host name to pin');
    }
    if (value is! List<Object?> ||
        value.length < minPins ||
        value.length > maxPinsPerHost ||
        value.any((p) => p is! String || !isWellFormedPin(p))) {
      throw _format('host $key needs $minPins to $maxPinsPerHost pins');
    }
    final list = value.cast<String>();
    if (list.toSet().length != list.length) {
      throw _format('host $key repeats a pin');
    }
    out[key] = List.unmodifiable(list);
  }
  return Map.unmodifiable(out);
}

/// Reads the signed part of a timestamp.
TimestampMetadata parseTimestamp(MetadataDocument document) {
  final signed = _object(document._fields, 'the timestamp', const {
    '_type',
    'version',
    'expires',
    'specVersion',
    'meta',
  });
  final h = _header(signed, 'timestamp');
  final meta = _object(signed['meta'], 'meta', const {'snapshot.json'});
  final ref = _object(meta['snapshot.json'], 'the snapshot reference', const {
    'version',
    'sha256',
  });
  final sha = ref['sha256'];
  if (sha is! String || !_sha256Pattern.hasMatch(sha)) {
    throw _format('the snapshot reference is malformed');
  }
  return TimestampMetadata._(
    h.version,
    h.expires,
    _version(ref['version'], 'the snapshot version'),
    sha,
  );
}

/// Reads the signed part of a snapshot.
SnapshotMetadata parseSnapshot(MetadataDocument document) {
  final signed = _object(document._fields, 'the snapshot', const {
    '_type',
    'version',
    'expires',
    'specVersion',
    'meta',
  });
  final h = _header(signed, 'snapshot');
  final meta = _object(signed['meta'], 'meta', const {'targets.json'});
  final ref = _object(meta['targets.json'], 'the targets reference', const {
    'version',
  });
  return SnapshotMetadata._(
    h.version,
    h.expires,
    _version(ref['version'], 'the targets version'),
  );
}

/// Reads the targets fields of a signed manifest: its role, its version and
/// its expiry. The manifest has many other fields, which are not this
/// library's business.
TargetsMetadata parseTargets(Uint8List signed) {
  final Object? json;
  try {
    json = jsonDecode(utf8.decode(signed));
  } on FormatException {
    throw _format('the manifest is not JSON');
  }
  final m = _map(json, 'the manifest');
  if (m['role'] != 'targets') {
    throw _format('the manifest\'s role is ${m['role']}');
  }
  return TargetsMetadata._(
    _version(m['version'], 'the manifest\'s version'),
    _time(m['expires'], 'expires'),
  );
}

/// Whether [signature] over [message] verifies under [key]. An Ed25519 key
/// is 32 bytes and its signature 64; an ES256 key the 65-byte uncompressed
/// point and its signature the 64 bytes `r || s`.
Future<bool> _verifies(
  String algorithm,
  Uint8List key,
  Uint8List message,
  Uint8List signature,
) async {
  switch (canonicalAlgorithm(algorithm)) {
    case algEd25519:
      if (key.length != 32 || signature.length != 64) return false;
      return DartEd25519(sha512: const DartSha512()).verify(
        message,
        signature: Signature(
          signature,
          publicKey: SimplePublicKey(key, type: KeyPairType.ed25519),
        ),
      );
    case algES256:
      return p256Verify(key, message, signature);
  }
  return false;
}

/// The keys of a role that signed [message] validly, each counted once;
/// fails unless they reach the threshold. A signature by a key outside the
/// role, with an algorithm other than its key's, or with one this runtime
/// does not know, counts for nothing.
Future<List<String>> _validSigners(
  Uint8List message,
  List<DocumentSignature> signatures,
  Map<String, MetadataKey> listed,
  RoleKeys role,
) async {
  final signers = <String>[];
  for (final s in signatures) {
    final k = listed[s.keyId];
    if (k == null ||
        !role.keyIds.contains(s.keyId) ||
        signers.contains(s.keyId) ||
        canonicalAlgorithm(s.algorithm) != canonicalAlgorithm(k.algorithm) ||
        !isSupportedAlgorithm(s.algorithm)) {
      continue;
    }
    if (await _verifies(k.algorithm, k.publicKey, message, s.signature)) {
      signers.add(s.keyId);
    }
  }
  if (signers.length < role.threshold) {
    throw _fail(
      MetadataFault.threshold,
      '${signers.length} of the ${role.threshold} required signatures',
    );
  }
  return signers..sort();
}

/// The root made from the public keys an app embeds (`keys.json`), the
/// trust anchor when no root document is embedded (SEC-051): version 1,
/// without an expiry, each role holding the keys listed for it. The online
/// roles need one signature, as the server signs them with one key; the
/// root role needs a majority of its keys.
RootMetadata rootFromKeys(Iterable<TrustedKey> keys) {
  final listed = <String, MetadataKey>{};
  final byRole = <String, List<String>>{for (final r in metadataRoles) r: []};
  for (final k in keys) {
    final ids = byRole[k.role];
    if (ids == null) continue;
    listed[k.keyId] = MetadataKey(k.algorithm, k.publicKey);
    if (!ids.contains(k.keyId)) ids.add(k.keyId);
  }
  return RootMetadata(
    version: 1,
    expires: null,
    keys: listed,
    roles: {
      for (final r in metadataRoles)
        r: RoleKeys(
          List.unmodifiable(byRole[r]!),
          r == 'root' ? byRole[r]!.length ~/ 2 + 1 : 1,
        ),
    },
  );
}

/// Reads a root file kept by this runtime after it verified it: well formed
/// and signed by its own threshold.
Future<RootMetadata> loadRoot(Uint8List raw) async {
  final d = parseMetadataDocument(raw);
  final r = parseRoot(d);
  await _validSigners(d.signed, d.signatures, r.keys, r.role('root'));
  return r;
}

/// Verifies a root that replaces [previous]: the next version, signed by
/// the previous root's threshold and by its own, so that neither a stolen
/// old key nor a freshly made one can rotate alone (SEC-051).
Future<RootMetadata> nextRoot(RootMetadata previous, Uint8List raw) async {
  final d = parseMetadataDocument(raw);
  final r = parseRoot(d);
  if (r.version != previous.version + 1) {
    throw _fail(
      MetadataFault.rollback,
      'root version ${r.version} follows ${previous.version}',
    );
  }
  await _validSigners(
    d.signed,
    d.signatures,
    previous.keys,
    previous.role('root'),
  );
  await _validSigners(d.signed, d.signatures, r.keys, r.role('root'));
  return r;
}

/// Follows a chain of root files from the trusted root [trusted], in
/// order, and returns the last. Intermediate roots may have expired, as the
/// device was offline while they were current; the last must not have.
Future<RootMetadata> rootChain(
  RootMetadata trusted,
  List<Uint8List> chain,
  DateTime now,
) async {
  var current = trusted;
  for (final raw in chain) {
    current = await nextRoot(current, raw);
  }
  checkRootUnexpired(current, now);
  return current;
}

/// Fails when [root] has expired at [now].
void checkRootUnexpired(RootMetadata root, DateTime now) {
  final expires = root.expires;
  if (expires != null && !now.isBefore(expires)) {
    throw _fail(
      MetadataFault.expired,
      'root ${root.version} expired at ${expires.toIso8601String()}',
    );
  }
}

/// Checks the metadata chain against a trusted root (SEC-050).
final class MetadataVerifier {
  /// Creates a verifier at the time [now], the device clock at sync.
  const MetadataVerifier({
    required this.root,
    required this.now,
    this.production = false,
    this.keyEnvironments = const {},
    this.floor = const MetadataFloor(),
  });

  /// The trusted root.
  final RootMetadata root;

  /// The verification time.
  final DateTime now;

  /// Makes the verifier refuse metadata signed by a key not known to be a
  /// production key (SEC-056).
  final bool production;

  /// Each key's environment type, `production` or `development`, as
  /// `GetRootKeys` reports it.
  final Map<String, String> keyEnvironments;

  /// The versions already trusted.
  final MetadataFloor floor;

  /// Verifies a timestamp file: its signatures meet the root's timestamp
  /// threshold, it has not expired and it is not older than one already
  /// trusted.
  Future<TimestampMetadata> timestamp(Uint8List raw) async {
    final d = parseMetadataDocument(raw);
    await _signedBy('timestamp', d.signed, d.signatures);
    final t = parseTimestamp(d);
    _checkLife('timestamp', t.expires, t.version, floor.timestamp);
    return t;
  }

  /// Verifies a snapshot file against the timestamp [ts] that names it:
  /// the hash and version it announced, then the same checks as any role.
  Future<SnapshotMetadata> snapshot(Uint8List raw, TimestampMetadata ts) async {
    if (hexEncode(Uint8List.fromList(crypto.sha256.convert(raw).bytes)) !=
        ts.snapshotSha256) {
      throw _fail(
        MetadataFault.mismatch,
        'the snapshot is not the one the timestamp hashes',
      );
    }
    final d = parseMetadataDocument(raw);
    await _signedBy('snapshot', d.signed, d.signatures);
    final s = parseSnapshot(d);
    if (s.version != ts.snapshotVersion) {
      throw _fail(
        MetadataFault.mismatch,
        'snapshot version ${s.version}, the timestamp names '
        '${ts.snapshotVersion}',
      );
    }
    _checkLife('snapshot', s.expires, s.version, floor.snapshot);
    return s;
  }

  /// Verifies a manifest, the targets role, against the snapshot [snap]
  /// that pins its version. [signed] is the canonical signed part and
  /// [signatures] are the manifest's own.
  Future<TargetsMetadata> targets(
    Uint8List signed,
    List<DocumentSignature> signatures,
    SnapshotMetadata snap,
  ) async {
    if (signed.isEmpty || signed.length > metadataMaxBytes) {
      throw _format('the manifest is empty or too large');
    }
    await _signedBy('targets', signed, signatures);
    final t = parseTargets(signed);
    if (t.version != snap.targetsVersion) {
      throw _fail(
        MetadataFault.mismatch,
        'manifest version ${t.version}, the snapshot pins '
        '${snap.targetsVersion}',
      );
    }
    _checkLife('targets', t.expires, t.version, floor.targets);
    return t;
  }

  /// The signature and environment checks every role shares: the
  /// threshold under the root's keys for [role], and in production only
  /// production keys.
  Future<void> _signedBy(
    String role,
    Uint8List signed,
    List<DocumentSignature> signatures,
  ) async {
    final List<String> signers;
    try {
      signers = await _validSigners(
        signed,
        signatures,
        root.keys,
        root.role(role),
      );
    } on PluxException catch (e) {
      throw PluxException(e.code, '$role: ${e.message}', details: e.details);
    }
    if (production) {
      for (final id in signers) {
        if (keyEnvironments[id] != environmentProduction) {
          throw _fail(
            MetadataFault.developmentKey,
            '$role: key $id is not a production key',
          );
        }
      }
    }
  }

  void _checkLife(String role, DateTime expires, int version, int floor) {
    if (!now.isBefore(expires)) {
      throw _fail(
        MetadataFault.expired,
        '$role expired at ${expires.toIso8601String()}',
      );
    }
    if (version < floor) {
      throw _fail(
        MetadataFault.rollback,
        '$role version $version is below the trusted $floor',
      );
    }
  }
}
