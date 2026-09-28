# Bundle Format Reference

How a compiled plugin or app is stored and shipped: the container, its sections, how values are encoded, and what a reader checks before it uses anything. The requirements are spec §9.5–§9.6 (`BND-*`) and Appendix B; the decisions are [ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md). The Go implementation is `backend/internal/bundle`; the schemas are in [`schema/fbs/`](../../schema/fbs).

## 1. Container

A bundle (`.pxb`) is a fixed header, a section directory and the sections. All integers are little-endian.

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | `PLUX` |
| 4 | 2 | container version, `1` |
| 6 | 2 | bundle kind: `1` plugin, `2` app, `3` development |
| 8 | 4 | flags: bit 0 encrypted (reserved until `SEC-053`), bit 1 has a source map; other bits zero |
| 12 | 4 | section count *n* |
| 16 | 32 | header hash: SHA-256 of bytes 0–15 followed by the directory |
| 48 | 72 × *n* | directory |
| … | … | sections |

A directory entry is the section ID (16 bytes), its kind (2), two zero bytes, its offset (8) and length (8), the SHA-256 of the section (32) and four zero bytes. Entries are sorted by kind, then ID, with no duplicates; sections follow in the same order, each at an offset divisible by 8, separated only by zero bytes, with nothing after the last one.

The **bundle hash** is the header hash (`BND-005`). It covers the header fields and, through the directory, the hash of every section, so signing it signs the whole bundle (ADR-0004, P2). The container layout is a function of the sections alone: encoding the same sections in any order gives the same bytes (`CMP-002`).

Section IDs are the document UUID for `page` and `component` sections, the plugin or app UUID for sections that exist once per bundle, and the BCP 47 tag, NUL-padded to 16 bytes, for `l10n` sections (`bundle.LocaleID`).

## 2. Sections

Each section is an independent FlatBuffers buffer with its own root type and file identifier (`BND-012`), so it can be hashed, verified, read and replaced by a delta on its own. Sections refer to each other only by stable IDs, never by offsets (`BND-014`).

| Code | Kind | Identifier | Root type | One per |
|---|---|---|---|---|
| 1 | `meta` | `PXMT` | `Meta` | bundle |
| 2 | `page` | `PXPG` | `Page` | page |
| 3 | `component` | `PXCO` | `Component` | component |
| 4 | `actions` | `PXAC` | `Actions` | plugin |
| 5 | `pxl` | `PXEX` | `Programs` | plugin |
| 6 | `styles` | `PXST` | `Styles` | plugin or app |
| 7 | `strings` | `PXSG` | `Strings` | plugin or app |
| 8 | `l10n` | `PXLN` | `Locale` | locale |
| 9 | `timelines` | `PXTL` | `Timelines` | plugin |
| 10 | `schemas` | `PXSC` | `Schemas` | plugin or app |
| 11 | `assets-index` | `PXAS` | `AssetIndex` | plugin or app |
| 12 | `wasm` | `PXWM` | `WasmModule` | plugin (from P7; requires `section.wasm.v1`) |
| 13 | `sourcemap` | `PXSM` | `SourceMap` | development bundle |

All types are declared in [`schema/fbs/bundle.fbs`](../../schema/fbs/bundle.fbs); each kind's root and identifier in `schema/fbs/sections/`. The schema evolves additively only (`BND-000`): fields are appended and never removed, renumbered or retyped.

