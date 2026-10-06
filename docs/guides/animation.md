<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Animation Guide

How Plux pages move: props that animate when their value changes, nodes that animate in
and out, shared elements between pages, timelines with keyframes, gesture- and
scroll-driven motion, custom page transitions, and Lottie and Rive. The examples are the
`animation` conformance project
([`stage.page.json`](../../schema/testdata/documents/animation/plugins/stage/pages/stage.page.json)).
The design is [ADR-0050](../adr/0050-animation-engine.md).

## 1. Animate a node

A node's `animation` animates its animatable props, numbers and colours, whenever their
bound value changes (`ANI-001`). `enter` and `exit` play when the node is inserted or
removed, or its visibility changes (`ANI-003`). `hero` tags a shared element, which flies
between pages, Plux or native, that use the same tag (`ANI-004`):

```json
{"type": "Container",
 "animation": {"durationMs": 250, "curve": "easeOut",
               "enter": {"kind": "fade", "durationMs": 200},
               "exit": {"kind": "slideDown", "durationMs": 200},
               "hero": "banner"}}
```

## 2. Timelines

A page's `animations` are timelines: tracks of keyframes on a node's prop (opacity, scale,
angle, translation, colour, size), with curves, repeat and reverse (`ANI-002`). `autoplay`
starts one with the page, and `staggerMs` offsets each item of a list:

```json
"animations": [
  {"name": "pulse", "durationMs": 800, "autoplay": true, "repeatForever": true, "reverse": true,
   "tracks": [{"node": "<node id>", "prop": "opacity",
               "keyframes": [{"atMs": 0, "value": 1.0}, {"atMs": 800, "value": 0.2, "curve": "easeInOut"}]}]},
  {"name": "rows", "durationMs": 300, "autoplay": true, "staggerMs": 100,
   "tracks": [{"node": "<row id>", "prop": "opacity",
               "keyframes": [{"atMs": 0, "value": 0.0}, {"atMs": 300, "value": 1.0, "curve": "easeOut"}]}]}
]
```

Actions control timelines: `startAnimation` plays one, and `controlAnimation` plays,
pauses, seeks or reverses it.

## 3. Follow a gesture or a scroll

A timeline with a `driver` follows a drag or a scroll instead of the clock (`ANI-006`):
`extent` pixels of movement run it from start to end. A `spring` lets a dragged value
settle with physics:

```json
{"name": "parallax", "durationMs": 1000,
 "driver": {"kind": "scroll", "node": "<scrollable id>", "extent": 200},
 "tracks": [{"node": "<node id>", "prop": "angle", "keyframes": [{"atMs": 0, "value": 0.0}, {"atMs": 1000, "value": 1.0}]}]}
```

## 4. Page transitions

`routeOptions.transition` picks a built-in transition ([navigation](../reference/navigation.md)).
`"custom"` with `"timeline"` plays one of the page's timelines whose `scope` is `route`
(`NAV-010`). The compiler checks that it animates only what a page transition can:

```json
"routeOptions": {"transition": "custom", "timeline": "slideIn"},
"animations": [{"name": "slideIn", "scope": "route", "durationMs": 300,
  "tracks": [{"prop": "slideX", "keyframes": [{"atMs": 0, "value": 1.0}, {"atMs": 300, "value": 0.0, "curve": "easeOut"}]}]}]
```

## 5. Reduce motion

Every animation honours the platform's reduce-motion setting (`ANI-007`). Its
`reduceMotion` says how: `skip` jumps to the end, `shorten` plays at a quarter of the
duration, and `ignore` plays as declared, for motion that carries meaning. The compiler
warns about animations that are expensive, such as animating the layout of a large
subtree, and suggests a cheaper prop (`ANI-008`).

## 6. Lottie and Rive

Lottie and Rive files play in native slots from two optional packages (`ANI-005`):
`plux_lottie` and `plux_rive`. Add them with `plux init --packages plux_lottie,plux_rive`,
or register `PluxLottie.slots` and `PluxRive.slots` in `PluxConfig.nativeSlots`. A Rive
state machine's inputs bind to Plux state. Files are assets of the release, checked at
publish.
