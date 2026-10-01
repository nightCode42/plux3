# Document Model Reference

How a Plux project is written down: the files of the Git layout, the rules every document follows, and how documents are validated. The requirements are spec §7 (`SCH-*`); the decisions are [ADR-0025](../adr/0025-document-schema-toolchain.md) and [ADR-0010](../adr/0010-layered-widget-model.md). Every property of every document kind is listed in the generated [document-schema.md](document-schema.md).

## 1. Project layout

A project is a directory with one JSON file per page, component, action graph and template, plus one manifest per app and plugin, so that changes review line by line (`SCH-006`).

| File | Kind | Contents |
|---|---|---|
| `app.json` | `app` | The app: plugins, theme, locales, environments, flags, shared types, state and collections (`SCH-020`) |
| `theme.json` | `theme` | Design tokens in the W3C Design Tokens format, with dark-mode values (`THM-001`; paths and roles in [theming.md](theming.md)) |
| `native-catalogue.json` | `nativeCatalogue` | Optional: native routes, native slots and custom actions of a host build (`SCH-032`) |
| `translations/keys.json` | `translationKeys` | Optional: translation keys, referenced by identifier |
| `translations/<locale>.json` | `translations` | Messages of one locale in ICU MessageFormat |
| `assets/index.json` | `assetIndex` | Optional: assets, whose files live under `assets/` |
| `components/<key>.component.json` | `component` | Components shared by all plugins (`SCH-030`) |
| `templates/<key>.template.json` | `template` | Templates for Studio (`SCH-031`) |
| `plugins/<key>/plugin.json` | `plugin` | A plugin: pages, capabilities, state, collections (`SCH-021`) |
| `plugins/<key>/pages/<key>.page.json` | `page` | A page and its node tree (`SCH-022`) |
| `plugins/<key>/components/<key>.component.json` | `component` | Components private to the plugin |
| `plugins/<key>/actions/<key>.graph.json` | `actionGraph` | An action graph: page-scoped when it names a `page`, otherwise a plugin flow (`ACT-061`) |

Files are named after the document's key; a plugin's directory is named after its key; every plugin is listed in `app.json` and every page in its `plugin.json`. The example project in [`schema/testdata/documents/loan-calculator`](../../schema/testdata/documents/loan-calculator) uses every kind.

## 2. Identifiers, keys and names

| Concept | Form | Used for |
|---|---|---|
| Identifier | UUIDv7, lower-case `8-4-4-4-12` | Every entity; every cross-reference uses it, so renaming never breaks a reference (`SCH-002`) |
| Key | lower-kebab slug, unique within the parent | Human-addressable entities and file names: apps, plugins, pages, components, graphs, templates, collections |
| Name | lowerCamelCase | What PXL and generated code address: state entries, parameters, data sources, flags, variables, fields, props, slots |
| Type name | UpperCamelCase | Declared object and enum types; names starting with `Plux`, primitives and registry types are reserved |
| Route name | lower-kebab slug, unique across the app | How native code, deep links and navigation address a page, whichever plugin holds it (`SCH-025`); defaults to the page key |

A document's `kind` and `schemaVersion` are required. The current schema version is `1.0.0`.

## 3. Types and values

Types are written as type expressions (`SCH-010`):

```ebnf
type  = base [ "?" ] ;
base  = "string" | "int" | "double" | "bool" | "decimal" | "money" | "date" | "dateTime"
      | "duration" | "color" | "asset" | "route"
      | "list" "<" type ">" | "map" "<" "string" "," type ">" | TypeName ;
```

`TypeName` is an object type or enum declared in `types` of the app (visible everywhere) or of a plugin, or — in widget props and action inputs — a value type or enum of the [widget registry](widgets.md). `?` makes a type nullable.

Literals are written as follows; the compiler checks them against the declared type, and `schemagen` checks the defaults of the registry the same way.

