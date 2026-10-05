// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The core's built-in stores (plan p5 D5, D6): small key-value stores of
/// JSON values in one file each. [EncryptedFileStore] seals its file with
/// AES-256-GCM under a key made for this installation and kept by the
/// platform's secure storage (Keystore, Keychain), never in the clear;
/// [PlainFileStore] keeps its file unencrypted, for data whose protection
/// is the app sandbox. Files are read and written off the UI isolate
/// (L-6) and replaced atomically; a file that does not read back is
/// reported and discarded. The state engine keeps its session-surviving
/// entries in them: `persisted` plain, `secure` encrypted; ADR-0049's
/// built-in `PluxDatabaseAdapter` grows from this interface.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';
import 'package:cryptography/dart.dart';

/// A store of JSON values by key: the whole store is loaded and saved at
/// once, which suits the small stores the core keeps.
abstract interface class PluxKeyValueStore {
  /// Every entry; empty when nothing is stored.
  Future<Map<String, Object?>> load();

  /// Replaces every entry with [entries].
  Future<void> save(Map<String, Object?> entries);

  /// Removes the store and its key.
  Future<void> wipe();
}

/// Where the platform keeps secrets encrypted under a key it holds
/// (ADR-0029): the store's own key is one of them.
abstract interface class SecretStore {
  /// The secret [name], or null.
  Future<String?> read(String name);

  /// Stores secret [name].
  Future<void> write(String name, String value);

  /// Removes secret [name].
  Future<void> delete(String name);
}

/// Runs [work] off the UI isolate; [Isolate.run] in the runtime.
typedef OffIsolate = Future<R> Function<R>(FutureOr<R> Function() work);

Future<R> _isolateRun<R>(FutureOr<R> Function() work) => Isolate.run(work);

/// Why a store could not be used.
enum StoreFailure {
  /// The platform's secure storage gave no key, or the file system failed.
  unavailable,

  /// The file did not authenticate: altered, truncated or another
  /// installation's. It was removed.
  corrupt,

  /// The entries are larger than the store may hold.
  tooLarge,
}

/// A store operation failed; the message names the operation, never a
/// value.
final class StoreException implements Exception {
  /// Creates the exception.
  const StoreException(this.failure, this.message);

  /// What failed.
  final StoreFailure failure;

  /// The operation.
  final String message;

  @override
  String toString() => 'StoreException(${failure.name}): $message';
}

/// The 32-byte key [name] of this installation, kept in [secrets]: read,
/// or made from [random] and stored when [create] and there is none (or
/// what is stored is not a key); null when there is none and [create] is
/// false. Throws [StoreException] when the secure storage fails. The
/// encrypted state store and the data cache (DAT-010) keep their keys
/// this way.
Future<Uint8List?> installationKey(
  SecretStore secrets,
  String name, {
  required bool create,
  Random? random,
}) async {
  String? stored;
  try {
    stored = await secrets.read(name);
  } on Object {
    throw const StoreException(
      StoreFailure.unavailable,
      'reading the store key from secure storage',
    );
  }
  Uint8List? key;
  if (stored != null) {
    try {
      key = base64Decode(stored);
    } on FormatException {
      key = null;
    }
    if (key != null && key.length != _keyLength) key = null;
  }
  if (key != null || !create) return key;
  final r = random ?? Random.secure();
  key = Uint8List.fromList([
    for (var i = 0; i < _keyLength; i++) r.nextInt(256),
  ]);
  try {
    await secrets.write(name, base64Encode(key));
  } on Object {
    throw const StoreException(
      StoreFailure.unavailable,
      'writing the store key to secure storage',
    );
  }
  return key;
}

/// The file format's magic and version.
const List<int> _magic = [0x50, 0x58, 0x4b, 0x31]; // "PXK1"
const int _nonceLength = 12;
const int _macLength = 16;
const int _keyLength = 32;

