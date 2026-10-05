// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/core/host_events.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/persistence.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

import '../support/harness.dart';

/// The state engine (STA-*) on the state conformance project: every
/// scope, typed writes, computed entries, persistence and migrations, the
/// host's view of plugin writes and host events.
void main() {
  final g = Harness.goldens;
  late Harness h;

  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> start(WidgetTester tester) async {
    tester.view
      ..physicalSize = const Size(800, 1600)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(g.bundles['state/state.pxb']!, {
        'notes': g.bundles['state/notes.pxb']!,
      }),
    );
    await tester.pumpWidget(
      const MaterialApp(home: PluxScope(child: PluxView('home'))),
    );
    await settle(tester);
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label));
    await settle(tester);
  }

  Future<void> restart(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox());
    await tester.runAsync(Plux.dispose);
    await start(tester);
  }

  Map<String, Object?> app() => Plux.container.read(appStateProvider);

  testWidgets(
    'a page write rebuilds only the nodes that read it; computed entries follow [STA-001] [STA-010] [RT-012] [STA-004]',
    (tester) async {
      await start(tester);
      expect(find.text('count 0'), findsOneWidget);
      expect(find.text('total 0'), findsOneWidget);
      final label = tester.widget(find.text('label a'));
      final visits = tester.widget(find.text('visits 0'));
      await tap(tester, 'inc');
      expect(find.text('count 1'), findsOneWidget);
      expect(find.text('total 1'), findsOneWidget);
      expect(identical(tester.widget(find.text('label a')), label), isTrue);
      expect(identical(tester.widget(find.text('visits 0')), visits), isTrue);
      await tap(tester, 'label');
      expect(find.text('label b'), findsOneWidget);
      await tap(tester, 'reset');
      expect(find.text('count 0'), findsOneWidget);
    },
  );

  testWidgets(
    'a graph writes its run variables, the app and the plugin; the host sees the write and the event [STA-001] [STA-030] [HST-021]',
    (tester) async {
      await start(tester);
      final seen = <int?>[];
      final sub = Plux.state<int>('counter').watch().listen(seen.add);
      final events = <PluxHostEvent>[];
      final evs = Plux.events.listen(events.add);
      await tap(tester, 'bump');
      expect(find.text('app 5 10'), findsOneWidget);
      expect(find.text('visits 1'), findsOneWidget);
      expect(find.text('total 5'), findsOneWidget);
      expect(Plux.state<int>('counter').value, 5);
      expect(Plux.state<int>('doubled').value, 10);
      expect(seen, [5]);
      expect(events.single.payload, {'count': 5});
      await tap(tester, 'bump');
      expect(
        find.text('app 10 20'),
        findsOneWidget,
        reason: 'run variables start again with each run',
      );
      unawaited(sub.cancel());
      unawaited(evs.cancel());
    },
  );

  testWidgets(
    'patchState merges fields; a component instance keeps its own state [STA-001] [STA-002]',
    (tester) async {
      await start(tester);
      expect(find.text('draft first false'), findsOneWidget);
      await tap(tester, 'patch');
      expect(find.text('draft first true'), findsOneWidget);
      expect(find.text('Taps 0 0'), findsOneWidget);
      await tap(tester, 'tap');
      expect(find.text('Taps 1 0'), findsOneWidget);
      expect(find.text('count 0'), findsOneWidget);
    },
  );

  testWidgets(
    'a write of a value of the wrong type, or to a computed entry, is refused and changes nothing [STA-002]',
    (tester) async {
      await start(tester);
      final access = ScopeStateAccess(
        container: Plux.container,
        plugin: 'notes',
      );
      expect(
        () => access.write('app.counter', 'five'),
        throwsA(
          isA<StateWriteException>().having(
            (e) => e.code,
            'code',
            PluxErrorCode.stateWriteTypeMismatch,
          ),
        ),
      );
      expect(
        () => access.write('app.token', 7),
        throwsA(
          isA<StateWriteException>().having(
            (e) => e.message.contains('7'),
            'sensitive value in message',
            isFalse,
          ),
        ),
      );
      for (final path in [
        'app.doubled',
        'app.missing',
        'page.count',
        'nowhere',
      ]) {
        expect(
          () => access.write(path, 1),
          throwsA(
            isA<StateWriteException>().having(
              (e) => e.code,
              'code',
              PluxErrorCode.stateWriteRefused,
            ),
          ),
        );
      }
      expect(
        () => access.patch('app.counter', {'a': 1}),
        throwsA(isA<StateWriteException>()),
      );
      expect(await Plux.state<int>('doubled').set(3), isFalse);
      expect(app()['counter'], 0);
    },
  );

  testWidgets(
    'computed entries are evaluated only when a value they read changes [STA-004]',
    (tester) async {
      await start(tester);
      final n = Plux.container.read(appStateProvider.notifier);
      final before = n.computations;
      expect(await Plux.state<String>('theme').set('dark'), isTrue);
      expect(n.computations, before);
      expect(await Plux.state<int>('counter').set(2), isTrue);
      expect(n.computations, before + 1);
      expect(app()['doubled'], 4);
    },
  );

  testWidgets(
    'the change stream reports each change of a path once, and stops [STA-001]',
    (tester) async {
      await start(tester);
      final access = ScopeStateAccess(
        container: Plux.container,
        plugin: 'notes',
      );
      final changes = <(Object?, Object?)>[];
      final stop = access.listen('app.counter', (a, b) => changes.add((a, b)));
      final themes = <Object?>[];
      final stopTheme = access.listen('app.theme', (_, b) => themes.add(b));
      access.write('app.counter', 1);
      access.write('app.counter', 1);
      access.write('plugin.visits', 3);
      stop();
      access.write('app.counter', 2);
      expect(changes, [(0, 1)]);
      expect(themes, isEmpty);
      stopTheme();
    },
  );

  testWidgets(
    'persisted and secure entries survive a restart, secure ones encrypted and persisted ones plain without a key; session entries end with the app [STA-003] [SCH-012]',
    (tester) async {
      await start(tester);
      await tap(tester, 'bump');
      await tap(tester, 'secret');
      await tap(tester, 'patch');
      expect(await Plux.state<String>('theme').set('dark'), isTrue);
      await tester.runAsync(() => h.runtime.statePersistence.flush());
      final files = {
        for (final f in Directory(
          h.dir,
        ).listSync(recursive: true).whereType<File>())
          if (f.path.contains('plux-state')) f.uri.pathSegments.last: f,
      };
      expect(files.keys.toSet(), {'persisted.json', 'secure.pxk'});
      final secure = latin1.decode(files['secure.pxk']!.readAsBytesSync());
      expect(secure.contains('s3cr3t'), isFalse);
      final persisted = files['persisted.json']!.readAsStringSync();
      expect(
        jsonDecode(persisted),
        isA<Map<String, Object?>>().having(
          (m) => m.length,
          'entries',
          greaterThan(0),
        ),
      );
      expect(persisted.contains('s3cr3t'), isFalse);
      // Persisted state is plain by decision (plan p5 D6).
      expect(persisted.contains('first'), isTrue);
      expect(
        h.secrets.values.keys.where((k) => k.contains('persisted')),
        isEmpty,
        reason: 'persisted state keeps no key in secure storage',
      );
      await restart(tester);
      expect(app()['counter'], 5);
      expect(app()['token'], 's3cr3t');
      expect(app()['theme'], 'light');
      expect(find.text('app 5 10'), findsOneWidget);
      expect(find.text('draft first true'), findsOneWidget);
      expect(find.text('visits 0'), findsOneWidget);
      expect(
        h.errors.map((e) => e.code),
        isNot(contains(PluxErrorCode.actionsNotAvailable)),
      );
    },
  );

  testWidgets(
    'a stored value of the previous type is migrated; one of an unknown type starts from the default [STA-040]',
    (tester) async {
      await start(tester);
      String fp(String canonical) =>
          sha256.convert(utf8.encode(canonical)).toString().substring(0, 16);
      final decls = Plux.container.read(appStateProvider.notifier).decls;
      final p = h.runtime.statePersistence
        ..write(
          Persistence.persisted,
          ':${decls['level']!.id}',
          fp('string'),
          'abcd',
        )
        ..write(
          Persistence.persisted,
          ':${decls['counter']!.id}',
          fp('bool'),
          true,
        );
      await tester.runAsync(p.flush);
      await restart(tester);
      expect(app()['level'], 4);
      expect(app()['counter'], 0);
      expect(
        h.errors
            .where((e) => e.code == PluxErrorCode.stateMigrationFailed)
            .map((e) => e.details['state']),
        ['counter'],
      );
    },
  );

  testWidgets(
    'a tampered store is discarded and reported; every entry starts from its default [STA-003]',
    (tester) async {
      await start(tester);
      await tap(tester, 'bump');
      await tester.runAsync(() => h.runtime.statePersistence.flush());
      final f = Directory(h.dir)
          .listSync(recursive: true)
          .whereType<File>()
          .firstWhere((f) => f.path.endsWith('persisted.json'));
      final bytes = f.readAsBytesSync();
      bytes[bytes.length - 1] ^= 1;
      f.writeAsBytesSync(bytes);
      await restart(tester);
      expect(app()['counter'], 0);
      expect(
        h.errors.map((e) => e.code),
        contains(PluxErrorCode.stateStoreCorrupt),
      );
    },
  );

  testWidgets(
    'Plux.wipeData removes stored state, its keys and cached responses [HST-001] [DAT-010]',
    (tester) async {
      await start(tester);
      await tap(tester, 'bump');
      await tester.runAsync(() => h.runtime.statePersistence.flush());
      // A response the data layer cached for the user.
      final root = Directory(h.dir)
          .listSync(recursive: true)
          .whereType<Directory>()
          .firstWhere((d) => d.path.endsWith('plux-state'))
          .parent
          .path;
      final cached = File('$root/data/plain/${'a' * 64}')
        ..createSync(recursive: true)
        ..writeAsBytesSync(List.filled(12, 0));
      await tester.runAsync(Plux.wipeData);
      expect(cached.existsSync(), isFalse, reason: 'cached responses go too');
      await settle(tester);
      expect(app()['counter'], 0);
      expect(h.secrets.values, isEmpty);
      expect(
        Directory(h.dir)
            .listSync(recursive: true)
            .where((f) => f.path.contains('plux-state/') && f is File),
        isEmpty,
      );
    },
  );

  testWidgets(
    'Plux.sendEvent delivers to the connected sink; without one, or refused, it is reported with PLX-5307 [HST-013]',
    (tester) async {
      await start(tester);
      expect(await Plux.sendEvent('refresh', {'by': 2}), isFalse);
      final sink = _Sink();
      h.runtime.hostEventSink = sink;
      expect(await Plux.sendEvent('refresh', {'by': 2}), isTrue);
      expect(sink.got.single.payload, {'by': 2});
      expect(await Plux.sendEvent('nope'), isFalse);
      expect(
        h.errors.where((e) => e.code == PluxErrorCode.hostEventRefused),
        hasLength(2),
      );
    },
  );

  test(
    'a page instance without a model keeps its initial values [STA-001]',
    () {
      final c = ProviderContainer();
      addTearDown(c.dispose);
      final i = PageInstance({'a': 1});
      expect(c.read(pageStateProvider(i)), {'a': 1});
    },
  );
}

final class _Sink implements HostEventSink {
  final List<PluxHostEvent> got = [];

  @override
  bool deliver(PluxHostEvent event) {
    if (event.name != 'refresh') return false;
    got.add(event);
    return true;
  }
}
