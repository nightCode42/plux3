# schema

Language-neutral contracts shared by the compiler, the runtime and Studio. Everything here is the single source for generated Go, Dart and TypeScript code (`SCH-001`, `WGT-002`, `BND-001`).

| Path | Contents | Consumers |
|---|---|---|
| `json/` | JSON Schema 2020-12 for documents (§7, ADR-0025); `json/registry/` for widget descriptors, value types, enums and actions | Go validator; generated Go, Dart and TypeScript types |
| `widgets/` | Widget descriptors (Layers 1 and 2), value types, enums, the permanent-ID lock, the Flutter API snapshot and the generated coverage table (§8, ADR-0010) | Compiler registry, runtime decoders, Studio, AI grounding |
| `actions/` | Built-in action descriptors with typed inputs and outputs (Appendix D) | Compiler, runtime, Studio |
| `pxl/` | PXL bytecode (opcodes, constant tags, error kinds), standard-library signatures and ISO 4217 currencies (ADR-0009) | Go checker and VM, Dart VM (generated tables) |
| `fbs/` | FlatBuffers IDL for bundle sections: every type in `bundle.fbs`, the root type and file identifier of each section kind in `sections/` (§9, ADR-0002, [bundle-format.md](../docs/reference/bundle-format.md)) | Generated Go and Dart accessors, verifier layout tables |
| `limits.json` | The limits registry: key, unit, default, hard maximum, scopes (§30.4) | Compiler, server, runtime, Studio |
| `errors.json` | The error catalogue exported from `backend/internal/plxerr` (generated, ADR-0018) | Dart and TypeScript generators |
| `testdata/` | Cross-language conformance vectors: canonicalisation, PXL (`testdata/pxl/`, format in [pxl.md](../docs/reference/pxl.md#9-conformance-vectors)), bundles written by Go and read by Dart (`testdata/bundles/`), conformance projects (`testdata/documents/`) compiled to golden bundles ([compiler.md](../docs/reference/compiler.md#7-conformance-projects), `QA-003`) | Go, Dart and TypeScript tests |

Everything generated from these files is committed and regenerated only with `make gen` (`CI-003`).

## Widget and action registries

- **A widget, value type, enum or action** is one JSON file, named after it, with its permanent ID written in the file. Take the next free ID of its kind; never reuse one, even of a removed entry (`BND-011`). `make gen` checks the registry, records new IDs in `widgets/ids.lock.json` and regenerates the Go, Dart and TypeScript registries, [docs/reference/widgets.md](../docs/reference/widgets.md), [docs/reference/actions.md](../docs/reference/actions.md) and [widgets/COVERAGE.md](widgets/COVERAGE.md).
- **A member** (prop, event, slot, field, value, input) takes the next free ID within its entry. A member added after an entry's first release raises the entry's `revision` and records it (`WGT-004`).
- **Flutter counterparts:** every constructor parameter of a mirrored Flutter class, and every value of a mirrored enum, is covered by a member (`"flutter": "<parameter>"`) or listed in `excluded` with a reason (`WGT-003`). `widgets/flutter-api.json` is the snapshot of the pinned Flutter SDK; after a Flutter upgrade, or when a new counterpart is named, run `make widgets-api gen` and review the coverage table.

Changes here are contract changes: read [AGENTS.md](../AGENTS.md) §7 before editing.
