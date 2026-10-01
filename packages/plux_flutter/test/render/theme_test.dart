// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/theme.dart';

/// Tokens from a map of paths to PXL values; W3C values are the same.
TokenSource tokensOf(Map<String, Object?> tokens) => (
  token: (path) => tokens[path],
  raw: (path) => tokens[path],
  paths: (prefix) =>
      (tokens.keys.where((k) => k.startsWith(prefix)).toList()..sort()),
);

void main() {
  test('every colour role reads back what it sets [THM-001]', () {
    final scheme = ColorScheme.fromSeed(seedColor: Colors.blue);
    final roles = {
      for (final (i, role) in colorRoles.indexed)
        role: Color(0xFF000000 | (i + 1)),
    };
    final set = withColorRoles(scheme, roles);
    for (final role in colorRoles) {
      expect(roleColor(set, role), roles[role], reason: role);
    }
    expect(roleColor(set, 'brandAccent'), isNull);
    expect(withColorRoles(scheme, const {}), same(scheme));
  });

  test('every text role reads back what it sets', () {
    final styles = {
      for (final (i, role) in textRoles.indexed)
        role: TextStyle(fontSize: i + 1.0),
    };
    final theme = textTheme(styles);
    for (final role in textRoles) {
      expect(roleText(theme, role), styles[role], reason: role);
    }
    expect(roleText(theme, 'caption'), isNull);
  });

  test('colours and text styles convert to PXL values', () {
    expect(rgbaOf(const Color(0x80112233)), 0x11223380);
    expect(
      textStyleValue(
        const TextStyle(
          fontFamily: 'Inter',
          fontFamilyFallback: ['Noto'],
          fontSize: 14,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.5,
          height: 1.2,
          color: Color(0xFF6750A4),
        ),
      ),
      {
        'fontFamily': 'Inter',
        'fontFamilyFallback': ['Noto'],
        'fontSize': 14.0,
        'fontWeight': 'w600',
        'letterSpacing': 0.5,
        'height': 1.2,
        'color': const PxlColor(0x6750A4FF),
      },
    );
    expect(textStyleValue(const TextStyle()), isEmpty);
  });

  test('script fonts become fallbacks once each, after existing ones '
      '[THM-004]', () {
    final fallback = scriptFallbacks(
      tokensOf({
        'font.script.Ethi': ['Noto Sans Ethiopic', 42],
        'font.script.Arab': 'Noto Sans Arabic',
        'font.script.Latn': 7,
        'font.base': 'Inter',
      }),
    );
    expect(fallback, ['Noto Sans Arabic', 'Noto Sans Ethiopic']);
    final theme = withFallback(
      const TextTheme(
        bodyMedium: TextStyle(fontFamilyFallback: ['Noto Sans Arabic']),
      ),
      fallback,
    );
    expect(theme.bodyMedium?.fontFamilyFallback, [
      'Noto Sans Arabic',
      'Noto Sans Ethiopic',
    ]);
    const empty = TextTheme();
    expect(withFallback(empty, const []), same(empty));
  });

  group('sources [HST-012] [THM-003]', () {
    final host = ThemeData(
      colorScheme: ColorScheme.fromSeed(seedColor: Colors.teal),
    );
    final tokens = tokensOf({
      'color.primary': const PxlColor(0x6750A4FF),
      'typography.bodyLarge': {
        'fontFamily': 'Inter',
        'fontFamilyFallback': ['Noto Sans'],
        'fontSize': 18.0,
        'fontWeight': 'w500',
        'letterSpacing': 0.25,
        'height': 1.4,
      },
      'color.unused': 'not a colour',
    });

    test('host keeps the host theme and has no Cupertino theme', () {
      final t = PluxTheme.resolve(
        host: host,
        source: PluxThemeSource.host,
        dark: false,
        tokens: tokens,
      );
      expect(t.material.colorScheme, host.colorScheme);
      expect(t.cupertino, isNull);
      expect(
        t.role('color.primary'),
        PxlColor(rgbaOf(host.colorScheme.primary)),
      );
      expect(t.role('spacing.md'), isNull);
      expect(t.role('color.brandAccent'), isNull);
      expect(t.role('typography.caption'), isNull);
    });

    test('pluxOverHost replaces the roles the tokens set', () {
      final t = PluxTheme.resolve(
        host: host,
        source: PluxThemeSource.pluxOverHost,
        dark: false,
        tokens: tokens,
      );
      expect(t.material.colorScheme.primary, const Color(0xFF6750A4));
      expect(t.material.colorScheme.secondary, host.colorScheme.secondary);
      final body = t.material.textTheme.bodyLarge!;
      expect(body.fontFamily, 'Inter');
      expect(body.fontSize, 18);
      expect(body.fontWeight, FontWeight.w500);
      expect(t.role('typography.bodyLarge'), containsPair('fontSize', 18.0));
      expect(t.cupertino?.primaryColor, const Color(0xFF6750A4));
      expect(t.cupertino?.textTheme.textStyle.fontFamily, 'Inter');
    });

    test('plux builds the themes from the tokens in the mode asked', () {
      final t = PluxTheme.resolve(
        host: host,
        source: PluxThemeSource.plux,
        dark: true,
        tokens: tokens,
      );
      expect(t.material.brightness, Brightness.dark);
      expect(t.material.colorScheme.primary, const Color(0xFF6750A4));
      expect(t.cupertino?.brightness, Brightness.dark);
      final plain = PluxTheme.resolve(
        host: host,
        source: PluxThemeSource.plux,
        dark: false,
        tokens: tokensOf(const {}),
      );
      expect(
        plain.material.colorScheme.primary,
        isNot(host.colorScheme.primary),
      );
    });
  });
}
