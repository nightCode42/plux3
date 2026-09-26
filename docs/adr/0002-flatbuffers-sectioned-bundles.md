# 0002. FlatBuffers sections in a hashed container for bundles

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `BND-001`–`BND-018`, `CMP-005`, `CMP-020`, `CMP-021`, `CMP-041`, `SEC-054`, `QA-004`

## Context and problem

The compiler turns documents into bundles that devices download, verify and read at every page build. A bundle must be read **without parsing or copying** (`BND-003`, `BND-015`), so that a 300-node page builds in ≤ 8 ms on a mid-tier phone (`NFR-003`). It must be **patchable per page** so that a one-word change costs ≤ 2 KiB on the wire (`NFR-005`). It must be **verifiable before use** (`SEC-052`, `BND-006`) and **evolve additively for years** once P3 is tagged (`BND-000`). It must carry **data only** (`SEC-054`). Which binary format, and which container around it, satisfies all of these?

## Decision drivers

- Zero-copy reads from memory-mapped files in Dart, with generated accessors.
- Forward and backward compatibility without version negotiation: old runtimes skip what they do not know (`BND-018`).
- Independent sections, so a delta replaces one section and each section is hashed and verified on its own (`BND-012`, `BND-014`).
- Deterministic output: byte-identical bundles for the same input on every platform (`CMP-002`).
- A verifier for untrusted input in Go now and in Dart in P3; neither FlatBuffers runtime ships one.
- Small, well-maintained dependencies with permissive licences.

## Considered options

1. **FlatBuffers sections inside a fixed, hashed container.**
2. Protocol Buffers sections inside the same container.
3. Cap'n Proto.
4. A custom binary format.
5. Compressed JSON.

## Decision

Chosen option: **1**. Each section is an independent FlatBuffers buffer with its own root type and file identifier, inside the container of spec Appendix B.1.

### Container

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | `magic` = ASCII `PLUX` |
| 4 | 2 | `container_version` = 1, uint16 little-endian |
| 6 | 2 | `bundle_kind`: 1 plugin, 2 app, 3 development |
| 8 | 4 | `flags`: bit 0 encrypted (reserved until `SEC-053`), bit 1 has source map; other bits zero |
| 12 | 4 | `section_count`, uint32 |
| 16 | 32 | `header_hash` = SHA-256 of bytes 0–15 followed by the section directory |
| 48 | 72 × n | section directory |
| … | … | section payloads, each starting at an 8-byte-aligned offset, gaps zero-filled |

A directory entry is **72 bytes**: section ID (16), kind (uint16), reserved (2, zero), offset (uint64), length (uint64), SHA-256 of the payload (32), reserved (4, zero). The specification's earlier figure of 64 bytes did not match its own field list; the maintainer chose to keep every field and correct the size (spec 1.1.2). All integers are little-endian.

- **Bundle hash** (`BND-005`): the `header_hash`. It commits to the header fields and to every section hash through the directory, so signing it (P2, ADR-0004) signs the whole bundle.
- **Section IDs**: the document UUID for `page` and `component` sections; the plugin (or app) UUID for sections that exist once per bundle; the BCP 47 tag, ASCII, NUL-padded, for `l10n` (tags longer than 16 bytes are rejected).
- **Order**: entries and payloads are sorted by `(kind, id)`, so the layout is a function of content only.

### Section kinds

| Code | Kind | File identifier | One per |
|---|---|---|---|
| 1 | `meta` | `PXMT` | bundle |
| 2 | `page` | `PXPG` | page |
| 3 | `component` | `PXCO` | component |
| 4 | `actions` | `PXAC` | plugin |
| 5 | `pxl` | `PXEX` | plugin |
| 6 | `styles` | `PXST` | plugin |
| 7 | `strings` | `PXSG` | plugin |
| 8 | `l10n` | `PXLN` | locale |
| 9 | `timelines` | `PXTL` | plugin |
| 10 | `schemas` | `PXSC` | plugin |
| 11 | `assets-index` | `PXAS` | plugin |
| 12 | `wasm` | `PXWM` | plugin (P7) |
| 13 | `sourcemap` | `PXSM` | development bundle |

`schemas` holds state, data-source and local-collection schemas; it is the section `BND-004` called `state-schema` (renamed in spec 1.1.2 to match Appendix B.2). Unknown kinds are skipped unless a `required_features` entry names them (`BND-018`).

### Encoding principles

