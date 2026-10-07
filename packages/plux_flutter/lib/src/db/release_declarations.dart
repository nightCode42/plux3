// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The database declarations of an active release (DB-004, DB-005): the
/// collections of the app and of each plugin that requires `db.v1`, read
/// from the bundles' schemas sections when first asked, with the
/// security profile and limits of the app bundle.
library;

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/db/schema.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart' show uuidString;

/// The declarations of [release], as [renderer] reads its bundles.
final class ReleaseDbDeclarations implements DbDeclarations {
  /// Creates the declarations.
  ReleaseDbDeclarations(this.release, this.renderer);

  /// The release.
  final ActiveRelease release;

  /// Reads the bundles.
  final PluxRenderer renderer;

  final Map<String, List<DbCollectionSchema>> _collections = {};

  @override
  int get sequence => release.sequence;

  @override
  bool get requireEncryption =>
      const {'strict', 'maximum'}.contains(release.meta('').securityProfile);

  @override
  late final Map<String, int> limits = release.limits;

  bool _usesDb(String plugin) =>
      release.meta(plugin).requiredFeatures?.contains('db.v1') ?? false;

  @override
  List<DbCollectionSchema> collectionsOf(String plugin) =>
      _collections[plugin] ??= _read(plugin);

  List<DbCollectionSchema> _read(String plugin) {
    if (!_usesDb(plugin)) return const [];
    final view = renderer.view(release, plugin);
    final ns = dbNamespace(plugin);
    return [for (final c in view.collections) _schema(view, ns, c)];
  }

  DbCollectionSchema _schema(BundleView view, String ns, fbs.Collection c) {
    final id = c.id == null ? '' : uuidString(uuidOf(c.id!));
    String s(int i) => view.string(i);
    return DbCollectionSchema(
      name: '$ns/$id',
      id: id,
      key: s(c.key),
      version: c.version == 0 ? 1 : c.version,
      fields: [
        for (final p in c.fields ?? const <fbs.Param>[])
          DbField(s(p.name), s(p.type)),
      ],
      primaryKey: [for (final k in c.primaryKey ?? const <int>[]) s(k)],
      indexes: [
        for (final i in c.indexes ?? const <fbs.Index>[])
          [for (final f in i.fields ?? const <int>[]) s(f)],
      ],
      migrations: [
        for (final m in c.migrations ?? const <fbs.CollectionMigration>[])
          DbMigrationPlan(
            from: m.from,
            rename: {
              for (final r in m.rename ?? const <fbs.FieldRename>[])
                s(r.to): s(r.from),
            },
            drop: [for (final d in m.drop ?? const <int>[]) s(d)],
            reset: [for (final d in m.reset ?? const <int>[]) s(d)],
          ),
      ],
    );
  }

  @override
  List<String> droppedOf(String plugin) {
    if (!_usesDb(plugin)) return const [];
    final ns = dbNamespace(plugin);
    return [
      for (final u in renderer.view(release, plugin).droppedCollections)
        '$ns/${uuidString(uuidOf(u))}',
    ];
  }

  @override
  NamedType? Function(String name) typesOf(String plugin) {
    final all = renderer.typesOf(release, plugin);
    return (name) => all[name];
  }
}
