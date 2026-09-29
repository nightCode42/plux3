# Theming Reference

An app's theme is a W3C Design Tokens document (`theme.json`, `THM-001`) compiled into the app bundle's `styles` section. This page fixes which token paths mean what to the runtime; the design is [ADR-0032](../adr/0032-design-tokens-to-material-and-cupertino.md).

## 1. Theme sources

`PluxConfig.themeSource` chooses the themes of Plux pages (`HST-012`, `THM-003`):

| Source | Material theme | Cupertino theme |
|---|---|---|
| `host` (default) | the host's `Theme.of(context)` | the host's |
| `pluxOverHost` | the host's, with every role the tokens set replaced | derived from the Material theme (§4) |
| `plux` | built from the tokens alone: a Material 3 scheme seeded from `color.primary` (Material's baseline primary when absent), with every role the tokens set | derived from the Material theme (§4) |

A token that names a role (§2) reads the role from the page's Material theme, so a prop bound to `color.primary` follows the host under `host` and the tokens under `plux`. Every other token reads its own value.

## 2. Roles

| Token path | Theme role |
|---|---|
| `color.<role>` | the `ColorScheme` role of that name: `primary`, `onPrimary`, `primaryContainer`, `onPrimaryContainer`, `primaryFixed`, `primaryFixedDim`, `onPrimaryFixed`, `onPrimaryFixedVariant`, the same eight for `secondary` and `tertiary`, `error`, `onError`, `errorContainer`, `onErrorContainer`, `surface`, `onSurface`, `surfaceDim`, `surfaceBright`, `surfaceContainerLowest`, `surfaceContainerLow`, `surfaceContainer`, `surfaceContainerHigh`, `surfaceContainerHighest`, `onSurfaceVariant`, `outline`, `outlineVariant`, `shadow`, `scrim`, `inverseSurface`, `onInverseSurface`, `inversePrimary`, `surfaceTint` |
| `typography.<style>` | the `TextTheme` style of that name: `displayLarge`, `displayMedium`, `displaySmall`, `headlineLarge`, `headlineMedium`, `headlineSmall`, `titleLarge`, `titleMedium`, `titleSmall`, `bodyLarge`, `bodyMedium`, `bodySmall`, `labelLarge`, `labelMedium`, `labelSmall` |

Any other path — `color.brandAccent`, `spacing.md`, `radius.card`, `elevation.*`, `motion.duration.*`, `motion.easing.*`, `breakpoint.*` — has no role and is read by the props bound to it. A `typography` token sets `fontFamily` (a list gives the fallbacks), `fontSize`, `fontWeight`, `letterSpacing` and `lineHeight`.

## 3. Modes, contrast and brands

Every token has a light value and may have a dark one (`$extensions.dev.plux.dark`, `THM-002`). The mode is the one the host sets with `Plux.setThemeMode`; under `system` it is the host theme's brightness for `host` and `pluxOverHost`, and the platform's for `plux`.

A token is read, first match wins, from:

1. `brands.<brand>.<path>` when the host selected a brand with `Plux.setBrand` (`THM-003`);
2. `contrast.high.<path>` when the platform asks for high contrast (`MediaQuery.highContrast`);
3. `<path>`.

## 4. Cupertino

Under `pluxOverHost` and `plux`, the Cupertino theme is derived: `primary` → `primaryColor`, `onPrimary` → `primaryContrastingColor`, `surface` → `scaffoldBackgroundColor`, `surfaceContainer` → `barBackgroundColor`, and the families of `bodyLarge`, `titleLarge` and `displaySmall` → the text, navigation title and large navigation title styles, at Cupertino's sizes.

## 5. Fonts per script

`font.script.<ISO 15924 code>` (`font.script.Ethi`, `font.script.Arab`, …) is a `fontFamily` token listing the families for one script. Under every source, the families of all of them, in path order, are appended to the fallback list of every Material text style and of the Cupertino text styles, so a string in any listed script finds a glyph (`THM-004`). Families the host bundles are used by name.
