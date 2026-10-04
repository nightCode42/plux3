// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Each scan resolves Flutter and the routers, which takes seconds.
@Timeout(Duration(minutes: 4))
library;

import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:plux_native_scan/plux_native_scan.dart';
import 'package:test/test.dart';

const _id = '01f0c450-6c00-7000-8000-00000000c0de';

/// Writes a fixture host app into a temporary directory: its files under
/// lib/, and a package configuration that resolves the workspace's
/// packages (Flutter, go_router, auto_route, plux_flutter) by absolute
/// paths, as `flutter pub get` would in a host project.
Directory _app(Map<String, String> lib) {
  final dir = Directory.systemTemp.createTempSync('plux_scan');
  addTearDown(() => dir.deleteSync(recursive: true));
  final workspace = File(
    p.normalize(p.absolute('../../.dart_tool/package_config.json')),
  );
  final config =
      jsonDecode(workspace.readAsStringSync()) as Map<String, Object?>;
  final base = workspace.parent.uri;
  final packages = [
    for (final pkg
        in (config['packages']! as List).cast<Map<String, Object?>>())
      {...pkg, 'rootUri': base.resolve(pkg['rootUri']! as String).toString()},
    {
      'name': 'fixture_app',
      'rootUri': dir.uri.toString(),
      'packageUri': 'lib/',
      'languageVersion': '3.13',
    },
  ];
  File(p.join(dir.path, '.dart_tool', 'package_config.json'))
    ..createSync(recursive: true)
    ..writeAsStringSync(jsonEncode({...config, 'packages': packages}));
  File(p.join(dir.path, 'pubspec.yaml'))
      .writeAsStringSync('name: fixture_app\nenvironment:\n  sdk: ^3.13.0\n');
  for (final MapEntry(:key, :value) in lib.entries) {
    File(p.join(dir.path, 'lib', key))
      ..createSync(recursive: true)
      ..writeAsStringSync(value);
  }
  return dir;
}

/// The Dart SDK inside the Flutter SDK the workspace resolves: under
/// `flutter test` the analyzer cannot find one from its own executable.
String _dartSdk() {
  final workspace = File(
    p.normalize(p.absolute('../../.dart_tool/package_config.json')),
  );
  final config =
      jsonDecode(workspace.readAsStringSync()) as Map<String, Object?>;
  final flutter = (config['packages']! as List)
      .cast<Map<String, Object?>>()
      .singleWhere((e) => e['name'] == 'flutter');
  final root = workspace.parent.uri.resolve('${flutter['rootUri']}/');
  return p.join(p.fromUri(root), '..', '..', 'bin', 'cache', 'dart-sdk');
}

Future<ScanResult> _scan(Directory app, {List<String> slots = const []}) =>
    scan(
      root: app.path,
      slots: slots,
      host: '1.4.0+52',
      id: _id,
      sdkPath: _dartSdk(),
    );

/// One host app holding every fixture: go_router routes, auto_route pages,
/// the registrations of a plain app and a slot with every parameter shape.
final _fixtures = <String, String>{
  'router.dart': '''
import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';

final router = GoRouter(routes: [
  GoRoute(path: '/', builder: (c, s) => const SizedBox(), routes: [
    GoRoute(name: 'profile', path: 'profile/:userId', builder: (c, s) => const SizedBox()),
  ]),
  GoRoute(name: 'order', path: '/orders/:orderId/items/:itemId', builder: (c, s) => const SizedBox()),
]);
''',
  'pages.dart': '''
import 'package:auto_route/auto_route.dart';
import 'package:flutter/widgets.dart';

@RoutePage()
class ProfilePage extends StatelessWidget {
  const ProfilePage({super.key, @PathParam('userId') required this.id, @QueryParam() this.tab});
  final String id;
  final int? tab;
  @override
  Widget build(BuildContext context) => const SizedBox();
}

@RoutePage(name: 'SettingsRoute')
class SettingsView extends StatelessWidget {
  const SettingsView({super.key});
  @override
  Widget build(BuildContext context) => const SizedBox();
}

class Order {}

@RoutePage()
class OrderScreen extends StatelessWidget {
  const OrderScreen({super.key, required this.order});
  final Order order;
  @override
  Widget build(BuildContext context) => const SizedBox();
}
''',
  'main.dart': '''
import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';
import 'package:plux_flutter/plux_flutter.dart';

class ProfileParams {
  const ProfileParams({required this.userId, this.tabs = const []});
  factory ProfileParams.fromJson(Map<String, Object?> j) => ProfileParams(userId: j['userId']! as String);
  final String userId;
  final List<String> tabs;
}

class ScanIn {
  const ScanIn(this.prompt, {this.when});
  factory ScanIn.fromJson(Map<String, Object?> j) => ScanIn(j['prompt']! as String);
  final String prompt;
  final DateTime? when;
}

final router = GoRouter(routes: [
  GoRoute(name: 'profile', path: '/p/:id', builder: (c, s) => const SizedBox()),
]);

final config = PluxConfig(
  appId: 'app',
  endpoint: Uri.parse('https://plux.example.com'),
  nativeRoutes: {
    'profile': PluxNativeRoute<ProfileParams, bool>(params: ProfileParams.fromJson, builder: (c, p) => const SizedBox()),
    'raw': PluxNativeRoute<String, void>(params: (j) => '', builder: (c, p) => const SizedBox()),
  },
  nativeActions: {
    'scan': PluxNativeAction<ScanIn, Map<String, int>?>(input: ScanIn.fromJson, handler: (i) => null),
    'ping': PluxNativeAction<void, void>(input: (j) {}, handler: (_) {}),
  },
);
''',
  'map_card.dart': '''
import 'package:flutter/widgets.dart';

class MapCard extends StatelessWidget {
  const MapCard({
    super.key,
    required this.zoom,
    this.title,
    this.count = 0,
    this.tags = const [],
    this.scores,
    required this.at,
    this.onPan,
    this.onTap,
    this.onCount,
    this.child,
    this.onMany,
  });
  final double zoom;
  final String? title;
  final int count;
  final List<String> tags;
  final Map<String, int>? scores;
  final DateTime at;
  final ValueChanged<Offset>? onPan;
  final VoidCallback? onTap;
  final ValueChanged<int>? onCount;
  final Widget? child;
  final void Function(int, int)? onMany;
  @override
  Widget build(BuildContext context) => const SizedBox();
}
''',
};

