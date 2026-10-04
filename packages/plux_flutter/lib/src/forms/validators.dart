// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The built-in validators of forms (STA-020), as pure functions over field
/// values in PXL's value model: strings, `int`, `double`, [Decimal],
/// [PxlDate], [PxlDateTime] and lists. Each returns null when the value is
/// valid, or a [ValidationFailure] with a stable code the form maps to its
/// message. Every validator but [Validators.required] accepts an absent
/// value — null or the empty string — so optional fields validate only
/// what is entered. The checks are the PXL standard library's own
/// (`isEmail`, `isIban`, `matches`, `isPhone`), so a form and a binding
/// never disagree.
library;

import 'package:plux_flutter/src/pxl/builtins.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/phone.dart';
import 'package:plux_flutter/src/pxl/regex.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// Why a value is invalid.
final class ValidationFailure {
  /// Creates a failure.
  const ValidationFailure(this.code, [this.args = const {}]);

  /// A stable code: `required`, `tooShort`, `tooLong`, `belowMin`,
  /// `aboveMax`, `pattern`, `email`, `phone`, `iban`, `tooManyDecimals`,
  /// `tooManyDigits` or `notANumber`.
  final String code;

  /// The bounds the message may show, such as `min` or `max`.
  final Map<String, Object> args;

  @override
  bool operator ==(Object other) =>
      other is ValidationFailure &&
      other.code == code &&
      other.args.length == args.length &&
      args.entries.every((e) => other.args[e.key] == e.value);

  @override
  int get hashCode => Object.hash(code, Object.hashAllUnordered(args.keys));

  @override
  String toString() => 'ValidationFailure($code, $args)';
}

/// A validator of one field value.
typedef Validator = ValidationFailure? Function(Object? value);

bool _absent(Object? v) => v == null || (v is String && v.isEmpty);

/// The built-in validators.
abstract final class Validators {
  /// Fails on null, an empty or blank string, and an empty list.
  static Validator required() =>
      (v) => switch (v) {
        null => const ValidationFailure('required'),
        final String s when s.trim().isEmpty => const ValidationFailure(
          'required',
        ),
        final List<Object?> l when l.isEmpty => const ValidationFailure(
          'required',
        ),
        _ => null,
      };

  /// Bounds the length of a string, in code points as PXL's `len` counts
  /// them, or of a list.
  static Validator length({int? min, int? max}) => (v) {
    if (_absent(v)) return null;
    final n = switch (v) {
      final String s => s.runes.length,
      final List<Object?> l => l.length,
      _ => null,
    };
    if (n == null) return null;
    if (min != null && n < min) {
      return ValidationFailure('tooShort', {'min': min});
    }
    if (max != null && n > max) {
      return ValidationFailure('tooLong', {'max': max});
    }
    return null;
  };

  /// Bounds a number — `int`, `double`, [Decimal] or numeric text —
  /// compared exactly as decimals.
  static Validator range({Decimal? min, Decimal? max}) => (v) {
    if (_absent(v)) return null;
    final d = _decimal(v);
    if (d == null) return const ValidationFailure('notANumber');
    if (min != null && d.compareTo(min) < 0) {
      return ValidationFailure('belowMin', {'min': '$min'});
    }
    if (max != null && d.compareTo(max) > 0) {
      return ValidationFailure('aboveMax', {'max': '$max'});
    }
    return null;
  };

  /// Matches a string against a `pxl.regex.v1` pattern, as PXL's `matches`
  /// does: anywhere, unless the pattern anchors with `^` and `$`. Throws
  /// [RegexError] when the pattern is invalid or exceeds [limits]; the
  /// compiler rejects such patterns at publish time (PLX-2020–2022).
  static Validator regex(String pattern, RegexLimits limits) {
    final re = Regex.compile(pattern, limits);
    return (v) => _absent(v) || v is! String || re.hasMatch(v)
        ? null
        : const ValidationFailure('pattern');
  }

  /// An ASCII e-mail address, as PXL's `isEmail`.
  static Validator email() =>
      (v) => _absent(v) || v is! String || isEmailAddress(v)
      ? null
      : const ValidationFailure('email');

  /// A valid phone number read with [region] as the default for numbers
  /// without a country calling code, as PXL's `isPhone`; the form passes
  /// the device locale's region when the field declares none.
  static Validator phone(String region) =>
      (v) => _absent(v) || v is! String || isValidPhone(v, region)
      ? null
      : const ValidationFailure('phone');

  /// An IBAN with a correct mod-97 check digit pair, as PXL's `isIban`.
  static Validator iban() =>
      (v) => _absent(v) || v is! String || isIbanNumber(v)
      ? null
      : const ValidationFailure('iban');

  /// Bounds a [PxlDate] or a [PxlDateTime] — [T] — inclusive; a value of
  /// another type is left to the field's type check.
  static Validator dateRange<T extends Comparable<T>>({T? min, T? max}) => (v) {
    if (v is! T) return null;
    if (min != null && v.compareTo(min) < 0) {
      return ValidationFailure('belowMin', {'min': '$min'});
    }
    if (max != null && v.compareTo(max) > 0) {
      return ValidationFailure('aboveMax', {'max': '$max'});
    }
    return null;
  };

  /// Bounds the digits of a number: at most [maxScale] after the point and
  /// [maxIntegerDigits] before it, ignoring trailing fraction zeros.
  static Validator decimalPrecision({int? maxScale, int? maxIntegerDigits}) =>
      (v) {
        if (_absent(v)) return null;
        final d = _decimal(v)?.reduced();
        if (d == null) return const ValidationFailure('notANumber');
        final scale = d.scale < 0 ? 0 : d.scale;
        if (maxScale != null && scale > maxScale) {
          return ValidationFailure('tooManyDecimals', {'max': maxScale});
        }
        final integer = d.unscaled.abs().toString().length - scale;
        if (maxIntegerDigits != null && integer > maxIntegerDigits) {
          return ValidationFailure('tooManyDigits', {'max': maxIntegerDigits});
        }
        return null;
      };

  /// Runs [validators] in order and returns the first failure.
  static ValidationFailure? first(List<Validator> validators, Object? value) {
    for (final v in validators) {
      final f = v(value);
      if (f != null) return f;
    }
    return null;
  }
}

Decimal? _decimal(Object? v) => switch (v) {
  final Decimal d => d,
  final int i => Decimal.fromInt(i),
  final double f => Decimal.fromDouble(f),
  final String s => Decimal.tryParse(s.trim()),
  _ => null,
};
