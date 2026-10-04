# 0042. The ten-minute quick start is measured in P10

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `HST-034`, `DX-001`

## Context and problem

`HST-034` and `DX-001` are one `MUST`, seen from the host and from the documentation. A
developer new to Plux gets from `flutter create` to a published Plux page on a device in
at most ten minutes, by following the quick-start guide. Recorded usability sessions
verify it. Spec 1.1.8 tags both P4.

The time depends mostly on tooling that later phases build or change:

- P10 brings the Dev app, QR pairing and the CLI's log and trace viewer, which shorten
  the path from a project to a page on a device;
- P5–P9 change what a first page involves: state, data, security and governance.

The maintainer decided (P4 plan D9):

- the quick start is measured after the maintainer has tested the project by hand and
  given feedback;
- the chosen phase is P10.

A change to when a `MUST` is delivered needs an ADR (spec, Document Control).

## Decision drivers

- The requirement measures the product as developers will meet it, not an intermediate
  state.
- Usability sessions are costly. They are run once, against the tooling that shapes the
  result.
- The requirement's text and its ten-minute bound stay unchanged.

## Considered options

1. **Keep P4.** Write the guide and run the sessions now.
2. **Move to P10**, the developer-experience phase.
3. **Move to P11**, with Studio.

## Decision

Chosen option: **2, P10.**

- Option 1 would measure a path that P5–P10 change, so the sessions would have to be run
  again.
- Option 3 ties a host-integration measure to Studio, which the quick start does not need.

Spec 1.2.0 re-tags `HST-034` and `DX-001` to P10, and Appendix K's counts change with
them: P4 has 29 `MUST`s and P10 gains two. The requirement text is unchanged.

P4 still delivers what the quick start will rely on, each with its own requirement:

- `plux init` (`HST-032`);
- `plux codegen` (`HST-030`);
- `plux create` (`GEN-001`);
- the routing and host guides (`DX-002`).

P10's plan writes the guide against the Dev app and QR pairing, and times it in recorded
sessions (P4 plan §9.4).

## Consequences

- **Positive.** The ten-minute claim is measured once, on the tooling it depends on.
- **Negative.** Until P10, the project makes no verified claim about time to a first page.
- **Follow-up.** P10's plan includes `HST-034` and `DX-001`, and starts them after the
  maintainer's hands-on feedback.

## Options in detail

### Option 1: keep P4

The requirement would be met in its original phase. But the guide would describe a set-up
without the Dev app and pairing, and every later phase would invalidate the timing.

### Option 3: P11

The quick start would come with the rest of the developer surface. But it is a host and
CLI path, and does not need Studio. Waiting for P11 delays a measure that P10's tooling
already enables.
