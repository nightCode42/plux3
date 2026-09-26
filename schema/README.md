# schema

Language-neutral contracts shared by the compiler, the runtime and Studio. Everything here is the single source for generated Go, Dart and TypeScript code (`SCH-001`, `WGT-002`, `BND-001`).

| Path | Contents | Consumers |
|---|---|---|
| `json/` | JSON Schema 2020-12 for documents (§7, ADR-0025) and for widget descriptors | Go validator; generated Go, Dart and TypeScript types |
| `widgets/` | Widget descriptors (Layers 1 and 2), value types, enums, the permanent-ID lock, the Flutter API snapshot and the generated coverage table (§8, ADR-0010) | Compiler registry, runtime decoders, Studio, AI grounding |
| `actions/` | Built-in action descriptors with typed inputs and outputs (Appendix D) | Compiler, runtime, Studio |
| `pxl/` | PXL standard-library signatures and currency data (ADR-0009) | Go checker and evaluator, Dart VM |
| `fbs/` | FlatBuffers IDL for bundle sections (§9, ADR-0002) | Generated Go and Dart code, verifier layout tables |
| `limits.json` | The limits registry: key, unit, default, hard maximum, scopes (§30.4) | Compiler, server, runtime, Studio |
| `errors.json` | The error catalogue exported from `backend/internal/plxerr` (generated, ADR-0018) | Dart and TypeScript generators |
| `testdata/` | Cross-language conformance vectors: canonicalisation, PXL, documents → bundles and diagnostics (`QA-003`) | Go, Dart and TypeScript tests |

Everything generated from these files is committed and regenerated only with `make gen` (`CI-003`).

Changes here are contract changes: read [AGENTS.md](../AGENTS.md) §7 before editing.
