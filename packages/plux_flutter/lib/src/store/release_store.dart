// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The release store of ADR-0021: content-addressed bundles and assets,
/// one immutable record per release, and a pointer file replaced only by
/// an atomic rename. Staging writes objects, then the record, then the
/// pointer, each durable before the next begins, so a crash or power loss
/// at any instant leaves either the old or the new release fully active
/// (SYN-005). Garbage collection deletes only what the durable pointer no
/// longer reaches (SYN-012).
///
/// The store does blocking file I/O and hashing; it runs on the sync
/// isolate, except [ReleaseStore.open], which reads two small files.
library;

import 'dart:io';
import 'dart:typed_data';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/pointer.dart';
import 'package:plux_flutter/src/store/release_record.dart';

/// Makes a directory's entries durable (`fsync` of the directory). Dart
/// cannot open a directory, so production code passes the native one.
typedef DirectorySync = void Function(String path);

/// Called at every durable step of staging and activation, with its name;
/// failure-injection tests throw from it to simulate a crash there
/// (QA-009).
typedef StepHook = void Function(String step);

/// The kinds of stored objects.
enum ObjectKind {
  /// Bundles, named by bundle hash.
  bundles,

  /// Asset files, named by SHA-256.
  assets,
}

/// What the store decided at the start of a launch (SYN-006).
enum LaunchOutcome {
  /// Nothing to do.
  normal,

  /// The active release failed its trial; the store went back to the last
  /// known good release and pinned it.
  revertedToLastKnownGood,
}

/// The release store of one app, environment and channel.
final class ReleaseStore {
  ReleaseStore._(this.root, this._syncDirectory, this._hook, this._pointer);

  /// Opens (and creates) the store at [root]. A missing or corrupt pointer
  /// file opens an empty store (ADR-0021).
  factory ReleaseStore.open(
    String root, {
    DirectorySync? syncDirectory,
    StepHook? onStep,
  }) {
    for (final d in [
      'objects/bundles',
      'objects/assets',
      'objects/tmp',
      'releases',
    ]) {
      Directory('$root/$d').createSync(recursive: true);
    }
    final file = File('$root/state');
    StorePointer? pointer;
    if (file.existsSync()) {
      pointer = StorePointer.decode(file.readAsBytesSync());
    }
    return ReleaseStore._(
      root,
      syncDirectory ?? (_) {},
      onStep ?? (_) {},
      pointer ?? StorePointer.empty,
    );
  }

  /// The store's directory.
  final String root;

  final DirectorySync _syncDirectory;
  final StepHook _hook;
  StorePointer _pointer;

  /// The current pointer.
  StorePointer get pointer => _pointer;

  /// The path of a stored object.
  String objectPath(ObjectKind kind, String hash) =>
      '$root/objects/${kind.name}/$hash';

  /// Whether an object is stored.
  bool hasObject(ObjectKind kind, String hash) =>
      File(objectPath(kind, hash)).existsSync();

  /// The path of an object being downloaded.
  String partPath(String name) => '$root/objects/tmp/$name.part';

  /// Moves a verified download from [part] into the store as [hash],
  /// durably: `fsync` the file, rename, `fsync` the directory (step S1).
  void commitObject(ObjectKind kind, String hash, String part) {
    _guard(() {
      final f = File(part).openSync(mode: FileMode.append);
      try {
        f.flushSync();
      } finally {
        f.closeSync();
      }
      _hook('object.written');
      File(part).renameSync(objectPath(kind, hash));
      _syncDirectory('$root/objects/${kind.name}');
      _hook('object.renamed');
    });
  }

  /// Writes [bytes] as the object [hash] through a temporary file.
  void writeObject(ObjectKind kind, String hash, Uint8List bytes) {
    final part = partPath('$hash.write');
    _guard(() => _writeDurably(part, bytes));
    commitObject(kind, hash, part);
  }

  /// The record of release [sequence], or null when there is none or it
  /// cannot be read.
  ReleaseRecord? record(int sequence) {
    final f = File('$root/releases/$sequence.json');
    if (!f.existsSync()) return null;
    try {
      return ReleaseRecord.decode(f.readAsBytesSync());
    } on FormatException {
      return null;
    }
  }

