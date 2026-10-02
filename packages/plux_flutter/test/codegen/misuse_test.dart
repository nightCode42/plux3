// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The analyzer resolves Flutter and the runtime, which takes seconds.
@Timeout(Duration(minutes: 4))
library;

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// Host code that misuses the generated API, one case per file; each must
/// be a compile error (HST-030).
const _misuse = {
  'missing_parameter': 'PluxScreens.detail();',
  'parameter_type': 'PluxScreens.detail(itemId: 7);',
  'unknown_route': 'PluxScreens.nowhere();',
  'result_type':
      "final Future<int?> r = PluxScreens.detail(itemId: '7').push(context);",
  'state_type': "PluxAppState.counter.set('3');",
  'hidden_state': 'PluxAppState.theme;',
  'event_field': 'PluxHostEvents.confirmed.listen((e) => e.choice);',
  'flag_type': 'final int n = PluxFlags.secondLabel;',
  'missing_prop': 'PluxComponents.counterCard();',
};

/// The same calls, used correctly.
const _correct = '''
final Future<String?> r = PluxScreens.detail(itemId: '7').push(context);
PluxAppState.counter.set(3);
PluxHostEvents.confirmed.listen((e) => e.answer);
final String label = PluxFlags.secondLabel;
PluxComponents.counterCard(label: 'Count');
''';

String _library(String body) =>
    '''
import 'package:flutter/widgets.dart';

import 'routing.g.dart';

void use(BuildContext context) {
  $body
}
''';

/// The workspace's package configuration, and the Dart SDK inside the
/// Flutter SDK it resolves: under `flutter test` there is no `dart` of
/// the test's own.
(Map<String, Object?>, Uri, String) _workspace() {
  final file = File.fromUri(
    Directory.current.uri.resolve('../../.dart_tool/package_config.json'),
  );
  final config = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
  final flutter = (config['packages']! as List)
      .cast<Map<String, Object?>>()
      .singleWhere((e) => e['name'] == 'flutter');
  final root = file.parent.uri.resolve('${flutter['rootUri']}/');
  return (
    config,
    file.parent.uri,
    root.resolve('../../bin/cache/dart-sdk/bin/dart').toFilePath(),
  );
}

void main() {
  test(
    'misusing the generated API is a compile error in the host app [HST-030]',
    () async {
      final dir = Directory.systemTemp.createTempSync('plux_codegen');
      addTearDown(() => dir.deleteSync(recursive: true));
      final (config, base, dart) = _workspace();
      // A host package resolving the workspace's packages by absolute
      // paths, as `flutter pub get` would.
      final packages = [
        for (final pkg
            in (config['packages']! as List).cast<Map<String, Object?>>())
          {
            ...pkg,
            'rootUri': base.resolve(pkg['rootUri']! as String).toString(),
          },
        {
          'name': 'host',
          'rootUri': dir.uri.toString(),
          'packageUri': 'lib/',
          'languageVersion': '3.13',
        },
      ];
      File.fromUri(dir.uri.resolve('.dart_tool/package_config.json'))
        ..createSync(recursive: true)
        ..writeAsStringSync(jsonEncode({...config, 'packages': packages}));
      File.fromUri(dir.uri.resolve('pubspec.yaml'))
          .writeAsStringSync('name: host\nenvironment:\n  sdk: ^3.13.0\n');
      final lib = Directory.fromUri(dir.uri.resolve('lib/'))..createSync();
      File('test/codegen/routing.g.dart')
          .copySync('${lib.path}/routing.g.dart');
      File('${lib.path}/correct.dart').writeAsStringSync(_library(_correct));
      for (final MapEntry(:key, :value) in _misuse.entries) {
        File('${lib.path}/$key.dart').writeAsStringSync(_library(value));
      }

      final run = await Process.run(dart, [
        'analyze',
        '--format=machine',
        dir.path,
      ]);
      final errors = <String, List<String>>{};
      for (final line in LineSplitter.split('${run.stdout}${run.stderr}')) {
        final f = line.split('|');
        if (f.length < 8 || f[0] != 'ERROR') continue;
        final file = Uri.file(f[3]).pathSegments.last;
        errors
            .putIfAbsent(file.substring(0, file.length - 5), () => [])
            .add(f[2]);
      }
      expect(errors['correct'], isNull, reason: '${errors['correct']}');
      expect(errors['routing.g'], isNull, reason: '${errors['routing.g']}');
      for (final name in _misuse.keys) {
        expect(errors[name], isNotEmpty, reason: '$name compiled');
      }
    },
  );
}
