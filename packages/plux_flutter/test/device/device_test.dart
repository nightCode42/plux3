// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/features.dart';
import 'package:plux_flutter/src/device/guard.dart';
import 'package:plux_flutter/src/device/platform.dart';
import 'package:plux_flutter/src/device/services.dart';
import 'package:plux_flutter/src/pxl/values.dart';

import '../actions/engine_test.dart' show FakeNavigator, id, input, lit;

final class FakeUrls implements UrlOpener {
  final List<Uri> opened = [];
  bool answer = true;

  @override
  Future<bool> open(Uri uri) async {
    opened.add(uri);
    return answer;
  }
}

final class FakePlatform implements DevicePlatform {
  final List<Map<String, String?>> shares = [];
  final List<String> asked = [];
  bool grant = true;
  PluxDeviceException? failure;

  @override
  Future<void> share({
    String? text,
    String? url,
    String? filePath,
    String? mimeType,
  }) async {
    if (failure case final f?) throw f;
    shares.add({'text': text, 'url': url, 'filePath': filePath});
  }

  @override
  Future<bool> requestPermission(String permission) async {
    asked.add(permission);
    if (failure case final f?) throw f;
    return grant;
  }
}

final class FakeMedia implements PluxMediaPicker {
  List<PluxPickedFile> images = const [];
  PluxPickedFile? photo;
  List<PluxPickedFile> files = const [];
  final List<String> calls = [];

  @override
  Future<List<PluxPickedFile>> pickImages({
    required bool multiple,
    required int limit,
    int? maxDimension,
  }) async {
    calls.add('images $multiple $limit $maxDimension');
    return images;
  }

  @override
  Future<PluxPickedFile?> capturePhoto({int? maxDimension}) async {
    calls.add('photo $maxDimension');
    return photo;
  }

  @override
  Future<List<PluxPickedFile>> pickFiles({
    required List<String> mimeTypes,
    required bool multiple,
    required int limit,
  }) async {
    calls.add('files ${mimeTypes.join(',')} $multiple $limit');
    return files;
  }
}

final class FakeScanner implements PluxCodeScanner {
  PluxScanResult? result;
  List<String>? asked;

  @override
  Future<PluxScanResult?> scan(List<String> formats) async {
    asked = formats;
    return result;
  }
}

final class FakeLocation implements PluxLocationProvider {
  PluxDeviceException? failure;
  String? asked;

  @override
  Future<PluxPosition> current(String accuracy) async {
    asked = accuracy;
    if (failure case final f?) throw f;
    return PluxPosition(
      latitude: 52.5,
      longitude: 13.4,
      accuracy: 12,
      timestamp: DateTime.utc(2026, 1, 2, 3, 4, 5),
    );
  }
}

final class Package implements PluxDevicePackage {
  Package(this.name, this.services);

  @override
  final String name;

  @override
  final Map<Type, Object> services;
}