/// A [PluxKeyValueStore] in one encrypted file.
final class EncryptedFileStore implements PluxKeyValueStore {
  /// Creates the store at [path] whose key is secret [keyName] of
  /// [secrets]; [label] binds the file to its purpose (authenticated, not
  /// encrypted), so files cannot be swapped between stores. Entries larger
  /// than [maxBytes], encoded, are refused.
  EncryptedFileStore({
    required this.path,
    required this.secrets,
    required this.keyName,
    required this.label,
    required this.maxBytes,
    OffIsolate? offIsolate,
    Random? random,
  }) : _off = offIsolate ?? _isolateRun,
       _random = random ?? Random.secure();

  /// The file.
  final String path;

  /// Where the key is kept.
  final SecretStore secrets;

  /// The key's name in [secrets].
  final String keyName;

  /// The file's purpose, authenticated with its content.
  final String label;

  /// The largest encoded size of the entries.
  int maxBytes;

  final OffIsolate _off;
  final Random _random;
  Uint8List? _key;

  /// The key: read once, or made and stored when [create] and there is
  /// none; null when there is none and [create] is false.
  Future<Uint8List?> _keyFor({required bool create}) async => _key ??=
      await installationKey(secrets, keyName, create: create, random: _random);

  @override
  Future<Map<String, Object?>> load() async {
    if (!File(path).existsSync()) return {};
    final key = await _keyFor(create: false);
    if (key == null) {
      // A file without its key cannot be read: it is another
      // installation's, or the key was lost with a restore.
      await _remove();
      throw const StoreException(
        StoreFailure.corrupt,
        'the store has no key on this installation',
      );
    }
    final file = path;
    final aad = utf8.encode(label);
    final Map<String, Object?>? result;
    try {
      result = await _off(() => _open(file, key, aad));
    } on FileSystemException {
      throw const StoreException(
        StoreFailure.unavailable,
        'reading the store file',
      );
    }
    if (result == null) {
      await _remove();
      throw const StoreException(
        StoreFailure.corrupt,
        'the store did not authenticate',
      );
    }
    return result;
  }

  @override
  Future<void> save(Map<String, Object?> entries) async {
    final key = await _keyFor(create: true);
    final nonce = Uint8List.fromList([
      for (var i = 0; i < _nonceLength; i++) _random.nextInt(256),
    ]);
    final file = path;
    final aad = utf8.encode(label);
    final limit = maxBytes;
    final bool ok;
    try {
      ok = await _off(() => _seal(file, key!, nonce, aad, entries, limit));
    } on FileSystemException {
      throw const StoreException(
        StoreFailure.unavailable,
        'writing the store file',
      );
    }
    if (!ok) {
      throw StoreException(
        StoreFailure.tooLarge,
        'the entries exceed $limit bytes',
      );
    }
  }

  @override
  Future<void> wipe() async {
    await _remove();
    _key = null;
    try {
      await secrets.delete(keyName);
    } on Object {
      throw const StoreException(
        StoreFailure.unavailable,
        'removing the store key from secure storage',
      );
    }
  }

  Future<void> _remove() async {
    final f = File(path);
    if (f.existsSync()) await f.delete();
  }
}

/// Opens [file] off the UI isolate: the entries, or null when the file
/// does not authenticate.
Map<String, Object?>? _open(String file, List<int> key, List<int> aad) {
  final data = File(file).readAsBytesSync();
  if (data.length < _magic.length + _nonceLength + _macLength) return null;
  for (var i = 0; i < _magic.length; i++) {
    if (data[i] != _magic[i]) return null;
  }
  final nonce = data.sublist(_magic.length, _magic.length + _nonceLength);
  final cipher = data.sublist(
    _magic.length + _nonceLength,
    data.length - _macLength,
  );
  final mac = Mac(data.sublist(data.length - _macLength));
  try {
    final clear = DartAesGcm.with256bits().decryptSync(
      SecretBox(cipher, nonce: nonce, mac: mac),
      secretKeyData: SecretKeyData(key),
      aad: aad,
    );
    final decoded = jsonDecode(utf8.decode(clear));
    return decoded is Map<String, Object?> ? decoded : null;
  } on SecretBoxAuthenticationError {
    return null;
  } on FormatException {
    return null;
  }
}

