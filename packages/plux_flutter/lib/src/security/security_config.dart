// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Remote security configuration on the device (SEC-182, ADR-0053): the
/// device document, its hash, the RFC 7396 merge patch that updates it, the
/// encrypted store that keeps it, and the effective settings the rest of
/// the runtime reads. Everything here is pure or touches only the
/// [SecretStore]; the sync engine drives it on the sync isolate (L-6, L-7).
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/security/settings.g.dart';
import 'package:plux_flutter/src/store/kv_store.dart';
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart' show SecurityConfigRef;

export 'package:plux_flutter/src/security/settings.g.dart';

/// Applies the JSON merge patch [patch] to [target] exactly as RFC 7396
/// §2 defines: objects merge recursively, a null member deletes, and
/// anything else replaces. Neither argument is modified.
Object? applyMergePatch(Object? target, Object? patch) {
  if (patch is! Map<String, Object?>) return patch;
  final result = <String, Object?>{
    if (target is Map<String, Object?>) ...target,
  };
  for (final MapEntry(:key, :value) in patch.entries) {
    if (value == null) {
      result.remove(key);
    } else {
      result[key] = applyMergePatch(result[key], value);
    }
  }
  return result;
}

/// The SHA-256 of the canonical JSON (RFC 8785) of the device document
/// [document]: what the signed manifest's `config.sha256` holds.
Uint8List configHash(Map<String, Object?> document) => Uint8List.fromList(
  sha256.convert(utf8.encode(canonicalJson(document))).bytes,
);

bool _same(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  var diff = 0;
  for (var i = 0; i < a.length; i++) {
    diff |= a[i] ^ b[i];
  }
  return diff == 0;
}

/// The device document of one configuration version, with the version and
/// the hash the signed manifest pinned for it.
final class StoredSecurityConfig {
  /// Creates a stored configuration.
  const StoredSecurityConfig(this.version, this.document, this.sha256);

  /// Reads the stored form; null when it is not one.
  static StoredSecurityConfig? tryParse(String text) {
    try {
      final j = jsonDecode(text) as Map<String, Object?>;
      final hash = j['sha256']! as String;
      return StoredSecurityConfig(
        j['version']! as int,
        j['document']! as Map<String, Object?>,
        base64.decode(hash),
      );
    } on Object {
      return null;
    }
  }

  /// The version; 0 is the built-in defaults.
  final int version;

  /// The device document `{"profile": …, "overrides": {…}}`.
  final Map<String, Object?> document;

  /// The hash the verified manifest pinned for [document].
  final Uint8List sha256;

  /// Whether [document] still hashes to [sha256].
  bool get intact => _same(configHash(document), sha256);

  /// The stored form.
  String encode() => jsonEncode({
    'version': version,
    'document': document,
    'sha256': base64.encode(sha256),
  });
}

/// Keeps the verified configuration in the platform's secret store, one
/// per app and environment, as a single secret that is replaced whole.
final class SecurityConfigStore {
  /// Creates the store for [appId] and [environment] in [secrets].
  SecurityConfigStore(this.secrets, String appId, String environment)
    : name = 'security-config.${_safe(appId)}.${_safe(environment)}'
          .toLowerCase();

  /// Where the secrets are kept.
  final SecretStore secrets;

  /// The secret's name.
  final String name;

  static String _safe(String s) => s.replaceAll(RegExp('[^A-Za-z0-9._-]'), '_');

  /// The stored configuration, or null when there is none, it is
  /// unreadable, or its document no longer hashes to the hash stored with
  /// it; a stored value that is not valid is removed (SEC-182). A secret
  /// store that fails reads as none.
  Future<StoredSecurityConfig?> read() async {
    final String? text;
    try {
      text = await secrets.read(name);
    } on Object {
      return null;
    }
    if (text == null) return null;
    final stored = StoredSecurityConfig.tryParse(text);
    if (stored != null && stored.intact) return stored;
    await clear();
    return null;
  }

  /// Replaces the stored configuration with [config]; the secret is
  /// written whole, so a reader sees the old value or the new one.
  Future<void> write(StoredSecurityConfig config) async {
    try {
      await secrets.write(name, config.encode());
    } on Object {
      throw const PluxException(
        PluxErrorCode.syncFailed,
        'the security configuration cannot be written to secure storage',
      );
    }
  }

  /// Removes the stored configuration.
  Future<void> clear() async {
    try {
      await secrets.delete(name);
    } on Object {
      // Nothing to remove, or storage that fails reads as none anyway.
    }
  }
}

/// What applying a manifest's configuration pin came to.
sealed class ConfigApplied {
  const ConfigApplied();
}

/// The device already holds the pinned configuration.
final class ConfigCurrent extends ConfigApplied {
  /// Creates the result.
  const ConfigCurrent();
}

/// The pinned configuration, built from the patch and verified.
final class ConfigUpdated extends ConfigApplied {
  /// Creates the result.
  const ConfigUpdated(this.config);

  /// What to keep.
  final StoredSecurityConfig config;
}

/// The patched document does not hash to the signed hash, or the update
/// is out of bounds: nothing is applied.
final class ConfigRejected extends ConfigApplied {
  /// Creates the result.
  const ConfigRejected(this.error);

  /// Why (`PLX-6040`, `PLX-6042`).
  final PluxException error;
}

