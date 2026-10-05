// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Forms (STA-020, ADR-0047): the declarations a bundle carries and the
/// controller that keeps a form's state in its page's or component's scope.
///
/// A form's state is one scope entry named after the form, read by
/// bindings like any other state, so a node rebuilds only when the path it
/// selects changes (RT-012):
///
/// - `values`: each field's value, of its declared type;
/// - `errors`: each field's message, shown once the field is touched;
/// - `dirty` and `touched`: each field's flags;
/// - `status`: `idle`, `submitting`, `succeeded` or `failed`;
/// - `valid`: whether every field passes its validators;
/// - `validating`: whether an asynchronous check is pending.
///
/// `setState` writes a field's value (`<form>.values.<field>`) or touched
/// flag (`<form>.touched.<field>`); everything else follows from the
/// validators. Asynchronous validators run their graph after the field has
/// been quiet for its debounce time, with restart semantics: a newer value
/// replaces a pending check, and a stale result is discarded.
library;

import 'dart:async';

import 'package:flutter/widgets.dart' show WidgetsBinding;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/forms/validators.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/regex.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/state/scope_state.dart' show sameValue;

/// Runs an asynchronous validator's [graph] with [value] as its event,
/// under the trigger [key], once [debounce] has passed without another
/// call for [key]; a newer call replaces an older one, whose future
/// completes with null (ADR-0039's debounce and restart).
typedef FormGraphRunner = Future<RunResult?> Function(
  fbs.Uuid graph,
  Object? value,
  String key,
  Duration debounce,
);

/// Evaluates a custom rule's program against its roots.
typedef FormEvaluate = Object? Function(
  Program program,
  Map<String, Object?> roots,
);

/// One validator of a field.
final class FieldValidator {
  /// Creates a validator.
  const FieldValidator({
    required this.kind,
    this.message,
    this.check,
    this.rule,
    this.graph,
    this.debounce = Duration.zero,
  });

  /// The kind.
  final fbs.ValidatorKind kind;

  /// The declared message, or null for the built-in one.
  final String? message;

  /// A built-in check, for every kind but Custom and Async.
  final Validator? check;

  /// A Custom validator's predicate over `value` and `form`.
  final Program? rule;

  /// An Async validator's graph.
  final fbs.Uuid? graph;

  /// How long the field stays unchanged before an Async check runs.
  final Duration debounce;

  /// Whether the validator runs a graph.
  bool get isAsync => kind == fbs.ValidatorKind.Async;
}

/// One field of a form.
final class FormFieldDecl {
  /// Creates a field.
  const FormFieldDecl({
    required this.name,
    required this.type,
    required this.initial,
    required this.validators,
  });

  /// The field's name.
  final String name;

  /// The declared type, or null when it cannot be parsed.
  final PxlType? type;

  /// The initial value, in PXL form.
  final Object? initial;

  /// The validators, in declaration order.
  final List<FieldValidator> validators;
}

/// A form as its bundle declares it.
final class FormDecl {
  /// Creates a form.
  const FormDecl({required this.name, required this.fields});

  /// The form's name: its scope entry.
  final String name;

  /// The fields, in declaration order.
  final List<FormFieldDecl> fields;

  /// The field [name], or null.
  FormFieldDecl? field(String name) {
    for (final f in fields) {
      if (f.name == name) return f;
    }
    return null;
  }

  /// The form's initial values.
  Map<String, Object?> get initialValues => {
    for (final f in fields) f.name: f.initial,
  };
}

/// The region of the device locale, the default of phone validators.
String deviceRegion() =>
    WidgetsBinding.instance.platformDispatcher.locale.countryCode ?? '';

/// Decodes the [forms] of a page or component section, whose strings are
/// in [strings] and programs in [bundle]; [literal] resolves an initial
/// value or a bound, [types] holds the named types fields may use, and
/// [regex] bounds the patterns. Phone validators without a region read
/// [region] when they run.
List<FormDecl> decodeForms(
  List<fbs.Form>? forms, {
  required BundleView bundle,
  required StringTable strings,
  required Map<String, NamedType> types,
  required Object? Function(fbs.Value? v) literal,
  required RegexLimits regex,
  String Function() region = deviceRegion,
}) {
  PxlType? parse(String src) {
    try {
      return PxlType.parse(src, (n) => types[n]);
    } on FormatException {
      return null;
    }
  }

  String? str(int i) => i == 0 ? null : strings(i);
  return [
    for (final f in forms ?? const <fbs.Form>[])
      FormDecl(
        name: strings(f.name),
        fields: [
          for (final fd in f.fields ?? const <fbs.FormField>[])
            FormFieldDecl(
              name: strings(fd.name),
              type: parse(strings(fd.type)),
              initial: fd.initial == null ? null : literal(fd.initial),
              validators: [
                for (final v in fd.validators ?? const <fbs.FormValidator>[])
                  FieldValidator(
                    kind: v.kind,
                    message: str(v.message),
                    check: _check(v, str, literal, regex, region),
                    rule: v.rule == 0 ? null : bundle.program(v.rule),
                    graph: v.graph,
                    debounce: Duration(milliseconds: v.debounceMs),
                  ),
              ],
            ),
        ],
      ),
  ];
}

