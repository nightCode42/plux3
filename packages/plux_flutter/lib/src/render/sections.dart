// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Views of a bundle's sections for rendering (ADR-0031, RT-010): read in
/// place through the generated accessors over the mapped bytes, each
/// section checked once before its first read (BND-006), and nothing
/// decoded ahead of use.
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

/// Compares two 64-bit integers as unsigned, the order of `ulong` keys.
int compareUnsigned(int a, int b) => (a ^ _signBit).compareTo(b ^ _signBit);

const int _signBit = 1 << 63;

/// The UUID of a section ID or an `fbs.Uuid`, as two 64-bit halves.
typedef UuidKey = (int hi, int lo);

/// An app state entry: its name, type expression and default, whether
/// the host may read and write it (ADR-0023), and whether it declares a
/// persistence, which P5 brings.
typedef AppStateDecl = ({
  String name,
  String type,
  fbs.Value? defaultValue,
  bool exposed,
  bool persisted,
});

/// A declared parameter, prop or input of the native catalogue.
typedef NativeParam = ({String name, String type, bool required});

/// A native route's declaration: its parameters and result type.
typedef NativeRouteDecl = ({List<NativeParam> params, String? result});

/// A native slot's declaration: its props and events, in order.
typedef NativeSlotDecl = ({
  List<NativeParam> props,
  List<({String name, String? payload})> events,
});

/// A custom action's declaration: its inputs and output type.
typedef NativeActionDecl = ({List<NativeParam> inputs, String? output});

/// The key of a section ID.
UuidKey uuidOfId(Uint8List id) {
  final v = ByteData.sublistView(id);
  return (v.getUint64(0), v.getUint64(8));
}

/// The key of a UUID field.
UuidKey uuidOf(fbs.Uuid u) => (u.hi, u.lo);

int _compareUuid(UuidKey a, UuidKey b) {
  final c = compareUnsigned(a.$1, b.$1);
  return c != 0 ? c : compareUnsigned(a.$2, b.$2);
}

/// The plugin-wide sections of one bundle: strings, styles and tokens,
/// PXL programs, translations, pages and components.
final class BundleView {
  /// Views [bundle], checking sections through [gate].
  BundleView(this.bundle, this.gate);

  /// The bundle.
  final MappedBundle bundle;

  /// First-use section checks.
  final SectionGate gate;

  List<String?>? _strings;
  fbs.Strings? _stringsTable;
  fbs.Styles? _styles;
  bool _stylesRead = false;
  List<fbs.Program>? _programs;
  final Map<int, Program> _decoded = {};
  final Map<String, fbs.Locale?> _locales = {};

  Section? _single(int kind) {
    final s = bundle.container.ofKind(kind);
    if (s.isEmpty) return null;
    gate.check(s.first);
    return s.first;
  }

