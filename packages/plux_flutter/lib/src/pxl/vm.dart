// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The PXL bytecode VM (PXL-001, PXL-007). It evaluates verified programs
/// exactly as the Go VM does: the same instructions, costs, limits and
/// typed errors, checked by the shared conformance vectors.
library;

import 'package:plux_flutter/src/pxl/builtins.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

/// Limits of one evaluation, from the limits registry (LIM-001).
final class PxlLimits {
  /// Creates limits.
  const PxlLimits({
    required this.budget,
    required this.stringLength,
    required this.collectionSize,
    required this.decimalDigits,
  });

  /// The registry defaults.
  PxlLimits.defaults()
    : this(
        budget: PluxLimit.pxlOperationBudget.defaultValue,
        stringLength: PluxLimit.pxlStringLength.defaultValue,
        collectionSize: PluxLimit.pxlCollectionSize.defaultValue,
        decimalDigits: PluxLimit.pxlDecimalDigits.defaultValue,
      );

  /// Operations an evaluation may perform.
  final int budget;

  /// Code points of a produced string.
  final int stringLength;

  /// Entries of a produced list or map.
  final int collectionSize;

  /// Digits of a produced decimal.
  final int decimalDigits;
}

/// A typed run-time error.
final class PxlError implements Exception {
  /// Creates an error.
  const PxlError(this.kind, this.message);

  /// The kind, shared with Go.
  final PxlErrorKind kind;

  /// A description for developers.
  final String message;

  @override
  String toString() => 'pxl: ${kind.name}: $message';
}

/// The outcome of an evaluation: a value or an error.
sealed class PxlResult {
  const PxlResult();
}

/// A successful evaluation.
final class PxlValue extends PxlResult {
  /// Creates the result.
  const PxlValue(this.value);

  /// The value.
  final Object? value;
}

/// A failed evaluation.
final class PxlFailure extends PxlResult {
  /// Creates the result.
  const PxlFailure(this.error);

  /// The error.
  final PxlError error;
}

/// Evaluates [program] with the root values [inputs] within [limits]. The
/// result is a value or a typed error; it never throws.
PxlResult evaluate(
  Program program,
  Map<String, Object?> inputs,
  PxlLimits limits,
) {
  try {
    return PxlValue(Vm(program, inputs, limits).run());
  } on PxlError catch (e) {
    return PxlFailure(e);
  } on InvalidProgram catch (e) {
    return PxlFailure(PxlError(PxlErrorKind.invalidProgram, e.message));
  } on TypeError catch (e) {
    return PxlFailure(
      PxlError(PxlErrorKind.invalidProgram, 'operand of the wrong type: $e'),
    );
  }
}

/// The iterator of a macro loop.
final class _Iterator {
  _Iterator(this.list);

  final List<Object?> list;
  int next = 0;
}

/// One evaluation.
final class Vm {
  /// Creates the VM for one evaluation.
  Vm(this.program, this.inputs, this.limits)
    : locals = List<Object?>.filled(program.locals, null);

  /// The program.
  final Program program;

  /// The root values.
  final Map<String, Object?> inputs;

  /// The limits.
  final PxlLimits limits;

  /// The local slots.
  final List<Object?> locals;

  final List<Object?> _stack = [];
  int _used = 0;

  /// Spends operations of the budget.
  void charge(int n) {
    _used += n;
    if (_used > limits.budget) {
      throw PxlError(
        PxlErrorKind.budgetExceeded,
        'more than ${limits.budget} operations',
      );
    }
  }

  /// Checks a produced value against the size limits.
  void checkSize(Object? v) {
    switch (v) {
      case final String s
          when s.length > limits.stringLength &&
              s.runes.length > limits.stringLength:
        throw PxlError(
          PxlErrorKind.sizeLimit,
          'a string longer than ${limits.stringLength} code points',
        );
      case final List<Object?> l when l.length > limits.collectionSize:
        throw PxlError(
          PxlErrorKind.sizeLimit,
          'a list of more than ${limits.collectionSize} items',
        );
      case final Map<String, Object?> m when m.length > limits.collectionSize:
        throw PxlError(
          PxlErrorKind.sizeLimit,
          'a map of more than ${limits.collectionSize} entries',
        );
      case final Decimal d when d.digits > limits.decimalDigits:
        throw PxlError(
          PxlErrorKind.sizeLimit,
          'a decimal of more than ${limits.decimalDigits} digits',
        );
      case final Money m:
        checkSize(m.amount);
    }
  }