/// The built-in check of [v]; null for Custom and Async.
Validator? _check(
  fbs.FormValidator v,
  String? Function(int) str,
  Object? Function(fbs.Value? v) literal,
  RegexLimits limits,
  String Function() region,
) {
  Object? bound(fbs.Value? b) => b == null ? null : literal(b);
  return switch (v.kind) {
    fbs.ValidatorKind.Required => Validators.required(),
    fbs.ValidatorKind.Length => Validators.length(
      min: bound(v.min) as int?,
      max: bound(v.max) as int?,
    ),
    fbs.ValidatorKind.Range => Validators.range(
      min: _decimal(bound(v.min)),
      max: _decimal(bound(v.max)),
    ),
    fbs.ValidatorKind.Regex => _regex(str(v.pattern) ?? '', limits),
    fbs.ValidatorKind.Email => Validators.email(),
    fbs.ValidatorKind.Phone => () {
      final fixed = str(v.region);
      return (Object? value) => Validators.phone(fixed ?? region())(value);
    }(),
    fbs.ValidatorKind.Iban => Validators.iban(),
    fbs.ValidatorKind.DateRange => _dateRange(bound(v.min), bound(v.max)),
    fbs.ValidatorKind.DecimalPrecision => Validators.decimalPrecision(
      maxScale: v.maxScale < 0 ? null : v.maxScale,
      maxIntegerDigits: v.maxIntegerDigits < 0 ? null : v.maxIntegerDigits,
    ),
    _ => null,
  };
}

/// A regex check; a pattern the runtime cannot compile — the compiler
/// refuses those at publish — fails every value rather than none.
Validator _regex(String pattern, RegexLimits limits) {
  try {
    return Validators.regex(pattern, limits);
  } on RegexError {
    return (_) => const ValidationFailure('pattern');
  }
}

Validator _dateRange(Object? min, Object? max) => switch ((min, max)) {
  (final PxlDate? lo, final PxlDate? hi) => Validators.dateRange<PxlDate>(
    min: lo,
    max: hi,
  ),
  (final PxlDateTime? lo, final PxlDateTime? hi) =>
    Validators.dateRange<PxlDateTime>(min: lo, max: hi),
  _ => (_) => null,
};

Decimal? _decimal(Object? v) => switch (v) {
  final Decimal d => d,
  final int i => Decimal.fromInt(i),
  final double f => Decimal.fromDouble(f),
  _ => null,
};

/// The built-in message of [f] until P8 brings localised messages.
String builtInMessage(ValidationFailure f) {
  final a = f.args;
  return switch (f.code) {
    'required' => 'Required',
    'tooShort' => 'At least ${a['min']} characters',
    'tooLong' => 'At most ${a['max']} characters',
    'belowMin' => 'Must be at least ${a['min']}',
    'aboveMax' => 'Must be at most ${a['max']}',
    'pattern' => 'Not in the expected format',
    'email' => 'Not a valid email address',
    'phone' => 'Not a valid phone number',
    'iban' => 'Not a valid IBAN',
    'tooManyDecimals' => 'At most ${a['max']} decimal places',
    'tooManyDigits' => 'At most ${a['max']} digits before the decimal point',
    'notANumber' => 'Not a number',
    'unchecked' => 'Could not be checked',
    _ => 'Invalid',
  };
}

bool _absent(Object? v) => v == null || (v is String && v.isEmpty);

/// What a form controller reads and writes: its scope entry, and the
/// services of its scope.
final class FormHost {
  /// Creates a host.
  const FormHost({
    required this.read,
    required this.write,
    required this.evaluate,
    this.run,
    this.report,
  });

  /// The form's scope entry.
  final Map<String, Object?>? Function() read;

  /// Replaces the form's scope entry; ignored once the scope is disposed.
  final void Function(Map<String, Object?> state) write;

  /// Evaluates custom rules.
  final FormEvaluate evaluate;

  /// Runs asynchronous validators' graphs; null where no engine runs.
  final FormGraphRunner? run;

  /// Reports problems.
  final void Function(PluxException error)? report;
}

/// Finds a form of the scope where a run started (STA-020): the
/// component's, else the page's.
abstract interface class FormLookup {
  /// The controller of form [name], or null when no scope in reach
  /// declares it.
  FormController? form(String name);
}