| Type | Literal | Example |
|---|---|---|
| `string` | JSON string | `"Loan"` |
| `int` | JSON integer within ±(2⁵³ − 1), the exact range of I-JSON; larger values come from bindings | `12` |
| `double` | JSON number | `0.5` |
| `bool` | `true` or `false` | `true` |
| `decimal` | string of digits with an optional fraction, no exponent | `"1250.00"` |
| `money` | object of a decimal `amount` and an ISO 4217 `currency` | `{"amount": "9.99", "currency": "EUR"}` |
| `date` | `YYYY-MM-DD` | `"2026-09-26"` |
| `dateTime` | RFC 3339 with an offset | `"2026-09-26T10:00:00+02:00"` |
| `duration` | whole milliseconds, not negative | `300` |
| `color` | `#RRGGBB` or `#RRGGBBAA`, as in design tokens | `"#5B3DF5"` |
| `asset` | no literal: `{"$asset": "<asset-id>"}` | — |
| `route` | route name | `"loan-result"` |
| enum | value name | `"center"` |
| value type | object of its fields, or the name of one of its constants | `{"all": 16}`, `"zero"` |
| `list<T>`, `map<string,T>` | JSON array, JSON object | `[1, 2]` |
| `T?` | also `null` | `null` |

A **prop value** is exactly one of (`SCH-011`): a literal of the prop's type; `{"$expr": "<PXL>"}`; `{"$token": "color.primary"}`; `{"$t": "<translation-key-id>", "args": {…}}`; or `{"$asset": "<asset-id>"}`. Fields and items of a literal object or list may themselves be bindings, such as a padding whose inset is a spacing token.

Fields, parameters and state entries may be tagged `"sensitive": true` (`SCH-012`).

## 4. Nodes

A node is a widget (`type`) or a component instance (`component` with its `id` and `version`), never both (`SCH-023`). It may have `props`; `events`, each handled by a reference to an action graph (`{"$graph": "<id>"}`) or an inline graph (`{"steps": […]}`), with an optional concurrency policy (`parallel`, `drop`, `restart`, `queue`, `debounce:<ms>`, `throttle:<ms>`); either `children` or named `slots` (a slot holds one node or a list), never both; `visible` (a boolean or a PXL binding); `semantics`; `testId`; and `responsive` prop overrides for the window-size classes `medium` and `expanded`, cascading over the `compact` base (`WGT-010`).

## 5. Action graphs

A graph is a list of steps; the first step is the entry. A step names an `action` from the catalogue, its `input` values, and its successors: `next`, `onSuccess`, `onError`, or named `branches`. A step may declare a `retry` policy and a timeout. Later steps read earlier results as `steps.<id>.output` (§14.1).

## 6. Extension properties

Unknown properties are errors, except properties whose names start with `x-`, which are allowed on any object, preserved in storage and ignored by the compiler (`SCH-004`). Tools use them for their own metadata.

## 7. Canonical form

Each document is canonicalised with RFC 8785 before hashing, diffing and storage, so equal content has equal bytes (`SCH-003`). Parsing is strict: duplicate keys, invalid UTF-8, unpaired surrogates and integers that canonicalisation would change are rejected. Files in the Git layout use the same key order and number form with two-space indentation.

## 8. Validation

Validation runs in three tiers and reports every problem as a diagnostic with a code, a severity, the file, a JSON Pointer and, for PXL, a character range (`SCH-040`, [error catalogue](errors.md)):

1. **Structural** — the JSON Schemas in `schema/json/`: shapes, formats, required properties, unknown properties. Performed by `backend/internal/schema`, also for a single edited document (`SCH-042`).
2. **Semantic** — references resolve, types match the widget descriptors, PXL type-checks, route parameters are satisfied at every navigation, keys and route names are unique, `onEnter` redirects do not loop. Performed by the compiler.
3. **Policy** — accessibility, performance budgets, security and store-policy lints. Performed by the compiler.

Sizes and counts are limited by the [limits registry](limits.md) (`SCH-005`).

## 9. Versions and migrations

A document declares its `schemaVersion`. Before validation it is migrated forward, step by step, to the current version; an unknown version is an error, never a guess (`SCH-000`). Migrations are deterministic and covered by golden tests from every released version (`SCH-043`).
