# 0010. Layered widget model with a descriptor registry

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `WGT-001`–`WGT-005`, `WGT-010`, `BND-011`, `SCH-023`, `CMP-020`, `CMP-040`

## Context and problem

Plux pages are trees of widgets described as data. Flutter has roughly 400 public widgets, many of them framework plumbing, and many constructor parameters are live Dart objects — callbacks, controllers, builders, focus nodes — that cannot be expressed as data. Flutter's API changes every release, while bundles must stay readable by installed runtimes for years (`BND-000`). Which widgets does Plux offer, how is each described, and how do we prove the description matches Flutter?

## Decision drivers

- One source of truth for the compiler, the runtime's prop decoders, Studio's property panels and AI grounding (`WGT-002`).
- Stable binary encoding: IDs never change or get reused (`BND-011`).
- Evidence that every Flutter parameter is either supported or excluded for a recorded reason, re-checked on every Flutter upgrade (`WGT-003`).
- Safe evolution: new props declare the runtime that understands them (`WGT-004`).
- A catalogue small enough to implement, test and document well.

## Considered options

1. **Three layers — curated Flutter mirrors, Plux components, host-app native slots — each widget defined by a JSON descriptor with permanent IDs, checked against the Flutter SDK by a generated coverage table.**
2. Mirror every public Flutter widget automatically from the SDK.
3. A small, Plux-specific widget vocabulary unrelated to Flutter's names.

## Decision

Chosen option: **1**, with the layers of spec §8: Layer 1 mirrors curated Flutter widgets with their names and semantics plus structural primitives; Layer 2 holds Plux components built from Layer 1; Layer 3 is the host app's own widgets (native slots, P4).

### Descriptors

Every widget type is one file in `schema/widgets/layer1/` or `schema/widgets/layer2/`, validated by `schema/json/registry/widget-descriptor.schema.json` (`WGT-001`). A descriptor declares the type name and permanent ID, layer, phase, Flutter counterpart (library, class, named constructor), `props` (name, ID, type, default, constraints, documentation, Flutter parameter, `revision`, deprecation), `events` (name, ID, payload type, Flutter callback), `children` or `slots` (name, ID, single or list, required), platform support, accessibility requirements, a build-cost hint in microseconds on the mid-tier reference device (`CMP-040`), Studio metadata (category, icon, documentation) and `excluded` Flutter parameters with their reasons.

Prop types use the type grammar of `SCH-010`, extended with the **value types** in `schema/widgets/types/` — `EdgeInsets`, `Alignment`, `BoxDecoration`, `TextStyle`, `InputDecoration`, `ButtonStyle` and the others Flutter needs — and the **enums** in `schema/widgets/enums/` (`MainAxisAlignment`, `TextAlign`, …), and a descriptor may declare single-letter type parameters bound per node, such as the item type of a list. Value types and enums are shared by all descriptors and actions; their fields and values carry permanent IDs too. A value type may declare named **constants**, such as `Alignment.center` or `EdgeInsets.zero`, so a literal is either an object of its fields or a constant's name. Enums that mirror a Flutter enum name it and must cover or exclude each of its values. Defaults are literals of the member's type, checked by `schemagen`; numeric props carry the bounds Flutter asserts as `constraints`. Object-typed prop values are deduplicated as styles (`CMP-020`).

### Permanent IDs

IDs are written in the descriptors, so reviewers see them, and recorded in `schema/widgets/ids.lock.json`, an append-only history that includes retired names, keyed by path (`widget/Text`, `widget/Text/prop/data`, `enum/TextAlign/value/center`, …). `make gen` appends new entries and fails when a name changes ID or takes an ID the lock has given to another name, retired or not; `make gen-check` fails if the lock is stale; and `make registry-lock-check`, run in CI against the pull request's base, fails when an entry is changed or removed (`BND-011`). Widgets, value types, enums and actions are four ID spaces; props, events and slots are each numbered within their widget, fields within their value type, values within their enum and inputs within their action. Actions (Appendix D) follow the same scheme in `schema/actions/`, validated by `schema/json/registry/action.schema.json`: typed inputs and output, type parameters bound from the step's context (the route's parameters for `navigate`, the state entry's type for `setState`), what a string input names (`ref`: a state entry, data source, flow, …) so the compiler can resolve it and Studio can offer a picker, named branches, and effects.

### Coverage table

