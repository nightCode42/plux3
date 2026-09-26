# Compiler Reference

The Plux compiler turns a project in the [Git layout](document-model.md#1-project-layout) into signed-ready bundles in the [bundle format](bundle-format.md) (spec §9, `CMP-*`). It is the Go package `backend/internal/compiler`: a pure library used unchanged by the server, the `plux` CLI and tests (`CMP-001`, layering rule L-5). It reads an `fs.FS`, never the clock, the environment or the network, and returns bytes and diagnostics, so the same project and compiler version give byte-identical bundles on every platform (`CMP-002`).

## 1. API

| Function | Result |
|---|---|
| `Compile(fsys, opts)` | `Result`: the app bundle, one bundle per plugin (sorted by key), the reference graph and every diagnostic. Bundles are produced only when no error is reported. |
| `NewValidator(fsys, opts)` | A `Validator` holding the loaded project, and the project's structural diagnostics. |
| `(*Validator).ValidatePage(file, data)` | The diagnostics of an edited or new page (§5). |

`Options` holds the limits registry (`LIM-001`; `DefaultOptions` uses its defaults), the mode (`Release` or `Development`) and the compiler version written into every meta section. Each `Bundle` carries its kind, UUID, key, encoded bytes, bundle hash (`BND-005`), sorted required features (`BND-008`) and, in release mode, its source map as separate bytes (`CMP-041`).

Neither `Compile` nor `ValidatePage` panics: an unexpected failure is recovered and reported as `PLX-2201` (`CMP-052`), and both are fuzzed (`FuzzCompile`, `FuzzValidatePage`).

## 2. Pipeline

The stages of `CMP-003` run in order over one compilation unit. The **checking** stages always run, so one compilation reports every problem it can find; the **producing** stages run only when no error was reported.

| Stage | Does | Typical diagnostics |
|---|---|---|
| load | `schema.Loader`: parse each file (strict I-JSON), migrate to the current schema version, validate it structurally against the JSON Schemas (`SCH-001`–`SCH-006`) | `PLX-10xx` |
| resolve | Index every entity; claim IDs, keys and route names; resolve references to pages, components, graphs, translations, tokens and assets; build the node trees; start the reference graph (`SCH-041`) | `PLX-1011`, `PLX-1101`–`PLX-1103`, `PLX-1114` |
| typecheck | Build the environment of every use site (§3) and compile every PXL expression; bind widget type parameters from their expressions (`PXL-002`, `CMP-023`) | `PLX-20xx` |
| semantic | Check props, events, children and slots against the descriptors; literals against their types and constraints; defaults, mocks and computed state; action inputs; navigation parameters; redirect loops; registry revisions against the minimum runtime (`SCH-040`, `WGT-004`) | `PLX-11xx`, `PLX-12xx` |
| policy | Per-page budgets (`CMP-040`); interactive nodes without an accessible name, ahead of the full `A11Y-002` check; secret-like literals and insecure URLs; sensitive values sent to analytics (`SCH-012`); executable content in assets (`SEC-054`) | `PLX-1310`–`PLX-1314`, `PLX-1401`, `PLX-1500`–`PLX-1503` |
| optimise | §4 | — |
| lower | Lower action graphs to steps with index successors; a custom action of the native catalogue becomes a `callNative` step with its name and inputs (Appendix D) | — |
| encode | Write the sections of every bundle (§6) | `PLX-2202` |
| assets | Write the asset index with each asset's SHA-256, the meta, pxl, styles and strings sections and the source map | — |
| hash | Assemble each container, read it back with `bundle.Read` as a self-check, and check sizes (`bundle.pluginSize`, `release.appSize`) | `PLX-1320`, `PLX-2201` |

Budgets report a warning above the limit's warning threshold and an error above the limit. The animation budget is always 0 in P1: animations arrive with timelines (P5).

## 3. Scopes

Every expression is compiled against the roots of its use site (spec Appendix E.2). Types the compiler synthesises for roots are named with the reserved prefix `Plux`, which documents cannot use for their own types (`PLX-1101`).

| Root | Available in | Type |
|---|---|---|
| `app`, `plugin` | everywhere; `plugin` only inside a plugin | the app's and the plugin's state entries |
| `page`, `params` | a page, its lifecycle handlers and its page-scoped graphs | the page's state entries and parameters |
| `component`, `props` | a component definition and its inline graphs | the component's state entries and props |
| `params` | a plugin flow | the flow's inputs |
| `event` | an action graph run by handlers | the payload of the events that run it; all handlers of one graph must agree on it (`PLX-1113`) |
| `item`, `index` | the nodes of an item template (`ListView.item`, `ForEach.item`, …) and the steps reachable from a `forEach` step's `body` branch | the element type of the bound items, and `int`; the innermost loop wins |
| `steps.<id>` | action graph inputs | `output` (the action's output type, nullable: the step may not have run) and `error` (`PluxActionError?`) |
| `data.<source>` | everywhere a data source is visible (app, plugin, page) | `value` (nullable), `loading`, `error` |
| `flags`, `env`, `user` | everywhere | the app's flags, variables and user context |
| `device` | everywhere | `platform`, `osVersion`, `locale`, `textScale`, `darkMode`, `sizeClass`, `assuranceLevel` |
| `now` | everywhere | `dateTime`, frozen per evaluation |

`form` is not a root before forms land (P5); an expression that reads it fails with `PLX-2003`.

**Bindings.** A prop, action input or override takes a literal or a binding: `$expr` (a PXL expression whose type must be assignable to the prop's; an `int` expression is widened to `double` or `decimal` by recompiling it with a conversion), `$token` (a design token whose W3C `$type` fits the prop: `color` → `color`, `dimension` and `number` → `double`, `fontFamily` → `string`, `fontWeight` → `FontWeight`, `duration` → `duration`, `typography` → `TextStyle`, `shadow` → `BoxShadow`), `$t` (a translation key, optionally with arguments, for `string` props) and `$asset` (an asset UUID). Literals are checked in the forms of [document-model.md §3](document-model.md#3-types-and-values); integers must be within ±(2⁵³−1) to survive every JSON reader.

**Graphs.** An inline handler graph (`{"steps": […]}`) has no document UUID; the compiler derives a stable version 8 UUID from its anchor (node or page UUID) and event name (`BND-014`). A navigation step must pass every required parameter of its target page, with the declared types, and no others (`PLX-1203`–`PLX-1205`). Pages whose `onEnter` navigates unconditionally — the first step and those it reaches through `next` — in a cycle are reported (`PLX-1206`). Inputs that name another entity (`ref` in the action descriptor) are resolved for `state`, `flow`, `dataSource`, `collection`, `function` and `nativeAction`; references to animations, forms, streams, operations, permissions, tabs, host and telemetry events are accepted as strings until their phases add the declarations.

## 4. Optimisation

The optimiser rewrites the checked trees without changing what they render (`CMP-020`, `CMP-022`, `CMP-024`):

- An expression that folds to a scalar constant becomes a literal, so the runtime reads it without evaluating.
- A node whose `visible` is constantly false is removed with its subtree; a constant `true` is dropped as the default.
- A `Padding` whose only content is another `Padding`, both with literal non-directional insets and nothing else on the inner node, becomes one `Padding` with the summed insets.
- A literal prop equal to its descriptor default is omitted (override layers keep theirs: they reset a changed base value).
- Value-type objects are deduplicated into the styles section by content ID; strings are interned per section.
- Hints: page roots and template items get `RepaintBoundary`; subtrees that read no state, tokens or translations and handle no events get `Static`.

## 5. Incremental validation

`ValidatePage` serves Studio as the user types (`SCH-042`): the project is loaded once by `NewValidator`, and each call re-parses only the edited page, puts it in a copy of the project and runs the checking stages with the edited page in **focus** — only that page, its lifecycle and inline graphs and its page-scoped action graphs are checked, while redirect loops are still followed through every page. It returns the diagnostics located in the page and in the graphs it owns, which an edit of its state or parameters can break. The loaded project is never modified. A file outside a plugin's `pages/` directory is refused with `PLX-1020`, as is a new page until its `plugin.json` lists it.

## 6. Encoding and determinism

Sections are written as described in [bundle-format.md](bundle-format.md). Everything that reaches a bundle is ordered by content, never by map iteration or input order: plugins by key, pages in the order their `plugin.json` lists them, components by file, props by permanent ID, steps in document order, styles and programs by content ID, strings by first use in that order. Component instances address the component's props sorted by name, and its events and slots in document order; native slot nodes address the catalogue's props and events in the catalogue's order.

In **development** mode every bundle of the app, the app bundle included, is a development bundle with its source map embedded; in **release** mode the source map is returned beside the bundle. The source map locates each node by its page or component UUID and index, and each step by its graph UUID and index.

Determinism is tested by compiling the conformance projects repeatedly and after shuffling the key order and whitespace of every document (`TestDeterminismProperty`), and by golden bundles pinned byte for byte in `schema/testdata/bundles/`, which the Dart tests read too.

## 7. Conformance projects

| Project | Exercises |
|---|---|
| [`loan-calculator`](../../schema/testdata/documents/loan-calculator) | Appendix A: state, parameters, computed state, decimal arithmetic, page-scoped graphs, navigation, translations, tokens, assets |
| [`features`](../../schema/testdata/documents/features) | Shared and plugin components with slots, item templates, `forEach` loops, responsive overrides, native slots and actions, flows, parallel lanes, retries, the optimiser |

Each compiles without diagnostics; `TestInvalidProjects` breaks the loan calculator in one way per case and names the diagnostic that must follow. Goldens change only with `go test ./internal/compiler -update` and are reviewed as a format or behaviour change.

## 8. Performance

`BenchmarkCompile50Pages` compiles the loan calculator with 48 copies of its calculator page and their graphs (a 50-page plugin, `CMP-050`: ≤ 1 s); `BenchmarkValidatePage` validates one page of that project (`SCH-042`, `NFR-033`: ≤ 50 ms p95). Results are recorded in `docs/benchmarks/`.
