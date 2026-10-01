// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Verification of signed manifests and baseline bundle signatures, in the
/// order ADR-0029 fixes (SEC-052, SEC-055, BND-008). Only the fields of the
/// verified `signed` document are ever used; nothing the transport added
/// next to it is trusted.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart'
    show KeyPairType, Signature, SimplePublicKey;
import 'package:cryptography/dart.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/verify/jcs.dart';

/// The only signature algorithm accepted (ADR-0004, SEC-122).
const String ed25519Algorithm = 'ed25519';

/// A public key the host app embeds (`keys.json` from `plux pull`,
/// SEC-051).
final class TrustedKey {
  /// Creates a key.
  const TrustedKey({
    required this.keyId,
    required this.algorithm,
    required this.role,
    required this.publicKey,
  });

  /// Reads one entry of `keys.json`: `keyId`, `algorithm`, `role` and the
  /// hex `publicKey`.
  factory TrustedKey.fromJson(Map<String, Object?> json) => TrustedKey(
    keyId: json['keyId']! as String,
    algorithm: json['algorithm']! as String,
    role: json['role']! as String,
    publicKey: hexDecode(json['publicKey']! as String),
  );

  /// The key's ID, as signatures name it.
  final String keyId;

  /// The algorithm; only `ed25519` is accepted.
  final String algorithm;

  /// The update-metadata role (ADR-0004); manifests need `targets`.
  final String role;

  /// The raw public key.
  final Uint8List publicKey;
}

/// One signature over a signed document.
final class DocumentSignature {
  /// Creates a signature.
  const DocumentSignature({
    required this.keyId,
    required this.algorithm,
    required this.signature,
  });

  /// Reads `{"keyid", "alg", "sig"}` as the manifest carries it.
  factory DocumentSignature.fromJson(Map<String, Object?> json) =>
      DocumentSignature(
        keyId: json['keyid']! as String,
        algorithm: json['alg']! as String,
        signature: base64.decode(json['sig']! as String),
      );

  /// The signing key's ID.
  final String keyId;

  /// The algorithm.
  final String algorithm;

  /// The signature bytes.
  final Uint8List signature;
}

/// A bundle the manifest names.
final class BundleRef {
  const BundleRef._(
    this.hash,
    this.size,
    this.requiredFeatures,
    this.minRuntime,
  );

  /// The bundle hash (BND-005).
  final Uint8List hash;

  /// The size in bytes.
  final int size;

  /// The features the bundle requires (BND-008).
  final List<String> requiredFeatures;

  /// The oldest runtime that can use it.
  final String minRuntime;

  /// The hash as the manifest writes it, `sha256:<hex>`.
  String get hashRef => 'sha256:${hexEncode(hash)}';
}

/// A plugin of the release.
final class PluginRef {
  const PluginRef._(this.key, this.version, this.bundle);

  /// The plugin key.
  final String key;

  /// The plugin version.
  final int version;

  /// Its bundle.
  final BundleRef bundle;
}

/// The switches a device obeys (REL-030).
final class ControlFlags {
  const ControlFlags._(
    this.killSwitches,
    this.appKillSwitch,
    this.mandatory,
    this.message,
  );

  /// Plugins that must not render (RT-022).
  final List<String> killSwitches;

  /// Whether the whole app's Plux content is switched off.
  final bool appKillSwitch;

  /// Whether this release must be activated before another Plux page
  /// shows (REL-070).
  final bool mandatory;

  /// The message shown while a switch is on.
  final String message;
}

/// A verified manifest: the fields of its signed part, and the bytes.
final class VerifiedManifest {
  const VerifiedManifest._({
    required this.signed,
    required this.app,
    required this.environment,
    required this.channel,
    required this.releaseSequence,
    required this.issuedAt,
    required this.expires,
    required this.appBundle,
    required this.plugins,
    required this.control,
  });

  /// The canonical bytes the signatures cover.
  final Uint8List signed;

  /// The app ID.
  final String app;

  /// The environment key.
  final String environment;

  /// The channel key.
  final String channel;

  /// The release sequence.
  final int releaseSequence;

  /// When the manifest was signed.
  final DateTime issuedAt;

  /// When it stops being valid.
  final DateTime expires;

  /// The app bundle.
  final BundleRef appBundle;

  /// The plugins, by key order.
  final List<PluginRef> plugins;

  /// The control switches.
  final ControlFlags control;

