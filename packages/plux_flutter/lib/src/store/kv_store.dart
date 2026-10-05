// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The core's built-in store (plan p5 D5, D6): a small key-value store of
/// JSON values in one file, encrypted with AES-256-GCM under a key made
/// for this installation and kept by the platform's secure storage
/// (Keystore, Keychain), never in the clear. Files are read and written,
/// and sealed and opened, off the UI isolate (L-6); a file that does not
/// authenticate is reported and discarded, never read. The state engine
/// keeps its session-surviving entries in it; ADR-0049's built-in
/// `PluxDatabaseAdapter` grows from this interface.
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
  Future<Uint8List?> _keyFor({required bool create}) async {
    final have = _key;
    if (have != null) return have;
    String? stored;
    try {
      stored = await secrets.read(keyName);
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
    if (key == null) {
      if (!create) return null;
      key = Uint8List.fromList([
        for (var i = 0; i < _keyLength; i++) _random.nextInt(256),
      ]);
      try {
        await secrets.write(keyName, base64Encode(key));
      } on Object {
        throw const StoreException(
          StoreFailure.unavailable,
          'writing the store key to secure storage',
        );
      }
    }
    return _key = key;
  }

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