  /// Stages [record] (steps S2 and S3): writes the record durably, then
  /// points `staged` at it. Every object it names must be stored.
  void stage(ReleaseRecord record) {
    for (final b in record.bundles) {
      if (!hasObject(ObjectKind.bundles, b.hash)) {
        throw StateError('bundle ${b.hash} is not stored');
      }
    }
    for (final a in record.assets) {
      if (!hasObject(ObjectKind.assets, a)) {
        throw StateError('asset $a is not stored');
      }
    }
    final path = '$root/releases/${record.sequence}.json';
    _guard(() {
      _writeDurably('$path.tmp', record.encode());
      _hook('record.written');
      File('$path.tmp').renameSync(path);
      _syncDirectory('$root/releases');
      _hook('record.renamed');
    });
    writePointer(_pointer.copyWith(staged: record.sequence));
  }

  /// Activates the staged release: one pointer write that makes the
  /// active release the last known good and starts the new one's trial.
  void activate() {
    final staged = _pointer.staged;
    if (staged == null) throw StateError('nothing is staged');
    final previous = _pointer.active;
    writePointer(
      StorePointer(
        active: staged,
        lastKnownGood: previous,
        highestAccepted: _pointer.highestAccepted,
        etag: _pointer.etag,
        trial: previous == null ? null : Trial(sequence: staged),
      ),
    );
  }

  /// Makes [record] the active release directly, with no trial: the
  /// baseline embedded in the host app (SYN-007).
  void installBaseline(ReleaseRecord record) {
    stage(record);
    writePointer(
      StorePointer(
        active: record.sequence,
        highestAccepted: record.sequence > _pointer.highestAccepted
            ? record.sequence
            : _pointer.highestAccepted,
      ),
    );
  }

  /// Records that a manifest of [sequence] was accepted, with its [etag]
  /// (SEC-055, NFR-006).
  void accept(int sequence, String etag) {
    writePointer(
      _pointer.copyWith(
        highestAccepted: sequence > _pointer.highestAccepted
            ? sequence
            : _pointer.highestAccepted,
        etag: etag,
      ),
    );
  }

  /// Starts a launch (SYN-006): a trial whose previous launch never reached
  /// a healthy point counts that launch as a crash; three failures within
  /// the first two launches revert to the last known good release.
  LaunchOutcome beginLaunch() {
    final t = _pointer.trial;
    if (t == null || t.sequence != _pointer.active) return LaunchOutcome.normal;
    final failures = t.failures + (t.open ? 1 : 0);
    if (failures >= 3) return _revert();
    if (t.launches >= 2) {
      writePointer(_pointer.copyWith(clearTrial: true));
      return LaunchOutcome.normal;
    }
    writePointer(
      _pointer.copyWith(
        trial: t.copyWith(
          launches: t.launches + 1,
          failures: failures,
          open: true,
        ),
      ),
    );
    return LaunchOutcome.normal;
  }

  /// Records that the launch reached a healthy point.
  void markHealthy() {
    final t = _pointer.trial;
    if (t == null || !t.open) return;
    writePointer(_pointer.copyWith(trial: t.copyWith(open: false)));
  }

  /// Records a failure attributed to Plux; returns true when the active
  /// release has now failed its trial and should be reverted.
  bool recordFailure() {
    final t = _pointer.trial;
    if (t == null || t.sequence != _pointer.active) return false;
    final next = t.copyWith(failures: t.failures + 1);
    writePointer(_pointer.copyWith(trial: next));
    return next.failures >= 3;
  }

  /// Reverts to the last known good release now; returns whether it did.
  bool revert() => _revert() == LaunchOutcome.revertedToLastKnownGood;

  LaunchOutcome _revert() {
    final good = _pointer.lastKnownGood;
    if (good == null) {
      writePointer(_pointer.copyWith(clearTrial: true));
      return LaunchOutcome.normal;
    }
    writePointer(
      StorePointer(
        active: good,
        pinned: good,
        rejected: _pointer.active,
        highestAccepted: _pointer.highestAccepted,
        etag: '',
      ),
    );
    return LaunchOutcome.revertedToLastKnownGood;
  }