  /// The app bundle followed by every plugin bundle.
  Iterable<BundleRef> get bundles sync* {
    yield appBundle;
    for (final p in plugins) {
      yield p.bundle;
    }
  }
}

/// Where and by what a manifest is verified.
final class VerificationContext {
  /// Creates a context.
  const VerificationContext({
    required this.keys,
    required this.app,
    required this.environment,
    required this.channel,
    required this.now,
    required this.highestAccepted,
    required this.runtimeVersion,
    required this.supportsFeature,
    this.maxSize = 1 << 20,
  });

  /// The embedded keys.
  final List<TrustedKey> keys;

  /// The configured app.
  final String app;

  /// The configured environment.
  final String environment;

  /// The configured channel.
  final String channel;

  /// The device's clock.
  final DateTime now;

  /// The highest sequence accepted for the channel (SEC-055); 0 for none.
  final int highestAccepted;

  /// This runtime's version.
  final String runtimeVersion;

  /// Whether this runtime supports a required feature (BND-008).
  final bool Function(String feature) supportsFeature;

  /// The largest signed document accepted, in bytes.
  final int maxSize;
}

/// Verifies a manifest's [signed] bytes and [signatures] in [context], in
/// the order of ADR-0029, and returns its fields. Throws [PluxException]
/// with `PLX-3001`, `PLX-3002`, `PLX-3003` or `PLX-3010`.
Future<VerifiedManifest> verifyManifest(
  Uint8List signed,
  List<DocumentSignature> signatures,
  VerificationContext context,
) async {
  if (signed.isEmpty || signed.length > context.maxSize) {
    throw _invalid('the signed document is empty or too large');
  }
  if (!isCanonicalJson(signed)) {
    throw _invalid('the signed document is not in RFC 8785 canonical form');
  }
  if (!await _signedByRole(signed, signatures, context.keys, 'targets')) {
    throw _invalid('no signature verifies under an embedded targets key');
  }
  final m = parseManifest(signed);
  if (m.app != context.app ||
      m.environment != context.environment ||
      m.channel != context.channel) {
    throw _invalid(
      'the manifest is for ${m.app}/${m.environment}/${m.channel}',
    );
  }
  if (!context.now.isBefore(m.expires)) {
    throw PluxException(
      PluxErrorCode.manifestExpired,
      'the manifest expired at ${m.expires.toIso8601String()}',
    );
  }
  if (m.releaseSequence < context.highestAccepted) {
    throw PluxException(
      PluxErrorCode.rollbackRejected,
      'release ${m.releaseSequence} is older than ${context.highestAccepted}',
    );
  }
  for (final b in m.bundles) {
    if (compareVersions(b.minRuntime, context.runtimeVersion) > 0) {
      throw PluxException(
        PluxErrorCode.unsupportedRequiredFeature,
        'a bundle needs runtime ${b.minRuntime}',
        details: {'minRuntime': b.minRuntime},
      );
    }
    for (final f in b.requiredFeatures) {
      if (!context.supportsFeature(f)) {
        throw PluxException(
          PluxErrorCode.unsupportedRequiredFeature,
          'a bundle requires $f',
          details: {'feature': f},
        );
      }
    }
  }
  return m;
}

/// Whether [signature] over the 32-byte bundle [hash] verifies under an
/// embedded `targets` key, as publish signs bundles (ADR-0004).
Future<bool> verifyBundleSignature(
  Uint8List hash,
  DocumentSignature signature,
  List<TrustedKey> keys,
) => _signedByRole(hash, [signature], keys, 'targets');

Future<bool> _signedByRole(
  Uint8List message,
  List<DocumentSignature> signatures,
  List<TrustedKey> keys,
  String role,
) async {
  final ed = DartEd25519(sha512: const DartSha512());
  for (final s in signatures) {
    if (s.algorithm != ed25519Algorithm || s.signature.length != 64) continue;
    for (final k in keys) {
      if (k.keyId != s.keyId ||
          k.role != role ||
          k.algorithm != ed25519Algorithm ||
          k.publicKey.length != 32) {
        continue;
      }
      final ok = await ed.verify(
        message,
        signature: Signature(
          s.signature,
          publicKey: SimplePublicKey(k.publicKey, type: KeyPairType.ed25519),
        ),
      );
      if (ok) return true;
    }
  }
  return false;
}

