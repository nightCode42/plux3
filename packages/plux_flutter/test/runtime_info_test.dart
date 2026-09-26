// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

void main() {
  group('PluxRuntimeInfo', () {
    test('version matches pubspec.yaml', () {
      final pubspec = File('pubspec.yaml').readAsLinesSync();
      final declared = pubspec
          .firstWhere((line) => line.startsWith('version:'))
          .substring('version:'.length)
          .trim();

      expect(PluxRuntimeInfo.version, declared);
    });

    test('version is a semantic version', () {
      final semver = RegExp(
        r'^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$',
      );

      expect(semver.hasMatch(PluxRuntimeInfo.version), isTrue);
    });

    test('userAgent combines package name and version', () {
      expect(
        PluxRuntimeInfo.userAgent,
        '${PluxRuntimeInfo.packageName}/${PluxRuntimeInfo.version}',
      );
    });
  });
}