  /// The latest verified control switches, or null.
  ControlState? control() {
    final f = File('$root/control');
    return f.existsSync() ? ControlState.decode(f.readAsBytesSync()) : null;
  }

  /// Replaces the control switches atomically.
  void writeControl(ControlState state) {
    _guard(() {
      _writeDurably('$root/control.tmp', state.encode());
      File('$root/control.tmp').renameSync('$root/control');
      _syncDirectory(root);
    });
  }

  /// Replaces the pointer file atomically: write `state.tmp`, `fsync`,
  /// rename to `state`, `fsync` the directory.
  void writePointer(StorePointer next) {
    _guard(() {
      _writeDurably('$root/state.tmp', next.encode());
      _hook('pointer.written');
      File('$root/state.tmp').renameSync('$root/state');
      _syncDirectory(root);
      _hook('pointer.renamed');
    });
    _pointer = next;
  }

  /// The bytes the store's objects and records occupy.
  int usage() {
    var n = 0;
    for (final d in [
      'objects/bundles',
      'objects/assets',
      'objects/tmp',
      'releases',
    ]) {
      for (final f in Directory('$root/$d').listSync().whereType<File>()) {
        n += f.lengthSync();
      }
    }
    return n;
  }

  /// The bytes the objects of the kept releases occupy.
  int keptUsage() {
    final seen = <String>{};
    var n = 0;
    for (final seq in _pointer.kept) {
      final r = record(seq);
      if (r == null) continue;
      for (final b in r.bundles) {
        if (seen.add('b${b.hash}')) {
          n += _size(objectPath(ObjectKind.bundles, b.hash));
        }
      }
      for (final a in r.assets) {
        if (seen.add('a$a')) n += _size(objectPath(ObjectKind.assets, a));
      }
    }
    return n;
  }

  /// Deletes records and objects no kept release references, and partial
  /// downloads other than [keepParts] (SYN-012). Runs only after a pointer
  /// write has completed.
  void collectGarbage({Set<String> keepParts = const {}}) {
    final bundles = <String>{}, assets = <String>{};
    final kept = _pointer.kept;
    for (final seq in kept) {
      final r = record(seq);
      if (r == null) continue;
      bundles.addAll(r.bundles.map((b) => b.hash));
      assets.addAll(r.assets);
    }
    for (final f in Directory('$root/releases').listSync().whereType<File>()) {
      final name = f.uri.pathSegments.last;
      final seq = int.tryParse(name.replaceAll('.json', ''));
      if (seq == null || !kept.contains(seq) || !name.endsWith('.json')) {
        _delete(f);
      }
    }
    for (final (kind, keep) in [
      (ObjectKind.bundles, bundles),
      (ObjectKind.assets, assets),
    ]) {
      for (final f in Directory(
        '$root/objects/${kind.name}',
      ).listSync().whereType<File>()) {
        if (!keep.contains(f.uri.pathSegments.last)) _delete(f);
      }
    }
    for (final f in Directory(
      '$root/objects/tmp',
    ).listSync().whereType<File>()) {
      if (!keepParts.contains(f.path)) _delete(f);
    }
  }

  static int _size(String path) {
    final f = File(path);
    return f.existsSync() ? f.lengthSync() : 0;
  }

  static void _delete(File f) {
    try {
      f.deleteSync();
    } on FileSystemException {
      // Deleted by another pass, or not deletable now; the next pass retries.
    }
  }

  void _writeDurably(String path, List<int> bytes) {
    final f = File(path).openSync(mode: FileMode.write);
    try {
      f
        ..writeFromSync(bytes)
        ..flushSync();
    } finally {
      f.closeSync();
    }
  }

  /// Turns a lack of space into `PLX-3030` (SYN-012).
  void _guard(void Function() write) {
    try {
      write();
    } on FileSystemException catch (e) {
      final code = e.osError?.errorCode;
      if (code == 28 || code == 122) {
        throw PluxException(
          PluxErrorCode.diskQuotaExceeded,
          'the device is out of storage: ${e.message}',
        );
      }
      rethrow;
    }
  }
}