- **meta** — identity, plugin version, compiler and document-schema versions, sorted required features and minimum runtime (`CMP-005`, `BND-008`); capabilities and limits; the pages of a plugin for cross-plugin navigation; for app bundles the plugins, locales, entry route, flags, native catalogue and security profile. Its own strings are inline, because it is read before any other section; the values it holds (flag defaults) index the bundle's strings section like every plugin-wide value.
- **page**, **component** — a flat node array whose element 0 is the root, with children and slot fills as indices into it (`BND-015`), and the section's own string table (`CMP-020`). A component instance is a node naming the component's UUID and version, whose prop, event and slot IDs are indices into the component's declared props (sorted by name), events and slots (in document order) (`CMP-021`, `BND-017`). A node placing a native slot of the catalogue (`SCH-032`) has neither a widget nor a component; its `native_slot` is the slot's type name in the string table, and its prop and event IDs are indices in the catalogue's order.
- **actions** — action graphs and flows; `steps[0]` is the entry, successors are step indices (−1 for none), and parallel and bounded iteration are actions whose named branches point at the steps they run.
- **pxl** — compiled programs sorted by ID; each is the program encoding of [pxl.md §8](pxl.md#8-bytecode), which carries its result type and read set (`CMP-023`).
- **styles** — deduplicated value-type objects sorted by ID (`CMP-020`), and the design tokens of an app with their `$type` and dark-mode values.
- **strings** — the table shared by the plugin-wide sections; index 0 is the empty string.
- **l10n**, **timelines**, **schemas**, **assets-index**, **wasm**, **sourcemap** — translations of one locale; animation timelines; declared types, state, data sources, collections, variables and user context; asset references with their SHA-256 and, for raster images, their WebP and AVIF variants at 1×, 2× and 3× (`CMP-030`), each with its media type, density, size in pixels, SHA-256 and bytes; a device-placed function module; node and step locations in the documents: a node by its page or component UUID and index, a step by its graph's UUID and index.

## 3. Nodes and values

A **node** is a widget, by its permanent ID (`BND-011`), or a component instance. It holds props, event handlers, children, slot fills, `visible`, semantics, a test ID, override layers and rendering hints (`CMP-024`) — a byte of `NodeHints` bits, stored as a plain `ubyte` because flatc's Dart generator reads a `bit_flags` field as one enum value and fails on combined flags. Props, action inputs and overrides are `{id, value}` pairs keyed by permanent IDs (`BND-015`). Responsive, platform, experiment and locale variants are **override layers** — a condition and prop overrides — over the base node, never duplicated subtrees (`BND-016`). An event handler names an action graph in the actions section by UUID, with its concurrency policy.

A **value** is one table: a `kind` and one field per representation, so a read is a single indirection and Go and Dart treat it identically.

| Kind | Encoding |
|---|---|
| `Bool`, `Int` | `i` (bool as 0 or 1) |
| `Double` | `d` |
| `String`, `Route`, `Token` | `s`, an index into the section's string table |
| `Decimal` | `unscaled`, two's-complement big-endian, and `scale` |
| `Money` | as decimal, and `s` the currency code |
| `Date` | `i`, days since 1970-01-01 |
| `DateTime` | `i`, microseconds since 1970-01-01T00:00Z, and `offset` in minutes |
| `Duration` | `i`, microseconds |
| `Color` | `i`, `0xAARRGGBB` |
| `Enum` | `i`, the permanent value ID; `s`, the member name, for enums declared in documents |
| `Asset`, `Translation` | `uuid`; a translation's arguments in `entries` |
| `List`, `Map`, `Object` | `items`; `entries` keyed by string index (maps, declared types) or permanent field ID (value types) |
| `Style`, `Expr` | `i`, the content-addressed ID in the styles or pxl section |

Content-addressed IDs of programs and styles are the first eight bytes of a SHA-256, read as a little-endian integer: of the program encoding for programs, and for styles of a canonical JSON form of the value that holds strings inline and programs by ID, so a style's ID does not depend on any string table. A collision is a compile error (`PLX-2202`). UUIDs are stored as two big-endian 64-bit halves.

## 4. Reading

`bundle.Read` checks, in order, and returns the first failure:

1. The header: magic, version, kind, flags (`PLX-3040`); encryption is not supported yet (`PLX-3043`); the size against `bundle.pluginSize` or, for app bundles, `release.appSize` (`PLX-1320`).
2. The header hash and the directory: order, reserved bytes, alignment, bounds, zero-filled gaps, no trailing bytes (`PLX-3040`).
3. Every section's SHA-256 (`PLX-3041`).
4. Every section of a known kind: its size limit (`bundle.pageSectionSize`, `bundle.deviceFunctionModuleSize`) and the **verifier** (`PLX-3042`). Sections of unknown kinds are skipped (`BND-018`) — a runtime that needs one refuses the bundle through its required feature — but are refused when they start with the signature of native code, a script, a Dart snapshot or a WebAssembly module (`PLX-1503`, `SEC-054`, `BND-009`).
5. The meta section: exactly one, of the header's bundle kind; a source map only in development bundles, matching the flag (`CMP-041`) — a development build makes every bundle of the app, the app bundle included, a development bundle; a wasm section only with `section.wasm.v1`; every required feature supported by the reader, otherwise `PLX-3010` naming the feature (`BND-008`).

Sections are returned as views of the input, never copied (`BND-003`).

**Verifier.** Neither FlatBuffers runtime ships a verifier, so `make gen` derives layout tables from each section's binary schema — for every table, each field's slot, kind, size, alignment and referenced table — and a small interpreter checks a buffer against them before any accessor runs: the file identifier, every offset and its alignment, every vtable and table, each present field within the buffer and aligned, required fields, strings (in bounds, NUL-terminated, valid UTF-8) and vector lengths, within `bundle.verifierDepth` nested tables and `bundle.verifierTables` tables and vectors (`LIM-001`). Like the upstream C++ verifier, it does not bound fields by the table size a vtable records: builders share one vtable between tables of different sizes, so a field is checked against the buffer. Tests show that every mutated buffer the verifier accepts can be read completely with the generated accessors, and the verifier and `Read` are fuzzed (`QA-004`). The Dart runtime reuses the same tables from P3 (`BND-006`, `SEC-052`); until then, the Dart `BundleContainer` checks only the container structure and serves tests.

## 5. Transport

Bundles are stored uncompressed so that they can be memory-mapped; for transport they are one zstd frame that declares its content size (`BND-007`). `bundle.Decompress` refuses a frame that declares no size or more than the caller's limit before it allocates, so a compression bomb costs nothing, and checks that the output has exactly the declared size (`PLX-3044`). Compression is deterministic.

## 6. Tooling

`make gen` runs the pinned `flatc` (built by `make install-flatc`) on `schema/fbs/bundle.fbs` for the Go package `backend/internal/bundle/fbs` and the Dart library `packages/plux_flutter/lib/src/bundle/fbs/bundle_fbs_generated.dart`, and on `schema/fbs/sections/*.fbs` for the binary schemas from which schemagen writes `backend/internal/bundle/layout_gen.go`. A section is finished with `bundle.Finish`, which writes its kind's file identifier. `schema/testdata/bundles/sample.pxb` is written by the Go tests (`-update`) and read by the Dart tests, so both languages agree on the format.
