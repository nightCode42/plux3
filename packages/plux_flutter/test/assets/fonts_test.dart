// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/assets/font_name.dart';
import 'package:plux_flutter/src/assets/fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// A font file holding only a `name` table with [names]: (platform,
/// language, name ID, text) records.
Uint8List fontWith(List<(int, int, int, String)> names) {
  final strings = BytesBuilder();
  final records = <List<int>>[];
  for (final (platform, language, id, text) in names) {
    final bytes = platform == 1
        ? latin1.encode(text)
        : [
            for (final u in text.codeUnits) ...[u >> 8, u & 0xff],
          ];
    records.add([platform, 0, language, id, bytes.length, strings.length]);
    strings.add(bytes);
  }
  final table = BytesBuilder();
  final header = ByteData(6 + records.length * 12)
    ..setUint16(0, 0)
    ..setUint16(2, records.length)
    ..setUint16(4, 6 + records.length * 12);
  for (final (i, r) in records.indexed) {
    for (final (j, v) in r.indexed) {
      header.setUint16(6 + i * 12 + j * 2, v);
    }
  }
  table
    ..add(header.buffer.asUint8List())
    ..add(strings.takeBytes());
  final body = table.takeBytes();
  final file = ByteData(12 + 16 + body.length)
    ..setUint32(0, 0x00010000)
    ..setUint16(4, 1);
  final out = file.buffer.asUint8List()
    ..setAll(12, ascii.encode('name'))
    ..setAll(28, body);
  file
    ..setUint32(12 + 8, 28)
    ..setUint32(12 + 12, body.length);
  return out;
}

void main() {
  test('a font is named by its typographic family, else its family, '
      'preferring Windows US English [THM-004]', () {
    expect(
      fontFamilyName(
        fontWith([
          (1, 0, 1, 'Mac Family'),
          (3, 0x407, 1, 'Deutsche Familie'),
          (3, 0x409, 1, 'Noto Sans Ethiopic Bold'),
        ]),
      ),
      'Noto Sans Ethiopic Bold',
    );
    expect(
      fontFamilyName(
        fontWith([
          (3, 0x409, 1, 'Noto Sans Ethiopic Bold'),
          (3, 0x409, 16, 'Noto Sans Ethiopic'),
        ]),
      ),
      'Noto Sans Ethiopic',
    );
    expect(fontFamilyName(fontWith([(1, 0, 1, 'Mac Only')])), 'Mac Only');
    expect(fontFamilyName(fontWith([(3, 0x409, 2, 'Regular')])), isNull);
    expect(fontFamilyName(Uint8List(8)), isNull);
    expect(fontFamilyName(Uint8List(3)), isNull);
  });

  test('font files load once each, grouped by family, and a bad one is '
      'reported while the rest load [THM-004] [AST-001]', () async {
    final dir = Directory.systemTemp.createTempSync('plux_fonts');
    addTearDown(() => dir.deleteSync(recursive: true));
    String put(Uint8List bytes) {
      final hash = sha256.convert(bytes).toString();
      File('${dir.path}/$hash').writeAsBytesSync(bytes);
      return hash;
    }

    final regular = put(fontWith([(3, 0x409, 1, 'Brand')]));
    final bold = put(fontWith([(3, 0x409, 1, 'Brand'), (3, 0x409, 2, 'B')]));
    final other = put(fontWith([(3, 0x409, 1, 'Other')]));
    final nameless = put(fontWith([(3, 0x409, 2, 'Regular')]));
    final loads = <String, int>{};
    Future<void> load(String family, List<Uint8List> files) async =>
        loads[family] = files.length;
    final loaded = <String>{};
    final verified = VerifiedAssets();
    await expectLater(
      loadFonts(
        {
          for (final h in [regular, bold, other, nameless]) h: '${dir.path}/$h',
        },
        loaded,
        verified,
        load: load,
      ),
      throwsA(isA<PluxException>()),
    );
    expect(loads, {'Brand': 2, 'Other': 1});
    expect(loaded, {regular, bold, other});
    loads.clear();
    await loadFonts(
      {regular: '${dir.path}/$regular'},
      loaded,
      verified,
      load: load,
    );
    expect(loads, isEmpty, reason: 'already loaded');
    final tampered = sha256.convert([1]).toString();
    File('${dir.path}/$tampered').writeAsBytesSync([2]);
    await expectLater(
      loadFonts(
        {tampered: '${dir.path}/$tampered'},
        loaded,
        verified,
        load: load,
      ),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.assetHashMismatch,
        ),
      ),
    );
  });
}
