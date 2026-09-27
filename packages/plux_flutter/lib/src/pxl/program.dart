// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Decoding and verification of PXL programs (PXL-003, docs/reference/pxl.md
/// §8). The runtime never parses PXL source; it evaluates programs the
/// compiler encoded.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// A malformed program.
final class InvalidProgram implements Exception {
  /// Creates the error.
  const InvalidProgram(this.message);

  /// What is wrong.
  final String message;

  @override
  String toString() => 'InvalidProgram: $message';
}

/// A decoded instruction.
final class Instr {
  /// Creates an instruction.
  const Instr(this.op, this.operands, this.next);

  /// The opcode.
  final int op;

  /// The operands.
  final List<int> operands;

  /// The offset of the next instruction.
  final int next;
}

/// A verified program.
final class Program {
  Program._(
    this.locals,
    this.maxStack,
    this.result,
    this.constants,
    this.reads,
    this.code,
  ) {
    _verify();
  }

  /// Decodes and verifies [data]; throws [InvalidProgram].
  factory Program.decode(Uint8List data) {
    final r = _Reader(data);
    if (r.uvarint() != bytecodeVersion) {
      throw const InvalidProgram('unsupported version');
    }
    final locals = r.uvarint();
    final maxStack = r.uvarint();
    final result = r.str();
    final constants = [for (var n = r.count(); n > 0; n--) r.value(64)];
    final reads = [for (var n = r.count(); n > 0; n--) r.str()];
    final code = r.bytes(r.uvarint());
    if (r.pos != data.length) throw const InvalidProgram('trailing data');
    if (locals > 256) throw const InvalidProgram('too many locals');
    return Program._(locals, maxStack, result, constants, reads, code);
  }

  /// The number of local slots.
  final int locals;

  /// The largest stack depth.
  final int maxStack;

  /// The result type expression.
  final String result;

  /// The constant pool.
  final List<Object?> constants;

  /// The state paths the program reads (CMP-023).
  final List<String> reads;

  /// The instruction stream.
  final Uint8List code;

  late final List<Instr?> _instrs = _decodeAll();

  /// The instruction at [pc]; null when pc is not an instruction boundary.
  Instr? at(int pc) => pc < _instrs.length ? _instrs[pc] : null;

  List<Instr?> _decodeAll() {
    final out = List<Instr?>.filled(code.length, null);
    for (var pc = 0; pc < code.length;) {
      final op = code[pc];
      final widths = op < operandWidths.length ? operandWidths[op] : null;
      if (widths == null) throw InvalidProgram('bad instruction at $pc');
      var pos = pc + 1;
      final operands = <int>[];
      for (final w in widths) {
        if (pos + w > code.length) {
          throw InvalidProgram('truncated instruction at $pc');
        }
        var v = 0;
        for (var i = 0; i < w; i++) {
          v |= code[pos + i] << (8 * i);
        }
        operands.add(v);
        pos += w;
      }
      out[pc] = Instr(op, operands, pos);
      pc = pos;
    }
    return out;
  }

  static const _jumps = {
    Op.jump,
    Op.jumpIfFalse,
    Op.jumpIfTrue,
    Op.andJump,
    Op.orJump,
    Op.nullJump,
    Op.coalesceJump,
    Op.iterNext,
  };

  void _verify() {
    if (code.isEmpty) throw const InvalidProgram('empty code');
    if (maxStack < 1 || maxStack > code.length) {
      throw const InvalidProgram('bad max stack');
    }
    for (final in_ in _instrs) {
      if (in_ != null) _verifyOperands(in_);
    }
  }

