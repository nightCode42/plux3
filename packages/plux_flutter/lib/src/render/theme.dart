// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The themes of Plux pages (ADR-0032, docs/reference/theming.md): the
/// host's, the host's with the Plux theme's roles, or the Plux theme's
/// alone; Material 3 roles taken from `color.<role>` and
/// `typography.<style>` tokens, Cupertino derived from them, and the
/// fonts of `font.script.<code>` appended to every text style's fallbacks
/// (THM-001–THM-004, HST-012).
library;

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// The Material 3 `ColorScheme` roles a `color.<role>` token sets.
const colorRoles = <String>[
  'primary', 'onPrimary', 'primaryContainer', 'onPrimaryContainer', //
  'primaryFixed', 'primaryFixedDim', 'onPrimaryFixed', 'onPrimaryFixedVariant',
  'secondary', 'onSecondary', 'secondaryContainer', 'onSecondaryContainer',
  'secondaryFixed', 'secondaryFixedDim', 'onSecondaryFixed',
  'onSecondaryFixedVariant',
  'tertiary', 'onTertiary', 'tertiaryContainer', 'onTertiaryContainer',
  'tertiaryFixed', 'tertiaryFixedDim', 'onTertiaryFixed',
  'onTertiaryFixedVariant',
  'error', 'onError', 'errorContainer', 'onErrorContainer',
  'surface', 'onSurface', 'surfaceDim', 'surfaceBright',
  'surfaceContainerLowest', 'surfaceContainerLow', 'surfaceContainer',
  'surfaceContainerHigh', 'surfaceContainerHighest', 'onSurfaceVariant',
  'outline', 'outlineVariant', 'shadow', 'scrim', 'inverseSurface',
  'onInverseSurface', 'inversePrimary', 'surfaceTint',
];

/// The Material 3 `TextTheme` styles a `typography.<style>` token sets.
const textRoles = <String>[
  'displayLarge', 'displayMedium', 'displaySmall', //
  'headlineLarge', 'headlineMedium', 'headlineSmall',
  'titleLarge', 'titleMedium', 'titleSmall',
  'bodyLarge', 'bodyMedium', 'bodySmall',
  'labelLarge', 'labelMedium', 'labelSmall',
];

/// The seed of the Plux theme's colours when the app defines no
/// `color.primary`: Material 3's baseline primary.
const _baselineSeed = Color(0xFF6750A4);

/// Reads the app's design tokens: [token] returns the token at a path as
/// its PXL value with the brand and high-contrast overlays applied, and
/// [raw] its W3C value before conversion (for lists of font families).
typedef TokenSource = ({
  Object? Function(String path) token,
  Object? Function(String path) raw,
  Iterable<String> Function(String prefix) paths,
});

/// The Material and Cupertino themes of the Plux pages under a context,
/// and the PXL value of a token that names a theme role.
final class PluxTheme {
  PluxTheme._(this.material, this.cupertino);

  /// Resolves the themes for [source] over the [host] theme, in [dark]
  /// mode, from the app's [tokens].
  factory PluxTheme.resolve({
    required ThemeData host,
    required PluxThemeSource source,
    required bool dark,
    required TokenSource tokens,
  }) {
    final colors = <String, Color>{
      for (final role in colorRoles) role: ?_color(tokens.token('color.$role')),
    };
    final texts = <String, TextStyle>{
      for (final role in textRoles)
        role: ?_textStyle(tokens.token('typography.$role')),
    };
    final fallback = scriptFallbacks(tokens);
    final base = switch (source) {
      PluxThemeSource.host => host,
      PluxThemeSource.pluxOverHost => host.copyWith(
        colorScheme: withColorRoles(host.colorScheme, colors),
        textTheme: host.textTheme.merge(textTheme(texts)),
      ),
      PluxThemeSource.plux => _pluxTheme(dark, colors, texts),
    };
    final material = base.copyWith(
      textTheme: withFallback(base.textTheme, fallback),
      primaryTextTheme: withFallback(base.primaryTextTheme, fallback),
    );
    return PluxTheme._(
      material,
      source == PluxThemeSource.host
          ? null
          : cupertinoOf(base.colorScheme, base.textTheme, fallback),
    );
  }

  /// The Material theme of Plux pages.
  final ThemeData material;

  /// The Cupertino theme of Plux pages, derived from [material]; null
  /// under the host source, where the host's Cupertino theme applies. A
  /// nested Material `Theme` keeps the Cupertino theme above it, so pages
  /// set this one explicitly.
  final CupertinoThemeData? cupertino;

