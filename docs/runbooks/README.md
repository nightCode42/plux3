# Runbooks

One runbook per alert and per failure mode introduced in a phase (DoD-9, `OBS-004`). The first runbooks are written in **P2**.

| Runbook | Covers | Phase |
|---|---|---|
| [Server](server.md) | `plux-server`: dependencies, publishing and signing, manifests and devices, data, upgrades | P2 |
| [Runtime](runtime.md) | `plux_flutter` on devices: sync, releases, rendering, telemetry, host builds | P3 |
| [Key ceremony](key-ceremony.md) | The offline root of the update metadata: first root (2 of 3), rotation, lost or compromised keys (`SEC-121`) | P6 |
