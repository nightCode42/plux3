# 0023. Mixed native and plugin screens: native slots and `PluxView` with shared exposed state

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `HST-021`, `WGT-033`, `NAV-004`, `STA-030`, `HST-001`, `HST-013`, `RT-020`

## Context and problem

`HST-021` asks for mixed screens in both directions:

- a plugin page holds native slots: existing host widgets with props from bindings and
  typed events into action graphs (`WGT-033`);
- a native screen holds any number of `PluxView`s, each a plugin page or exported
  component, chosen by name only, with typed inputs, events back to native code, its own
  error boundary and a sizing mode (`NAV-004`).

Both sides share state through exposed state entries, which native code can observe as
streams (`STA-030`), so content on one screen stays consistent.

The spec plans this ADR for P4 (§32). The maintainer split the state part (P4 plan D4):

- P4 builds the native side: `Plux.state<T>` reads, writes and watches an exposed entry,
  and plugin bindings react to native writes.
- Plugin-side writes arrive with P5's `setState`, so `HST-021` and `STA-030` stay `WIP`
  until then.

In P3:

- `PluxView(route)` hosts one page, inside its own boundary;
- bindings read parameters, the page's declared initial state, translations and the
  theme;
- every state feature lands in P5 (ADR-0031, *What P3 does not do*).

## Decision drivers

- **Existing widgets are not modified** (`WGT-030`, `HST-031`).
- **Names only.** Native code never names a plugin (`NAV-003`, `NAV-004`).
- **Contained failures.** A slot or a view that fails shows its fallback, never a broken
  screen (`RT-020`).
- **One state.** Several views and slots on one screen read one value, through the
  runtime's Riverpod container (ADR-0008). Nothing is copied.
- **Typed at both edges.** Values crossing between host and plugin are checked against
  declared types.
- **No half-implemented P5 state.** P4 adds only what the native side needs.

## Considered options

1. **Slots as nodes built from the registration, views over the shared runtime, and
   exposed state as live app-level providers** that the host writes through typed handles.
2. **Platform views or separate engines per view.**
3. **State synchronised by messages** between the host and each view.

## Decision

Chosen option: **1.**

- Option 2 costs a Flutter engine or a platform view per embedding, and loses shared state.
- Option 3 copies state, so two views could disagree for a frame. It also adds a protocol
  that a shared container makes unnecessary.

### Native slots in plugin pages (`WGT-033`)

A slot node names a slot `type` from the native catalogue (ADR-0041). Its builder is the
host's registration:

```dart
nativeSlots: {'MapCard': PluxNativeSlot(MapCard.new)}
```

**Building the widget:**

- The runtime decodes each prop by the catalogue's declared type and calls the
  constructor tear-off with `Function.apply` and named arguments. The widget class is
  unchanged.
- A host whose widget needs adapting registers a builder function instead.
- Props come from bindings like any node's, so a slot rebuilds when the values it reads
  change.

**Events:**

- A callback parameter named `on…` that the catalogue declares as an event receives a
  closure. Calling it starts the node's handler graph, with the payload as `event`, after
  checking it against the declared payload type.
- A closure taking `Object?` fits any one-argument callback type, because function
  parameters are contravariant. A no-argument callback receives a no-argument closure.

**Layout and failures:**

- The slot is an ordinary child: constraints in, size out. In a scrollable it gets the
  scrollable's constraints, as any child does.
- A slot that throws while building, or that the host did not register, is replaced by
  the page's node fallback, inside the page's boundary (`RT-020`). The second case reports
  `PLX-4201`.

### `PluxView` in native screens (`NAV-004`)

`PluxView('<name>', inputs: …, onEvent: …, sizing: …)` hosts a plugin page, or an
**exported component**, by name.

**Names:**

- A name resolves to a route first, then to an exported component.
- From R7 the compiler requires exported component keys to be unique across the app and
  different from every route name, so a name means one thing.

**Inputs and events:**

- **Inputs** are the page's parameters or the component's props. They are checked like
  route parameters (`PLX-4101`), and typed by the classes `plux codegen` writes (R8,
  `HST-030`).
- **`onEvent`** receives a component's declared events, each with its typed payload.
- An inline page has no route to pop, so its `pop(result)` arrives as an event too.
- Host events that a view emits with `emitHostEvent` go to `Plux.events` (below), like
  any other.

**Sizing:**

| Mode | Behaviour |
|---|---|
| `intrinsic` | The view sizes to its content within the incoming constraints. |
| `fixed` | The host gives a size. |
| `expand` | The view fills the incoming constraints, which must be bounded. |

**Shared runtime and failures:**

- Every view on a screen uses the one runtime and its container, so state and caches are
  shared, and opening a second view costs no second sync or verification.
- Each view has its own boundary and fallback (P3).
- A route that does not resolve reports `PLX-4100` and shows the fallback.

### Host events (`HST-013`, plugin to host)

`Plux.events` is a stream of the host events that plugins emit with `emitHostEvent`
(ADR-0039). Each event has a name and a payload, checked against the app document's
`hostEvents` declaration, which spec 1.2.0 adds (maintainer, P4 plan A11).

- `plux codegen` writes one Dart class per declared event (R8), so the host listens to
  typed events.
- The other half of `HST-013`, the host sending events into Plux, is a trigger
  (`ACT-002`) and arrives in P5.

### Exposed state (`STA-030`, native side)

An exposed entry is an app document `state` entry marked `exposed`.

- Hosts address entries by name, never by plugin, so from R7 the compiler refuses
  `exposed` on plugin, page and component entries.
- `Plux.state<T>(name)` returns a handle:
  - `value` reads the current value;
  - `set(T)` writes it;
  - `watch()` is a `Stream<T>` that emits on every change.
- `plux codegen` writes typed accessors for each exposed entry (R8).
- A write is checked against the entry's declared type. A value of the wrong type is
  refused, reports `PLX-4203` and leaves the value unchanged. A name that is not exposed
  is refused with the same code.

**What P4 makes live:**

- App-level state entries become live Riverpod providers, holding their declared default
  until the host writes them.
- Plugin bindings that read an entry rebuild through the existing `select` path, so every
  view and slot on a screen sees a write in the same frame.

**What stays P5's:**

- `setState` and the other plugin-side writes;
- computed entries;
- persistence;
- state watchers as triggers.

An exposed entry that declares persistence is kept in memory in P4, and reports
`PLX-4010` once in debug builds.

## Consequences

- **Positive.**
  - Native widgets and plugin content mix in both directions without changing host code.
  - One state is shared by every view on a screen.
  - Failures stay inside their boundary.
- **Negative.**
  - Slot construction by `Function.apply` is checked only at run time. The catalogue's
    types and the scanner (ADR-0041) are what keep it right, and a failure shows the
    node fallback.
  - Until P5, plugins only read exposed state.
- **Follow-up.**
  - R6: slot nodes.
  - R7: `PluxView` v2, `Plux.events`, `Plux.state<T>`, the compiler checks above, and the
    starter's mixed screen.
  - R8: generated accessors and classes.
  - P5: plugin-side writes, and the host-to-Plux half of `HST-013`.

## Options in detail

### Option 2: platform views or one engine per view

Each embedding would be isolated. But it costs memory and start-up time per view,
`NAV-004`'s "any number of `PluxView`s" becomes expensive, and state would need
synchronising across engines anyway.

### Option 3: state synchronised by messages

It would work across engines. But each view would hold a copy, so two views could show
different values until messages arrive. It also adds a protocol to version, when one
Riverpod container already gives one source of truth.