PluxPickedFile file(String name, {String mime = 'image/jpeg'}) =>
    PluxPickedFile(
      path: '/private/$name',
      name: name,
      mimeType: mime,
      sizeBytes: 42,
    );

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late List<PluxException> reports;
  late FakeUrls urls;
  late FakePlatform platform;
  late List<String> links;
  late List<MethodCall> platformCalls;
  late Map<String, List<String>> declared;
  late Map<String, List<String>> domains;
  late fbs.DeepLinks? deepLinks;
  late List<PluxDevicePackage> packages;
  Set<String>? allowed;
  var secure = false;
  late Map<String, int> limits;

  DeviceGuard guard() => DeviceGuard(
    capabilities: (plugin) => (
      deviceApis: declared[plugin] ?? const [],
      networkDomains: domains[plugin] ?? const [],
    ),
    deepLinks: () => deepLinks,
    report: reports.add,
    allowed: allowed,
    limits: () => limits,
  );

  ActionHost host({String plugin = 'shop'}) => ActionHost(
    context: StepContext(
      navigator: FakeNavigator(),
      emit: (_, _) {},
      nativeActions: const NoNativeActions(),
      services: deviceServices(
        packages: packages,
        openLink: (uri) async {
          links.add(uri.toString());
          return true;
        },
        urls: urls,
        platform: platform,
      ),
      device: DeviceScope(plugin: plugin, guard: guard(), secure: secure),
    ),
    limits: const ActionLimits(
      stepsPerRun: 100,
      stepTimeout: Duration(seconds: 5),
      runTimeout: Duration(seconds: 10),
    ),
    report: reports.add,
    record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
    route: 'home',
    pluginKey: plugin,
  );

  Future<(RunResult, Map<String, Object?>)> run(
    String action,
    Map<String, Object?> inputs, {
    String plugin = 'shop',
  }) async {
    final h = host(plugin: plugin);
    final result = (await h.start(
      ActionGraph(
        id: 'g',
        steps: [
          GraphStep(
            id: 's',
            action: id(action),
            inputs: {
              for (final e in inputs.entries)
                input(action, e.key): lit(e.value),
            },
            next: 1,
          ),
          GraphStep(
            id: 'end',
            action: id('stop'),
            inputs: {
              input('stop', 'result'): (roots) =>
                  ((roots['steps']! as Map)['s'] as Map)['output'],
            },
          ),
        ],
      ),
      roots: () => const {},
      key: 'k',
    ))!;
    return (result, <String, Object?>{'output': result.result});
  }

  setUp(() {
    reports = [];
    urls = FakeUrls();
    platform = FakePlatform();
    links = [];
    declared = {
      'shop': [
        'haptics',
        'clipboard',
        'share',
        'photos',
        'camera',
        'files',
        'location',
        'notifications',
      ],
    };
    domains = {
      'shop': ['example.com', '*.cdn.example.com'],
    };
    deepLinks = null;
    packages = [];
    allowed = null;
    secure = false;
    limits = {};
    platformCalls = [];
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, (call) async {
          platformCalls.add(call);
          return null;
        });
    addTearDown(
      () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null),
    );
  });

  group('capabilities [SEC-080]', () {
    test('the runtime supports device.v1', () {
      expect(RuntimeFeatures().supports('device.v1'), isTrue);
    });

    test('an undeclared device operation is blocked before it reaches the device and reported [SEC-080]', () async {
      declared = {
        'shop': ['clipboard'],
      };
      final (r, _) = await run('haptic', {'pattern': 'light'});
      expect(r.outcome, RunOutcome.failed);
      expect(r.error?.kind, ActionErrorKind.permission);
      expect(r.error?.code, PluxErrorCode.deviceCapabilityBlocked);
      expect(platformCalls, isEmpty);
      expect(reports.first.code, PluxErrorCode.deviceCapabilityBlocked);
      expect(reports.first.details, containsPair('plugin', 'shop'));
      expect(reports.first.details, containsPair('api', 'haptics'));
    });

    test('a plugin that declares nothing may do nothing [SEC-080]', () async {
      final (r, _) = await run('copyToClipboard', {
        'text': 'x',
      }, plugin: 'other');
      expect(r.error?.code, PluxErrorCode.deviceCapabilityBlocked);
      expect(platformCalls, isEmpty);
    });

    test('a declared operation runs [SEC-080]', () async {
      final (r, _) = await run('haptic', {'pattern': 'heavy'});
      expect(r.outcome, RunOutcome.ok);
      expect(platformCalls.single.method, 'HapticFeedback.vibrate');
      expect(platformCalls.single.arguments, 'HapticFeedbackType.heavyImpact');
      expect(reports, isEmpty);
    });

    test(
      'allowedCapabilities narrows what the app approved at run time [SEC-080]',
      () async {
        allowed = {'clipboard'};
        final (r, _) = await run('haptic', {'pattern': 'light'});
        expect(r.error?.code, PluxErrorCode.deviceCapabilityBlocked);
        expect(r.error?.message, contains('the host does not allow'));
        final (ok, _) = await run('copyToClipboard', {'text': 'x'});
        expect(ok.outcome, RunOutcome.ok);
      },
    );

    test('requestPermission needs the permission declared [SEC-080]', () async {
      final (denied, _) = await run('requestPermission', {
        'permission': 'contacts',
      });
      expect(denied.error?.code, PluxErrorCode.deviceCapabilityBlocked);
      expect(platform.asked, isEmpty);
      final (ok, _) = await run('requestPermission', {
        'permission': 'location',
      });
      expect(ok.outcome, RunOutcome.ok);
      expect(platform.asked, ['location']);
    });

    test(
      'every device action maps to a device API of the schema [SEC-080]',
      () {
        expect(
          DeviceGuard.apiOf.values.toSet(),
          everyElement(
            isIn(const [
              'camera',
              'photos',
              'files',
              'location',
              'contacts',
              'biometrics',
              'notifications',
              'clipboard',
              'share',
              'haptics',
            ]),
          ),
        );
      },
    );
  });

  group('core actions', () {
    test('haptic plays each pattern', () async {
      for (final p in ['light', 'medium', 'heavy', 'selection', 'vibrate']) {
        await run('haptic', {'pattern': p});
      }
      expect(
        [for (final c in platformCalls) c.arguments ?? c.method],
        [
          'HapticFeedbackType.lightImpact',
          'HapticFeedbackType.mediumImpact',
          'HapticFeedbackType.heavyImpact',
          'HapticFeedbackType.selectionClick',
          'HapticFeedback.vibrate',
        ],
      );
    });

    test(
      'copyToClipboard copies text, and is blocked on a secure page [SEC-090]',
      () async {
        final (ok, _) = await run('copyToClipboard', {'text': 'hello'});
        expect(ok.outcome, RunOutcome.ok);
        final set = platformCalls.single;
        expect(set.method, 'Clipboard.setData');
        expect((set.arguments as Map)['text'], 'hello');

        platformCalls.clear();
        secure = true;
        final (blocked, _) = await run('copyToClipboard', {'text': 'secret'});
        expect(blocked.outcome, RunOutcome.failed);
        expect(blocked.error?.kind, ActionErrorKind.permission);
        expect(blocked.error?.code, PluxErrorCode.clipboardBlocked);
        expect(platformCalls, isEmpty);
      },
    );

    test('copyToClipboard obeys device.clipboardChars [LIM-001]', () async {
      limits = {'device.clipboardChars': 3};
      final (r, _) = await run('copyToClipboard', {'text': 'abcd'});
      expect(r.error?.code, PluxErrorCode.clipboardBlocked);
      expect(platformCalls, isEmpty);
    });

    test(
      'openUrl opens HTTPS URLs on declared domains only [SEC-080]',
      () async {
        final (ok, _) = await run('openUrl', {
          'url': 'https://example.com/terms?x=1',
        });
        expect(ok.outcome, RunOutcome.ok);
        expect(urls.opened.single.toString(), 'https://example.com/terms?x=1');

        final (sub, _) = await run('openUrl', {
          'url': 'https://img.cdn.example.com/a',
        });
        expect(sub.outcome, RunOutcome.ok);

        for (final bad in [
          'https://evil.test/',
          'http://example.com/',
          'https://cdn.example.com/',
          'mailto:a@example.com',
          'javascript:alert(1)',
          'not a url',
        ]) {
          final (r, _) = await run('openUrl', {'url': bad});
          expect(r.error?.code, PluxErrorCode.openUrlBlocked, reason: bad);
        }
        expect(urls.opened, hasLength(2));
        expect(reports.map((e) => e.code).toSet(), {
          PluxErrorCode.openUrlBlocked,
        });
        // The report names the host, never the path or query (SEC-092).
        expect(reports.map((e) => e.message).join(), isNot(contains('terms')));
      },
    );

    test(
      'openUrl opens a deep link of the app inside the app [NAV-008]',
      () async {
        deepLinks = fbs.DeepLinks(
          fbs.DeepLinksObjectBuilder(
            hosts: ['links.example.org'],
            schemes: ['acme'],
            routes: [],
          ).toBytes(),
        );
        domains = {'shop': []};
        final (a, _) = await run('openUrl', {'url': 'acme://p/home'});
        final (b, _) = await run('openUrl', {
          'url': 'https://links.example.org/p/home',
        });
        expect(a.outcome, RunOutcome.ok);
        expect(b.outcome, RunOutcome.ok);
        expect(links, ['acme://p/home', 'https://links.example.org/p/home']);
        expect(urls.opened, isEmpty);
      },
    );

    test('openUrl fails when the platform cannot open the address', () async {
      urls.answer = false;
      final (r, _) = await run('openUrl', {'url': 'https://example.com/'});
      expect(r.error?.code, PluxErrorCode.deviceUnavailable);
    });

    test(
      'share hands text, a URL or a picked file to the share sheet',
      () async {
        final (a, _) = await run('share', {
          'text': 'hi',
          'url': 'https://example.com',
        });
        expect(a.outcome, RunOutcome.ok);
        expect(platform.shares.single, containsPair('text', 'hi'));

        packages = [
          Package('plux_media', {
            PluxMediaPicker: FakeMedia()..photo = file('a.jpg'),
          }),
        ];
        final h = host();
        final shot = await h.start(
          ActionGraph(
            id: 'g',
            steps: [
              GraphStep(id: 'p', action: id('capturePhoto'), next: 1),
              GraphStep(
                id: 's',
                action: id('share'),
                inputs: {
                  input('share', 'file'): (roots) =>
                      ((roots['steps']! as Map)['p'] as Map)['output'],
                },
              ),
            ],
          ),
          roots: () => const {},
          key: 'k',
        );
        expect(shot?.outcome, RunOutcome.ok);
        expect(
          platform.shares.last,
          containsPair('filePath', '/private/a.jpg'),
        );
      },
    );

    test(
      'share refuses an empty or unknown file, and platform failures are typed',
      () async {
        final (empty, _) = await run('share', {});
        expect(empty.error?.kind, ActionErrorKind.validation);
        final (unknown, _) = await run('share', {
          'file': {
            'handle': 'nope',
            'name': 'a',
            'mimeType': 'x/y',
            'sizeBytes': 1,
          },
        });
        expect(unknown.error?.kind, ActionErrorKind.validation);
        platform.failure = const PluxDeviceException.unavailable('no sheet');
        final (r, _) = await run('share', {'text': 'x'});
        expect(r.error?.code, PluxErrorCode.deviceUnavailable);
      },
    );

    test('requestPermission takes the granted or denied branch', () async {
      const h = 'requestPermission';
      final ctx = StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        services: deviceServices(
          packages: const [],
          openLink: (_) async => true,
          platform: platform,
        ),
        device: DeviceScope(plugin: 'shop', guard: guard()),
      );
      final handler = deviceHandlerOf(h);
      var r = await handler.run(ctx, {'permission': 'camera'}) as StepDone;
      expect((r.output, r.branch), (true, 'granted'));
      platform.grant = false;
      r = await handler.run(ctx, {'permission': 'camera'}) as StepDone;
      expect((r.output, r.branch), (false, 'denied'));
      expect(
        () => handler.run(ctx, {'permission': 'haptics'}),
        throwsA(isA<ActionError>()),
      );
    });
  });

  group('optional packages [RT-060]', () {
    test('an action whose package the host lacks fails with a typed permission error [RT-060]', () async {
      for (final (action, inputs, package)
          in <(String, Map<String, Object?>, String)>[
            ('pickImage', {}, 'plux_media'),
            ('capturePhoto', {}, 'plux_media'),
            ('pickFile', {}, 'plux_media'),
            ('scanCode', {}, 'plux_scanner'),
            ('getLocation', {}, 'plux_location'),
          ]) {
        final (r, _) = await run(action, inputs);
        expect(r.outcome, RunOutcome.failed, reason: action);
        expect(r.error?.kind, ActionErrorKind.permission, reason: action);
        expect(
          r.error?.code,
          PluxErrorCode.devicePackageMissing,
          reason: action,
        );
        expect(r.error?.message, contains(package), reason: action);
      }
    });

    test(
      'pickImage returns picked files behind handles, never paths',
      () async {
        final media = FakeMedia()..images = [file('a.jpg'), file('b.jpg')];
        packages = [
          Package('plux_media', {PluxMediaPicker: media}),
        ];
        limits = {'device.pickCount': 1};
        final (r, out) = await run('pickImage', {
          'multiple': true,
          'maxDimension': 800,
        });
        expect(r.outcome, RunOutcome.ok);
        expect(media.calls.single, 'images true 1 800');
        final picked = (out['output']! as List).cast<Map<String, Object?>>();
        expect(picked, hasLength(1));
        expect(picked.single['name'], 'a.jpg');
        expect(picked.single['mimeType'], 'image/jpeg');
        expect(picked.single['sizeBytes'], 42);
        expect(picked.single['handle'], startsWith('pf-'));
        expect(picked.single.toString(), isNot(contains('/private')));
      },
    );

    test('a cancelled pick takes the cancelled branch', () async {
      packages = [
        Package('plux_media', {PluxMediaPicker: FakeMedia()}),
      ];
      final ctx = StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        services: deviceServices(
          packages: packages,
          openLink: (_) async => true,
        ),
        device: DeviceScope(plugin: 'shop', guard: guard()),
      );
      for (final (action, inputs) in <(String, Map<String, Object?>)>[
        ('pickImage', {}),
        ('capturePhoto', {}),
        (
          'pickFile',
          {
            'mimeTypes': <Object?>['application/pdf'],
          },
        ),
      ]) {
        final r = await deviceHandlerOf(action).run(ctx, inputs) as StepDone;
        expect(r.branch, 'cancelled', reason: action);
      }
    });

    test(
      'pickFile passes the accepted types and capturePhoto the size',
      () async {
        final media = FakeMedia()
          ..files = [file('d.pdf', mime: 'application/pdf')]
          ..photo = file('p.jpg');
        packages = [
          Package('plux_media', {PluxMediaPicker: media}),
        ];
        final (f, out) = await run('pickFile', {
          'mimeTypes': ['application/pdf'],
        });
        expect(f.outcome, RunOutcome.ok);
        expect(media.calls.single, 'files application/pdf false 1');
        expect(
          ((out['output']! as List).single as Map)['mimeType'],
          'application/pdf',
        );
        final (p, photo) = await run('capturePhoto', {'maxDimension': 1024});
        expect(p.outcome, RunOutcome.ok);
        expect(media.calls.last, 'photo 1024');
        expect((photo['output']! as Map)['name'], 'p.jpg');
      },
    );

    test('scanCode returns the decoded value and format', () async {
      final scanner = FakeScanner()
        ..result = const PluxScanResult(
          value: 'https://x.test',
          format: 'qrCode',
        );
      packages = [
        Package('plux_scanner', {PluxCodeScanner: scanner}),
      ];
      final (r, out) = await run('scanCode', {
        'formats': ['qrCode', 'ean13'],
      });
      expect(r.outcome, RunOutcome.ok);
      expect(scanner.asked, ['qrCode', 'ean13']);
      expect(out['output'], {'value': 'https://x.test', 'format': 'qrCode'});
    });

    test('getLocation returns a GeoLocation and maps failures', () async {
      final location = FakeLocation();
      packages = [
        Package('plux_location', {PluxLocationProvider: location}),
      ];
      final (r, out) = await run('getLocation', {'accuracy': 'high'});
      expect(r.outcome, RunOutcome.ok);
      expect(location.asked, 'high');
      final geo = out['output']! as Map<String, Object?>;
      expect(geo['latitude'], 52.5);
      expect(geo['altitude'], isNull);
      expect(geo['timestamp'], isA<PxlDateTime>());

      location.failure = const PluxDeviceException.denied('location denied');
      final (denied, _) = await run('getLocation', {});
      expect(denied.error?.kind, ActionErrorKind.permission);
      expect(denied.error?.code, PluxErrorCode.devicePermissionDenied);
      expect(location.asked, 'balanced');

      location.failure = const PluxDeviceException.unavailable('location off');
      final (off, _) = await run('getLocation', {});
      expect(off.error?.code, PluxErrorCode.deviceUnavailable);
    });
  });

  group('feedback', () {
    testWidgets(
      'showSnackbar shows a themed message and takes the action branch',
      (tester) async {
        final key = GlobalKey();
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(body: SizedBox(key: key)),
          ),
        );
        final ctx = StepContext(
          navigator: FakeNavigator(),
          emit: (_, _) {},
          nativeActions: const NoNativeActions(),
          device: DeviceScope(
            plugin: 'shop',
            guard: guard(),
            context: () => key.currentContext,
          ),
        );
        final run = deviceHandlerOf('showSnackbar').run(ctx, {
          'message': 'Saved',
          'actionLabel': 'Undo',
          'duration': 4000,
        });
        await tester.pump();
        expect(find.text('Saved'), findsOneWidget);
        await tester.pump(const Duration(milliseconds: 750));
        await tester.tap(find.text('Undo'));
        await tester.pumpAndSettle();
        final result = await run as StepDone;
        expect(result.branch, 'action');

        final timed = deviceHandlerOf('showSnackbar')
            .run(ctx, {'message': 'Later'});
        expect(timed, isA<StepDone>());
        await tester.pumpAndSettle(const Duration(seconds: 10));
      },
    );

    testWidgets('showToast shows a short message that goes away', (
      tester,
    ) async {
      final key = GlobalKey<NavigatorState>();
      await tester.pumpWidget(
        MaterialApp(navigatorKey: key, home: const Scaffold()),
      );
      final ctx = StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        device: DeviceScope(
          plugin: 'shop',
          guard: guard(),
          overlay: () => key.currentState?.overlay,
        ),
      );
      final r = deviceHandlerOf('showToast')
          .run(ctx, {'message': 'Copied', 'duration': 500});
      expect(r, isA<StepDone>());
      await tester.pump();
      expect(find.text('Copied'), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();
      expect(find.text('Copied'), findsNothing);
    });

    test('feedback without anywhere to show fails with a typed error', () {
      final ctx = StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        device: DeviceScope(plugin: 'shop', guard: guard()),
      );
      expect(
        () => deviceHandlerOf('showToast').run(ctx, {'message': 'x'}),
        throwsA(isA<ActionError>()),
      );
      expect(
        () => deviceHandlerOf('showSnackbar').run(ctx, {'message': 'x'}),
        throwsA(isA<ActionError>()),
      );
    });
  });
}

ActionHandler deviceHandlerOf(String action) => builtInHandlers[action]!;
