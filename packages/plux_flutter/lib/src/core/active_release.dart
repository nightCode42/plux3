// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The release pages render from (ADR-0021): its bundles mapped from the
/// store (RT-010), each checked against the release record's bundle hash
/// when it is mapped, each section checked on first use (ADR-0029), and a
/// route index built lazily from the plugins' meta sections. Pages hold
/// leases; the mappings are released when the release is replaced and the
/// last lease ends, so a release is never swapped under a page (SYN-004).
library;

import 'dart:io';
import 'dart:typed_data';

import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/mmap/mapped_file.dart';
import 'package:plux_flutter/src/store/release_record.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// One mapped bundle.
final class MappedBundle {
  MappedBundle._(this.key, this.version, this._file, this.container);

  /// The plugin key; empty for the app bundle.
  final String key;

  /// The plugin version.
  final int version;

  final MappedFile _file;

  /// The container, read in place.
  final BundleContainer container;

  fbs.Meta? _meta;

  void _release() => _file.release();
}

/// A page's place: its plugin, its section and route.
final class PageRef {
  /// Creates a reference.
  const PageRef({
    required this.plugin,
    required this.pageKey,
    required this.route,
    required this.section,
  });

  /// The plugin key.
  final String plugin;

  /// The page key.
  final String pageKey;

  /// The app-wide route name (SCH-025).
  final String route;

  /// The page section, verified on first use.
  final Section section;
}

/// The active release.
final class ActiveRelease {
  ActiveRelease._(
    this.record,
    this._store,
    this.gate,
    this.control,
    this._report,
  );

  /// Opens release [record] from [store]: maps the app bundle and checks
  /// its header against the record (ADR-0029). Plugin bundles are mapped
  /// when first used; a bundle that fails its check is passed to [report]
  /// once and fails every later use the same way.
  factory ActiveRelease.open(
    ReleaseStore store,
    ReleaseRecord record,
    VerifierLimits limits, {
    void Function(PluxException error)? report,
  }) {
    final r = ActiveRelease._(
      record,
      store,
      SectionGate(limits),
      _control(store, record),
      report,
    );
    r.bundle('');
    return r;
  }

  /// The release record.
  final ReleaseRecord record;

  final ReleaseStore _store;

  /// First-use section checks (BND-006).
  final SectionGate gate;

  /// The control switches in force (RT-022).
  final ControlState control;

  final void Function(PluxException error)? _report;
  final Map<String, MappedBundle> _bundles = {};
  final Map<String, PluxException> _failed = {};
  Map<String, (String, fbs.PageEntry)>? _routes;
  int _leases = 0;
  bool _retired = false;

  /// The release sequence.
  int get sequence => record.sequence;

  /// Pages currently holding this release.
  int get leases => _leases;

  static ControlState _control(ReleaseStore store, ReleaseRecord r) {
    final c = store.control();
    if (c != null && c.sequence == r.sequence) return c;
    return ControlState(
      sequence: r.sequence,
      killSwitches: r.killSwitches,
      appKillSwitch: r.appKillSwitch,
      message: r.message,
    );
  }

  /// The bundle of plugin [key] (empty for the app bundle), mapped and its
  /// header checked on first use; throws [PluxException].
  MappedBundle bundle(String key) {
    final have = _bundles[key];
    if (have != null) return have;
    final failed = _failed[key];
    if (failed != null) throw failed;
    if (_retired) throw StateError('release $sequence has been released');
    final entry = record.bundles.where((b) => b.key == key).firstOrNull;
    if (entry == null) {
      throw PluxException(
        PluxErrorCode.resourceNotFound,
        'release $sequence has no plugin $key',
      );
    }
    final name = key.isEmpty ? 'the app' : key;
    final MappedFile file;
    try {
      file = MappedFile.open(_store.objectPath(ObjectKind.bundles, entry.hash));
    } on FileSystemException catch (e) {
      final error = PluxException(
        PluxErrorCode.bundleMalformed,
        'the stored bundle of $name cannot be read: ${e.message}',
      );
      _failed[key] = error;
      _report?.call(error);
      throw error;
    }
    try {
      final c = BundleContainer.parse(file.bytes);
      final expected = hexDecode(entry.hash);
      final actual = bundleHash(file.bytes, c.sections.length);
      var same = expected.length == actual.length;
      for (var i = 0; same && i < actual.length; i++) {
        same = expected[i] == actual[i];
      }
      if (!same) {
        throw PluxException(
          PluxErrorCode.bundleMalformed,
          'the stored bundle of $name is not the signed one',
        );
      }
      return _bundles[key] = MappedBundle._(key, entry.version, file, c);
    } on PluxException catch (e) {
      file.release();
      _failed[key] = e;
      _report?.call(e);
      rethrow;
    } on Object {
      file.release();
      rethrow;
    }
  }

