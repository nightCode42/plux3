// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flat_buffers/flat_buffers.dart' as fb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/clock.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/forms/form_state.dart';
import 'package:plux_flutter/src/forms/handlers.dart';
import 'package:plux_flutter/src/forms/validators.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

import '../actions/engine_test.dart' show FakeNavigator, id, input, lit;
import '../support/harness.dart';

Uint8List _bundle(String name) =>
    File('../../schema/testdata/bundles/forms/$name').readAsBytesSync();

/// Forms (STA-020) on the forms conformance project: every validator kind,
/// the default phone region from the device locale, asynchronous
/// validation with debouncing and stale results discarded, submit, reset,
/// a component's own form, and rebuilds limited to the paths that changed.
void main() {
  late Harness h;

  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> start(WidgetTester tester, {Locale? locale}) async {
    tester.view
      ..physicalSize = const Size(800, 2400)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    tester.platformDispatcher.localeTestValue =
        locale ?? const Locale('en', 'GB');
    addTearDown(tester.platformDispatcher.clearLocaleTestValue);
    await tester.runAsync(
      () =>
          h.startFrom(_bundle('forms.pxb'), {'signup': _bundle('signup.pxb')}),
    );
    await tester.pumpWidget(
      const MaterialApp(home: PluxScope(child: PluxView('home'))),
    );
    await settle(tester);
  }

  Finder field(int i) => find.byType(TextFormField).at(i);
  const name = 0, email = 1, phone = 2, iban = 3, amount = 4, confirm = 5;
  const address = 6;

  Future<void> enter(WidgetTester tester, int i, String text) async {
    await tester.enterText(field(i), text);
    await tester.pump();
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label));
    await tester.pump();
    await tester.pump();
  }

  void shows(String text) => expect(find.text(text), findsOneWidget);

  testWidgets(
    'each validator kind reports its message once the field is touched [STA-020]',
    (tester) async {
      await start(tester);
      shows('status idle valid false validating false');
      shows('name -');
      shows('email -');
      await tap(tester, 'validate');
      shows('message invalid');
      shows('name Enter your name');
      shows('email Required');
      for (final f in ['phone', 'iban', 'age', 'amount', 'start', 'confirm']) {
        shows('$f -');
      }

      await enter(tester, name, 'A');
      shows('name At least 2 characters');
      await enter(tester, name, 'Ad4');
      shows('name Letters only');
      await enter(tester, email, 'ada');
      shows('email Not a valid email address');
      await enter(tester, email, 'ada@example.com');
      shows('email -');
      await enter(tester, phone, '12');
      shows('phone Not a valid phone number');
      await enter(tester, phone, '07400 123456');
      shows('phone -');
      await enter(tester, iban, 'DE88 3704 0044 0532 0130 00');
      shows('iban Not a valid IBAN');
      await enter(tester, iban, 'DE89 3704 0044 0532 0130 00');
      shows('iban -');
      await enter(tester, amount, '12.345');
      shows('amount At most 2 decimal places');
      await enter(tester, amount, '1234567');
      shows('amount At most 6 digits before the decimal point');
      await enter(tester, amount, '1234.5');
      shows('amount -');
      await tap(tester, 'age 17');
      shows('age Must be at least 18');
      await tap(tester, 'age 30');
      shows('age -');
      await tap(tester, 'start 2025');
      shows('start Must be at least 2026-01-01');
      await tap(tester, 'start 2026');
      shows('start -');
      await enter(tester, confirm, 'other@example.com');
      shows('confirm The addresses differ');
      await enter(tester, confirm, 'ada@example.com');
      shows('confirm -');
      await tester.pump(const Duration(seconds: 1));
    },
  );

  testWidgets(
    'phone numbers without a calling code read the device locale\'s region [STA-020]',
    (tester) async {
      await start(tester, locale: const Locale('en', 'US'));
      await tap(tester, 'validate');
      await enter(tester, phone, '07400 123456');
      shows('phone Not a valid phone number');
      await enter(tester, phone, '+44 7400 123456');
      shows('phone -');
    },
  );

  testWidgets(
    'an asynchronous validator runs once the field is quiet, and a stale check is discarded [STA-020]',
    (tester) async {
      await start(tester);
      await enter(tester, name, 'taken');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pump();
      shows('dirty true touched true');
      shows('status idle valid false validating true');
      // Within the debounce time a new value replaces the pending check.
      await tester.pump(const Duration(milliseconds: 200));
      await enter(tester, name, 'Ada');
      await tester.pump(const Duration(milliseconds: 250));
      shows('name -');
      shows('status idle valid false validating true');
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 150));
      shows('name -');
      shows('status idle valid false validating false');

      // A check already running is restarted by a newer value: the
      // result for "taken" never shows.
      await enter(tester, name, 'taken');
      await tester.pump(const Duration(milliseconds: 350));
      await enter(tester, name, 'Grace');
      await tester.pump(const Duration(milliseconds: 100));
      shows('name -');
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 150));
      shows('name -');
      shows('status idle valid false validating false');

      await enter(tester, name, 'taken');
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 150));
      shows('name That name is taken');
    },
  );

  testWidgets(
    'submit waits for pending checks, returns the typed values and follows the run; reset restores the initial values [STA-020]',
    (tester) async {
      await start(tester);
      await tap(tester, 'submit');
      shows('message refused form signup is invalid: name, email');
      shows('status idle valid false validating false');

      await enter(tester, name, 'Ada Lovelace');
      await enter(tester, email, 'ada@example.com');
      await enter(tester, confirm, 'ada@example.com');
      // Submitted before the name's check ran: submit runs it at once.
      await tester.tap(find.text('submit'));
      await tester.pump();
      shows('status idle valid false validating true');
      await tester.pump(const Duration(milliseconds: 150));
      await tester.pump();
      shows('message sent Ada Lovelace');
      shows('status succeeded valid true validating false');
      shows('dirty true touched true');

      await tap(tester, 'reset');
      shows('value ');
      shows('status idle valid false validating false');
      shows('dirty false touched false');
      shows('name -');
      expect(
        tester.widget<TextFormField>(field(name)).controller!.text,
        isEmpty,
      );
      await tester.pump(const Duration(seconds: 1));
    },
  );

  testWidgets(
    'a field write rebuilds only the nodes that read what changed [STA-020] [STA-010] [RT-012]',
    (tester) async {
      await start(tester);
      await tap(tester, 'validate');
      final emailError = tester.widget(find.text('email Required'));
      final ibanError = tester.widget(find.text('iban -'));
      final message = tester.widget(find.text('message invalid'));
      final emailField = tester.widget(field(email));
      await enter(tester, name, 'Ada');
      shows('value Ada');
      expect(
        identical(tester.widget(find.text('email Required')), emailError),
        isTrue,
      );
      expect(identical(tester.widget(find.text('iban -')), ibanError), isTrue);
      expect(
        identical(tester.widget(find.text('message invalid')), message),
        isTrue,
      );
      expect(identical(tester.widget(field(email)), emailField), isTrue);
      await tester.pump(const Duration(seconds: 1));
    },
  );

  testWidgets('a component keeps its own form [STA-020]', (tester) async {
    await start(tester);
    await tap(tester, 'join');
    shows('join Required');
    await enter(tester, address, 'ada');
    shows('join Not a valid email address');
    await enter(tester, address, 'ada@example.com');
    shows('join -');
    shows('name -');
  });

  group('the form engine [STA-020]', () {
    late ProviderContainer container;
    late PageInstance page;
    late List<(Object?, Duration)> runs;

    FormDecl decl() => FormDecl(
      name: 'f',
      fields: [
        FormFieldDecl(
          name: 'code',
          type: const PxlType(PxlKind.string),
          initial: '',
          validators: [
            FieldValidator(
              kind: fbs.ValidatorKind.Required,
              check: Validators.required(),
            ),
            FieldValidator(
              kind: fbs.ValidatorKind.Async,
              graph: fbs.Uuid.reader.read(
                fb.BufferContext.fromBytes(Uint8List(16)),
                0,
              ),
              debounce: const Duration(milliseconds: 10),
            ),
          ],
        ),
        FormFieldDecl(
          name: 'count',
          type: const PxlType(PxlKind.int, nullable: true),
          initial: null,
          validators: const [],
        ),
      ],
    );

    setUp(() {
      runs = [];
      container = ProviderContainer();
      page = PageInstance(
        const {},
        model: ScopeModel(
          kind: StateScopeKind.page,
          owner: 'p',
          decls: const [],
          evaluate: (_, _) => null,
          types: const {},
          forms: [decl()],
          runForm: (graph, value, key, debounce) async {
            runs.add((value, debounce));
            return RunResult(
              RunOutcome.ok,
              result: value == 'bad' ? false : null,
              steps: 1,
            );
          },
        ),
      );
      container.listen(pageStateProvider(page), (_, _) {});
    });
    tearDown(() => container.dispose());

    ScopeStateAccess access() =>
        ScopeStateAccess(container: container, plugin: 'p', page: page);
    Map<String, Object?> form() =>
        container.read(pageStateProvider(page))['f']! as Map<String, Object?>;

    test('writes reach values and touched flags only, typed', () async {
      final s = access();
      s.write('page.f.values.count', 3);
      expect((form()['values']! as Map)['count'], 3);
      expect((form()['dirty']! as Map)['count'], isTrue);
      expect(
        () => s.write('page.f.values.count', 'three'),
        throwsA(
          isA<StateWriteException>().having(
            (e) => e.code,
            'code',
            PluxErrorCode.stateWriteTypeMismatch,
          ),
        ),
      );
      for (final path in ['page.f', 'page.f.errors.code', 'page.f.values.x']) {
        expect(
          () => s.write(path, 'x'),
          throwsA(isA<StateWriteException>()),
          reason: path,
        );
      }
      s.write('page.f.touched.code', true);
      expect((form()['errors']! as Map)['code'], 'Required');
    });

    test('a false result of an asynchronous check is the validator\'s failure; validate runs pending checks at once', () async {
      final s = access();
      s.write('page.f.values.code', 'bad');
      expect(form()['validating'], isTrue);
      final ok = await s.form('f')!.validate();
      expect(ok, isFalse);
      expect(runs.last, ('bad', Duration.zero));
      expect((form()['errors']! as Map)['code'], 'Invalid');
      s.write('page.f.values.code', 'good');
      expect(await s.form('f')!.validate(), isTrue);
    });

    test(
      'without an engine an asynchronous check ends unchecked, and is reported',
      () async {
        final reports = <PluxException>[];
        final bare = PageInstance(
          const {},
          model: ScopeModel(
            kind: StateScopeKind.page,
            owner: 'p',
            decls: const [],
            evaluate: (_, _) => null,
            types: const {},
            forms: [decl()],
            report: reports.add,
          ),
        );
        container.listen(pageStateProvider(bare), (_, _) {});
        final s = ScopeStateAccess(
          container: container,
          plugin: 'p',
          page: bare,
        );
        s.write('page.f.values.code', 'x');
        expect(await s.form('f')!.validate(), isFalse);
        final state =
            container.read(pageStateProvider(bare))['f']!
                as Map<String, Object?>;
        expect((state['errors']! as Map)['code'], 'Could not be checked');
        expect(state['validating'], isFalse);
        expect(reports.first.code, PluxErrorCode.formAsyncValidatorFailed);
      },
    );

    test('an invalid submitForm takes the invalid branch when the step wires it, else fails [STA-020]', () async {
      final c = StepContext(
        navigator: const _NoNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        state: access(),
        clock: const ActionClock(),
      );
      final wired = c.forStep(const ['invalid']);
      final r = await formHandlers['submitForm']!.run(wired, {'form': 'f'});
      expect((r as StepDone).branch, 'invalid');
      expect(r.output, isNull);
      expect(form()['status'], isNot('submitting'));
      await expectLater(
        Future(
          () => formHandlers['submitForm']!.run(c.forStep(const ['other']), {
            'form': 'f',
          }),
        ),
        throwsA(
          isA<ActionError>().having(
            (e) => e.code,
            'code',
            PluxErrorCode.formInvalid,
          ),
        ),
      );
      access().write('page.f.values.code', 'good');
      final ok = await formHandlers['submitForm']!.run(wired, {'form': 'f'});
      expect((ok as StepDone).branch, isNull);
      expect(ok.output, {'code': 'good', 'count': null});
    });

    test('a run follows the invalid branch of a submitForm step that wires it [STA-020]', () async {
      var syncs = 0;
      ActionHost host() => ActionHost(
        context: StepContext(
          navigator: FakeNavigator(),
          emit: (_, _) {},
          nativeActions: const NoNativeActions(),
          state: access(),
          clock: const ActionClock(),
          sync: () => syncs++,
        ),
        limits: const ActionLimits(
          stepsPerRun: 100,
          stepTimeout: Duration(seconds: 5),
          runTimeout: Duration(seconds: 10),
        ),
        report: (_) {},
        record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
        route: 'home',
        pluginKey: 'p',
        debug: true,
      );
      ActionGraph submit(Map<String, int> branches) => ActionGraph(
        id: 'g',
        steps: [
          GraphStep(
            id: 'submit',
            action: id('submitForm'),
            inputs: {input('submitForm', 'form'): lit('f')},
            branches: branches,
          ),
          GraphStep(id: 'after', action: id('sync')),
        ],
      );

      final wired = await host().start(
        submit(const {'invalid': 1}),
        roots: () => const {},
        key: 'wired',
      );
      expect(wired?.outcome, RunOutcome.ok);
      expect(syncs, 1);

      final unwired = await host().start(
        submit(const {}),
        roots: () => const {},
        key: 'unwired',
      );
      expect(unwired?.outcome, RunOutcome.failed);
      expect(unwired?.error?.code, PluxErrorCode.formInvalid);
      expect(syncs, 1);
    });

    test('the form actions run on the scope\'s form and fail with PLX-5351 elsewhere', () async {
      final c = StepContext(
        navigator: const _NoNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        state: access(),
        clock: const ActionClock(),
      );
      final v = await formHandlers['validateForm']!.run(c, {'form': 'f'});
      expect((v as StepDone).branch, 'invalid');
      await expectLater(
        Future(() => formHandlers['submitForm']!.run(c, {'form': 'f'})),
        throwsA(
          isA<ActionError>()
              .having((e) => e.code, 'code', PluxErrorCode.formInvalid)
              .having((e) => e.kind, 'kind', ActionErrorKind.validation),
        ),
      );
      access().write('page.f.values.code', 'good');
      final scope = RunScope(c.state);
      final done = await formHandlers['submitForm']!.run(c.forRun(scope), {
        'form': 'f',
      });
      expect((done as StepDone).output, {'code': 'good', 'count': null});
      expect(form()['status'], 'submitting');
      scope.end(succeeded: false);
      expect(form()['status'], 'failed');
      formHandlers['resetForm']!.run(c, {'form': 'f'});
      expect(form()['status'], 'idle');
      expect((form()['values']! as Map)['code'], '');
      await expectLater(
        Future(() => formHandlers['resetForm']!.run(c, {'form': 'other'})),
        throwsA(
          isA<ActionError>().having(
            (e) => e.code,
            'code',
            PluxErrorCode.formNotInScope,
          ),
        ),
      );
    });
  });
}

final class _NoNavigator implements RunNavigator {
  const _NoNavigator();

  @override
  Future<void> navigate(
    String route,
    Map<String, Object?> params,
    String mode,
    String? until,
  ) async {}

  @override
  Future<Object?> present(
    String route,
    Map<String, Object?> params, {
    required bool sheet,
    required bool dismissible,
  }) async => null;

  @override
  void pop(Object? result) {}

  @override
  void switchTab(String tab) {}
}