  /// The string at [index] of the strings section.
  String string(int index) {
    final strings = _strings ??= () {
      final s = _single(SectionKind.strings);
      _stringsTable = s == null ? null : fbs.Strings(s.data);
      return List<String?>.filled(_stringsTable?.strings?.length ?? 0, null);
    }();
    if (index < 0 || index >= strings.length) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'string $index is outside the strings section',
      );
    }
    return strings[index] ??= _stringsTable!.strings![index];
  }

  fbs.Styles? get _stylesTable {
    if (!_stylesRead) {
      _stylesRead = true;
      final s = _single(SectionKind.styles);
      _styles = s == null ? null : fbs.Styles(s.data);
    }
    return _styles;
  }

  /// The style with content-addressed [id].
  fbs.Style style(int id) {
    final list = _stylesTable?.styles;
    final i = list == null
        ? -1
        : _search(list.length, (i) => compareUnsigned(list[i].id, id));
    if (i < 0) {
      throw PluxException(PluxErrorCode.bundleMalformed, 'no style $id');
    }
    return list![i];
  }

  /// The design token at [path], or null (app bundles only).
  fbs.Token? token(String path) {
    final list = _stylesTable?.tokens;
    if (list == null) return null;
    final i = _search(list.length, (i) => (list[i].path ?? '').compareTo(path));
    return i < 0 ? null : list[i];
  }

  Map<String, fbs.Asset>? _assets;

  /// The bundle's assets-index by asset ID, read once.
  Map<String, fbs.Asset> _assetIndex() => _assets ??= () {
    final s = _single(SectionKind.assetsIndex);
    return {
      for (final a
          in s == null
              ? const <fbs.Asset>[]
              : fbs.AssetIndex(s.data).assets ?? const <fbs.Asset>[])
        if (a.id case final i?) uuidString(uuidOf(i)): a,
    };
  }();

  /// The asset with ID [id] (a UUID string), or null.
  fbs.Asset? asset(String id) => _assetIndex()[id];

  /// The asset with key [key], or null.
  fbs.Asset? assetByKey(String key) {
    for (final a in _assetIndex().values) {
      if (a.key == key) return a;
    }
    return null;
  }

  /// The paths of the design tokens that start with [prefix], in order.
  Iterable<String> tokenPaths(String prefix) sync* {
    final list = _stylesTable?.tokens;
    if (list == null) return;
    var lo = 0, hi = list.length;
    while (lo < hi) {
      final mid = (lo + hi) >> 1;
      if ((list[mid].path ?? '').compareTo(prefix) < 0) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    for (var i = lo; i < list.length; i++) {
      final path = list[i].path ?? '';
      if (!path.startsWith(prefix)) return;
      yield path;
    }
  }

  /// The PXL program with content-addressed [id], decoded once.
  Program program(int id) {
    final have = _decoded[id];
    if (have != null) return have;
    final list = _programs ??= () {
      final s = _single(SectionKind.pxl);
      return s == null
          ? const <fbs.Program>[]
          : fbs.Programs(s.data).programs ?? const <fbs.Program>[];
    }();
    final i = _search(list.length, (i) => compareUnsigned(list[i].id, id));
    if (i < 0) {
      throw PluxException(PluxErrorCode.bundleMalformed, 'no program $id');
    }
    final code = list[i].code;
    try {
      return _decoded[id] = Program.decode(
        Uint8List.fromList(code ?? const []),
      );
    } on InvalidProgram catch (e) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'program $id: ${e.message}',
      );
    }
  }

  List<fbs.Graph>? _graphs;

  /// The action graph with [id] from the actions section, or null
  /// (ADR-0039). Graphs are sorted by ID.
  fbs.Graph? graph(UuidKey id) {
    final list = _graphs ??= () {
      final s = _single(SectionKind.actions);
      return s == null
          ? const <fbs.Graph>[]
          : fbs.Actions(s.data).graphs ?? const <fbs.Graph>[];
    }();
    final i = _search(list.length, (i) {
      final g = list[i].id;
      return g == null ? -1 : _compareUuid(uuidOf(g), id);
    });
    return i < 0 ? null : list[i];
  }

  /// The flow [key]: a graph of the actions section that belongs to no
  /// page (ACT-061), or null.
  fbs.Graph? flow(String key) {
    graph((0, 0));
    for (final g in _graphs ?? const <fbs.Graph>[]) {
      if (g.page == null && g.key != 0 && string(g.key) == key) return g;
    }
    return null;
  }

  /// The translation [key] in locale [tag], or null.
  String? message(String tag, UuidKey key) {
    final locale = _locales.putIfAbsent(tag, () {
      final id = Uint8List(16)..setAll(0, tag.codeUnits.take(16));
      for (final s in bundle.container.ofKind(SectionKind.l10n)) {
        if (_sameBytes(s.id, id)) {
          gate.check(s);
          return fbs.Locale(s.data);
        }
      }
      return null;
    });
    final list = locale?.messages;
    if (list == null) return null;
    final i = _search(list.length, (i) {
      final k = list[i].key;
      return k == null ? -1 : _compareUuid(uuidOf(k), key);
    });
    return i < 0 ? null : list[i].text;
  }

  Map<String, NamedType>? _types;

  /// The types this bundle declares (SCH-010), by name.
  Map<String, NamedType> get types => _types ??= () {
    final s = _single(SectionKind.schemas);
    final decls = s == null
        ? const <fbs.TypeDecl>[]
        : fbs.Schemas(s.data).types ?? const <fbs.TypeDecl>[];
    final out = <String, NamedType>{
      for (final d in decls)
        string(d.name): d.members != null && d.members!.isNotEmpty
            ? NamedType.enumeration(string(d.name), [
                for (final m in d.members!) string(m),
              ])
            : NamedType.object(string(d.name)),
    };
    return out;
  }();

  /// The user-context attributes the app declares (HST-011): name, type
  /// expression and whether each is sensitive (app bundles only).
  late final List<({String name, String type, bool sensitive})> userContext =
      () {
        final s = _single(SectionKind.schemas);
        final decls = s == null
            ? const <fbs.Param>[]
            : fbs.Schemas(s.data).userContext ?? const <fbs.Param>[];
        return [
          for (final p in decls)
            (
              name: string(p.name),
              type: string(p.type),
              sensitive: p.sensitive,
            ),
        ];
      }();

  /// The app's state entries (app bundles only), in document order:
  /// computed entries arrive with P5 and are left out (ADR-0023).
  late final List<AppStateDecl> appState = [
    for (final e in _schemas?.state ?? const <fbs.StateEntry>[])
      if (e.computed == 0)
        (
          name: string(e.name),
          type: string(e.type),
          defaultValue: e.$default,
          exposed: e.exposed,
          persisted: e.persistence != fbs.Persistence.Memory,
        ),
  ];

  /// The data sources the bundle's schemas section declares (ADR-0048).
  List<fbs.DataSource> get dataSources =>
      _schemas?.dataSources ?? const <fbs.DataSource>[];

  late final fbs.Schemas? _schemas = () {
    final s = _single(SectionKind.schemas);
    return s == null ? null : fbs.Schemas(s.data);
  }();

  List<NativeParam> _nativeParams(List<fbs.Param>? ps) => [
    for (final p in ps ?? const <fbs.Param>[])
      (name: string(p.name), type: string(p.type), required: p.$required),
  ];

  String? _typeOrNull(int index) => index == 0 ? null : string(index);

  /// The native routes of the catalogue the app was compiled against, by
  /// name (NAV-002, ADR-0041; app bundles only).
  late final Map<String, NativeRouteDecl> nativeRoutes = {
    for (final r in _schemas?.nativeRoutes ?? const <fbs.NativeRouteDecl>[])
      string(r.name): (
        params: _nativeParams(r.params),
        result: _typeOrNull(r.result),
      ),
  };

  /// The native slots, by type (WGT-033): props and events in the
  /// catalogue's order, which slot nodes address by index.
  late final Map<String, NativeSlotDecl> nativeSlots = {
    for (final s in _schemas?.nativeSlots ?? const <fbs.NativeSlotDecl>[])
      string(s.type): (
        props: _nativeParams(s.props),
        events: [
          for (final e in s.events ?? const <fbs.ComponentEvent>[])
            (name: string(e.name), payload: _typeOrNull(e.payload)),
        ],
      ),
  };

  /// The custom actions, by name (ACT-060).
  late final Map<String, NativeActionDecl> nativeActions = {
    for (final a in _schemas?.nativeActions ?? const <fbs.NativeActionDecl>[])
      string(a.name): (
        inputs: _nativeParams(a.inputs),
        output: _typeOrNull(a.output),
      ),
  };

  /// Fills in the fields of the object types in [all], which holds this
  /// bundle's types and those they may refer to; throws [FormatException].
  void resolveFields(Map<String, NamedType> all) {
    final s = _single(SectionKind.schemas);
    if (s == null) return;
    for (final d in fbs.Schemas(s.data).types ?? const <fbs.TypeDecl>[]) {
      final fields = all[string(d.name)]?.fields;
      if (fields == null) continue;
      for (final f in d.fields ?? const <fbs.Param>[]) {
        fields[string(f.name)] = PxlType.parse(string(f.type), (n) => all[n]);
      }
    }
  }

  /// The locales this bundle has translations for.
  List<String> get locales => [
    for (final s in bundle.container.ofKind(SectionKind.l10n))
      String.fromCharCodes(s.id.takeWhile((b) => b != 0)),
  ];

  /// The section of the page or component with [id], or null.
  Section? section(int kind, UuidKey id) {
    for (final s in bundle.container.ofKind(kind)) {
      if (uuidOfId(s.id) == id) return s;
    }
    return null;
  }
}