  /// The PXL value of [path] when it names a theme role — `color.<role>`
  /// or `typography.<style>` — read from [material], so that a prop bound
  /// to a role token follows the theme source; null for other paths.
  Object? role(String path) {
    if (path.startsWith('color.')) {
      final c = roleColor(material.colorScheme, path.substring(6));
      return c == null ? null : PxlColor(rgbaOf(c));
    }
    if (path.startsWith('typography.')) {
      final s = roleText(material.textTheme, path.substring(11));
      return s == null ? null : textStyleValue(s);
    }
    return null;
  }

  static ThemeData _pluxTheme(
    bool dark,
    Map<String, Color> colors,
    Map<String, TextStyle> texts,
  ) {
    final brightness = dark ? Brightness.dark : Brightness.light;
    final scheme = withColorRoles(
      ColorScheme.fromSeed(
        seedColor: colors['primary'] ?? _baselineSeed,
        brightness: brightness,
      ),
      colors,
    );
    final theme = ThemeData(colorScheme: scheme, brightness: brightness);
    return theme.copyWith(textTheme: theme.textTheme.merge(textTheme(texts)));
  }
}

/// The families of every `font.script.<code>` token, in path order
/// (THM-004).
List<String> scriptFallbacks(TokenSource tokens) => [
  for (final path in tokens.paths('font.script.'))
    ...switch (tokens.raw(path)) {
      final String f => [f],
      final List<Object?> l => l.whereType<String>(),
      _ => const <String>[],
    },
];

/// [theme] with [fallback] appended to every style's font fallbacks.
TextTheme withFallback(TextTheme theme, List<String> fallback) {
  if (fallback.isEmpty) return theme;
  TextStyle? add(TextStyle? s) => s?.copyWith(
    fontFamilyFallback: [
      ...?s.fontFamilyFallback,
      for (final f in fallback)
        if (!(s.fontFamilyFallback?.contains(f) ?? false)) f,
    ],
  );
  return theme.copyWith(
    displayLarge: add(theme.displayLarge),
    displayMedium: add(theme.displayMedium),
    displaySmall: add(theme.displaySmall),
    headlineLarge: add(theme.headlineLarge),
    headlineMedium: add(theme.headlineMedium),
    headlineSmall: add(theme.headlineSmall),
    titleLarge: add(theme.titleLarge),
    titleMedium: add(theme.titleMedium),
    titleSmall: add(theme.titleSmall),
    bodyLarge: add(theme.bodyLarge),
    bodyMedium: add(theme.bodyMedium),
    bodySmall: add(theme.bodySmall),
    labelLarge: add(theme.labelLarge),
    labelMedium: add(theme.labelMedium),
    labelSmall: add(theme.labelSmall),
  );
}

/// The Cupertino theme derived from Material roles: `primary` →
/// `primaryColor`, `surface` → the scaffold, `surfaceContainer` → bars,
/// `bodyLarge` → text, `titleLarge` → navigation titles.
CupertinoThemeData cupertinoOf(
  ColorScheme scheme,
  TextTheme text,
  List<String> fallback,
) {
  final base = CupertinoTextThemeData(primaryColor: scheme.primary);
  TextStyle merged(TextStyle into, TextStyle? from) => into
      .merge(from?.copyWith(color: into.color, fontSize: into.fontSize))
      .copyWith(
        fontFamilyFallback: [...?from?.fontFamilyFallback, ...fallback],
      );
  return CupertinoThemeData(
    brightness: scheme.brightness,
    primaryColor: scheme.primary,
    primaryContrastingColor: scheme.onPrimary,
    scaffoldBackgroundColor: scheme.surface,
    barBackgroundColor: scheme.surfaceContainer,
    textTheme: base.copyWith(
      textStyle: merged(base.textStyle, text.bodyLarge),
      navTitleTextStyle: merged(base.navTitleTextStyle, text.titleLarge),
      navLargeTitleTextStyle: merged(
        base.navLargeTitleTextStyle,
        text.displaySmall,
      ),
    ),
  );
}

