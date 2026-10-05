// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The values of one state scope instance (STA-001): `app`, `plugin`,
/// `page`, `component` or `run`. Each is a Riverpod notifier holding an
/// immutable map replaced on every write, so bindings keep subscribing with
/// `select` to the exact paths they read (STA-010, RT-012). Writes are
/// checked against the entry's declared type (STA-002); computed entries
/// are evaluated when a value they read changes, and only then (STA-004);
/// session, persisted and secure entries are kept by [StatePersistence],
/// versioned by their type's fingerprint and migrated when it changed
/// (STA-003, STA-040).
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show ProviderListenable;
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/state/persistence.dart';

/// The scopes of state (STA-001); each is also the PXL root its entries are
/// read under.
enum StateScopeKind {
  /// Shared by every plugin.
  app,

  /// One plugin's.
  plugin,

  /// One page instance's.
  page,

  /// One component instance's.
  component,

  /// One action run's variables.
  run,
}

/// One state entry as its bundle declares it.
final class StateDecl {
  /// Creates a declaration.
  const StateDecl({
    required this.name,
    required this.id,
    required this.typeExpr,
    required this.type,
    required this.defaultValue,
    this.computed,
    this.persistence = Persistence.memory,
    this.sensitive = false,
    this.exposed = false,
    this.fingerprint = '',
    this.migrationFrom = '',
    this.migrationType,
    this.migration,
    this.migrationReset = false,
  });

  /// The entry's name.
  final String name;

  /// The entry's UUID, which keys its stored value.
  final String id;

  /// The declared type expression.
  final String typeExpr;

  /// The declared type, or null when it cannot be parsed.
  final PxlType? type;

  /// The declared default, in PXL form.
  final Object? defaultValue;

  /// The program of a computed entry.
  final Program? computed;

  /// How the entry persists.
  final Persistence persistence;

  /// Whether the value is sensitive (SCH-012): it never appears in a
  /// message.
  final bool sensitive;

  /// Whether the host may read and write it (STA-030).
  final bool exposed;

  /// The version of a stored entry's type (STA-040).
  final String fingerprint;

  /// The fingerprint of the type the migration reads, or ''.
  final String migrationFrom;

  /// The type the migration reads as `previous`.
  final String? migrationType;

  /// The migration's program.
  final Program? migration;

  /// Whether a value of another type starts from the default.
  final bool migrationReset;

  /// Whether the entry is computed, and so read-only.
  bool get isComputed => computed != null;
}

/// Decodes the state [entries] of a bundle section, whose strings are in
/// [strings] and programs in [bundle]; [literal] resolves a default and
/// [types] holds the named types the entries may use.
List<StateDecl> decodeState(
  List<fbs.StateEntry>? entries, {
  required BundleView bundle,
  required StringTable strings,
  required Map<String, NamedType> types,
  required Object? Function(fbs.Value? v) literal,
}) {
  PxlType? parse(String src) {
    try {
      return PxlType.parse(src, (n) => types[n]);
    } on FormatException {
      return null;
    }
  }

  String str(int i) => i == 0 ? '' : strings(i);
  return [
    for (final e in entries ?? const <fbs.StateEntry>[])
      StateDecl(
        name: strings(e.name),
        id: e.id == null ? '' : uuidString(uuidOf(e.id!)),
        typeExpr: strings(e.type),
        type: parse(strings(e.type)),
        defaultValue: e.computed != 0 ? null : literal(e.$default),
        computed: e.computed == 0 ? null : bundle.program(e.computed),
        persistence: switch (e.persistence) {
          fbs.Persistence.Session => Persistence.session,
          fbs.Persistence.Persisted => Persistence.persisted,
          fbs.Persistence.Secure => Persistence.secure,
          _ => Persistence.memory,
        },
        sensitive: e.sensitive,
        exposed: e.exposed,
        fingerprint: str(e.fingerprint),
        migrationFrom: str(e.migrationFrom),
        migrationType: e.migrationType == 0 ? null : strings(e.migrationType),
        migration: e.migration == 0 ? null : bundle.program(e.migration),
        migrationReset: e.migrationReset,
      ),
  ];
}

/// Evaluates a program against roots; throws [BindingError].
typedef Evaluate = Object? Function(
  Program program,
  Map<String, Object?> roots,
);

/// What a scope instance's notifier needs.
final class ScopeModel {
  /// Creates a model.
  ScopeModel({
    required this.kind,
    required this.owner,
    required List<StateDecl> decls,
    required this.evaluate,
    required this.types,
    this.persistence,
    this.report,
    this.parents = const {},
    this.extraRoots,
  }) : decls = List.unmodifiable(decls),
       byName = {for (final d in decls) d.name: d};