/// A binary search over indices; [compare] compares the element at an
/// index with the key. Returns the index, or -1.
int _search(int length, int Function(int i) compare) {
  var lo = 0, hi = length - 1;
  while (lo <= hi) {
    final mid = (lo + hi) >> 1;
    final c = compare(mid);
    if (c == 0) return mid;
    if (c < 0) {
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  return -1;
}

bool _sameBytes(Uint8List a, Uint8List b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// A page or component section: its node array and string table, read in
/// place, with strings decoded on first use.
final class NodeSection {
  NodeSection._(
    this.section,
    this.nodes,
    this._table,
    this.page,
    this.component,
  ) : _strings = List<String?>.filled(_table.length, null);

  /// Reads a page section (checked by the caller).
  factory NodeSection.page(Section s) {
    final p = fbs.Page(s.data);
    return NodeSection._(
      s,
      p.nodes ?? const [],
      p.strings ?? const [],
      p,
      null,
    );
  }

  /// Reads a component section (checked by the caller).
  factory NodeSection.component(Section s) {
    final c = fbs.Component(s.data);
    return NodeSection._(
      s,
      c.nodes ?? const [],
      c.strings ?? const [],
      null,
      c,
    );
  }

  /// The section.
  final Section section;

  /// The node array; element 0 is the root.
  final List<fbs.Node> nodes;

  /// The page, for a page section.
  final fbs.Page? page;

  /// The component, for a component section.
  final fbs.Component? component;

  final List<String> _table;
  final List<String?> _strings;

  /// The string at [index] of the section's own table.
  String string(int index) {
    if (index < 0 || index >= _strings.length) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'string $index is outside the section',
      );
    }
    return _strings[index] ??= _table[index];
  }

  /// The node at [index].
  fbs.Node node(int index) {
    if (index < 0 || index >= nodes.length) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'node $index is outside the section',
      );
    }
    return nodes[index];
  }

  /// The section's size, what the cache counts.
  int get bytes => section.data.length;
}

