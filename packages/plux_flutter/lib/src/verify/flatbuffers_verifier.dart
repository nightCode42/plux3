// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The FlatBuffers verifier of the runtime (BND-006, ADR-0029): it walks a
/// section against the layout tables `make gen` derives from the section
/// schemas — the same tables the Go verifier interprets — and checks every
/// offset, size, alignment, vtable, string and vector, within a nesting
/// depth and a count of tables and vectors, before any accessor reads it.
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/verify/layout.g.dart';

/// How a field is stored.
enum FieldKind {
  /// An inline scalar.
  scalar,

  /// An inline struct.
  struct,

  /// An offset to a string.
  string,

  /// An offset to a table.
  table,

  /// An offset to a vector of scalars.
  vectorScalar,

  /// An offset to a vector of structs.
  vectorStruct,

  /// An offset to a vector of strings.
  vectorString,

  /// An offset to a vector of tables.
  vectorTable,
}

/// A field of a table: its vtable slot, how it is stored, the size and
/// alignment of the inline value or vector element, and the index of a
/// referenced table in `tableLayouts`.
final class FieldLayout {
  /// Creates a field layout.
  const FieldLayout(
    this.name,
    this.id,
    this.kind,
    this.size,
    this.align,
    this.table, {
    required this.required,
  });

  /// The field name.
  final String name;

  /// The vtable slot.
  final int id;

  /// How it is stored.
  final FieldKind kind;

  /// The size of the inline value or vector element.
  final int size;

  /// The alignment of the inline value or vector element.
  final int align;

  /// The referenced table, or -1.
  final int table;

  /// Whether the schema requires the field.
  final bool required;
}

/// A table and its fields.
final class TableLayout {
  /// Creates a table layout.
  const TableLayout(this.name, this.fields);

  /// The table name.
  final String name;

  /// Its fields.
  final List<FieldLayout> fields;
}

/// The file identifier of each section kind the runtime knows.
const Map<int, String> sectionIdentifiers = {
  SectionKind.meta: 'PXMT',
  SectionKind.page: 'PXPG',
  SectionKind.component: 'PXCO',
  SectionKind.actions: 'PXAC',
  SectionKind.pxl: 'PXEX',
  SectionKind.styles: 'PXST',
  SectionKind.strings: 'PXSG',
  SectionKind.l10n: 'PXLN',
  SectionKind.timelines: 'PXTL',
  SectionKind.schemas: 'PXSC',
  SectionKind.assetsIndex: 'PXAS',
  SectionKind.wasm: 'PXWM',
  SectionKind.sourceMap: 'PXSM',
};

/// Checks that [data] is a well-formed section of [kind]; throws a
/// [PluxException] with `PLX-3042` otherwise. [maxDepth] and [maxVisits]
/// come from the limits registry (`bundle.verifierDepth`,
/// `bundle.verifierTables`).
void verifySection(
  int kind,
  Uint8List data, {
  required int maxDepth,
  required int maxVisits,
}) {
  final ident = sectionIdentifiers[kind];
  final root = ident == null ? null : rootLayouts[ident];
  if (root == null) throw _fail('no layout for section kind $kind');
  if (data.length < 8 || data.length > 0x7fffffff) {
    throw _fail('size ${data.length}');
  }
  if (String.fromCharCodes(data, 4, 8) != ident) {
    throw _fail('file identifier is not $ident');
  }
  _Verifier(data, maxDepth, maxVisits).table(_u32(data, 0), root, 1);
}

PluxException _fail(String message) => PluxException(
  PluxErrorCode.sectionVerificationFailed,
  'invalid FlatBuffers buffer: $message',
);

int _u32(Uint8List b, int pos) =>
    b[pos] | b[pos + 1] << 8 | b[pos + 2] << 16 | b[pos + 3] << 24;

int _u16(Uint8List b, int pos) => b[pos] | b[pos + 1] << 8;

final class _Verifier {
  _Verifier(this.buf, this.maxDepth, this.maxVisits);

  final Uint8List buf;
  final int maxDepth;
  final int maxVisits;
  int visits = 0;

  bool inBounds(int pos, int n) => pos >= 0 && n >= 0 && pos + n <= buf.length;

  void visit() {
    if (++visits > maxVisits) {
      throw _fail('more than $maxVisits tables and vectors');
    }
  }