  /// The scope.
  final StateScopeKind kind;

  /// The owning bundle's key ('' for the app), which prefixes stored keys.
  final String owner;

  /// The entries, in declaration order.
  final List<StateDecl> decls;

  /// The entries by name.
  final Map<String, StateDecl> byName;

  /// Evaluates computed entries and migrations.
  final Evaluate evaluate;

  /// The named types values may have.
  final Map<String, NamedType> types;

  /// Where session, persisted and secure values live; none keeps every
  /// value in memory.
  final StatePersistence? persistence;

  /// Reports problems.
  final void Function(PluxException error)? report;

  /// The other scopes computed entries may read, by root.
  final Map<String, ProviderListenable<Map<String, Object?>>> parents;

  /// Further roots computed entries may read (`params`, `props`, …).
  final Map<String, Object?> Function()? extraRoots;

  /// The root this scope's entries are read under.
  String get root => kind.name;

  /// The key of [d]'s stored value.
  String keyOf(StateDecl d) => '$owner:${d.id}';
}

/// A refused state write: [code] says why; the message never holds a
/// sensitive value.
final class StateWriteException implements Exception {
  /// Creates the exception.
  const StateWriteException(this.code, this.message);

  /// `PLX-5301` for a value of the wrong type, `PLX-5302` otherwise.
  final PluxErrorCode code;

  /// What was refused.
  final String message;

  @override
  String toString() => 'StateWriteException(${code.id}): $message';
}

/// The value [v] as a value of [d]'s type, normalised; throws
/// [StateWriteException] (PLX-5301) when it does not have the type.
Object? conformTo(StateDecl d, Object? v) {
  final t = d.type;
  // Registry enums carry their permanent value IDs.
  if (t == null || (t.kind == PxlKind.enumeration && v is int)) return v;
  try {
    return fromJson(t, toJson(v));
  } on FormatException catch (e) {
    throw StateWriteException(
      PluxErrorCode.stateWriteTypeMismatch,
      d.sensitive
          ? 'state ${d.name} is a ${d.typeExpr}; the value is not'
          : 'state ${d.name} is a ${d.typeExpr}: ${e.message}',
    );
  }
}

/// The value at [path] under [v], or null.
Object? valueAt(Object? v, Iterable<String> path) {
  var cur = v;
  for (final k in path) {
    if (cur is! Map<String, Object?>) return null;
    cur = cur[k];
  }
  return cur;
}

/// Deep equality of state values.
bool sameValue(Object? a, Object? b) {
  if (identical(a, b)) return true;
  if (a is List<Object?> && b is List<Object?>) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!sameValue(a[i], b[i])) return false;
    }
    return true;
  }
  if (a is Map<String, Object?> && b is Map<String, Object?>) {
    if (a.length != b.length) return false;
    for (final e in a.entries) {
      if (!b.containsKey(e.key) || !sameValue(e.value, b[e.key])) return false;
    }
    return true;
  }
  return a == b;
}