/// A bounded LRU of node sections keyed by section hash (RT-013), cleared
/// on memory pressure.
final class SectionCache {
  /// Creates a cache of at most [maxEntries] sections and [maxBytes].
  SectionCache({required this.maxEntries, required this.maxBytes});

  /// The most sections kept; changed by [resize].
  int maxEntries;

  /// The most bytes of sections kept; changed by [resize].
  int maxBytes;

  /// Applies the app's limits (LIM-004), evicting what no longer fits.
  void resize({required int maxEntries, required int maxBytes}) {
    this.maxEntries = maxEntries;
    this.maxBytes = maxBytes;
    _evict();
  }

  final Map<String, NodeSection> _entries = {};
  int _bytes = 0;

  /// Sections held.
  int get length => _entries.length;

  /// Bytes held.
  int get bytes => _bytes;

  /// The section for [s], read with [read] when not cached.
  NodeSection get(Section s, NodeSection Function() read) {
    final key = String.fromCharCodes(s.hash);
    final hit = _entries.remove(key);
    if (hit != null) {
      _entries[key] = hit; // most recently used last
      return hit;
    }
    final fresh = read();
    _entries[key] = fresh;
    _bytes += fresh.bytes;
    _evict();
    return fresh;
  }

  /// Drops the least recently used sections beyond the bounds; the newest
  /// stays even when it alone exceeds the byte bound.
  void _evict() {
    while (_entries.length > maxEntries ||
        (_bytes > maxBytes && _entries.length > 1)) {
      final oldest = _entries.keys.first;
      _bytes -= _entries.remove(oldest)!.bytes;
    }
  }

  /// Drops every entry.
  void clear() {
    _entries.clear();
    _bytes = 0;
  }
}