  void _verifyOperands(Instr in_) {
    final o = in_.operands;
    switch (in_.op) {
      case Op.constValue || Op.loadRoot || Op.getField || Op.mapGet:
        if (o[0] >= constants.length) {
          throw const InvalidProgram('constant out of range');
        }
        if (in_.op != Op.constValue && constants[o[0]] is! String) {
          throw const InvalidProgram('needs a string constant');
        }
      case Op.loadLocal || Op.iterInit || Op.iterList || Op.iterNext:
        for (final slot in o.take(2)) {
          if (slot >= locals) throw const InvalidProgram('local out of range');
        }
      case Op.call:
        if (o[0] == 0 || o[0] > overloads.length) {
          throw const InvalidProgram('unknown function');
        }
        final def = overloads[o[0] - 1];
        if (o[1] != def.params.length && (!def.variadic || o[1] == 0)) {
          throw const InvalidProgram('wrong argument count');
        }
      case Op.sortBy:
        if (o[0] == 0 || o[0] > CmpKind.ofDuration) {
          throw const InvalidProgram('unknown comparison kind');
        }
    }
    if (_jumps.contains(in_.op)) {
      final t = o.last;
      if (t != code.length && (t >= code.length || _instrs[t] == null)) {
        throw const InvalidProgram('jump into an instruction');
      }
    }
  }
}

/// Bounds-checked reading of program bytes.
final class _Reader {
  _Reader(this.data);

  final Uint8List data;
  int pos = 0;

  int byte() {
    if (pos >= data.length) throw const InvalidProgram('truncated');
    return data[pos++];
  }

  int uvarint() {
    var result = 0, shift = 0;
    while (true) {
      final b = byte();
      if (shift >= 63 && b > 1) throw const InvalidProgram('bad varint');
      result |= (b & 0x7F) << shift;
      if (b < 0x80) return result;
      shift += 7;
    }
  }

  int varint() {
    final u = uvarint();
    return (u >>> 1) ^ -(u & 1);
  }

  int count() {
    final n = uvarint();
    if (n < 0 || n > data.length - pos) {
      throw const InvalidProgram('count exceeds the data');
    }
    return n;
  }

  Uint8List bytes(int n) {
    if (n < 0 || n > data.length - pos) throw const InvalidProgram('truncated');
    final out = Uint8List.sublistView(data, pos, pos + n);
    pos += n;
    return Uint8List.fromList(out);
  }

  String str() {
    try {
      return utf8.decode(bytes(uvarint()));
    } on FormatException {
      throw const InvalidProgram('invalid UTF-8');
    }
  }

  Object? value(int depth) {
    if (depth == 0) throw const InvalidProgram('constant nested too deeply');
    switch (byte()) {
      case ConstTag.ofNull:
        return null;
      case ConstTag.ofBool:
        final b = byte();
        if (b > 1) throw const InvalidProgram('bad bool');
        return b == 1;
      case ConstTag.ofInt:
        return ByteData.sublistView(bytes(8)).getInt64(0, Endian.little);
      case ConstTag.ofDouble:
        final f = ByteData.sublistView(bytes(8)).getFloat64(0, Endian.little);
        if (f.isNaN || f.isInfinite) {
          throw const InvalidProgram('non-finite double');
        }
        return f;
      case ConstTag.ofString:
        return str();
      case ConstTag.ofDecimal:
        return Decimal.tryParse(str()) ??
            (throw const InvalidProgram('bad decimal'));
      case ConstTag.ofMoney:
        final d = Decimal.tryParse(str());
        final code = ascii.decode(bytes(3), allowInvalid: true);
        if (d == null || !minorUnits.containsKey(code)) {
          throw const InvalidProgram('bad money');
        }
        return Money(d, code);
      case ConstTag.ofDate:
        final d = varint();
        if (!validDate(d)) throw const InvalidProgram('date out of range');
        return PxlDate(d);
      case ConstTag.ofDateTime:
        final t = PxlDateTime(varint(), varint());
        if (t.offset.abs() > 1439 || !validDate(t.local.$1)) {
          throw const InvalidProgram('dateTime out of range');
        }
        return t;
      case ConstTag.ofDuration:
        return PxlDuration(varint());
      case ConstTag.ofColor:
        return PxlColor(ByteData.sublistView(bytes(4)).getUint32(0));
      case ConstTag.ofList:
        return [for (var n = count(); n > 0; n--) value(depth - 1)];
      case ConstTag.ofMap:
        final out = <String, Object?>{};
        String? prev;
        for (var n = count(); n > 0; n--) {
          final k = str();
          if (prev != null && compareCodePoints(k, prev) <= 0) {
            throw const InvalidProgram('map keys out of order');
          }
          out[k] = value(depth - 1);
          prev = k;
        }
        return out;
      default:
        throw const InvalidProgram('unknown constant tag');
    }
  }
}