/// [scheme] with the [roles] given replaced.
ColorScheme withColorRoles(ColorScheme scheme, Map<String, Color> roles) {
  if (roles.isEmpty) return scheme;
  return scheme.copyWith(
    primary: roles['primary'],
    onPrimary: roles['onPrimary'],
    primaryContainer: roles['primaryContainer'],
    onPrimaryContainer: roles['onPrimaryContainer'],
    primaryFixed: roles['primaryFixed'],
    primaryFixedDim: roles['primaryFixedDim'],
    onPrimaryFixed: roles['onPrimaryFixed'],
    onPrimaryFixedVariant: roles['onPrimaryFixedVariant'],
    secondary: roles['secondary'],
    onSecondary: roles['onSecondary'],
    secondaryContainer: roles['secondaryContainer'],
    onSecondaryContainer: roles['onSecondaryContainer'],
    secondaryFixed: roles['secondaryFixed'],
    secondaryFixedDim: roles['secondaryFixedDim'],
    onSecondaryFixed: roles['onSecondaryFixed'],
    onSecondaryFixedVariant: roles['onSecondaryFixedVariant'],
    tertiary: roles['tertiary'],
    onTertiary: roles['onTertiary'],
    tertiaryContainer: roles['tertiaryContainer'],
    onTertiaryContainer: roles['onTertiaryContainer'],
    tertiaryFixed: roles['tertiaryFixed'],
    tertiaryFixedDim: roles['tertiaryFixedDim'],
    onTertiaryFixed: roles['onTertiaryFixed'],
    onTertiaryFixedVariant: roles['onTertiaryFixedVariant'],
    error: roles['error'],
    onError: roles['onError'],
    errorContainer: roles['errorContainer'],
    onErrorContainer: roles['onErrorContainer'],
    surface: roles['surface'],
    onSurface: roles['onSurface'],
    surfaceDim: roles['surfaceDim'],
    surfaceBright: roles['surfaceBright'],
    surfaceContainerLowest: roles['surfaceContainerLowest'],
    surfaceContainerLow: roles['surfaceContainerLow'],
    surfaceContainer: roles['surfaceContainer'],
    surfaceContainerHigh: roles['surfaceContainerHigh'],
    surfaceContainerHighest: roles['surfaceContainerHighest'],
    onSurfaceVariant: roles['onSurfaceVariant'],
    outline: roles['outline'],
    outlineVariant: roles['outlineVariant'],
    shadow: roles['shadow'],
    scrim: roles['scrim'],
    inverseSurface: roles['inverseSurface'],
    onInverseSurface: roles['onInverseSurface'],
    inversePrimary: roles['inversePrimary'],
    surfaceTint: roles['surfaceTint'],
  );
}

/// The colour of [role] in [scheme], or null for an unknown role.
Color? roleColor(ColorScheme scheme, String role) => switch (role) {
  'primary' => scheme.primary,
  'onPrimary' => scheme.onPrimary,
  'primaryContainer' => scheme.primaryContainer,
  'onPrimaryContainer' => scheme.onPrimaryContainer,
  'primaryFixed' => scheme.primaryFixed,
  'primaryFixedDim' => scheme.primaryFixedDim,
  'onPrimaryFixed' => scheme.onPrimaryFixed,
  'onPrimaryFixedVariant' => scheme.onPrimaryFixedVariant,
  'secondary' => scheme.secondary,
  'onSecondary' => scheme.onSecondary,
  'secondaryContainer' => scheme.secondaryContainer,
  'onSecondaryContainer' => scheme.onSecondaryContainer,
  'secondaryFixed' => scheme.secondaryFixed,
  'secondaryFixedDim' => scheme.secondaryFixedDim,
  'onSecondaryFixed' => scheme.onSecondaryFixed,
  'onSecondaryFixedVariant' => scheme.onSecondaryFixedVariant,
  'tertiary' => scheme.tertiary,
  'onTertiary' => scheme.onTertiary,
  'tertiaryContainer' => scheme.tertiaryContainer,
  'onTertiaryContainer' => scheme.onTertiaryContainer,
  'tertiaryFixed' => scheme.tertiaryFixed,
  'tertiaryFixedDim' => scheme.tertiaryFixedDim,
  'onTertiaryFixed' => scheme.onTertiaryFixed,
  'onTertiaryFixedVariant' => scheme.onTertiaryFixedVariant,
  'error' => scheme.error,
  'onError' => scheme.onError,
  'errorContainer' => scheme.errorContainer,
  'onErrorContainer' => scheme.onErrorContainer,
  'surface' => scheme.surface,
  'onSurface' => scheme.onSurface,
  'surfaceDim' => scheme.surfaceDim,
  'surfaceBright' => scheme.surfaceBright,
  'surfaceContainerLowest' => scheme.surfaceContainerLowest,
  'surfaceContainerLow' => scheme.surfaceContainerLow,
  'surfaceContainer' => scheme.surfaceContainer,
  'surfaceContainerHigh' => scheme.surfaceContainerHigh,
  'surfaceContainerHighest' => scheme.surfaceContainerHighest,
  'onSurfaceVariant' => scheme.onSurfaceVariant,
  'outline' => scheme.outline,
  'outlineVariant' => scheme.outlineVariant,
  'shadow' => scheme.shadow,
  'scrim' => scheme.scrim,
  'inverseSurface' => scheme.inverseSurface,
  'onInverseSurface' => scheme.onInverseSurface,
  'inversePrimary' => scheme.inversePrimary,
  'surfaceTint' => scheme.surfaceTint,
  _ => null,
};

