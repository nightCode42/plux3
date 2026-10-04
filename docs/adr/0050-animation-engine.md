# 0050. Animation: Flutter's animation framework driven by the bundle, Lottie and Rive in optional packages

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `ANI-001`–`ANI-008`, `NAV-010`, `WGT-021`, `CMP-033`, `RT-060`, `RT-061`, `BND-017`, `A11Y-007` (its reduce-motion part), `LIM-001`

## Context and problem

P5 brings animation (spec §14.6): implicit animation of bound props (`ANI-001`), timelines
with keyframes, curves, stagger, repeat and reverse controlled from actions (`ANI-002`),
enter and exit transitions (`ANI-003`), Hero between Plux pages and between Plux and native
pages (`ANI-004`), Lottie and Rive (`ANI-005`), gesture- and scroll-linked animation with
springs (`ANI-006`, a `SHOULD` the maintainer chose to deliver, B1), reduce motion
(`ANI-007`) and compiler warnings for expensive patterns (`ANI-008`). P4 left custom
timeline page transitions to P5 (`NAV-010`, [ADR-0040](0040-navigation-delegate-and-router-adapters.md)).

What exists: the bundle's timelines section (`Timeline`, `BND-017`), the `page.animations`
budget in the limits registry, dotLottie packaging at publish (`CMP-033`,
[ADR-0027](0027-asset-pipeline.md)), and `LottieView` and `RiveView` in the catalogue,
placed in `plux_lottie` and `plux_rive`. The maintainer decided (plan D16, accepted in B2):
`plux_lottie` on the `lottie` package, `plux_rive` on Rive's official Flutter runtime, each
an optional package with its licence and size checked.

## Decision drivers

- **Native performance:** animations run on Flutter's own tickers and render objects, not
  on rebuilds of the node tree per frame.
- **Accessibility:** reduce motion is honoured by every animation (`ANI-007`).
- **Bounded:** timelines per page within `page.animations` (`LIM-001`).
- **Heavy runtimes stay optional** (`RT-060`, `RT-061`, `WGT-021`).
- **Publish-time checks** for expensive patterns (`ANI-008`).

## Considered options

1. **Flutter's animation framework driven by the bundle**: implicit animation widgets,
   `AnimationController`s per running timeline, `Hero`, physics simulations; Lottie and
   Rive in optional packages.
2. **A third-party animation package in the core** (for example a declarative animation
   DSL package).
3. **Per-frame state writes**: timelines write state and bindings rebuild nodes each frame.

## Decision

Chosen option: **1.** Option 2 adds a dependency to every host app for what the framework
already does. Option 3 rebuilds widgets every frame and breaks `RT-012`'s promise that a
node rebuilds only when its data changes.

### Implicit animation (`ANI-001`)

Widget descriptors mark the props that can animate, with their value type (number, colour,
size, offset, alignment, edge insets, border radius). A node whose animatable prop declares
an animation (duration, curve, delay) animates from the old value to the new one when its
binding changes, through an implicit animation widget around the node's builder. Props not
marked animatable cannot declare one; the compiler refuses it.

### Timelines (`ANI-002`)

- A timeline in the bundle's timelines section holds keyframes per target prop — transform
  (translate, scale, rotate), opacity, colour, size and other animatable props — with
  curves, stagger across the items of a list, repeat and reverse.
- A page owns its timelines. Each running timeline is one `AnimationController` on the
  page's ticker, and its targets read it through transitions on their render objects
  (`Transform`, `Opacity`, and their kin), so a frame repaints without rebuilding nodes.
- `startAnimation` and `controlAnimation` (`play`, `pause`, `seek`, `reverse`) control a
  timeline from a graph; a run waiting for a timeline to finish is cancelled with its
  page ([ADR-0045](0045-action-engine-completion.md)).
- The number of timelines per page is bounded by `page.animations`; the compiler refuses
  more.

### Enter and exit (`ANI-003`)

A node declares an enter and an exit transition, built from the same keyframe model. A
node shown or hidden by its visibility binding, and an item inserted into or removed from
a bound list (by its key), plays them; a removed node stays mounted until its exit ends.

### Hero (`ANI-004`)