  /// The meta section of a bundle, checked on first use.
  fbs.Meta meta(String key) {
    final b = bundle(key);
    final existing = b._meta;
    if (existing != null) return existing;
    final s = b.container.ofKind(SectionKind.meta).single;
    gate.check(s);
    return b._meta = fbs.Meta(s.data);
  }

  /// The stored file of the release's asset file [hash], or null when the
  /// release does not list it or the store lacks it (AST-001).
  String? assetPath(String hash) {
    if (!record.assets.contains(hash)) return null;
    final path = _store.objectPath(ObjectKind.assets, hash);
    return File(path).existsSync() ? path : null;
  }

  /// The telemetry sampling rates the app bundle carries, from 0 to 1 by
  /// event name (ANL-003, ADR-0034).
  Map<String, double> get telemetrySampling => {
    for (final e in meta('').telemetrySampling ?? const <fbs.Sampling>[])
      ?e.event: e.rate / 1000,
  };

  /// The limits the app bundle carries (LIM-004).
  Map<String, int> get limits => {
    for (final l in meta('').limits ?? const <fbs.Limit>[]) ?l.key: l.value,
  };

  /// The app's entry route, if it has one.
  String? get entryRoute => meta('').entryRoute;

  /// Resolves an app-wide route name to its page (SCH-025); null when no
  /// plugin has it (NAV-011 from P4).
  PageRef? page(String route) {
    final index = _routes ??= _index();
    final hit = index[route];
    if (hit == null) return null;
    final (plugin, entry) = hit;
    return _pageRef(plugin, entry, route);
  }

  /// The fallback page plugin [key] declares (RT-022), or null.
  PageRef? fallbackPage(String key) {
    final id = meta(key).fallbackPage;
    if (id == null) return null;
    final want = _uuid(id);
    final entry = (meta(key).pages ?? const <fbs.PageEntry>[])
        .where((p) => p.id != null && _same(_uuid(p.id!), want))
        .firstOrNull;
    if (entry == null) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'plugin $key names a fallback page it does not list',
      );
    }
    return _pageRef(key, entry, entry.route ?? '');
  }

  PageRef _pageRef(String plugin, fbs.PageEntry entry, String route) {
    final id = _uuid(entry.id!);
    final section = bundle(plugin).container
        .ofKind(SectionKind.page)
        .where((s) => _same(s.id, id))
        .firstOrNull;
    if (section == null) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'plugin $plugin lists page ${entry.key} without its section',
      );
    }
    return PageRef(
      plugin: plugin,
      pageKey: entry.key ?? '',
      route: route,
      section: section,
    );
  }

  Map<String, (String, fbs.PageEntry)> _index() {
    final out = <String, (String, fbs.PageEntry)>{};
    for (final b in record.bundles) {
      if (b.isApp) continue;
      for (final p in meta(b.key).pages ?? const <fbs.PageEntry>[]) {
        final route = p.route;
        if (route != null && p.id != null) out[route] = (b.key, p);
      }
    }
    return out;
  }

  /// Whether plugin [key] is switched off (RT-022).
  bool disabled(String key) =>
      control.appKillSwitch || control.killSwitches.contains(key);

  /// Takes a lease for a page.
  void acquire() => _leases++;

  /// Returns a lease; the last one of a retired release unmaps it.
  void releaseLease() {
    if (_leases > 0) _leases--;
    if (_retired && _leases == 0) _unmap();
  }

  /// Marks this release replaced; it unmaps when no page holds it.
  void retire() {
    _retired = true;
    if (_leases == 0) _unmap();
  }

  void _unmap() {
    for (final b in _bundles.values) {
      b._release();
    }
    _bundles.clear();
  }

  static Uint8List _uuid(fbs.Uuid u) {
    final b = ByteData(16)
      ..setUint64(0, u.hi)
      ..setUint64(8, u.lo);
    return b.buffer.asUint8List();
  }

  static bool _same(Uint8List a, Uint8List b) {
    for (var i = 0; i < 16; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }
}
