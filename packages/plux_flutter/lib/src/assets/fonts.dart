// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The app's font assets, registered under the families their files name
/// (THM-004, docs/reference/theming.md §5).
library;

import 'dart:io';

import 'package:flutter/services.dart';
import 'package:plux_flutter/src/assets/font_name.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// Registers font files as one family; Flutter's `FontLoader` by default.
typedef FamilyLoader = Future<void> Function(
  String family,
  List<Uint8List> files,
);

/// Loads with Flutter's `FontLoader`.
Future<void> fontLoader(String family, List<Uint8List> files) {
  final loader = FontLoader(family);
  for (final f in files) {
    loader.addFont(Future.value(ByteData.sublistView(f)));
  }
  return loader.load();
}

/// Loads the font [files] (hash → stored path) not in [loaded], each
/// checked against its hash, grouped by the family its `name` table
/// gives; adds what it loaded to [loaded]. Throws [PluxException] for a
/// file that fails its check or names no family; the rest still load.
Future<void> loadFonts(
  Map<String, String> files,
  Set<String> loaded,
  VerifiedAssets verified, {
  FamilyLoader load = fontLoader,
}) async {
  final byFamily = <String, List<Uint8List>>{};
  PluxException? failure;
  for (final MapEntry(key: hash, value: path) in files.entries) {
    if (loaded.contains(hash)) continue;
    try {
      final bytes = await File(path).readAsBytes();
      await verified.check(path, hash, bytes);
      final family = fontFamilyName(bytes);
      if (family == null) {
        throw PluxException(
          PluxErrorCode.propValueInvalid,
          'the font asset file $hash names no family',
        );
      }
      (byFamily[family] ??= []).add(bytes);
      loaded.add(hash);
    } on PluxException catch (e) {
      failure ??= e;
    }
  }
  for (final MapEntry(key: family, value: list) in byFamily.entries) {
    await load(family, list);
  }
  if (failure != null) throw failure;
}