  int deref(int pos) {
    if (pos % 4 != 0 || !inBounds(pos, 4)) {
      throw _fail('offset at $pos is misaligned or out of bounds');
    }
    final off = _u32(buf, pos);
    if (off == 0 || off > 0x7fffffff || !inBounds(pos + off, 4)) {
      throw _fail('offset at $pos points outside the buffer');
    }
    return pos + off;
  }

  void table(int pos, int t, int depth) {
    if (depth > maxDepth) throw _fail('tables nested deeper than $maxDepth');
    visit();
    if (pos % 4 != 0 || !inBounds(pos, 4)) {
      throw _fail('table at $pos is misaligned or out of bounds');
    }
    final soffset = _u32(buf, pos).toSigned(32);
    final vt = pos - soffset;
    if (vt < 0 || vt % 2 != 0 || vt + 4 > buf.length) {
      throw _fail('vtable of the table at $pos is misaligned or out of bounds');
    }
    final vtSize = _u16(buf, vt), tableSize = _u16(buf, vt + 2);
    if (vtSize < 4 ||
        vtSize % 2 != 0 ||
        !inBounds(vt, vtSize) ||
        tableSize < 4 ||
        !inBounds(pos, tableSize)) {
      throw _fail('table at $pos has an invalid vtable');
    }
    final layout = tableLayouts[t];
    for (final f in layout.fields) {
      final slot = 4 + 2 * f.id;
      final off = slot + 2 <= vtSize ? _u16(buf, vt + slot) : 0;
      if (off == 0) {
        if (f.required) throw _fail('${layout.name}.${f.name} is required');
        continue;
      }
      field(pos, off, f, depth);
    }
  }

  void field(int pos, int off, FieldLayout f, int depth) {
    final inline = f.kind == FieldKind.scalar || f.kind == FieldKind.struct;
    final size = inline ? f.size : 4, align = inline ? f.align : 4;
    // A field must lie in the buffer, not within the vtable's table size:
    // builders share a vtable between tables of different sizes.
    final at = pos + off;
    if (off < 4 || !inBounds(at, size) || at % align != 0) {
      throw _fail('field ${f.name} at $at is misplaced');
    }
    if (inline) return;
    final target = deref(at);
    switch (f.kind) {
      case FieldKind.string:
        string(target);
      case FieldKind.table:
        table(target, f.table, depth + 1);
      default:
        vector(target, f, depth);
    }
  }

  void string(int pos) {
    visit();
    final n = _u32(buf, pos);
    if (n > 0x7fffffff || !inBounds(pos + 4, n + 1) || buf[pos + 4 + n] != 0) {
      throw _fail('string at $pos is out of bounds or not terminated');
    }
    if (!validUtf8(buf, pos + 4, pos + 4 + n)) {
      throw _fail('string at $pos is not valid UTF-8');
    }
  }

  void vector(int pos, FieldLayout f, int depth) {
    visit();
    final n = _u32(buf, pos);
    final elem =
        f.kind == FieldKind.vectorString || f.kind == FieldKind.vectorTable
        ? 4
        : f.size;
    if (n * elem > buf.length - pos - 4) {
      throw _fail('vector ${f.name} at $pos is out of bounds');
    }
    if (f.kind == FieldKind.vectorScalar || f.kind == FieldKind.vectorStruct) {
      return;
    }
    for (var i = 0; i < n; i++) {
      final target = deref(pos + 4 + 4 * i);
      if (f.kind == FieldKind.vectorString) {
        string(target);
      } else {
        table(target, f.table, depth + 1);
      }
    }
  }
}

/// Whether bytes [start] to [end] of [b] are valid UTF-8, with the rules of
/// Go's `utf8.Valid`: no overlong forms, no surrogates, nothing above
/// U+10FFFF.
bool validUtf8(Uint8List b, int start, int end) {
  var i = start;
  while (i < end) {
    final c = b[i];
    if (c < 0x80) {
      i++;
      continue;
    }
    int n, min;
    if (c >= 0xC2 && c <= 0xDF) {
      n = 1;
      min = 0x80;
    } else if (c >= 0xE0 && c <= 0xEF) {
      n = 2;
      min = 0x800;
    } else if (c >= 0xF0 && c <= 0xF4) {
      n = 3;
      min = 0x10000;
    } else {
      return false;
    }
    if (i + n >= end) return false;
    var cp = c & (0x3F >> n);
    for (var k = 1; k <= n; k++) {
      final cc = b[i + k];
      if (cc & 0xC0 != 0x80) return false;
      cp = cp << 6 | cc & 0x3F;
    }
    if (cp < min || cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) {
      return false;
    }
    i += n + 1;
  }
  return true;
}
