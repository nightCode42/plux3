# 0008. Riverpod as the runtime state engine

- **Status:** Accepted
- **Date:** 2026-09-28; revised 2026-10-04 (see [Revision](#revision-2026-10-04-the-state-engine-in-p5))
- **Requirements:** `RT-003`, `RT-012`, `CMP-023`, `HST-001`, `STA-*` (from P5)

## Context and problem

Every node of a Plux page reads values — page parameters, page state, translations, the
theme, and from P5 data sources and exposed state — through PXL bindings whose read sets
the compiler records (`CMP-023`). When one of those values changes, only the nodes that
read it may rebuild (`RT-012`): a 300-node page must build in ≤ 8 ms (`NFR-003`), and a
text field that rebuilds its page on every keystroke cannot meet that. The runtime also
lives inside host apps that already have a state-management choice, most often Riverpod,
sometimes none. Which engine holds and propagates Plux state, and how does it coexist
with the host's?

## Decision drivers

- Fine-grained subscriptions: a node rebuilds only when a value in its read set changes.
- Coexistence with host apps that use Riverpod, sharing or nesting their container, and
  with host apps that do not (`RT-003`).
- State scoped to a page instance, disposed with it, with no global mutable state
  (AGENTS.md §8).
- Testable without widgets: providers can be read and overridden in plain Dart tests.
- A small, maintained, permissively licensed dependency with no code generation in the
  host's build.

## Considered options

1. **Riverpod (`flutter_riverpod`) without code generation.**
2. `InheritedWidget`/`InheritedModel` and `ValueNotifier`s written in-house.
3. Bloc.
4. Provider.
5. Signals (`signals` package).

## Decision

Chosen option: **1**, as the specification requires (`RT-003`), with `flutter_riverpod`
3.4.3 (MIT, maintained by the Riverpod author, pure Dart). Providers are declared by hand;
`riverpod_generator`, `riverpod_annotation` and `build_runner` are **not** used, so host
apps need no code generation step and the runtime has no generated-code dependency.

### Containers (`RT-003`)

The runtime works with exactly one `ProviderContainer`, chosen at `Plux.initialize`:

| Mode | How it is chosen | Container |
|---|---|---|
| Self-contained (default) | no container given | Plux creates its own and disposes it in `Plux.dispose()` |
| Shared | `PluxConfig.container` is the host's container | Plux providers are read from the host's container; Plux never disposes it |
| Nested | `PluxConfig.parentContainer` is the host's container | Plux creates `ProviderContainer(parent: host)`, so host overrides are visible to Plux, and disposes only its child |

Plux widgets (`PluxView`, pages opened with `Plux.open`) insert an
`UncontrolledProviderScope` for the runtime's container unless the nearest enclosing scope
already holds it, so they work under a host `ProviderScope`, under none, and inside
another Plux page. Riverpod providers are top-level `final` declarations, not mutable
globals; all state lives in the container.

### Provider graph (P3)

| Provider | Kind | Holds |
|---|---|---|
| `pluxRuntimeProvider` | `Provider` (overridden at start) | the runtime facade: release store, sync client, telemetry |
| `activeReleaseProvider` | `NotifierProvider` | the active release: mapped bundles and the route index; replaced only by activation (`SYN-004`) |
| `pageStateProvider(instance)` | `NotifierProvider.family`, auto-dispose | one page instance's parameters and state, an immutable map replaced on every write |
| `environmentProvider` | `NotifierProvider` | locale, theme mode, brand, consent, platform, size class |
| `limitsProvider` | `Provider` | the limits of the active app bundle (`LIM-001`, `LIM-004`) |
| `syncStatusProvider` | `StreamProvider` | the latest `SyncEvent` (`SYN-013`) |

A page instance gets a unique key when it mounts; its nodes find it through an inherited
`PluxPageScope`. Page state is immutable and replaced on each write, so equality of a read
value is identity for collections and `==` for scalars.

### Subscriptions from read sets (`RT-012`, `CMP-023`)

Every compiled PXL program carries its read set: the root-qualified paths it reads, such as
`state.amount` or `params.productId`. When a node widget first builds, it takes the union of
the read sets of its bound props, its visibility and its override conditions, grouped by
root, and subscribes to each root once with `select`:

```dart
final values = ref.watch(pageStateProvider(page).select((s) => s.readAll(statePaths)));
```

`readAll` returns a fixed-length snapshot whose `==` compares element by element, so the
node rebuilds only when a value it reads changes. A node whose props are all constants
subscribes to nothing. A widget test counts builds per node and proves the rule.

### P3 scope

In P3 nothing inside a plugin writes state: actions arrive in P5 (`ACT-*`) and host
read/write of exposed state in P4/P5 (`STA-030`). The initial state of a page is the
defaults its document declares. The runtime writes state only in tests, through an
internal API that is not exported, so that the rebuild rule is verified now.

## Revision (2026-10-04, the state engine in P5)

P5 builds the write side on this design ([ADR-0046](0046-state-engine.md)): providers for
the `plugin` and `component` scopes beside `appStateProvider` and `pageStateProvider`,
with `run` variables kept in the run; typed writes applied per step in one notification;
computed entries as providers watching their read sets; state watchers through
`ProviderContainer.listen`; and persistence (`session`, `persisted`, `secure`) on the
core's built-in store ([ADR-0049](0049-local-persistence.md)), loaded and written off the
UI isolate. The containers, the read path from read sets and the `select` rule are
unchanged; the *P3 scope* above no longer applies once P5's R2 lands.

## Consequences

- **Positive:** fine-grained rebuilds come from the compiler's read sets, not from
  hand-written selectors; host apps share or nest their container without adapters;
  providers are testable in plain Dart; no code generation in host builds.
- **Negative:** one more dependency inside every host app (about 150 KiB of Dart AOT code,
  counted against `RT-061`); a Riverpod major version is a Plux upgrade decision, and host
  apps are held to a compatible Riverpod version.
- **Follow-up:** state writes, computed state and persistence with `STA-*` in P5; exposed
  state read/write for hosts in P4/P5; the provider graph grows with data sources in P5.

## Options in detail

### Option 2 — in-house inherited widgets and notifiers

No dependency, but a fine-grained subscription layer, scoping and disposal would be ours
to build and maintain, and host apps that use Riverpod would get no integration.

### Option 3 — Bloc

Event-driven and well known, but coarse-grained by default (a `BlocBuilder` rebuilds on
every state change unless each widget writes a `buildWhen`), and it does not share a
container with Riverpod hosts.

### Option 4 — Provider

The predecessor of Riverpod by the same author; `select` exists, but providers are bound
to the widget tree, which makes page-independent state and testing without widgets harder.

### Option 5 — Signals

Very fine-grained, but younger, with a smaller ecosystem and no host integration story;
the specification names Riverpod.