/// The state of one form of one scope instance (STA-020).
final class FormController {
  /// Creates the controller of [decl] over [host].
  FormController(this.decl, this.host);

  /// The form.
  final FormDecl decl;

  /// Its scope.
  final FormHost host;

  /// The last finished asynchronous check of each field: the value it
  /// checked and its message, null when the value passed.
  final Map<String, (Object?, String?)> _checked = {};

  /// The pending asynchronous check of each field.
  final Map<String, Future<void>> _pending = {};

  /// The generation of each field's checks; a result of an older one is
  /// stale.
  final Map<String, int> _generation = {};

  /// The state the form starts with (and returns to on reset).
  static Map<String, Object?> initialState(
    FormDecl decl,
    FormEvaluate evaluate,
  ) => _derive(
    decl,
    evaluate,
    values: decl.initialValues,
    dirty: {for (final f in decl.fields) f.name: false},
    touched: {for (final f in decl.fields) f.name: false},
    status: 'idle',
    checked: const {},
    pending: const {},
  );

  Map<String, Object?> get _state =>
      host.read() ?? initialState(decl, host.evaluate);

  Map<String, Object?> _part(String name) =>
      (_state[name] as Map<String, Object?>?) ?? const {};

  /// The current values.
  Map<String, Object?> get values => _part('values');

  /// Whether every field passes its validators.
  bool get valid => _state['valid'] == true;

  void _commit({
    Map<String, Object?>? values,
    Map<String, Object?>? dirty,
    Map<String, Object?>? touched,
    String? status,
  }) {
    host.write(
      _derive(
        decl,
        host.evaluate,
        values: values ?? this.values,
        dirty: dirty ?? _part('dirty'),
        touched: touched ?? _part('touched'),
        status: status ?? (_state['status'] as String? ?? 'idle'),
        checked: _checked,
        pending: _pending.keys.toSet(),
      ),
    );
  }

  /// Writes [value] to [field] (`<form>.values.<field>`): marks it dirty
  /// when it differs from its initial value and schedules its
  /// asynchronous validators. Throws [FormatException] when the value
  /// does not have the field's type.
  void writeValue(String field, Object? value) {
    final f = decl.field(field)!;
    final v = f.type == null ? value : fromJson(f.type!, toJson(value));
    _commit(
      values: {...values, field: v},
      dirty: {..._part('dirty'), field: !sameValue(v, f.initial)},
    );
    _schedule(f, v, immediately: false);
  }

  /// Sets [field]'s touched flag (`<form>.touched.<field>`), which shows
  /// its errors.
  void writeTouched(String field, {required bool touched}) =>
      _commit(touched: {..._part('touched'), field: touched});

  /// Whether [f]'s value [v] still needs an asynchronous check: it has
  /// asynchronous validators, is not absent, passes its other validators,
  /// and no finished check covers it.
  bool _needsCheck(FormFieldDecl f, Object? v) {
    if (!f.validators.any((x) => x.isAsync) || _absent(v)) return false;
    if (_syncError(decl, f, v, values, host.evaluate) != null) return false;
    final done = _checked[f.name];
    return done == null || !sameValue(done.$1, v);
  }

  /// Starts [f]'s asynchronous check of [v], after its debounce time
  /// unless [immediately]; a check already pending is replaced.
  void _schedule(FormFieldDecl f, Object? v, {required bool immediately}) {
    final generation = (_generation[f.name] ?? 0) + 1;
    _generation[f.name] = generation;
    if (!_needsCheck(f, v)) {
      if (_pending.remove(f.name) != null) _commit();
      return;
    }
    // The check is pending before it starts: without an engine it ends
    // at once, and must find itself to remove.
    final done = Completer<void>();
    _pending[f.name] = done.future;
    _commit();
    unawaited(
      _check(
        f,
        v,
        generation,
        immediately: immediately,
      ).whenComplete(done.complete),
    );
  }

  Future<void> _check(
    FormFieldDecl f,
    Object? v,
    int generation, {
    required bool immediately,
  }) async {
    String? message;
    var k = 0;
    for (final x in f.validators) {
      final index = k++;
      if (!x.isAsync || message != null) continue;
      final run = host.run;
      final graph = x.graph;
      if (run == null || graph == null) {
        message =
            x.message ?? builtInMessage(const ValidationFailure('unchecked'));
        _failed('no engine runs the asynchronous validator of ${f.name}');
        continue;
      }
      RunResult? r;
      try {
        r = await run(
          graph,
          v,
          'form:${decl.name}:${f.name}:$index',
          immediately ? Duration.zero : x.debounce,
        );
      } on Object {
        r = const RunResult(RunOutcome.failed, steps: 0);
      }
      if (_generation[f.name] != generation) return; // stale
      if (r == null || r.outcome == RunOutcome.cancelled) {
        // No run took place, as when the page is closing.
        message =
            x.message ?? builtInMessage(const ValidationFailure('unchecked'));
        continue;
      }
      if (r.outcome == RunOutcome.failed) {
        message =
            x.message ?? builtInMessage(const ValidationFailure('unchecked'));
        _failed(
          'the asynchronous validator of ${decl.name}.${f.name} failed: '
          '${r.error?.code.id ?? 'no result'}',
        );
        continue;
      }
      message = switch (r.result) {
        final String s when s.isNotEmpty => s,
        false => x.message ?? builtInMessage(const ValidationFailure('')),
        _ => null,
      };
    }
    if (_generation[f.name] != generation) return;
    _checked[f.name] = (v, message);
    _pending.remove(f.name)?.ignore();
    _commit();
  }