/// The style [role] of [theme], or null for an unknown role.
TextStyle? roleText(TextTheme theme, String role) => switch (role) {
  'displayLarge' => theme.displayLarge,
  'displayMedium' => theme.displayMedium,
  'displaySmall' => theme.displaySmall,
  'headlineLarge' => theme.headlineLarge,
  'headlineMedium' => theme.headlineMedium,
  'headlineSmall' => theme.headlineSmall,
  'titleLarge' => theme.titleLarge,
  'titleMedium' => theme.titleMedium,
  'titleSmall' => theme.titleSmall,
  'bodyLarge' => theme.bodyLarge,
  'bodyMedium' => theme.bodyMedium,
  'bodySmall' => theme.bodySmall,
  'labelLarge' => theme.labelLarge,
  'labelMedium' => theme.labelMedium,
  'labelSmall' => theme.labelSmall,
  _ => null,
};

/// A text theme holding the [styles] given.
TextTheme textTheme(Map<String, TextStyle> styles) => TextTheme(
  displayLarge: styles['displayLarge'],
  displayMedium: styles['displayMedium'],
  displaySmall: styles['displaySmall'],
  headlineLarge: styles['headlineLarge'],
  headlineMedium: styles['headlineMedium'],
  headlineSmall: styles['headlineSmall'],
  titleLarge: styles['titleLarge'],
  titleMedium: styles['titleMedium'],
  titleSmall: styles['titleSmall'],
  bodyLarge: styles['bodyLarge'],
  bodyMedium: styles['bodyMedium'],
  bodySmall: styles['bodySmall'],
  labelLarge: styles['labelLarge'],
  labelMedium: styles['labelMedium'],
  labelSmall: styles['labelSmall'],
);

/// The RGBA value of a Flutter colour.
int rgbaOf(Color c) {
  final argb = c.toARGB32();
  return ((argb & 0xffffff) << 8) | (argb >>> 24);
}

/// The Flutter colour of a PXL colour value, or null.
Color? _color(Object? v) =>
    v is PxlColor ? Color(((v.rgba & 0xff) << 24) | (v.rgba >>> 8)) : null;

/// A text style from the PXL value of a `typography` token.
TextStyle? _textStyle(Object? v) {
  if (v is! Map<String, Object?>) return null;
  return TextStyle(
    fontFamily: v['fontFamily'] as String?,
    fontFamilyFallback: (v['fontFamilyFallback'] as List<Object?>?)
        ?.whereType<String>()
        .toList(),
    fontSize: (v['fontSize'] as num?)?.toDouble(),
    fontWeight: switch (v['fontWeight']) {
      final String w when w.startsWith('w') => FontWeight.values.firstWhere(
        (f) => f.value == int.tryParse(w.substring(1)),
        orElse: () => FontWeight.normal,
      ),
      _ => null,
    },
    letterSpacing: (v['letterSpacing'] as num?)?.toDouble(),
    height: (v['height'] as num?)?.toDouble(),
  );
}

/// The PXL `TextStyle` object of a Flutter text style: the fields a
/// `typography` token can set, and its colour.
Map<String, Object?> textStyleValue(TextStyle s) => {
  'fontFamily': ?s.fontFamily,
  if (s.fontFamilyFallback case final f? when f.isNotEmpty)
    'fontFamilyFallback': f,
  'fontSize': ?s.fontSize,
  if (s.fontWeight case final w?) 'fontWeight': 'w${w.value}',
  'letterSpacing': ?s.letterSpacing,
  'height': ?s.height,
  if (s.color case final c?) 'color': PxlColor(rgbaOf(c)),
};