/// Seals [entries] into [file] off the UI isolate, replacing it
/// atomically; false when they exceed [limit] bytes.
bool _seal(
  String file,
  List<int> key,
  List<int> nonce,
  List<int> aad,
  Map<String, Object?> entries,
  int limit,
) {
  final clear = utf8.encode(jsonEncode(entries));
  if (clear.length > limit) return false;
  final box = DartAesGcm.with256bits().encryptSync(
    clear,
    secretKeyData: SecretKeyData(key),
    nonce: nonce,
    aad: aad,
  );
  final out = BytesBuilder(copy: false)
    ..add(_magic)
    ..add(box.nonce)
    ..add(box.cipherText)
    ..add(box.mac.bytes);
  File(file).parent.createSync(recursive: true);
  final tmp = File('$file.tmp')..writeAsBytesSync(out.takeBytes(), flush: true);
  tmp.renameSync(file);
  return true;
}

/// A [PluxKeyValueStore] in one unencrypted JSON file (plan p5 D6): for
/// `persisted` state, whose protection is the app sandbox, so saves and
/// loads cost no cryptography. It holds no key, so it writes nothing to
/// secure storage. The file is written to a temporary file and renamed,
/// off the UI isolate (L-6).
final class PlainFileStore implements PluxKeyValueStore {
  /// Creates the store at [path]; entries larger than [maxBytes], encoded,
  /// are refused.
  PlainFileStore({
    required this.path,
    required this.maxBytes,
    OffIsolate? offIsolate,
  }) : _off = offIsolate ?? _isolateRun;

  /// The file.
  final String path;

  /// The largest encoded size of the entries.
  int maxBytes;

  final OffIsolate _off;

  @override
  Future<Map<String, Object?>> load() async {
    if (!File(path).existsSync()) return {};
    final file = path;
    final Map<String, Object?>? result;
    try {
      result = await _off(() => _readPlain(file));
    } on FileSystemException {
      throw const StoreException(
        StoreFailure.unavailable,
        'reading the store file',
      );
    }
    if (result == null) {
      await _remove();
      throw const StoreException(
        StoreFailure.corrupt,
        'the store file is not a JSON object',
      );
    }
    return result;
  }

  @override
  Future<void> save(Map<String, Object?> entries) async {
    final file = path;
    final limit = maxBytes;
    final bool ok;
    try {
      ok = await _off(() => _writePlain(file, entries, limit));
    } on FileSystemException {
      throw const StoreException(
        StoreFailure.unavailable,
        'writing the store file',
      );
    }
    if (!ok) {
      throw StoreException(
        StoreFailure.tooLarge,
        'the entries exceed $limit bytes',
      );
    }
  }

  @override
  Future<void> wipe() => _remove();

  Future<void> _remove() async {
    final f = File(path);
    if (f.existsSync()) await f.delete();
  }
}

/// Reads [file] off the UI isolate: the entries, or null when it is not
/// a JSON object.
Map<String, Object?>? _readPlain(String file) {
  try {
    final decoded = jsonDecode(utf8.decode(File(file).readAsBytesSync()));
    return decoded is Map<String, Object?> ? decoded : null;
  } on FormatException {
    return null;
  }
}

/// Writes [entries] to [file] off the UI isolate, replacing it
/// atomically; false when they exceed [limit] bytes.
bool _writePlain(String file, Map<String, Object?> entries, int limit) {
  final bytes = utf8.encode(jsonEncode(entries));
  if (bytes.length > limit) return false;
  File(file).parent.createSync(recursive: true);
  final tmp = File('$file.tmp')..writeAsBytesSync(bytes, flush: true);
  tmp.renameSync(file);
  return true;
}
