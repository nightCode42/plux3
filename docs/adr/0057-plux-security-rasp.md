# 0057. `plux_security`: runtime application self-protection

- **Status:** Accepted (maintainer, 2026-10-07, P6 S0; P6 plan §2.1 B18)
- **Date:** 2026-10-07
- **Requirements:** `SEC-070`, `SEC-071`, `SEC-007`, `SEC-161`

## Context and problem

MASVS-RESILIENCE asks the app to detect hostile environments. `SEC-070` lists the
detections; `SEC-071` the responses and their effect on the assurance level.

## Decision drivers

- Plux's own code, no third-party RASP SDK (B18); optional package, no cost when absent.
- Detections are signals, not proofs: the server lowers assurance; the device never raises it.
- Nothing personal is reported.

## Considered options

1. **An optional package `plux_security` with native detectors on Android and iOS, a
   policy engine in Dart, and a `rasp_detection` telemetry event.**
2. A commercial RASP SDK.
3. Detections inside `plux_flutter`.

## Decision

Chosen option: **1**.

- **Detections:** root/jailbreak (files, properties, writable system paths, `su`,
  sandbox escape), hooking frameworks (Frida ports, named pipes and memory maps; Xposed,
  LSPosed, Substrate classes and libraries), debugger (`TracerPid`, `P_TRACED`), emulator
  and simulator, repackaging (signing certificate digest vs `PluxConfig`), installer
  source, screen mirroring and recording, overlays, accessibility services with suspicious
  capabilities. Each detector runs off the UI isolate, at start and on a timer.
- **Responses** per detection kind from the security configuration (`report`, `warn`,
  `degrade`, `block`; `raspRootHookingResponse` sets root and hooking): `degrade` disables
  pages and functions that require `AL3`; `block` shows the host's blocking screen.
- **Reporting:** the `rasp_detection` event (`necessary` category, kind and severity only)
  goes to the server through a DPoP-protected call; the server lowers the device's level
  at once (`PLX-6100` when a request is then refused).
- `plux_flutter` exposes a detector interface; without `plux_security` nothing is
  detected and `AL3` is never reached.

## Consequences

- **Positive:** auditable, owned code; no SDK licence; size only when used.
- **Negative:** detection is an arms race; signatures need maintenance.
- **Follow-up:** S9; real-device evidence in S11 (B3).

## Options in detail

### Option 2: a commercial SDK

Licence cost, closed code inside a security-critical path, and an extra vendor.

### Option 3: inside the runtime

Every app would pay the size and permissions, including apps that do not need it.
