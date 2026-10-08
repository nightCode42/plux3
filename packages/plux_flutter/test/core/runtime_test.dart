// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

import '../support/entering.dart';
import '../sync/fake_device.dart';
import '../sync/fake_server.dart';

/// Renders a page as its route, or throws when told to.
final class _TestRenderer with AllowsEveryGuard implements PageRenderer {
  bool fail = false;

  @override
  Widget build(
    BuildContext context,
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params, {
    bool routed = false,
    void Function(Object? result)? onPop,
    void Function(String event, Object? payload)? onEvent,
  }) {
    if (fail) throw StateError('broken page');
    return Text(
      'page ${page.route} of ${release.sequence} ${params['x'] ?? ''}',
      textDirection: TextDirection.ltr,
    );
  }
}

http.Client _client() => http.Client();

final _hostValue = Provider<int>((ref) => 0);

/// A baseline reader whose closure holds only the files, so it can be sent
/// to the sync isolate.
Future<Uint8List?> Function(String) _reader(Map<String, Uint8List> files) =>
    (p) async => files[p];

void main() {
  final goldens = Goldens.load();
  final demo = goldens.bundles['loan-calculator/demo.pxb']!;
  final loans = goldens.bundles['loan-calculator/loans.pxb']!;
  final tasks = goldens.bundles['features/tasks.pxb']!;
  final features = goldens.bundles['features/features.pxb']!;

  late FakePluxServer server;
  late String dir;
  late _TestRenderer renderer;
  final errors = <PluxException>[];

  setUp(() async {
    server = await FakePluxServer.start();
    dir = Directory.systemTemp.createTempSync('plux_rt').path;
    renderer = _TestRenderer();
    errors.clear();
  });
  tearDown(() async {
    await Plux.dispose();
    await server.close();
    Directory(dir).deleteSync(recursive: true);
  });

  PluxConfig config({
    StartupPolicy startup = const StartupPolicy.useCacheThenSync(),
    ActivationPolicy activation = ActivationPolicy.atSafePoint,
    ProviderContainer? container,
    ProviderContainer? parent,
    bool appFallback = true,
    Map<String, PluxFallbackBuilder> pluginFallbacks = const {},
  }) => PluxConfig(
    appId: FakePluxServer.app,
    endpoint: server.endpoint,
    rootKeys: [
      PluxPublicKey(
        keyId: server.keyId,
        algorithm: 'ed25519',
        role: 'targets',
        publicKey: server.publicKey,
      ),
    ],
    storageDirectory: dir,
    httpClient: _client,
    startup: startup,
    activation: activation,
    container: container,
    parentContainer: parent,
    onError: (e, _) => errors.add(e),
    fallbackBuilder: appFallback
        ? (_, e) =>
              Text('fallback ${e.code.id}', textDirection: TextDirection.ltr)
        : null,
    pluginFallbackBuilders: pluginFallbacks,
  );

  Future<PluxStartup> start(
    PluxConfig c, {
    Map<String, Uint8List>? baseline,
    Duration healthy = const Duration(hours: 1),
  }) async {
    final files = baseline ?? const <String, Uint8List>{};
    final s = await Plux.initializeWith(
      c,
      RuntimeOverrides(
        credentials: MemoryCredentialStore.new,
        configSecrets: MemorySecretStore.new,
        deviceKeys: FakeDeviceKeys.new,
        attestation: FakeAttestation.new,
        baseline: _reader(files),
        healthyAfter: healthy,
      ),
    );
    Plux.container.read(pluxRuntimeProvider)!.renderer = renderer;
    return s;
  }

  PluxRuntime rt() => Plux.container.read(pluxRuntimeProvider)!;

  /// Lets the sync isolate answer: its replies resume code in the test's
  /// fake-async zone, which runs only when the tester pumps.
  Future<void> settle(WidgetTester tester, bool Function() done) async {
    for (var i = 0; i < 200 && !done(); i++) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 10)),
      );
      await tester.pump();
    }
    expect(done(), isTrue);
  }

  Future<T> nextEvent<T extends SyncEvent>() =>
      Plux.syncEvents.firstWhere((e) => e is T).then((e) => e as T);

  testWidgets(
    'starts from the embedded baseline offline and renders it [SYN-007] [SYN-003] [HST-001]',
    (tester) async {
      final startup = await tester.runAsync(() async {
        server.release = null; // the server has nothing: offline in effect
        return start(
          config(),
          baseline: await server.baseline(5, demo, {'loans': loans}),
        );
      });
      expect(startup!.ready, isTrue);
      expect(startup.sequence, 5);
      expect(startup.toString(), contains('ready'));
      await tester.pumpWidget(
        const PluxScope(child: PluxView('result', inputs: {'x': 'p'})),
      );
      expect(find.text('page result of 5 p'), findsOneWidget);
    },
  );

  testWidgets(
    'reads the baseline from the host app\'s assets, which only the UI '
    'isolate can load, verifies it on the sync isolate, and the first sync '
    'is a delta from it [SYN-007] [SYN-011]',
    (tester) async {
      final files = await tester.runAsync(
        () => server.baseline(5, demo, {'loans': loans}),
      );
      final asked = <String>[];
      tester.binding.defaultBinaryMessenger.setMockMessageHandler(
        'flutter/assets',
        (message) async {
          final key = utf8.decode(Uint8List.sublistView(message!));
          asked.add(key);
          final f = files![key.replaceFirst('assets/plux/', '')];
          return f == null ? null : ByteData.sublistView(f);
        },
      );
      addTearDown(
        () => tester.binding.defaultBinaryMessenger.setMockMessageHandler(
          'flutter/assets',
          null,
        ),
      );
      server.release = null; // the server has nothing: offline in effect
      final startup = await tester.runAsync(
        () => Plux.initializeWith(
          config(),
          const RuntimeOverrides(
            credentials: MemoryCredentialStore.new,
            configSecrets: MemorySecretStore.new,
            deviceKeys: FakeDeviceKeys.new,
            attestation: FakeAttestation.new,
          ),
        ),
      );
      expect(startup!.sequence, 5, reason: '${startup.error} $errors');
      expect(asked, contains('assets/plux/baseline.json'));
      expect(asked, contains('assets/plux/bundles/loans.pxb'));

      // The first sync is a delta from the baseline: no full bundle moves.
      server
        ..addDelta(
          demo,
          features,
          goldens.deltas['compiled-loan-calculator/demo.pxb-to-features/'
              'features.pxb']!,
        )
        ..addDelta(
          loans,
          tasks,
          goldens.deltas['compiled-loan-calculator/loans.pxb-to-features/'
              'tasks.pxb']!,
        )
        ..release = FakeRelease(6, features, {'loans': tasks});
      final r = await tester.runAsync(() async => Plux.sync());
      expect(r!.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect(r.deltaRatio, lessThan(1));
      expect(
        server.requests.where((p) => p.startsWith('/v1/objects/bundles')),
        isEmpty,
      );
    },
  );

  testWidgets(
    'without a cache or baseline, waits for the first sync and reports failure [SYN-003]',
    (tester) async {
      final startup = await tester.runAsync(() async {
        final c = config();
        await server.close();
        return start(c);
      });
      expect(startup!.ready, isFalse);
      expect(startup.error!.code, PluxErrorCode.syncFailed);
      expect(startup.toString(), contains('no release'));
      await tester.pumpWidget(
        PluxScope(
          child: PluxView(
            'x',
            loadingBuilder: (_) =>
                const Text('loading', textDirection: TextDirection.ltr),
          ),
        ),
      );
      expect(find.text('loading'), findsOneWidget);
      server = await tester.runAsync(FakePluxServer.start) as FakePluxServer;
    },
  );

  testWidgets(
    'a first sync makes the first release active [SYN-001] [SYN-013]',
    (tester) async {
      final startup = await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        return start(config());
      });
      expect(startup!.sequence, 10, reason: '${startup.error} $errors');
      expect(Plux.container.read(activeReleaseProvider)!.sequence, 10);
      expect(Plux.container.read(syncStatusProvider), isA<SyncActivated>());
    },
  );

  testWidgets(
    'the runtime exposes the settings of the configuration its first sync applied [SEC-182]',
    (tester) async {
      await tester.runAsync(() async {
        server
          ..release = FakeRelease(10, demo, {'loans': loans})
          ..pinsConfig = true
          ..configVersion = 1
          ..configDocument = {
            'profile': 'strict',
            'overrides': {'inactivityLockTimeout': 120},
          }
          ..configPatches[0] = server.configDocument;
        await start(config());
      });
      final s = rt().settings.value;
      expect(errors, isEmpty);
      expect(s.version, 1);
      expect(s.profile, SecurityProfile.strict);
      expect(s.number(SecuritySetting.inactivityLockTimeout), 120);
    },
  );

  testWidgets(
    'every page of every plugin of the active release opens offline after a restart [SYN-008]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        final c = config();
        final s = await start(c);
        expect(s.sequence, 10, reason: '${s.error} $errors');
        await Plux.dispose();
        await server.close();
        final again = await start(c);
        expect(again.sequence, 10, reason: 'started from the store offline');
      });
      for (final route in ['loan-calculator', 'result']) {
        expect(rt().active.value!.page(route), isNotNull, reason: route);
        await tester.pumpWidget(PluxScope(child: PluxView(route)));
        await tester.pump();
        if (route == 'loan-calculator') {
          // It requires assurance AL1, which no device has before P6: it
          // resolves offline and fails closed (NAV-009).
          expect(find.text('page $route of 10 '), findsNothing);
          expect(
            errors.map((e) => e.code),
            contains(PluxErrorCode.navigationRefused),
          );
        } else {
          expect(find.text('page $route of 10 '), findsOneWidget);
        }
        await tester.pumpWidget(const SizedBox());
      }
    },
  );

  testWidgets(
    'a failing page shows its plugin\'s fallback, else the app\'s [RT-020]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(
          config(
            pluginFallbacks: {
              'loans': (_, e) => Text(
                'loans fallback ${e.code.id}',
                textDirection: TextDirection.ltr,
              ),
            },
          ),
        );
      });
      renderer.fail = true;
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.text('loans fallback PLX-4001'), findsOneWidget);
      expect(
        errors.map((e) => e.code),
        contains(PluxErrorCode.nodeBuildFailed),
      );
      await tester.pumpWidget(const PluxScope(child: PluxView('nowhere')));
      expect(
        find.text('fallback PLX-4100'),
        findsOneWidget,
        reason: 'no plugin',
      );
    },
  );

  testWidgets(
    'without a host fallback a failing page shows the themed default [RT-020]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config(appFallback: false));
      });
      renderer.fail = true;
      final semantics = tester.ensureSemantics();
      await tester.pumpWidget(
        MaterialApp(
          theme: ThemeData(colorSchemeSeed: Colors.teal),
          home: const PluxScope(child: PluxView('result')),
        ),
      );
      final fallback = find.byType(PluxDefaultFallback);
      expect(fallback, findsOneWidget);
      expect(find.text('PLX-4001'), findsOneWidget, reason: 'debug builds');
      expect(find.bySemanticsLabel('Content unavailable'), findsOneWidget);
      final box = tester.widget<ColoredBox>(
        find.descendant(of: fallback, matching: find.byType(ColoredBox)),
      );
      final context = tester.element(fallback);
      expect(box.color, Theme.of(context).colorScheme.surfaceContainerHighest);
      semantics.dispose();
    },
  );

  testWidgets(
    'never swaps a release under a mounted page; activates when it leaves [SYN-004]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config());
      });
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.text('page result of 10 '), findsOneWidget);
      expect(rt().mountedPages, 1);
      await tester.runAsync(() async {
        server.release = FakeRelease(11, features, {'tasks': tasks});
        final run = Plux.sync();
        final progress = run.progress.toList();
        final r = await run;
        expect(r.outcome, SyncOutcome.staged);
        expect(
          (await progress).map((e) => e.runtimeType),
          containsAllInOrder([SyncChecking, SyncStaged]),
          reason: 'a run carries its own events',
        );
      });
      await tester.pump();
      expect(
        rt().active.value!.sequence,
        10,
        reason: 'the page is still on screen',
      );
      expect(find.text('page result of 10 '), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
      await settle(tester, () => rt().active.value?.sequence == 11);
      expect((rt().lastEvent.value! as SyncActivated).sequence, 11);
      await tester.pumpWidget(const PluxScope(child: PluxView('tasks')));
      await tester.pump(); // its guard decides first (NAV-009)
      expect(find.text('page tasks of 11 '), findsOneWidget);
    },
  );

  for (final policy in [ActivationPolicy.immediate, ActivationPolicy.forced]) {
    testWidgets(
      '${policy.name} activates as soon as no Plux page is on screen [SYN-004]',
      (tester) async {
        await tester.runAsync(() async {
          server.release = FakeRelease(10, demo, {'loans': loans});
          await start(config(activation: policy));
          server.release = FakeRelease(11, features, {'tasks': tasks});
          expect((await Plux.sync()).outcome, SyncOutcome.staged);
        });
        await settle(tester, () => rt().active.value?.sequence == 11);
      },
    );
  }

  testWidgets(
    'nextLaunch leaves the staged release for the next start [SYN-004]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config(activation: ActivationPolicy.nextLaunch));
        server.release = FakeRelease(11, features, {'tasks': tasks});
        expect((await Plux.sync()).outcome, SyncOutcome.staged);
        expect(rt().active.value!.sequence, 10);
        await Plux.dispose();
        server.release = FakeRelease(11, features, {'tasks': tasks});
        final s = await start(config(activation: ActivationPolicy.nextLaunch));
        expect(s.sequence, 11);
      });
    },
  );

  testWidgets(
    'blockUntilSynced activates a new release before returning [SYN-003]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config());
        await Plux.dispose();
        server.release = FakeRelease(11, features, {'tasks': tasks});
        final s = await start(
          config(
            startup: const StartupPolicy.blockUntilSynced(
              Duration(seconds: 20),
            ),
          ),
        );
        expect(s.sequence, 11);
        expect(
          const StartupPolicy.blockUntilSynced(Duration(seconds: 1)).toString(),
          contains('block'),
        );
        expect(
          const StartupPolicy.useCacheThenSync().toString(),
          contains('useCache'),
        );
      });
    },
  );

  testWidgets(
    'a switched-off plugin renders the fallback for every route into it [RT-022]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(
          10,
          demo,
          {'loans': loans},
          killSwitches: ['loans'],
        );
        await start(config());
      });
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.text('fallback PLX-4020'), findsOneWidget);
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.text('fallback PLX-4020'), findsOneWidget);
      await tester.pumpWidget(
        const PluxScope(child: PluxView('no-such-route')),
      );
      expect(find.text('fallback PLX-4100'), findsOneWidget);
      final info = Plux.diagnostics.release.value!;
      expect(info.sequence, 10);
      expect(
        {for (final p in info.plugins) p.key: p.switchedOff},
        {'': false, 'loans': true},
      );
      expect(Plux.diagnostics.syncStatus.value, isA<SyncActivated>());
    },
  );

  testWidgets(
    'a switched-off plugin with a declared fallback page renders it [RT-022]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(
          10,
          features,
          {'tasks': tasks},
          killSwitches: ['tasks'],
        );
        await start(config());
      });
      await tester.pumpWidget(const PluxScope(child: PluxView('tasks')));
      expect(find.text('page tasks-unavailable of 10 '), findsOneWidget);
    },
  );

  testWidgets(
    'a switched-off plugin whose fallback page is guarded shows the generic fallback: the kill switch never opens a guarded page [RT-022] [NAV-009]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(
          10,
          goldens.bundles['routing/routing.pxb']!,
          {'nav': goldens.bundles['routing/nav.pxb']!},
          killSwitches: ['nav'],
        );
        await start(config());
      });
      // nav's fallback page is the vault, which requires assurance AL1.
      await tester.pumpWidget(const PluxScope(child: PluxView('home')));
      expect(find.text('page vault of 10 '), findsNothing);
      expect(find.text('fallback PLX-4020'), findsOneWidget);
    },
  );

  testWidgets(
    'the app kill switch renders the app-level fallback with its message [RT-022]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(
          10,
          features,
          {'tasks': tasks},
          appKillSwitch: true,
          message: 'back soon',
        );
        await start(config());
      });
      await tester.pumpWidget(
        PluxScope(
          child: PluxView(
            'tasks',
            fallbackBuilder: (_, e) => Text(
              '${e.code.id} ${e.message}',
              textDirection: TextDirection.ltr,
            ),
          ),
        ),
      );
      expect(find.text('PLX-4020 back soon'), findsOneWidget);
    },
  );

  testWidgets(
    'a plugin whose stored bundle was tampered with renders the fallback [RT-022] [SEC-052]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config());
        await Plux.dispose();
        final store = ReleaseStore.open(
          '$dir/${FakePluxServer.app}/production/production',
        );
        final path = store.objectPath(
          ObjectKind.bundles,
          Goldens.hashOf(loans),
        );
        final bytes = File(path).readAsBytesSync()..[100] ^= 1;
        File(path).writeAsBytesSync(bytes);
        server.release = null;
        await start(config());
      });
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.textContaining('fallback PLX-30'), findsOneWidget);
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.textContaining('fallback PLX-30'), findsOneWidget);
      final reported = errors.where(
        (e) => e.message.contains('not the signed one'),
      );
      expect(
        reported,
        hasLength(1),
        reason: 'reported once, however often it is used',
      );
      expect(
        Plux.diagnostics.log.value.map((d) => d.error),
        contains(reported.single),
      );
    },
  );

  testWidgets(
    'a plugin whose stored bundle is missing renders the fallback [RT-020] [RT-022]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = FakeRelease(10, demo, {'loans': loans});
        await start(config());
        await Plux.dispose();
        final store = ReleaseStore.open(
          '$dir/${FakePluxServer.app}/production/production',
        );
        File(store.objectPath(ObjectKind.bundles, Goldens.hashOf(loans)))
            .deleteSync();
        server.release = null;
        await start(config());
      });
      await tester.pumpWidget(const PluxScope(child: PluxView('result')));
      expect(find.textContaining('fallback PLX-30'), findsOneWidget);
      expect(
        errors.where((e) => e.message.contains('cannot be read')),
        hasLength(1),
      );
    },
  );

  testWidgets(
    'three failures of a new release revert to the last known good one [SYN-006] [RT-020]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = null;
        await start(
          config(),
          baseline: await server.baseline(5, demo, {'loans': loans}),
        );
        server.release = FakeRelease(6, demo, {'loans': tasks});
        expect((await Plux.sync()).outcome, SyncOutcome.staged);
        await nextEvent<SyncActivated>().timeout(
          const Duration(seconds: 5),
          onTimeout: () => const SyncActivated(6),
        );
        await Plux.dispose();
        server.release = null;
        await start(config()); // launch 1 of the trial
      });
      expect(rt().active.value!.sequence, 6);
      renderer.fail = true;
      for (var i = 0; i < 3; i++) {
        await tester.pumpWidget(
          PluxScope(key: ValueKey(i), child: const PluxView('tasks')),
        );
        await tester.pump(); // its guard decides first (NAV-009)
        expect(find.textContaining('fallback PLX-4001'), findsOneWidget);
        await tester.pumpWidget(const SizedBox());
        await settle(tester, () => true);
      }
      await settle(tester, () => rt().active.value?.sequence == 5);
      expect(
        errors.map((e) => e.code),
        contains(PluxErrorCode.revertedToLastKnownGood),
      );
    },
  );

  testWidgets(
    'works with its own, a shared or a nested Riverpod container [RT-003]',
    (tester) async {
      final host = ProviderContainer();
      addTearDown(host.dispose);
      await tester.runAsync(() async {
        server.release = null;
        await start(
          config(container: host),
          baseline: await server.baseline(5, demo, {'loans': loans}),
        );
      });
      expect(identical(Plux.container, host), isTrue);
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: host,
          child: const PluxScope(child: PluxView('result')),
        ),
      );
      expect(find.text('page result of 5 '), findsOneWidget);
      final parent = ProviderContainer(
        overrides: [_hostValue.overrideWithValue(42)],
      );
      addTearDown(parent.dispose);
      await tester.runAsync(() async {
        await Plux.dispose();
        await start(config(parent: parent));
      });
      expect(
        Plux.container.read(_hostValue),
        42,
        reason: 'nested: host overrides are visible to Plux',
      );
      expect(
        parent.read(pluxRuntimeProvider),
        isNull,
        reason: 'nested: the host container stays clean',
      );
    },
  );

  testWidgets(
    'host setters change the environment Plux pages render with [HST-001]',
    (tester) async {
      await tester.runAsync(() async {
        server.release = null;
        await start(
          config(),
          baseline: await server.baseline(5, demo, {'loans': loans}),
        );
      });
      Plux.setLocale(const Locale('am', 'ET'));
      Plux.setThemeMode(ThemeMode.dark);
      Plux.setBrand('acme');
      Plux.setConsent(const PluxConsent(analytics: true));
      Plux.setUserContext(
        const PluxUser(id: 'u1', attributes: {'tier': 'gold'}),
      );
      Plux.setAuthDelegate(null);
      final e = Plux.container.read(environmentProvider);
      expect(
        [e.locale, e.themeMode, e.brand, e.consent.analytics, e.user!.id],
        [const Locale('am', 'ET'), ThemeMode.dark, 'acme', true, 'u1'],
      );
      expect(Plux.isInitialized, isTrue);
      expect(() => Plux.initialize(config()), throwsStateError);
    },
  );

  testWidgets('the sync tile shows the state and syncs on tap [SYN-002]', (
    tester,
  ) async {
    await tester.runAsync(() async {
      server.release = FakeRelease(10, demo, {'loans': loans});
      await start(config());
    });
    await tester.pumpWidget(
      const PluxScope(
        child: MaterialApp(home: Material(child: PluxSyncTile())),
      ),
    );
    expect(find.textContaining('release 10'), findsOneWidget);
    await tester.tap(find.byType(ListTile));
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 500)),
    );
    await tester.pump();
    expect(find.textContaining('10'), findsWidgets);
  });

  testWidgets('Plux.open pushes a page on the host navigator [HST-001]', (
    tester,
  ) async {
    await tester.runAsync(() async {
      server.release = null;
      await start(
        config(),
        baseline: await server.baseline(5, demo, {'loans': loans}),
      );
    });
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (c) => TextButton(
            onPressed: () =>
                unawaited(Plux.open<void>(c, 'result', params: {'x': 'q'})),
            child: const Text('go'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('page result of 5 q'), findsOneWidget);
  });
}