  Object? _pop() => _stack.isEmpty
      ? throw const InvalidProgram('stack underflow')
      : _stack.removeLast();

  List<Object?> _popN(int n) {
    if (n > _stack.length) throw const InvalidProgram('stack underflow');
    final out = _stack.sublist(_stack.length - n);
    _stack.length -= n;
    return out;
  }

  /// Runs the program to its result.
  Object? run() {
    final code = program.code;
    for (var pc = 0; pc < code.length;) {
      final in_ =
          program.at(pc) ?? (throw InvalidProgram('bad instruction at $pc'));
      charge(1);
      pc = _step(in_);
      if (pc < 0 || pc > code.length) {
        throw const InvalidProgram('jump out of the code');
      }
      if (_stack.length > program.maxStack) {
        throw const InvalidProgram('the stack exceeds its declared depth');
      }
    }
    if (_stack.length != 1) {
      throw InvalidProgram('${_stack.length} values left on the stack');
    }
    return _stack.single;
  }

  int _step(Instr in_) {
    final o = in_.operands;
    switch (in_.op) {
      case Op.pushNull:
        _stack.add(null);
      case Op.pushTrue:
        _stack.add(true);
      case Op.pushFalse:
        _stack.add(false);
      case Op.constValue:
        _stack.add(program.constants[o[0]]);
      case Op.pop:
        _pop();
      case Op.loadRoot:
        final name = program.constants[o[0]]! as String;
        if (!inputs.containsKey(name)) {
          throw PxlError(
            PxlErrorKind.invalidInput,
            'no value for root "$name"',
          );
        }
        _stack.add(inputs[name]);
      case Op.loadLocal:
        _stack.add(locals[o[0]]);
      case Op.getField || Op.mapGet:
        final obj = _pop()! as Map<String, Object?>;
        final key = program.constants[o[0]]! as String;
        if (!obj.containsKey(key) && in_.op == Op.mapGet) {
          throw PxlError(PxlErrorKind.missingKey, 'no entry "$key"');
        }
        _stack.add(obj[key]);
      case Op.indexMap:
        final key = _pop()! as String;
        final obj = _pop()! as Map<String, Object?>;
        if (!obj.containsKey(key)) {
          throw PxlError(PxlErrorKind.missingKey, 'no entry "$key"');
        }
        _stack.add(obj[key]);
      case Op.indexList:
        final i = _pop()! as int;
        final l = _pop()! as List<Object?>;
        if (i < 0 || i >= l.length) {
          throw PxlError(
            PxlErrorKind.indexOutOfRange,
            'index $i of a list of ${l.length}',
          );
        }
        _stack.add(l[i]);
      case Op.jump ||
          Op.jumpIfFalse ||
          Op.jumpIfTrue ||
          Op.andJump ||
          Op.orJump ||
          Op.nullJump ||
          Op.coalesceJump:
        return _jump(in_);
      case Op.iterInit ||
          Op.iterNext ||
          Op.iterList ||
          Op.sortBy ||
          Op.newList ||
          Op.newMap ||
          Op.listAppend:
        return _collection(in_);
      case Op.call:
        _call(o[0], o[1]);
      default:
        _operator(in_.op);
    }
    return in_.next;
  }

  int _jump(Instr in_) {
    final target = in_.operands[0];
    if (in_.op == Op.jump) return target;
    if (_stack.isEmpty) throw const InvalidProgram('stack underflow');
    final top = _stack.last;
    switch (in_.op) {
      case Op.nullJump:
        return top == null ? target : in_.next;
      case Op.coalesceJump:
        if (top != null) return target;
        _stack.removeLast();
        return in_.next;
    }
    final b = top! as bool;
    if (in_.op == Op.jumpIfFalse || in_.op == Op.jumpIfTrue) {
      _stack.removeLast();
      return b == (in_.op == Op.jumpIfTrue) ? target : in_.next;
    }
    if (b == (in_.op == Op.orJump)) return target;
    _stack.removeLast();
    return in_.next;
  }

