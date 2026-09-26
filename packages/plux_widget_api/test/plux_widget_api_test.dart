// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:plux_widget_api/plux_widget_api.dart';
import 'package:test/test.dart';

/// Writes [files] (path → JSON) under a new temporary directory.
Directory _tree(Map<String, Object?> files) {
  final dir = Directory.systemTemp.createTempSync('plux_widget_api');
  addTearDown(() => dir.deleteSync(recursive: true));
  files.forEach((path, content) {
    File('${dir.path}/$path')
      ..createSync(recursive: true)
      ..writeAsStringSync(content is String ? content : jsonEncode(content));
  });
  return dir;
}

void main() {
  group('readTargets', () {
    test(
      'reads widgets, value types and enums, merging constructors [WGT-003]',
      () {
        final dir = _tree({
          'layer1/FilledButton.json': {
            'flutter': {
              'library': 'package:flutter/material.dart',
              'class': 'FilledButton',
              'constructors': ['tonal'],
            },
          },
          'layer1/FilledIcon.json': {
            'flutter': {
              'library': 'package:flutter/material.dart',
              'class': 'FilledButton',
              'constructors': ['', 'tonal'],
            },
          },
          'layer1/If.json': {'type': 'If'},
          'types/EdgeInsets.json': {
            'flutter': [
              {
                'library': 'package:flutter/painting.dart',
                'class': 'EdgeInsets',
                'constructors': ['all'],
              },
              {
                'library': 'package:flutter/painting.dart',
                'class': 'EdgeInsetsDirectional',
                'constructors': ['only'],
              },
            ],
          },
          'enums/Axis.json': {
            'flutter': {
              'library': 'package:flutter/painting.dart',
              'enum': 'Axis',
            },
          },
          'enums/Mode.json': {'name': 'Mode'},
          'flutter-api.json': {
            'flutter': {
              'library': 'x',
              'class': 'Y',
              'constructors': <String>[],
            },
          },
          'ids.lock.json': {'ids': <String, int>{}},
          'README.md': 'not JSON',
          'list.json': <Object>[],
        });

        final targets = readTargets(dir);

        expect(targets.map((t) => t.key), [
          'package:flutter/material.dart#FilledButton',
          'package:flutter/painting.dart#Axis',
          'package:flutter/painting.dart#EdgeInsets',
          'package:flutter/painting.dart#EdgeInsetsDirectional',
        ]);
        expect((targets.first as ClassTarget).constructors, ['', 'tonal']);
        expect(targets[1], isA<EnumTarget>());
        expect(targets[1].name, 'Axis');
      },
    );

    test('rejects malformed counterparts', () {
      expect(
        () => readTargets(
          _tree({
            'layer1/Bad.json': {'flutter': 'Text'},
          }),
        ),
        throwsFormatException,
      );
      expect(
        () => readTargets(
          _tree({
            'layer1/Bad.json': {
              'flutter': {
                'library': 'package:flutter/widgets.dart',
                'class': 'Text',
              },
            },
          }),
        ),
        throwsFormatException,
      );
    });
  });

  test('encode writes indented JSON with a final newline', () {
    expect(
      encode({'flutter': '1.0.0', 'classes': <String, Object?>{}}),
      '{\n  "flutter": "1.0.0",\n  "classes": {}\n}\n',
    );
  });

  group('flutterSdkVersion', () {
    test('reads the version of the SDK the workspace resolves', () {
      final dir = _tree({
        'sdk/bin/cache/flutter.version.json': {'frameworkVersion': '3.47.5'},
        'sdk/packages/flutter/pubspec.yaml': 'name: flutter',
      });
      File('${dir.path}/.dart_tool/package_config.json')
        ..createSync(recursive: true)
        ..writeAsStringSync(
          jsonEncode({
            'packages': [
              {'name': 'meta', 'rootUri': '../meta'},
              {'name': 'flutter', 'rootUri': '../sdk/packages/flutter'},
            ],
          }),
        );
      expect(flutterSdkVersion(dir), '3.47.5');
      expect(flutterSdk(dir)!.path, endsWith('/sdk/'));
    });

    test('is null when it cannot be determined', () {
      expect(flutterSdkVersion(_tree({})), isNull);
      expect(
        flutterSdkVersion(
          _tree({
            '.dart_tool/package_config.json': {'packages': <Object>[]},
          }),
        ),
        isNull,
      );
      expect(
        flutterSdkVersion(
          _tree({
            '.dart_tool/package_config.json': {
              'packages': [
                {
                  'name': 'flutter',
                  'rootUri': 'file:///nonexistent/packages/flutter/',
                },
              ],
            },
          }),
        ),
        isNull,
      );
    });
  });

  group('extract', () {
    // The analysis context of plux_flutter resolves the pinned Flutter SDK;
    // the analyzer needs its Dart SDK named when running under flutter test.
    final contextRoot = Directory('../plux_flutter').resolveSymbolicLinksSync();
    final sdkPath = '${flutterSdk(Directory('../..'))!.path}bin/cache/dart-sdk';

    test(
      'snapshots constructor parameters and enum values [WGT-003]',
      () async {
        final snapshot = await extract(
          const [
            ClassTarget('package:flutter/widgets.dart', 'Padding', ['']),
            EnumTarget('package:flutter/painting.dart', 'Axis'),
          ],
          contextRoot: contextRoot,
          flutterVersion: '0.0.0',
          sdkPath: sdkPath,
        );

        expect(snapshot['flutter'], '0.0.0');
        final padding =
            (snapshot['classes']!
                    as Map)['package:flutter/widgets.dart#Padding']
                as Map;
        final params = ((padding['constructors'] as Map)[''] as List)
            .cast<Map<String, Object?>>();
        expect(
          params.map((p) => p['name']),
          containsAll(['key', 'padding', 'child']),
        );
        expect(
          params.firstWhere((p) => p['name'] == 'padding'),
          containsPair('required', true),
        );
        expect(
          (snapshot['enums']! as Map)['package:flutter/painting.dart#Axis'],
          [
            {'name': 'horizontal'},
            {'name': 'vertical'},
          ],
        );
      },
    );

    test('reports every missing declaration', () async {
      await expectLater(
        extract(
          const [
            ClassTarget('package:flutter/widgets.dart', 'Padding', [
              '',
              'tight',
            ]),
            ClassTarget('package:flutter/widgets.dart', 'NoSuchWidget', ['']),
            EnumTarget('package:flutter/widgets.dart', 'Padding'),
            EnumTarget('package:flutter/nonexistent.dart', 'Axis'),
          ],
          contextRoot: contextRoot,
          flutterVersion: '0.0.0',
          sdkPath: sdkPath,
        ),
        throwsA(
          isA<StateError>().having(
            (e) => e.message,
            'message',
            allOf(
              contains('has no constructors "tight"'),
              contains('NoSuchWidget is not a class'),
              contains('Padding is not an enum'),
              contains('cannot resolve package:flutter/nonexistent.dart'),
            ),
          ),
        ),
      );
    });
  }, timeout: const Timeout(Duration(minutes: 3)));
}
