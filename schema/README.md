# schema

Language-neutral contracts shared by the compiler, the runtime and Studio. Everything here is the single source for generated Go, Dart and TypeScript code (`SCH-001`, `WGT-002`, `BND-001`).

| Directory | Contents | Arrives |
|---|---|---|
| `json/` | JSON Schema 2020-12 for documents (§7) | P1 |
| `widgets/` | Widget descriptors for Layers 1 and 2 (§8) | P1 |
| `fbs/` | FlatBuffers IDL for bundles and manifests (§9, ADR-0002) | P1 |
| `testdata/` | Cross-language conformance vectors (`QA-003`) | P1 |

Changes here are contract changes: read [AGENTS.md](../AGENTS.md) §7 before editing.
