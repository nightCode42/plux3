// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Where state entries live beyond their scope instance (STA-003):
/// `session` entries in memory until the app is closed, `persisted` and
/// `secure` entries in two built-in stores, each encrypted under its own
/// installation key (plan p5 D5, D6). Each stored value carries the
/// fingerprint of its entry's type, which versions it (STA-040). Writes
/// take effect at once and are saved behind, coalesced, off the UI
/// isolate; nothing about a value is ever logged (SCH-012).
library;

import 'dart:async';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

/// How an entry persists (STA-003).
enum Persistence {
  /// With its scope instance (the default).
  memory,

  /// Until the app is closed.
  session,

  /// In the built-in store.
  persisted,

  /// In the built-in secure store.
  secure,
}

/// A stored value: its JSON form and the fingerprint of the type it was
/// stored under.
typedef StoredValue = ({String fingerprint, Object? value});

/// The session, persisted and secure values of one runtime.
final class StatePersistence {
  /// Creates the persistence over [persisted] and [secure]; [report]
  /// receives failures, which name operations, never values.
  StatePersistence({
    required PluxKeyValueStore persisted,
    required PluxKeyValueStore secure,
    required this.report,
  }) : _stores = {Persistence.persisted: persisted, Persistence.secure: secure};

  /// Persistence kept only in memory, for a runtime without storage and
  /// for tests.
  StatePersistence.inMemory() : _stores = const {}, report = _ignore;

  static void _ignore(PluxException _) {}

  final Map<Persistence, PluxKeyValueStore> _stores;

  /// Receives failures; they name operations, never values.
  final void Function(PluxException error) report;
  final Map<Persistence, Map<String, Object?>> _values = {
    Persistence.session: {},
    Persistence.persisted: {},
    Persistence.secure: {},
  };
  final Set<Persistence> _dirty = {};
  Future<void>? _saving;
  bool _scheduled = false;

  /// Loads the stored values; a store that fails starts empty, reported.
  Future<void> load() async {
    for (final MapEntry(key: kind, value: store) in _stores.entries) {
      try {
        _values[kind] = {...await store.load(), ..._values[kind]!};
      } on StoreException catch (e) {
        _fail(e);
      }
    }
  }

  /// The value stored under [key], or null.
  StoredValue? read(Persistence kind, String key) {
    final r = _values[kind]?[key];
    if (r is! Map<String, Object?>) return null;
    final f = r['f'];
    return f is String ? (fingerprint: f, value: r['v']) : null;
  }

  /// Stores [value], in JSON form, under [key] with its type's
  /// [fingerprint]; persisted and secure values are saved behind.
  void write(Persistence kind, String key, String fingerprint, Object? value) {
    final m = _values[kind];
    if (m == null) return;
    m[key] = {'f': fingerprint, 'v': value};
    _changed(kind);
  }

  /// Removes the value under [key].
  void remove(Persistence kind, String key) {
    if (_values[kind]?.remove(key) != null) _changed(kind);
  }

  void _changed(Persistence kind) {
    if (!_stores.containsKey(kind)) return;
    _dirty.add(kind);
    if (_scheduled) return;
    _scheduled = true;
    // In the root zone, like the runtime's other background work, so a
    // save never waits on the zone that wrote.
    Zone.root.scheduleMicrotask(() {
      _scheduled = false;
      unawaited(flush());
    });
  }

  /// Saves every changed store; completes when they are saved.
  Future<void> flush() async {
    while (true) {
      final running = _saving;
      if (running != null) {
        await running;
        continue;
      }
      if (_dirty.isEmpty) return;
      final kinds = {..._dirty};
      _dirty.clear();
      final done = Completer<void>();
      _saving = done.future;
      try {
        for (final k in kinds) {
          try {
            await _stores[k]!.save({..._values[k]!});
          } on StoreException catch (e) {
            _fail(e);
          }
        }
      } finally {
        _saving = null;
        done.complete();
      }
    }
  }

  /// Removes every value, stored or not, and the stores with their keys
  /// (HST-001, `Plux.wipeData`).
  Future<void> wipe() async {
    await flush();
    for (final m in _values.values) {
      m.clear();
    }
    for (final s in _stores.values) {
      try {
        await s.wipe();
      } on StoreException catch (e) {
        _fail(e);
      }
    }
  }

  void _fail(StoreException e) => report(
    PluxException(switch (e.failure) {
      StoreFailure.unavailable => PluxErrorCode.stateStoreUnavailable,
      StoreFailure.corrupt => PluxErrorCode.stateStoreCorrupt,
      StoreFailure.tooLarge => PluxErrorCode.stateLimitExceeded,
    }, 'state store: ${e.message}'),
  );
}