`packages/plux_widget_api` is a development-only Dart tool that uses `package:analyzer` to resolve the pinned Flutter SDK and writes `schema/widgets/flutter-api.json`: for each Flutter class named by a descriptor or value type, every parameter of the mirrored constructors with its type, default, whether it is required and whether it is deprecated, and the values of every mirrored enum. It refuses to run unless the workspace resolves the pinned Flutter version. `tools/cmd/schemagen` joins this snapshot with the registry into `schema/widgets/COVERAGE.md` and fails unless every parameter is covered by members — a prop, an event, a slot, the children list or a value-type field; several members may compose one parameter, as the grid-delegate props of `GridView` do — or listed in `excluded` with one of the reasons of `WGT-003`: `callback→event`, `controller→state`, `builder→template`, `non-serialisable`, `deprecated` (only for parameters Flutter deprecates), `deferred`. Mappings and exclusions that name nothing in the snapshot fail too, so descriptors cannot go stale. The Dart CI job regenerates the snapshot with the pinned Flutter (`make widgets-api-check`), so an upgrade that adds a parameter fails CI until the parameter is supported or excluded. `key` is excluded as `non-serialisable`: the runtime derives keys from node IDs.

### Children and slots

A descriptor allows either `children` (a list of nodes) or named `slots`, never both (`SCH-023`). A slot is single (`"child"`), a list (`"actions"`) or an item template built per item with `item` and `index` in scope. This mirrors Flutter's `child`/`children` convention while giving multi-child widgets such as `Scaffold` explicit names; a widget with both named parts and a list, such as `ExpansionTile`, takes the list as a list slot named `children`.

### Evolution

A descriptor, value type and enum each has an integer `revision` and a table mapping each revision to the first runtime version that implements it; the entries of phase P3 record `0.1.0`, the first runtime release. Every prop, event, slot, field and value records the revision that added it. When a node uses members newer than the app's `minRuntimeVersion`, the compiler either rejects the publish or, when the app allows it, adds `widget.<Type>.v<revision>` (or `type.<Name>.v<revision>`, `enum.<Name>.v<revision>`) to the bundle's required features with a warning (`WGT-004`). Changes are additive: members are deprecated, never removed or renumbered.

### Responsive overrides

A node may override props per window-size class — `medium` (600–839 dp) and `expanded` (≥ 840 dp), with `compact` as the base — aligned with Material 3 (`WGT-010`). Overrides cascade mobile-first, like CSS `min-width` media queries: `expanded` applies on top of `medium`. They compile to override layers, not duplicated subtrees (`BND-016`).

### Structural primitives

`If`, `Match` (multi-branch by value), `ForEach`, `Responsive` and `Slot` are Layer 1 widgets with no Flutter counterpart and no coverage row. The spec's structural `Switch` was renamed `Match` (spec 1.1.2), because Layer 1 names follow Flutter and Material's toggle is `Switch`.

### Scope

P1 describes every Appendix C widget tagged P3 — Layer 1 and Layer 2 — the exit criterion of spec §5.1. Later widgets receive descriptors, with new permanent IDs, in their phases.

### Generated consumers

`schemagen` checks the registry — identity and permanence of IDs, type expressions, defaults and constraints, revisions, coverage — and emits the tables of `backend/internal/schema/registry` for the Go compiler, the permanent IDs for the Dart runtime (`registry.g.dart`), complete typed descriptors for Studio and AI grounding (`registry.gen.ts`), the references `docs/reference/widgets.md` and `docs/reference/actions.md`, and the coverage table. The JSON Schemas are enforced by a backend test over every registry file. No component keeps a hand-written list (`WGT-002`).

## Consequences

- **Positive:** one reviewed JSON file per widget drives four consumers; the coverage table turns "we support Flutter" into a checked claim; IDs are stable by test, not by discipline.
- **Negative:** descriptors are large, hand-authored data that must be kept accurate; Flutter upgrades require descriptor work (quarterly, `WGT-005`); a Dart tool with `analyzer` joins the workspace as a development dependency.
- **Follow-up:** runtime prop decoders generated from descriptors (P3), native-slot descriptors from `plux native scan` (P4), later-phase widgets.

## Options in detail

### Option 1 — layers and descriptors

Curated, stable and verifiable. The price is authoring descriptors and keeping them in step with Flutter, which the coverage table makes mechanical.

### Option 2 — mirror everything automatically

Maximal breadth with little authoring, but it would expose plumbing widgets and non-serialisable parameters, tie the bundle format to Flutter's churn, and leave behaviour — events, controllers, builders — undefined.

### Option 3 — a Plux-only vocabulary

Full control and stability, but Flutter developers could not transfer their knowledge, documentation would have to be written from scratch, and Studio users could not map a design to Flutter concepts.