  void _failed(String message) => host.report?.call(
    PluxException(
      PluxErrorCode.formAsyncValidatorFailed,
      message,
      details: {'form': decl.name},
    ),
  );

  /// Runs every validator, marks every field touched, waits for the
  /// asynchronous checks and returns whether the form is valid
  /// (validateForm).
  Future<bool> validate() async {
    _commit(touched: {for (final f in decl.fields) f.name: true});
    final current = values;
    for (final f in decl.fields) {
      if (_needsCheck(f, current[f.name])) {
        _schedule(f, current[f.name], immediately: true);
      }
    }
    while (_pending.isNotEmpty) {
      await Future.wait([..._pending.values]);
    }
    return valid;
  }

  /// Validates the form and, when it is valid, sets `submitting` and
  /// returns its values (submitForm); throws an [ActionError] of kind
  /// validation (PLX-5350) naming the invalid fields otherwise.
  Future<Map<String, Object?>> submit() async {
    if (!await validate()) {
      final errors = _part('errors');
      final invalid = [
        for (final f in decl.fields)
          if (errors[f.name] != null) f.name,
      ];
      throw ActionError(
        ActionErrorKind.validation,
        PluxErrorCode.formInvalid,
        'form ${decl.name} is invalid: ${invalid.join(', ')}',
      );
    }
    _commit(status: 'submitting');
    return values;
  }

  /// Ends a submission: `succeeded` when the run that submitted ended
  /// well, `failed` otherwise.
  void ended({required bool succeeded}) {
    if (_state['status'] != 'submitting') return;
    _commit(status: succeeded ? 'succeeded' : 'failed');
  }

  /// Restores the initial values and clears the flags, errors and pending
  /// checks (resetForm).
  void reset() {
    for (final f in decl.fields) {
      _generation[f.name] = (_generation[f.name] ?? 0) + 1;
    }
    _pending.clear();
    _checked.clear();
    host.write(initialState(decl, host.evaluate));
  }
}

/// The first failing built-in or custom validator's message for [f]'s
/// value [v], or null.
String? _syncError(
  FormDecl decl,
  FormFieldDecl f,
  Object? v,
  Map<String, Object?> values,
  FormEvaluate evaluate,
) {
  for (final x in f.validators) {
    if (x.isAsync) continue;
    final check = x.check;
    if (check != null) {
      final failure = check(v);
      if (failure != null) return x.message ?? builtInMessage(failure);
      continue;
    }
    final rule = x.rule;
    if (rule == null || _absent(v)) continue;
    Object? ok;
    try {
      ok = evaluate(rule, {'value': v, 'form': values});
    } on Object {
      ok = false;
    }
    if (ok != true) {
      return x.message ?? builtInMessage(const ValidationFailure(''));
    }
  }
  return null;
}

/// A form's state from its values, flags, status and asynchronous checks.
Map<String, Object?> _derive(
  FormDecl decl,
  FormEvaluate evaluate, {
  required Map<String, Object?> values,
  required Map<String, Object?> dirty,
  required Map<String, Object?> touched,
  required String status,
  required Map<String, (Object?, String?)> checked,
  required Set<String> pending,
}) {
  final errors = <String, Object?>{};
  var valid = true;
  for (final f in decl.fields) {
    final v = values[f.name];
    var error = _syncError(decl, f, v, values, evaluate);
    if (error == null && f.validators.any((x) => x.isAsync) && !_absent(v)) {
      final done = checked[f.name];
      if (done != null && sameValue(done.$1, v)) {
        error = done.$2;
      } else {
        valid = false; // not checked yet
      }
    }
    if (error != null) valid = false;
    errors[f.name] = touched[f.name] == true ? error : null;
  }
  return {
    'values': values,
    'errors': errors,
    'dirty': dirty,
    'touched': touched,
    'status': status,
    'valid': valid && pending.isEmpty,
    'validating': pending.isNotEmpty,
  };
}
