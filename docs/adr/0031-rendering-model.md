# 0031. Rendering model: generated node builders over mapped sections

- **Status:** Accepted
- **Date:** 2026-09-28; revised 2026-09-29 (see [Revision](#revision-2026-09-29-layout-errors-and-adaptive-widgets)); revised 2026-10-04 (see [Revision](#revision-2026-10-04-what-p3-does-not-do-done-in-p5))
- **Requirements:** `RT-010`–`RT-016`, `RT-020`–`RT-022`, `WGT-002`, `WGT-011`–`WGT-014`, `WGT-020`, `BND-015`–`BND-017`, `CMP-023`, `CMP-024`, `NFR-002`, `NFR-003`

## Context and problem

A page section is a flat array of nodes with typed props, bindings, override layers and
component instances (ADR-0002). The runtime must turn it into Flutter widgets fast enough
that a 300-node page builds in ≤ 8 ms (`NFR-003`), without first decoding the page into an
object graph (`RT-010`), building only what is on screen (`RT-011`) and rebuilding only
what changed (`RT-012`). The P3 catalogue has about 85 Layer 1 widgets with over a
thousand props, and `WGT-002` requires the runtime's prop decoders to be generated from the
descriptors rather than maintained by hand. Failures must stay inside the page that caused
them (`RT-020`). And P3 renders only: events and actions arrive in P5. How are nodes turned
into widgets?

## Decision drivers

- Zero-copy reads through the generated FlatBuffers accessors; no intermediate tree.
- Descriptors as the single source of prop decoding (`WGT-002`).
- Laziness and fine-grained rebuilds as structural properties, not per-widget care.
- Every failure contained and reported with its node path.
- A clear, explicit boundary for what P3 does not do.

## Considered options

1. **One `PluxNode` widget per node, reading the mapped section lazily, with builders and value decoders generated from the descriptors and a small hand-written set for structural and collection widgets.**
2. Decode each page into Dart objects once, then build widgets from them.
3. Hand-written builders for every widget.

## Decision

Chosen option: **1**.

### Pages and components in memory (`RT-010`, `RT-013`)

A page or component section is read in place through `fbs.Page`/`fbs.Component` over the
mapped bytes. What the first build of a section needs repeatedly — its string table as a
lazily filled list, the node index, the resolved component references — is held by a small
`SectionView`, cached in an LRU keyed by **section hash** and bounded by entry count and
bytes (`runtime.sectionCacheEntries`, `runtime.sectionCacheBytes`, limits registry). The
cache is cleared on `didHaveMemoryPressure` (`RT-013`). Props are decoded when a node
builds, from the accessors, never ahead of time.

### Node widgets

Each node is a `PluxNode` — a `ConsumerStatefulWidget` keyed by the node's UUID — that:

1. collects, once, the read sets of the PXL programs bound to its props, visibility and
   override conditions, and subscribes to them with `select` (ADR-0008);
2. picks the active override layers — size class from the window width (`WGT-010`),
   platform, locale and experiment variant — and merges their props over the base props
   (`BND-016`);
3. evaluates bound values with the PXL VM against the page's environments, resolving
   design tokens, styles and translations;
4. calls the widget type's builder with a `NodeContext` that gives typed props, children
   and slot fills as widgets, item templates as builders, and event wiring.

A node carrying the `RepaintBoundary` hint (`CMP-024`) is wrapped in one. The node's
`testId` becomes `Semantics(identifier:)`, which Android exposes as the resource ID and iOS
as the accessibility identifier for UI tests, and its semantics props become a `Semantics`
widget (`WGT-013`).

### Generated builders and decoders (`WGT-002`)

`schemagen` generates, from `schema/widgets/`:

- **value decoders** for every value type and enum — `EdgeInsets`, `BoxDecoration`,
  `TextStyle`, `ButtonStyle`, `MainAxisAlignment`, … — from their fields' Flutter mapping;
- **builders** for every Layer 1 widget whose props, events and slots map one to one onto
  a Flutter constructor, passing each prop to the named parameter and applying the
  descriptor's defaults;
- a **dispatch table** from permanent widget ID to builder, and a test that every widget
  whose phase has been reached has an entry.

Widgets whose behaviour is not a constructor call are written by hand against the same
generated decoders: the structural primitives (`If`, `Match`, `ForEach`, `Responsive`,
`Slot`), collections with item templates, controlled inputs (a text field's controller,
a switch's local value) and the Layer 2 components (`WGT-020`). A hand-written builder
registers in the same table, so there is one dispatch.

### Laziness (`RT-011`, `WGT-012`)

Children are passed to builders as widgets whose nodes build only when Flutter builds
them. `ListView`, `GridView`, `PageView` and the slivers take an **item template** and a
list value and build through `SliverChildBuilderDelegate`, instantiating the template per
visible index with `item` and `index` in scope; they show the declared `empty`, `loading`
and `error` slots by the list's status, and report `onEndReached` for pagination from P5.
`ForEach` is the bounded, non-lazy repeat.

### Adaptive widgets (`WGT-011`)

The widgets Flutter pairs with a Cupertino variant through an `.adaptive` constructor —
`Switch`, `SwitchListTile`, `Slider`, `Checkbox`, `CheckboxListTile`, `Radio`,
`RadioListTile` and `CircularProgressIndicator` — have an `adaptive` prop (a literal
`bool`, default `false`). Its descriptor names the constructor it selects
(`"constructor": "adaptive"`), and the generated builder calls that constructor while the
prop is true, each call with the arguments its constructor declares; Flutter then renders
the Cupertino look on iOS and Material elsewhere.

### Components (`BND-017`)

A component instance node renders the component's section: its props are the instance's
values by declaration index, its slots are the instance's fills, and its state is scoped
to the instance. Each instance is wrapped in an error boundary.

### Fault isolation (`RT-020`, `WGT-014`)

A `PluxErrorBoundary` wraps every page and every component instance. It contains:

- **build errors** — a node's builder runs inside `try`; an exception renders the
  boundary's fallback;
- **decode and PXL errors** — a bad value or a failed evaluation makes that prop take its
  declared default and reports the error; a node whose required value cannot be produced
  fails as a build error.

**Layout and paint errors** are contained by Flutter itself: it catches an exception in a
render object's `performLayout` or `paint`, confines the damage to that render object, and
reports it to `FlutterError.onError`, which belongs to the host app. No ancestor can observe
it, so Plux neither catches nor claims such errors; they reach the host's handler (Crashlytics,
Sentry or Flutter's default), and the P10 devtools attribute them to Plux nodes.

Every failure is logged with its node path (from the source map in development bundles,
from node indices otherwise), reported as a telemetry `error` event (`ANL-001`) with a
`PLX-4001` code, and never propagated to the host app or to other pages. The fallback is
themed and configurable per app and plugin. An unknown widget ID renders a neutral
placeholder and reports `PLX-4003`; the placeholder is empty in release builds and labelled
in debug builds.

### What P3 does not do

P3 renders; it does not act or navigate:

- **Events** declared on a node (`onPressed`, `onChanged`, …) are wired, so that a button
  with a handler is enabled, but the handler is a no-op that reports `PLX-4010` — "actions
  arrive in P5" — in debug builds. Error routing of action runs (`RT-021`) arrives with the
  executor in P5; its no-op here is explicit, not partial.
- **Interactive widgets** keep their input locally (a text field keeps its text, a switch
  its position) so that pages are usable, but write nothing to state.
- **Bindings** read page parameters, the page's declared initial state, translations and
  the theme; data sources, exposed state and computed state arrive in P4 and P5. Every
  state feature lands in P5 together (maintainer, 2026-09-29), so until then a computed
  entry is null and a binding reading it reports `PLX-4002`. The loan calculator's
  `calculator` page (`schema/testdata/documents/loan-calculator`) therefore shows its
  fallback in P3: its `monthlyPayment` is computed and feeds a required prop.
- **Navigation** is `Plux.open` of one page on the host navigator and `PluxView`;
  routes, guards and transitions are P4.

A bundle that needs any of these through `required_features` is refused cleanly
(`BND-008`), and one that merely declares them renders with the no-ops above.

### Timeline and first-frame measurement (`RT-015`)

Page builds, the first frame of a page and, from P5, action runs are wrapped in
`dart:developer` `TimelineTask`s named `plux.page.build`, `plux.page.firstFrame` and
`plux.action`, visible in Flutter DevTools, and their durations feed the `render_perf`
telemetry event.

### The device is the reference (`RT-016`)

What the runtime renders is authoritative; the Studio canvas (P11) is kept faithful to it by
the layout conformance suite, whose device side is generated from these same builders.

## Revision (2026-09-29: layout errors and adaptive widgets)

The maintainer decided two points the P3 implementation raised.

- **Layout and paint errors (`RT-020`).** The first version had a boundary render object
  catch them. That cannot work: Flutter catches an exception inside a descendant's
  `performLayout` or `paint` in `RenderObject.layout` and `PaintingContext` and reports it
  to `FlutterError.onError`; nothing propagates to an ancestor. The alternative — chaining
  a Plux handler onto `FlutterError.onError` while Plux runs — makes Plux own a global slot
  that crash reporters also set (whichever is set last wins), is global mutable state, and
  can attribute an error to a Plux node only with creator information that exists in debug
  builds. Plux therefore contains build, decode and PXL errors in its boundaries, and
  leaves layout and paint errors to Flutter's own containment and the host's handler.
  `RT-020` is reworded accordingly.
- **`adaptive` (`WGT-011`).** Nothing in a document could carry `adaptive: true`. It is an
  additive `adaptive` prop on the eight widgets Flutter pairs through `.adaptive`
  constructors, marked in their descriptors by the new `constructor` field; the
  constructors are mirrored in the Flutter snapshot, so their own parameters
  (`applyCupertinoTheme`, `useCupertinoCheckmarkStyle`) are covered like any other.
  Widgets with a Cupertino counterpart but no `.adaptive` constructor keep separate
  Material and Cupertino widgets. `WGT-011` is reworded to name this scope.

## Revision (2026-10-04, what P3 does not do, done in P5)

P5 completes what *What P3 does not do* deferred. Event handlers run on the action engine
since P4 ([ADR-0039](0039-action-engine-core.md)) and run every catalogue action from P5
([ADR-0045](0045-action-engine-completion.md)), with `RT-021`'s full error chain.
Interactive widgets write their values to state and forms
([ADR-0046](0046-state-engine.md), [ADR-0047](0047-forms-validators-regex-and-phone.md)).
Bindings read computed entries, plugin and component state and data sources
([ADR-0048](0048-data-layer.md)), so the loan calculator's `monthlyPayment` renders, and
lists bind to paginated sources (`WGT-012`). Action runs appear in the timeline as
`plux.action` (`RT-015`), and animation widgets, Lottie and Rive arrive
([ADR-0050](0050-animation-engine.md)). The node builders, containment and laziness
described here do not change.

## Consequences

- **Positive:** no page is ever decoded ahead of use; the descriptors drive decoding, so a
  new prop needs no hand-written runtime code; containment is structural; the P3 boundary
  is visible in code and in debug reports.
- **Negative:** the generator must understand Flutter constructor shapes well enough to
  emit correct calls, and some widgets need hand-written builders; one widget per node adds
  element overhead, which the benchmarks bound (`NFR-003`).
- **Follow-up:** events and actions in P5 replace the no-op handlers; native slots in P4;
  animation widgets in P5.

## Options in detail

### Option 2 — decode pages into objects

Simpler builders, but allocation and CPU proportional to page size on every open, the
opposite of `RT-010`, and a second representation to keep in step with the schema.

### Option 3 — hand-written builders

Full control, but over a thousand props to decode by hand, drift from the descriptors on
every Flutter upgrade, and a direct conflict with `WGT-002`.