- **Permanent numeric IDs** (`BND-011`): widget types, props, events, slots, actions, enum values and value-type fields are encoded by IDs assigned in the registries of ADR-0010 and never reused.
- **Flat node arrays** (`BND-015`): a page or component section holds `nodes: [Node]`; children and slot fills are `uint32` indices into that array. All counts and indices are 32-bit (`BND-013`).
- **Typed values** (`BND-015`, `SCH-010`): a prop is `{id: uint32, value: Value}`. `Value` is one table with a `kind` byte and one field per representation — integer (int, bool, date as days since 1970-01-01, date-time as microseconds since the epoch plus an offset in minutes, duration in microseconds, colour as ARGB, enum value ID), double, string-table index, decimal (unscaled two's-complement bytes and scale), currency, list, map entries — rather than a FlatBuffers union. One table keeps reads to a single indirection, is supported identically by the Go and Dart generators, and is simple to verify.
- **References across sections** (`BND-014`) are stable IDs only: document UUIDs as a 128-bit struct, and 64-bit content-addressed IDs (the first 8 bytes of SHA-256 of the canonical encoding) for PXL programs and style objects. A collision is a compile error. Nothing points at an offset in another section.
- **Strings** (`CMP-020`): `page` and `component` sections carry their own string tables, so editing one page changes one section; the `strings` section is the table shared by the plugin-wide sections.
- **Styles** (`CMP-020`): object-typed prop values (`EdgeInsets`, `TextStyle`, `BoxDecoration`, …) are deduplicated into the `styles` section and referenced by style ID.
- **Components** (`CMP-021`, `BND-017`): a definition is compiled once into its `component` section; an instance is a node referring to the component ID and version, with prop overrides and slot fills.
- **Variants** (`BND-016`): responsive, platform, experiment and locale variants are override layers — a condition plus prop overrides — attached to a base node, never duplicated subtrees.
- **Required features** (`BND-008`): the `meta` section lists feature strings — `pxl.v1`, `widget.<Type>.v<revision>`, `type.<Name>.v<revision>` and `enum.<Name>.v<revision>` for registry revisions (ADR-0010), `pxl.<group>.v1` for standard-library groups evaluated in later phases, `section.<kind>.v1` for new mandatory sections. A runtime refuses a bundle naming a feature it does not support (`PLX-3010`).
- **Header metadata** (`CMP-005`): the compiler version, the document schema version and the required features are recorded in the `meta` section, whose hash the header commits to.
- **Source maps** (`CMP-041`): development bundles (kind 3) carry a `sourcemap` section and flag bit 1; release bundles never do — the compiler returns the source map separately for server-side symbolication.
- **Data only** (`SEC-054`, `BND-009`): the only executable content is PXL bytecode, action graphs and, from P7, WebAssembly modules for the device interpreter. The reader rejects any payload kind that is not in this list.

### Determinism

FlatBuffers output depends only on the order of builder calls. The encoder therefore visits everything in a defined order — document order for nodes, ascending ID for props, events and slots, sorted keys for maps, sorted IDs for content-addressed objects — never Go map order, and never reads the clock or the environment (`CMP-002`). Golden bundles in `schema/testdata/` and a CI job on Linux, macOS and Windows prove byte-identical output.

### Verification

Neither the Go nor the Dart FlatBuffers runtime includes a verifier. `make gen` asks `flatc` for the binary schema (`.bfbs`) of every section and turns it into **layout tables** — for each table, the byte size and kind of every field. A small interpreter in each language walks a buffer against those tables and checks every offset, size, alignment, vtable, string (in bounds, NUL-terminated, valid UTF-8), vector length and nesting depth before any accessor runs, under limits from the registry (`LIM-001`). The Go verifier lands in P1 and is fuzzed (`QA-004`); the Dart verifier reuses the same tables in P3 (`BND-006`).

### Transport

Bundles are stored uncompressed so that devices can memory-map them (`BND-007`); for transport they are compressed with zstd. Decompression is bounded by the declared size and the limits registry, so a compressed bomb is rejected before allocation.

### Tooling and dependencies

| Component | Version | Licence | Purpose |
|---|---|---|---|
| `flatc` | v25.9.23 (tag commit `187240970746d00bbd26b0f5873ed54d2477f9f3`) | Apache-2.0 | Code generation; built from source at the pinned commit (`make install-flatc`), cached in CI |
| `github.com/google/flatbuffers` (Go) | v25.9.23 | Apache-2.0 | Builders and accessors in the compiler |
| `flat_buffers` (Dart) | 25.9.23 | Apache-2.0 | Accessors in `plux_flutter` |
| `github.com/klauspost/compress` (Go) | v1.20.1 | BSD-3-Clause, Apache-2.0 | zstd transport compression; also the basis of ADR-0003 patching |

Generated Go and Dart code is committed and checked by `make gen-check` (`BND-001`, `CI-003`).

**Schema files.** Every type is declared in one file, `schema/fbs/bundle.fbs`, in one namespace; each section kind's root type and file identifier are declared in `schema/fbs/sections/<kind>.fbs`, which includes it. Go and Dart accessors are generated from `bundle.fbs` — one package, one library — and the binary schema of each section from its root file. Separate files per section with shared types in an included file would be the obvious layout, but flatc's Dart generator (v25.9.23) refers to types from an included file of the same namespace without the import prefix it declares, so the generated library does not compile. Because the generated Go code then has no per-type file identifiers, `bundle.Finish` writes the identifier of each section kind.

## Consequences

- **Positive:** pages are read in place with generated accessors; one edited page is one changed section; each section is hashed, verified and patched on its own; additive evolution follows FlatBuffers' field rules; output is deterministic by construction.
- **Negative:** we own a verifier in two languages, and the layout-table generator must track `flatc`'s binary-schema format; FlatBuffers is harder to inspect by hand than JSON, so development tooling must decode bundles (`plux build --json`, Studio inspect).
- **Follow-up:** ADR-0003 (deltas), ADR-0004 (signing and the manifest), the Dart verifier in P3, the reference document `docs/reference/bundle-format.md`.

## Options in detail

### Option 1 — FlatBuffers sections in a container

Zero-copy access, mature code generation for Go and Dart, schema evolution rules that match `BND-000`. The container adds what FlatBuffers lacks: per-section hashing and addressing for deltas.

### Option 2 — Protocol Buffers

Excellent tooling and evolution rules, but messages must be parsed into objects before use: allocation and CPU proportional to page size on every open, contrary to `BND-015` and `NFR-003`.

### Option 3 — Cap'n Proto

Zero-copy with a built-in verifier, but the Dart implementation is unmaintained and Go support is third-party. Too much risk for the core format.

### Option 4 — Custom binary format

Full control, but code generation, evolution rules, tooling and verification would all be ours to build and maintain for no gain over FlatBuffers.

### Option 5 — Compressed JSON

Simple and debuggable, but requires parsing and allocation on every read, and makes typed, patchable sections harder to guarantee.
