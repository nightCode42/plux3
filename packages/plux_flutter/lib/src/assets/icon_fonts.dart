// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The icon fonts of a release (THM-005, ADR-0032 § Icons): the server
/// subsets Material Symbols and Cupertino icons to the icons each bundle
/// uses and indexes the font as the bundle's asset `@icons/<set>`, with
/// the icons' names in its `Plux` table. The runtime loads a font the
/// first time a page needs it, checked against its hash, under a family of
/// its own, and draws icons as glyphs of it.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/assets/font_name.dart';
import 'package:plux_flutter/src/assets/fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The assets-index key of [set]'s icon font. Document asset keys are
/// slugs, so it is never one of theirs.
String iconFontKey(String set) => '@icons/$set';

/// Whether [key] is an icon font's.
bool isIconFontKey(String key) => key.startsWith('@icons/');

/// An icon: a glyph of an icon font.
@immutable
final class IconGlyph {
  /// Creates the glyph.
  const IconGlyph(this.codePoint, this.family, {required this.mirrored});

  /// The character the font draws the icon for.
  final int codePoint;

  /// The family the font is loaded under.
  final String family;

  /// Whether the icon is mirrored in right-to-left text.
  final bool mirrored;

  @override
  bool operator ==(Object other) =>
      other is IconGlyph &&
      other.codePoint == codePoint &&
      other.family == family &&
      other.mirrored == mirrored;

  @override
  int get hashCode => Object.hash(codePoint, family, mirrored);
}

/// The icons an icon font names in its `Plux` table — one line of name,
/// hexadecimal code point and `1` when mirrored (else `0`) each — or null
/// when it has no readable table.
Map<String, (int, bool)>? iconFontNames(Uint8List font) {
  final table = fontTable(font, 'Plux');
  if (table == null) return null;
  final out = <String, (int, bool)>{};
  try {
    for (final line in const LineSplitter().convert(utf8.decode(table))) {
      final parts = line.split(' ');
      final code = parts.length == 3 ? int.tryParse(parts[1], radix: 16) : null;
      if (code == null) return null;
      out[parts[0]] = (code, parts[2] == '1');
    }
  } on FormatException {
    return null;
  }
  return out;
}

/// The icon fonts this process has loaded, by file hash. It notifies its
/// listeners when a font finishes loading, so icons drawn before then are
/// drawn again.
final class IconFonts extends ChangeNotifier {
  /// Creates the registry; [report] receives a font that cannot be used.
  IconFonts(this._verified, {required this._report, this._load = fontLoader});

  final VerifiedAssets _verified;
  final void Function(PluxException error) _report;
  final FamilyLoader _load;
  final Map<String, Future<void>> _loading = {};
  final Map<String, (String, Map<String, (int, bool)>)> _fonts = {};

  /// The icon [name] of the font file [hash] stored at [path]; null while
  /// the font loads (it starts loading) and when it cannot be used or does
  /// not name the icon.
  IconGlyph? glyph(String hash, String path, String name) {
    final font = _fonts[hash];
    if (font == null) {
      unawaited(load(hash, path));
      return null;
    }
    final (family, names) = font;
    final entry = names[name];
    return entry == null
        ? null
        : IconGlyph(entry.$1, family, mirrored: entry.$2);
  }

  /// Loads the font file [hash] stored at [path], once; a font that
  /// cannot be used is reported and draws no icons.
  Future<void> load(String hash, String path) =>
      _loading[hash] ??= _loadFont(hash, path);

  Future<void> _loadFont(String hash, String path) async {
    try {
      final bytes = await File(path).readAsBytes();
      await _verified.check(path, hash, bytes);
      final names = iconFontNames(bytes);
      if (names == null) {
        throw PluxException(
          PluxErrorCode.propValueInvalid,
          'the icon font $hash names no icons',
        );
      }
      final family = 'plux-icons-${hash.substring(0, 16)}';
      await _load(family, [bytes]);
      _fonts[hash] = (family, names);
    } on PluxException catch (e) {
      _fonts[hash] = ('', const {});
      _report(e);
    } on FileSystemException catch (e) {
      _fonts[hash] = ('', const {});
      _report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          'the icon font $hash cannot be read: ${e.message}',
        ),
      );
    }
    notifyListeners();
  }
}

/// An icon a node shows, resolved against the release's icon font.
final class PluxIconSource {
  /// Creates the source of icon [name] of the font file [hash] at [path].
  const PluxIconSource(this._fonts, this._hash, this._path, this.name);

  final IconFonts _fonts;
  final String _hash;
  final String _path;

  /// The icon's name.
  final String name;

  /// Notifies when the font has loaded.
  Listenable get changes => _fonts;

  /// The glyph, or null while the font loads or when it cannot be drawn.
  IconGlyph? get glyph => _fonts.glyph(_hash, _path, name);

  /// Loads the font, if it is not loaded yet.
  Future<void> load() => _fonts.load(_hash, _path);
}
