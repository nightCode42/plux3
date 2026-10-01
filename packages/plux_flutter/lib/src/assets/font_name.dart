// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The family name of a TrueType or OpenType font file, read from its
/// `name` table, so that a font asset is registered under the name a
/// `fontFamily` token uses (THM-004, docs/reference/theming.md).
library;

import 'dart:convert';
import 'dart:typed_data';

/// The typographic family (name ID 16), else the family (name ID 1), of
/// [font], preferring Windows Unicode entries in US English; null when
/// the file has no readable `name` table.
String? fontFamilyName(Uint8List font) {
  final table = fontTable(font, 'name');
  if (table == null) return null;
  try {
    return _family(ByteData.sublistView(table), 0);
  } on RangeError {
    return null;
  }
}

/// Table [tag] of the TrueType or OpenType [font], or null when it has
/// none or its table directory is damaged.
Uint8List? fontTable(Uint8List font, String tag) {
  try {
    final d = ByteData.sublistView(font);
    final tables = d.getUint16(4);
    for (var i = 0; i < tables; i++) {
      final rec = 12 + i * 16;
      if (String.fromCharCodes(font, rec, rec + 4) != tag) continue;
      final offset = d.getUint32(rec + 8), length = d.getUint32(rec + 12);
      return Uint8List.sublistView(font, offset, offset + length);
    }
    return null;
  } on RangeError {
    return null;
  } on ArgumentError {
    return null;
  }
}

String? _family(ByteData d, int table) {
  final count = d.getUint16(table + 2);
  final strings = table + d.getUint16(table + 4);
  String? best;
  var bestRank = 0;
  for (var i = 0; i < count; i++) {
    final r = table + 6 + i * 12;
    final platform = d.getUint16(r);
    final language = d.getUint16(r + 4);
    final nameId = d.getUint16(r + 6);
    if (nameId != 1 && nameId != 16) continue;
    final length = d.getUint16(r + 8);
    final start = strings + d.getUint16(r + 10);
    final bytes = d.buffer.asUint8List(d.offsetInBytes + start, length);
    final String text;
    if (platform == 3 || platform == 0) {
      final units = [
        for (var j = 0; j + 1 < length; j += 2) (bytes[j] << 8) | bytes[j + 1],
      ];
      text = String.fromCharCodes(units);
    } else if (platform == 1) {
      text = latin1.decode(bytes);
    } else {
      continue;
    }
    // Typographic family over family; Windows US English over the rest.
    final rank =
        (nameId == 16 ? 4 : 0) +
        (platform == 3 ? 2 : 0) +
        (language == 0x409 || platform != 3 ? 1 : 0);
    if (text.isNotEmpty && rank > bestRank) {
      best = text;
      bestRank = rank;
    }
  }
  return best;
}