/// Reads the fields of a signed manifest whose signature has been
/// verified; throws [PluxException] with `PLX-3001` when a field is
/// missing or of the wrong type. Exposed for [verifyManifest] and for the
/// parser's fuzz test (QA-004).
VerifiedManifest parseManifest(Uint8List signed) {
  final Object? json;
  try {
    json = jsonDecode(utf8.decode(signed));
  } on FormatException {
    throw _invalid('the signed document is not JSON');
  }
  try {
    final m = json! as Map<String, Object?>;
    if (m['type'] != 'manifest' ||
        m['specVersion'] != 1 ||
        m['role'] != 'targets') {
      throw _invalid('not a version 1 targets manifest');
    }
    final control = m['control']! as Map<String, Object?>;
    return VerifiedManifest._(
      signed: signed,
      app: m['app']! as String,
      environment: m['environment']! as String,
      channel: m['channel']! as String,
      releaseSequence: m['releaseSequence']! as int,
      issuedAt: DateTime.parse(m['issuedAt']! as String),
      expires: DateTime.parse(m['expires']! as String),
      appBundle: _bundle(m['appBundle']! as Map<String, Object?>),
      plugins: [
        for (final p
            in (m['plugins']! as List<Object?>).cast<Map<String, Object?>>())
          PluginRef._(p['key']! as String, p['version']! as int, _bundle(p)),
      ],
      control: ControlFlags._(
        (control['killSwitches']! as List<Object?>).cast<String>().toList(),
        control['appKillSwitch'] as bool? ?? false,
        control['mandatory'] as bool? ?? false,
        control['message'] as String? ?? '',
      ),
    );
  } on TypeError {
    throw _invalid('a field of the manifest is missing or of the wrong type');
  } on FormatException {
    throw _invalid('a time of the manifest is not RFC 3339');
  }
}

BundleRef _bundle(Map<String, Object?> b) {
  final ref = b['hash']! as String;
  if (!ref.startsWith('sha256:') || ref.length != 7 + 64) {
    throw _invalid('bundle hash $ref is not sha256:<64 hex digits>');
  }
  return BundleRef._(
    hexDecode(ref.substring(7)),
    b['size']! as int,
    (b['requiredFeatures']! as List<Object?>).cast<String>().toList(),
    b['minRuntime']! as String,
  );
}

PluxException _invalid(String message) =>
    PluxException(PluxErrorCode.manifestSignatureInvalid, message);

/// Compares two semantic versions (`1.2.3`, `1.2.3-beta.1`): negative when
/// [a] is older, zero when equal, positive when newer. Build metadata is
/// ignored; a pre-release is older than its release. An unparseable
/// version compares as newer than any other, so it is refused.
int compareVersions(String a, String b) {
  final pa = _semver(a), pb = _semver(b);
  if (pa == null) return pb == null ? 0 : 1;
  if (pb == null) return -1;
  for (var i = 0; i < 3; i++) {
    if (pa.$1[i] != pb.$1[i]) return pa.$1[i].compareTo(pb.$1[i]);
  }
  if (pa.$2 == pb.$2) return 0;
  if (pa.$2 == null) return 1;
  if (pb.$2 == null) return -1;
  final xa = pa.$2!.split('.'), xb = pb.$2!.split('.');
  for (var i = 0; i < xa.length && i < xb.length; i++) {
    final na = int.tryParse(xa[i]), nb = int.tryParse(xb[i]);
    final c = na != null && nb != null
        ? na.compareTo(nb)
        : na != null
        ? -1
        : nb != null
        ? 1
        : xa[i].compareTo(xb[i]);
    if (c != 0) return c;
  }
  return xa.length.compareTo(xb.length);
}

final _semverPattern = RegExp(
  r'^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$',
);

(List<int>, String?)? _semver(String v) {
  final m = _semverPattern.firstMatch(v);
  if (m == null) return null;
  return ([int.parse(m[1]!), int.parse(m[2]!), int.parse(m[3]!)], m[4]);
}

/// Hexadecimal of [bytes], lower case.
String hexEncode(List<int> bytes) =>
    bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

/// Bytes of the hexadecimal [s]; throws [FormatException] when it is not.
Uint8List hexDecode(String s) {
  if (s.length.isOdd || !_hex.hasMatch(s)) {
    throw FormatException('not hexadecimal', s);
  }
  final out = Uint8List(s.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = int.parse(s.substring(2 * i, 2 * i + 2), radix: 16);
  }
  return out;
}

final _hex = RegExp(r'^[0-9a-fA-F]*$');
