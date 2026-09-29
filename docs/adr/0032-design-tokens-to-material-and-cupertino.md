# 0032. Design tokens mapped to Material 3 and Cupertino themes

- **Status:** Accepted
- **Date:** 2026-09-28; icon fonts decided 2026-09-29 (§ Icons)
- **Requirements:** `THM-001`–`THM-005`, `HST-012`, `WGT-011`, `CMP-032`, `A11Y-*` (from P8)

## Context and problem

An app's theme is a W3C Design Tokens document compiled into the app bundle's `styles`
section as tokens with light and dark values (`THM-001`). Plux pages are rendered inside
host apps that have their own `ThemeData`, and must inherit it by default (`HST-012`,
`THM-003`), while white-label apps must switch brands at run time. Text must never render
as missing glyphs in any supported script (`THM-004`), and icons from Material Symbols,
Cupertino and custom sets must be available (`THM-005`). How do tokens become Flutter
themes, in what order do host theme, Plux theme and brand apply, and where do fonts and
icons come from?

## Decision drivers

- The host's look by default; the Plux theme and brands as explicit choices.
- One token vocabulary that works whether or not the app defines a theme.
- Light, dark and high contrast following the system (`THM-002`).
- No growth of the runtime package for fonts or icons (`RT-061`).
- No change to the document schema or the bundle format for P3.

## Considered options

1. **Tokens resolved per page from three layers — brand overlay, Plux theme, host theme — with a fixed mapping of token paths to Material 3 and Cupertino theme roles.**
2. Build one `ThemeData` from the Plux tokens and always use it.
3. Ignore the host theme unless the host maps it explicitly.

## Decision

Chosen option: **1**.

### Theme source (`HST-012`, `THM-003`)

`PluxConfig.theme` chooses the base, `Plux.setBrand` the overlay:

| `PluxThemeSource` | Material and Cupertino themes of Plux pages |
|---|---|
| `host` (default) | the host's `Theme.of(context)` and `CupertinoTheme.of(context)`; Plux tokens fill only the paths the host theme has no role for (spacing, radius, motion, custom colours) |
| `pluxOverHost` | the host's themes with every role the Plux theme defines replaced |
| `plux` | themes built from the Plux tokens alone |

A **brand overlay** is a token group named `brands.<brand>` inside the app's theme
document: when the host selects a brand, a token at `brands.<brand>.<path>` replaces the
token at `<path>`. Brands therefore need no schema change: they are ordinary tokens,
compiled, signed and delta-updated with the theme. Selecting a brand rebuilds only the
nodes that read tokens (ADR-0008).

### Token vocabulary

Token paths map to theme roles by a fixed, documented table: `color.<role>` to the
Material 3 `ColorScheme` roles (`color.primary`, `color.onPrimary`, `color.surface`, …),
`typography.<style>` to the `TextTheme` styles (`typography.bodyLarge`, …), and
`spacing.*`, `radius.*`, `elevation.*`, `motion.duration.*`, `motion.easing.*` and
`breakpoint.*` to values that props reference directly. Cupertino roles are derived
(`color.primary` → `primaryColor`, the text styles from `typography`). A prop bound to a
token (`Value.Token`) resolves through the same three layers, so a page styled by tokens
follows the host theme under `host` and the Plux theme under `plux`.

### Modes (`THM-002`)

Every token has a light and a dark value (`$extensions.dev.plux.dark`). The mode follows the
platform brightness unless the host sets `Plux.setThemeMode`. High contrast — a `SHOULD` —
uses a reserved token group `contrast.high.<path>` overlaying `<path>` when the platform
reports high contrast (`MediaQuery.highContrast`), before the brand overlay applies.

### Fonts per script (`THM-004`)

A `fontFamily` token may be a list, as the W3C format allows: the first family is used and
the rest become `fontFamilyFallback`. The reserved group `font.script.<ISO 15924 code>`
(`font.script.Ethi`, `font.script.Arab`, …) lists the families for one script; the runtime
appends them to the fallback list of every text style, so a string in any script listed
finds a glyph. Font files that the app supplies as assets are loaded at run time with
`FontLoader` from the verified asset store, not bundled in the host; families the host
bundles are used by name. Subsetting text fonts by script is `CMP-032` (P8).

### Icons (`THM-005`)

The `Icon` widget names an icon (`IconData`: a name and a set, `material` or `cupertino`).
Flutter's `IconData` is a `final class` whose code point must be a compile-time constant,
and the Flutter tool tree-shakes icon fonts to the constants an app uses, so the runtime
cannot use the host's icon fonts for icons chosen at publish time, and bundling the full
Material Symbols font (several MiB) would break `RT-061`. The icons must therefore reach
the device as fonts the **server** subsets to the glyphs an app release uses, delivered as
assets of the app bundle and drawn by the runtime as glyphs, exactly as Flutter's `Icon`
draws them (size, colour, fill, weight, grade and optical size as font variations).

This is the icon half of `CMP-032`, which the maintainer deferred to P8, and it needs two
new third-party inputs. The maintainer chose **(a)** on 2026-09-29, of these options:

- **(a) Chosen:** an in-house Go font subsetter that keeps glyph IDs (unused glyphs
  emptied, the `glyf`/`loca`, `gvar`, `cmap` and `hmtx` tables rewritten), run by the
  publish worker; the Material Symbols Outlined variable font and the Cupertino icons font
  (both Apache-2.0 / MIT) embedded in the server at pinned versions; compile-time
  validation of icon names from tables generated from those fonts. No library dependency;
  the fonts add to the server binary.
- **(b)** HarfBuzz `hb-subset` compiled to WebAssembly and run on wazero, like the image
  codecs (ADR-0027): a battle-tested subsetter, but a large C++ build to pin and reproduce.
- **(c)** Defer `THM-005` to P8 with `CMP-032`, rendering icons in P3 only from custom SVG
  icon sets (`vector_graphics`, ADR-0027 Revision).

Custom SVG icon sets are compiled to `vector_graphics` (`CMP-031`) in every option.

### Adaptive and Cupertino widgets (`WGT-011`)

Cupertino widgets read `CupertinoTheme`, derived as above, so an adaptive page looks right
on both platforms from one set of tokens.

## Consequences

- **Positive:** hosts keep their look with no configuration; white-label brands are data;
  one token vocabulary serves every source; no schema or bundle change.
- **Negative:** the fixed path-to-role table is a contract of its own, documented and
  extended additively; an in-house font subsetter to maintain, and the two icon fonts
  added to the server binary.
- **Follow-up:** the icon subsetter (R6); text-font subsetting (`CMP-032`) and
  per-locale typography with localisation in P8; the Studio design-system screen in P11.

## Options in detail

### Option 2 — one Plux `ThemeData`

Simple, but every Plux page would look foreign inside a host app unless the Plux theme
copied the host's, contrary to `HST-012`.

### Option 3 — host mapping only

Leaves white-label brands and Plux-defined themes to host code, contrary to `THM-003`.
