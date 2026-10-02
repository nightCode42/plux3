# apps

Flutter host applications. Each Flutter project is a separate pub workspace member.

| App | Purpose | Arrives |
|---|---|---|
| [`starter/`](starter/README.md) | Minimal host app used by the quick start (`DX-001`), `make dev` and the end-to-end tests (`QA-006`, `QA-010`) | P3 |
| [`add_to_app/`](add_to_app/README.md) | A Flutter module with the runtime, embedded in native Kotlin and Swift apps, and their UI tests (`HST-033`) | P4 |
| `plux_bank/` | Reference financial host app (`DX-004`) | P5 |
| `plux_express/` | Reference delivery host app (`DX-004`) | P5 |
| `plux_dev/` | Plux Dev companion app for live device preview (`DEV-001`) | P10 |

See [docs/requirements.md](../docs/requirements.md) §33 for the full repository layout.