/// Brings the configuration held in [current] (null: the built-in
/// defaults, version 0) to the version [ref] pins, which the verified
/// manifest signed (SEC-182, ADR-0053).
///
/// [sentVersion] is the version the manifest request carried. A request
/// that carried 0, or a response that says [fullRequired], is a patch from
/// the empty document; any other is a patch from [current]. The result is
/// applied only when its hash is the signed one.
ConfigApplied applyConfigUpdate({
  required SecurityConfigRef ref,
  required StoredSecurityConfig? current,
  required int sentVersion,
  required Uint8List? patch,
  required bool fullRequired,
}) {
  final held = current?.document ?? const <String, Object?>{};
  if (ref.version == (current?.version ?? 0) &&
      _same(configHash(held), ref.sha256)) {
    return const ConfigCurrent();
  }
  final fromEmpty = sentVersion == 0 || fullRequired || current == null;
  final maxBytes = PluxLimit.securityConfigBytes.defaultValue;
  final maxPatch = fromEmpty
      ? maxBytes
      : PluxLimit.securityConfigPatchBytes.defaultValue;
  if (patch != null && patch.length > maxPatch) {
    return ConfigRejected(
      PluxException(
        PluxErrorCode.securityConfigOutOfBounds,
        'the configuration patch is ${patch.length} bytes, over $maxPatch',
        details: {'limit': PluxLimit.securityConfigPatchBytes.key},
      ),
    );
  }
  Object? next = fromEmpty ? const <String, Object?>{} : held;
  if (patch != null) {
    try {
      next = applyMergePatch(next, jsonDecode(utf8.decode(patch)));
    } on FormatException {
      return _mismatch(ref);
    }
  }
  if (next is! Map<String, Object?>) return _mismatch(ref);
  final String canonical;
  try {
    canonical = canonicalJson(next);
  } on ArgumentError {
    return _mismatch(ref);
  }
  if (utf8.encode(canonical).length > maxBytes) {
    return ConfigRejected(
      PluxException(
        PluxErrorCode.securityConfigOutOfBounds,
        'the configuration is over $maxBytes bytes',
        details: {'limit': PluxLimit.securityConfigBytes.key},
      ),
    );
  }
  if (!_same(configHash(next), ref.sha256)) return _mismatch(ref);
  return ConfigUpdated(StoredSecurityConfig(ref.version, next, ref.sha256));
}

ConfigRejected _mismatch(SecurityConfigRef ref) => ConfigRejected(
  PluxException(
    PluxErrorCode.securityConfigHashMismatch,
    'the configuration for version ${ref.version} does not match the signed '
    'hash; the last good configuration is kept',
  ),
);

/// The settings in force on this device (SEC-182): the built-in defaults
/// of the document's profile with its overrides on top. Read-only; a new
/// value replaces it when a configuration is applied.
final class SecuritySettings {
  const SecuritySettings._(this.profile, this.version, this._values);

  /// The built-in defaults of the standard profile (version 0).
  static final SecuritySettings builtIn = SecuritySettings.fromDocument(
    const {},
  );

  /// The settings of the device document [document] at [version]. A
  /// missing or unknown profile is the standard profile; an override that
  /// names no setting the device receives, or has the wrong type, is
  /// ignored.
  factory SecuritySettings.fromDocument(
    Map<String, Object?> document, {
    int version = 0,
  }) {
    final profile = SecurityProfile.values.firstWhere(
      (p) => p.name == document['profile'],
      orElse: () => SecurityProfile.standard,
    );
    final overrides = switch (document['overrides']) {
      final Map<String, Object?> m => m,
      _ => const <String, Object?>{},
    };
    final values = <SecuritySetting, Object>{
      for (final s in SecuritySetting.values)
        s: switch (overrides[s.key]) {
          final Object v when _fits(s.type, v) => v,
          _ => s.defaultFor(profile),
        },
    };
    return SecuritySettings._(profile, version, Map.unmodifiable(values));
  }

  static bool _fits(SecuritySettingType type, Object v) => switch (type) {
    SecuritySettingType.boolean => v is bool,
    SecuritySettingType.seconds || SecuritySettingType.count => v is int,
    SecuritySettingType.choice => v is String,
  };

  /// The profile the defaults come from.
  final SecurityProfile profile;

  /// The configuration version in force; 0 is the built-in defaults.
  final int version;

  final Map<SecuritySetting, Object> _values;

  /// The value of [setting]: a bool, an int or a String by its type.
  Object operator [](SecuritySetting setting) => _values[setting]!;

  /// The value of the boolean [setting].
  bool flag(SecuritySetting setting) => _values[setting]! as bool;

  /// The value of the seconds or count [setting].
  int number(SecuritySetting setting) => _values[setting]! as int;

  /// The value of the choice [setting].
  String choice(SecuritySetting setting) => _values[setting]! as String;

  @override
  bool operator ==(Object other) =>
      other is SecuritySettings &&
      other.profile == profile &&
      other.version == version &&
      _values.entries.every((e) => other._values[e.key] == e.value);

  @override
  int get hashCode => Object.hash(
    profile,
    version,
    Object.hashAll([for (final s in SecuritySetting.values) _values[s]]),
  );

  @override
  String toString() => 'SecuritySettings(${profile.name}, version $version)';
}