  int _collection(Instr in_) {
    final o = in_.operands;
    switch (in_.op) {
      case Op.newList:
        final items = _popN(o[0]);
        charge(items.length);
        checkSize(items);
        _stack.add(items);
      case Op.newMap:
        final pairs = _popN(2 * o[0]);
        charge(o[0]);
        final out = <String, Object?>{};
        for (var i = 0; i < pairs.length; i += 2) {
          final k = pairs[i]! as String;
          if (out.containsKey(k)) {
            throw PxlError(PxlErrorKind.duplicateKey, 'key "$k" appears twice');
          }
          out[k] = pairs[i + 1];
        }
        _stack.add(out);
        checkSize(out);
      case Op.listAppend:
        final v = _pop();
        final l = (_pop()! as List<Object?>)..add(v);
        _stack.add(l);
        checkSize(l);
      case Op.iterInit:
        locals[o[0]] = _Iterator(_pop()! as List<Object?>);
      case Op.iterNext:
        final it = locals[o[0]];
        if (it is! _Iterator) {
          throw InvalidProgram('no iterator in local ${o[0]}');
        }
        if (it.next >= it.list.length) return o[2];
        locals[o[1]] = it.list[it.next++];
      case Op.iterList:
        final it = locals[o[0]];
        if (it is! _Iterator) {
          throw InvalidProgram('no iterator in local ${o[0]}');
        }
        _stack.add(it.list);
      default: // Op.sortBy
        _sortBy(o[0]);
    }
    return in_.next;
  }

  void _sortBy(int kind) {
    final items = _pop()! as List<Object?>;
    final keys = _pop()! as List<Object?>;
    if (items.length != keys.length) {
      throw const InvalidProgram('bad sortBy operands');
    }
    final n = items.length;
    charge(n * n.bitLength);
    final idx = List<int>.generate(n, (i) => i);
    // Stable: equal keys keep their order through the index tie-break.
    idx.sort((a, b) {
      final c = compareValues(kind, keys[a], keys[b]);
      return c != 0 ? c : a.compareTo(b);
    });
    _stack.add([for (final i in idx) items[i]]);
  }

  void _call(int id, int argc) {
    final args = _popN(argc);
    charge(args.fold(0, (sum, a) => sum + sizeOf(a)));
    final def = overloads[id - 1];
    if (!argsMatch(def, args)) {
      throw InvalidProgram(
        'arguments of ${def.name} do not match its signature',
      );
    }
    final fn = builtins[id - 1];
    if (fn == null) {
      throw PxlError(
        PxlErrorKind.unsupported,
        '${def.name} is evaluated from feature ${groupFeatures[def.group]}',
      );
    }
    final v = fn(this, args);
    charge(sizeOf(v));
    checkSize(v);
    _stack.add(v);
  }

  void _operator(int op) {
    switch (op) {
      case Op.not:
        _stack.add(!(_pop()! as bool));
      case Op.eq || Op.ne || Op.inList || Op.inMap:
        final b = _pop(), a = _pop();
        final visits = [0];
        final bool result;
        if (op == Op.inMap) {
          result = (b! as Map<String, Object?>).containsKey(a! as String);
        } else if (op == Op.inList) {
          var found = false;
          for (final x in b! as List<Object?>) {
            visits[0]++;
            if (deepEqual(a, x, visits)) {
              found = true;
              break;
            }
          }
          result = found;
        } else {
          result = deepEqual(a, b, visits) == (op == Op.eq);
        }
        _stack.add(result);
        charge(visits[0]);
      case Op.cmpInt ||
          Op.cmpDouble ||
          Op.cmpDec ||
          Op.cmpMoney ||
          Op.cmpString ||
          Op.cmpDate ||
          Op.cmpDatetime ||
          Op.cmpDur:
        final b = _pop(), a = _pop();
        _stack.add(compareValues(_cmpKinds[op]!, a, b));
      case Op.lt || Op.le || Op.gt || Op.ge:
        final c = _pop()! as int;
        _stack.add(switch (op) {
          Op.lt => c < 0,
          Op.le => c <= 0,
          Op.gt => c > 0,
          _ => c >= 0,
        });
      case Op.negInt ||
          Op.negDouble ||
          Op.negDec ||
          Op.negMoney ||
          Op.negDur ||
          Op.intToDouble ||
          Op.intToDec:
        final r = unaryOp(op, _pop());
        _stack.add(r);
        checkSize(r);
      default:
        final b = _pop(), a = _pop();
        final r = binaryOp(op, a, b);
        if (op == Op.concatString || op == Op.concatList) charge(sizeOf(r));
        _stack.add(r);
        checkSize(r);
    }
  }

  static const _cmpKinds = {
    Op.cmpInt: CmpKind.ofInt,
    Op.cmpDouble: CmpKind.ofDouble,
    Op.cmpDec: CmpKind.ofDecimal,
    Op.cmpMoney: CmpKind.ofMoney,
    Op.cmpString: CmpKind.ofString,
    Op.cmpDate: CmpKind.ofDate,
    Op.cmpDatetime: CmpKind.ofDateTime,
    Op.cmpDur: CmpKind.ofDuration,
  };
}