/// The values of a scope instance, with typed writes, memoised computed
/// entries and persistence. A notifier mixes it in, provides [model] and
/// calls [initialValues] from its `build`.
mixin ScopeValues on Notifier<Map<String, Object?>> {
  /// The scope's declarations and services, or null for none.
  ScopeModel? get model;

  /// Evaluations of computed entries so far: they run only when a value
  /// they read has changed (STA-004).
  int computations = 0;

  final Map<String, List<Object?>> _deps = {};

  /// The values the scope starts with: each entry's stored value, migrated
  /// when its type changed, else its default, or the value in [keep] that
  /// a new release keeps; then the computed entries. Listens to the other
  /// scopes computed entries read.
  Map<String, Object?> initialValues({Map<String, Object?> keep = const {}}) {
    final m = model;
    if (m == null) return const {};
    _deps.clear();
    final values = <String, Object?>{};
    for (final d in m.decls) {
      if (d.isComputed) continue;
      values[d.name] = keep.containsKey(d.name) ? keep[d.name] : _stored(m, d);
    }
    final roots = <String>{
      for (final d in m.decls)
        if (d.computed case final p?)
          for (final r in p.reads) r.split('.').first,
    };
    for (final MapEntry(:key, :value) in m.parents.entries) {
      if (!roots.contains(key)) continue;
      ref.listen(value, (_, _) {
        final next = _withComputed(state);
        if (!identical(next, state)) state = next;
      });
    }
    return _withComputed(values);
  }

  /// The value [d] starts with: stored, migrated or its default.
  Object? _stored(ScopeModel m, StateDecl d) {
    final p = m.persistence;
    if (p == null || d.persistence == Persistence.memory) return d.defaultValue;
    final r = p.read(d.persistence, m.keyOf(d));
    if (r == null) return d.defaultValue;
    Object? migrated() {
      if (r.fingerprint == d.fingerprint) {
        return conformTo(d, fromJson(d.type!, r.value));
      }
      if (d.migrationReset) return d.defaultValue;
      final prog = d.migration;
      final from = d.migrationType;
      if (prog == null || from == null || r.fingerprint != d.migrationFrom) {
        throw const FormatException('no migration reads the stored type');
      }
      final previous = fromJson(
        PxlType.parse(from, (n) => m.types[n]),
        r.value,
      );
      final v = conformTo(
        d,
        toPxl(m.evaluate(prog, {..._roots(m, const {}), 'previous': previous})),
      );
      p.write(d.persistence, m.keyOf(d), d.fingerprint, toJson(v));
      return v;
    }

    try {
      return migrated();
    } on Object {
      m.report?.call(
        PluxException(
          PluxErrorCode.stateMigrationFailed,
          'state ${d.name} starts from its default: its stored value could not be read or migrated',
          details: {'state': d.name},
        ),
      );
      p.remove(d.persistence, m.keyOf(d));
      return d.defaultValue;
    }
  }

  Map<String, Object?> _roots(ScopeModel m, Map<String, Object?> own) => {
    ...?m.extraRoots?.call(),
    for (final MapEntry(:key, :value) in m.parents.entries)
      key: ref.read(value),
    m.root: own,
  };

  /// [values] with the computed entries whose reads changed evaluated
  /// again; [values] itself when none changed.
  Map<String, Object?> _withComputed(Map<String, Object?> values) {
    final m = model;
    if (m == null) return values;
    Map<String, Object?>? out;
    for (final d in m.decls) {
      final prog = d.computed;
      if (prog == null) continue;
      final roots = _roots(m, out ?? values);
      final deps = [for (final r in prog.reads) valueAt(roots, r.split('.'))];
      final was = _deps[d.name];
      if (was != null && sameValue(was, deps)) continue;
      _deps[d.name] = deps;
      computations++;
      Object? v;
      try {
        v = toPxl(m.evaluate(prog, roots));
      } on BindingError catch (e) {
        m.report?.call(
          PluxException(
            PluxErrorCode.propValueInvalid,
            'computed state ${d.name}: ${e.message}',
            details: {'state': d.name},
          ),
        );
      }
      if (out == null && sameValue(values[d.name], v)) continue;
      (out ??= {...values})[d.name] = v;
    }
    return out ?? values;
  }

  StateDecl _writable(String name) {
    final d = model?.byName[name];
    if (d == null) {
      throw StateWriteException(
        PluxErrorCode.stateWriteRefused,
        'the ${model?.root ?? 'scope'} state has no entry $name',
      );
    }
    if (d.isComputed) {
      throw StateWriteException(
        PluxErrorCode.stateWriteRefused,
        'state $name is computed, so it cannot be written',
      );
    }
    return d;
  }

  /// Writes [value] to entry [name] (setState); throws
  /// [StateWriteException] when the entry does not exist, is computed, or
  /// the value does not have its type.
  void writeEntry(String name, Object? value) {
    final d = _writable(name);
    _commit(d, conformTo(d, value));
  }

  /// Merges [patch] into object entry [name] (patchState).
  void patchEntry(String name, Map<String, Object?> patch) {
    final d = _writable(name);
    final current = state[name];
    if (current is! Map<String, Object?>) {
      throw StateWriteException(
        PluxErrorCode.stateWriteRefused,
        'state $name is not an object to patch',
      );
    }
    _commit(d, conformTo(d, {...current, ...patch}));
  }

  /// Resets entry [name] to its declared default (resetState).
  void resetEntry(String name) {
    final d = _writable(name);
    _commit(d, d.defaultValue);
  }

  void _commit(StateDecl d, Object? v) {
    final m = model!;
    state = _withComputed({...state, d.name: v});
    if (d.persistence != Persistence.memory) {
      m.persistence?.write(d.persistence, m.keyOf(d), d.fingerprint, toJson(v));
    }
  }

  /// Starts every entry again from its default, after `Plux.wipeData`.
  void restart() {
    if (model != null) state = initialValues();
  }

  /// Replaces entry [name] with [value], already in its declared type,
  /// without storing it: the host's checked writes and tests.
  void set(String name, Object? value) =>
      state = _withComputed({...state, name: value});
}