void main() {
  late Directory app;
  late ScanResult r;
  setUpAll(() async {
    app = _app(_fixtures);
    r = await _scan(app, slots: ['MapCard', 'Missing']);
  });

  Map<String, Object?> route(String name) => (r.catalogue['routes']! as List)
      .cast<Map<String, Object?>>()
      .singleWhere((e) => e['name'] == name);

  Iterable<String> problems(String file) =>
      r.problems.where((e) => e.file == file).map((e) => e.message);

  test('go_router: every named GoRoute, nested too, with its path parameters [CLI-006] [HST-031]', () {
    expect(route('order'), {
      'name': 'order',
      'params': [
        {'name': 'orderId', 'type': 'string', 'required': true},
        {'name': 'itemId', 'type': 'string', 'required': true},
      ],
    });
    expect(problems('lib/router.dart'), isEmpty);
    expect(r.catalogue['host'], {'version': '1.4.0', 'build': 52});
  });

  test('auto_route: @RoutePage classes by their route names, @PathParam and @QueryParam parameters [CLI-006] [HST-031]', () {
    expect(route('ProfileRoute'), {
      'name': 'ProfileRoute',
      'params': [
        {'name': 'userId', 'type': 'string', 'required': true},
        {'name': 'tab', 'type': 'int?'},
      ],
    });
    expect(route('SettingsRoute'), {
      'name': 'SettingsRoute',
      'params': <Object?>[],
    });
    expect(
      (r.catalogue['routes']! as List).map((e) => (e as Map)['name']),
      isNot(contains('OrderRoute')),
    );
    expect(problems('lib/pages.dart'), [
      startsWith(
        'route OrderRoute takes the argument order, which a path cannot carry',
      ),
    ]);
  });

  test('plain registration: nativeRoutes and nativeActions typed by their type arguments; a registration wins over a discovered route [CLI-006] [ACT-060] [NAV-002]', () {
    expect(route('profile'), {
      'name': 'profile',
      'params': [
        {'name': 'userId', 'type': 'string', 'required': true},
        {'name': 'tabs', 'type': 'list<string>'},
      ],
      'result': 'bool',
    });
    expect(r.catalogue['actions'], [
      {'name': 'ping', 'inputs': <Object?>[]},
      {
        'name': 'scan',
        'inputs': [
          {'name': 'prompt', 'type': 'string', 'required': true},
          {'name': 'when', 'type': 'dateTime?'},
        ],
        'output': 'map<string,int>?',
      },
    ]);
    expect(problems('lib/main.dart'), [
      startsWith('route raw takes String, which is not a class'),
    ]);
  });

  test('slots: constructor parameters of every shape become props, on… callbacks events; unmapped types are reported and left out [WGT-030] [CLI-006]', () {
    expect(r.catalogue['slots'], [
      {
        'type': 'MapCard',
        'props': [
          {'name': 'zoom', 'type': 'double', 'required': true},
          {'name': 'title', 'type': 'string?'},
          {'name': 'count', 'type': 'int'},
          {'name': 'tags', 'type': 'list<string>'},
          {'name': 'scores', 'type': 'map<string,int>?'},
          {'name': 'at', 'type': 'dateTime', 'required': true},
        ],
        'events': [
          {'name': 'onTap'},
          {'name': 'onCount', 'payload': 'int'},
        ],
      },
    ]);
    expect(problems('lib/map_card.dart'), [
      contains('the event onPan carries Offset'),
      contains('child is a Widget?'),
      contains('the callback onMany is not an event'),
    ]);
    expect(problems('plux.yaml'), ['no class Missing is declared under lib/']);
  });

  test('the output is deterministic: sorted keys and entries, no timestamps, no absolute paths [CLI-006]', () async {
    final first = encodeCatalogue(r.catalogue);
    final again = await _scan(app, slots: ['MapCard', 'Missing']);
    expect(encodeCatalogue(again.catalogue), first);
    expect(first, isNot(contains(app.path)));
    final names = (r.catalogue['routes']! as List)
        .map((e) => (e as Map)['name'])
        .toList();
    expect(names, [...names]..sort());
    expect(first.indexOf('"actions"'), lessThan(first.indexOf('"host"')));
    expect(first, endsWith('}\n'));
    expect(jsonDecode(first), containsPair('id', _id));
  });
}