A node with a `hero` tag is wrapped in Flutter's `Hero`. Plux pages are routes on the
host's navigator (ADR-0040), so a Plux page and a native page whose widgets share a tag
animate between each other with no further mechanism. Tags are plain strings, so native
code can use them.

### Gesture- and scroll-linked animation (`ANI-006`)

A timeline's progress can follow a drag (with its velocity) or a scroll position instead of
time. Releasing a drag settles with a spring simulation from Flutter's physics library,
with the spring declared in the timeline. Parallax and collapsing headers are timelines
linked to a scroll position.

### Reduce motion (`ANI-007`)

Each animation declares what happens when the platform asks for reduced motion
(`MediaQuery.disableAnimations`): `disable` (jump to the end state, the default),
`shorten` (a short cross-fade) or `keep` (for motion that carries meaning, such as a
progress indicator). Hero, enter and exit transitions, page transitions, Lottie and Rive
follow the same setting.

### Expensive patterns (`ANI-008`)

The compiler warns, with a suggested alternative, when an animation:

- animates layout (size, padding, constraints) of a node with a large subtree — suggest a
  transform;
- animates the opacity of a large or complex subtree — suggest fading a smaller subtree
  or the colour's alpha;
- animates a clip over a large subtree.

"Large" is a registry threshold and each warning a registered compile-time code, both fixed
in R7 (`schema/limits.json`, `schema/errors.json`).

### Custom page transitions (`NAV-010`)

`routeOptions.transition` gains a reference to a timeline, an additive member (`SCH-000`).
The compiler checks that the timeline animates only what a page transition can (opacity
and transform of the incoming and outgoing page), and the route's page builder drives it
with the route's animation. With this, `NAV-010` can end `DONE`.

### `plux_lottie` (`ANI-005`)

- `LottieView` renders dotLottie with the `lottie` package (MIT, maintained,
  pure Dart).
- dotLottie archives were checked and rewritten canonically at publish (`CMP-033`) and are
  verified with the bundle before use (`SEC-052`). They are unpacked off the UI isolate
  (L-6), with the zip support `lottie` itself uses (its `archive` dependency, whose licence
  R7 records), so no zip reader of our own is written; the composition is parsed once per
  asset and cached.
- Play, pause, loop and progress are props, bindable to state.

### `plux_rive` (`ANI-005`)

- `RiveView` renders Rive files with Rive's official Flutter runtime, the `rive` package
  (MIT).
- State-machine inputs (booleans, numbers, triggers) are props bound to Plux state; a
  binding change sets the input.
- Recent versions of the runtime render through a native library. R7 checks how the
  pinned version supplies it — built from source in the host's build, or a prebuilt binary
  fetched at build time — and the licence of that library. A binary fetched from outside
  the package is put to the maintainer before the package is added; the alternative is the
  last version with the pure-Dart renderer.

### Packages and registration

Both packages register their widgets with the runtime at start-up through `PluxConfig`, as
other optional packages do. A page that uses `LottieView` or `RiveView` on a host without
the package renders the node fallback and reports a typed error; publishing warns when a
targeted host build lacks a package a release uses
([ADR-0051](0051-device-actions-packages-and-capabilities.md)). Each package's size per
ABI is measured when R7 adds it and recorded in the size journey's round 4.

## Consequences

- **Positive.**
  - Animations run at the framework's cost, on the raster path where possible, and never
    rebuild nodes per frame.
  - One keyframe model serves timelines, enter and exit, scroll-linked animation and page
    transitions.
  - Lottie and Rive cost nothing for apps that do not use them.
- **Negative.**
  - Every animatable prop needs a descriptor mark and a tween type; a new animatable type is
    a runtime change.
  - The Rive runtime's native library is a supply-chain question R7 must settle.
- **Follow-up.**
  - R7: implicit, timelines, enter and exit, Hero, linked animation, reduce motion, the
    compiler warnings, custom page transitions, `plux_lottie` and `plux_rive`;
    `WGT-021` ends `DONE`.

## Options in detail

### Option 2: an animation package in the core

Declarative animation packages chain effects on widgets well, but the bundle already
carries timelines in its own format, so the package would be an adapter layer over the
framework it wraps, and a dependency in every host app.

### Option 3: per-frame state writes

Writing a timeline's value to state each frame would reuse the binding path, but every
reading node would rebuild sixty or more times a second, and state watchers would fire per
frame.
