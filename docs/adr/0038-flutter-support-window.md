# 0038. Flutter support window: from 3.47 on, the latest stable and the previous one

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `RT-002`, `RT-001`

## Context and problem

`RT-002` asked for the latest stable Flutter and the previous stable. When P3 closes the
latest is 3.47.5 and the previous 3.44.9, and 3.44 cannot build the runtime: its SDK pins
the `meta` package at 1.18.0, while the build-hook stack the runtime's native code needs
(ADR-0030: `hooks` 2.2.0 through `record_use` 1.0 or later, `native_toolchain_c` 0.19.3)
requires `meta` 1.19 or later, which Flutter first ships in 3.47. Supporting 3.44 would
mean downgrading approved dependencies and lowering SDK floors.

## Decision drivers

- No dependency is downgraded to reach an older SDK.
- Users get a predictable window of supported Flutter releases.
- What is promised is tested in CI.

## Considered options

1. Downgrade the hook stack until 3.44 resolves, and test on it.
2. Start the window at 3.47: support the latest stable and the previous one, never a stable
   before 3.47.
3. Support the latest stable only.

## Decision

Chosen option: **2**, set by the maintainer. `RT-002` now reads: on the latest stable
Flutter and the previous stable, from Flutter 3.47 on. Today that is 3.47 alone; when the
next stable is released, 3.47 becomes the previous stable and both are supported, and from
then on every new stable joins the window as the oldest leaves it.

## Consequences

- **Positive:** no downgrade; the window grows into its full width with the next release.
- **Negative:** apps on Flutter 3.44 or earlier cannot use `plux_flutter`.
- **Follow-up:** when the next stable is released, CI gains a job that builds and tests the
  runtime on the previous stable, and the Flutter pin moves (`ci.md`); `RT-002` stays `WIP`
  until that job and API 24 coverage exist.

## Options in detail

### Option 1

Meets the old wording, at the cost of approved dependency versions and their fixes; the
hook API would have to be re-verified against the older releases.

### Option 2

As decided.

### Option 3

Simplest, but forces every user to upgrade Flutter the day a stable is released.
