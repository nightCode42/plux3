# 0025. Document schema toolchain: validation, canonicalisation and code generation

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `SCH-001`, `SCH-002`, `SCH-003`, `SCH-004`, `SCH-040`, `SCH-043`, `QA-002`, `CI-003`

## Context and problem

The document model is defined in JSON Schema 2020-12 and is the single source of truth (`SCH-001`). From it we need structural validation in Go with precise diagnostics (`SCH-040`), canonical bytes for hashing and diffing (`SCH-003`), and committed types for Go, Dart and TypeScript (`SCH-001`). We also need property-based tests (`QA-002`). Which tools do these jobs?

## Decision drivers

- Correct, complete JSON Schema 2020-12 validation, including `unevaluatedProperties` for `SCH-004`.
- Diagnostics with JSON paths and Plux codes (ADR-0018).
- Deterministic, idiomatic generated code in three languages, including `x-` extension preservation and discriminated unions.
- Few dependencies (dependencies.md §1); none that pulls another runtime (Node) into the build.
- Canonicalisation that matches RFC 8785 exactly, including number formatting.

## Considered options

1. **`santhosh-tekuri/jsonschema/v6` for validation; in-house RFC 8785 canonicalisation; an in-house generator (`tools/cmd/schemagen`) over a documented schema profile; `pgregory.net/rapid` for property tests.**
2. An in-house validator restricted to the profile.
3. quicktype (Node) or per-language generators (`go-jsonschema`, `json_serializable`, `json-schema-to-typescript`).
4. The reference canonicalisation library (`cyberphone/json-canonicalization`).

## Decision

Chosen option: **1**.

### Validation

`github.com/santhosh-tekuri/jsonschema/v6` (v6.0.3, Apache-2.0) implements drafts 4 to 2020-12, passes the official JSON-Schema-Test-Suite, and reports each error with its instance location and keyword. The schemas are embedded in the Go binary and compiled once. Errors map by keyword to registered diagnostics: unknown property → `PLX-1001`, missing required property → `PLX-1002`, wrong type → `PLX-1003`, and so on (ADR-0018). The maintainer chose the library over an in-house validator: a complete 2020-12 validator is a large and subtle surface, while this one is maintained, dependency-free and widely used.

### Schema profile

Documents under `schema/json/` use a documented subset of 2020-12 so that generated code stays idiomatic: objects with `properties`, `required` and `unevaluatedProperties: false`; `patternProperties` `^x-` for extensions (`SCH-004`); arrays; string-keyed maps (`additionalProperties` with a schema); string enums; `$ref` into `$defs`; `oneOf` either discriminated by a `const` property or by a single `$`-prefixed key (prop values: `$expr`, `$token`, `$t`, `$asset`); and raw values (`x-plux-raw: true`) that the compiler interprets using descriptors. `schemagen` rejects any schema outside the profile.

### Canonicalisation

RFC 8785 is implemented in `backend/internal/schema/jcs` with the standard library: property names sorted by UTF-16 code units, the ECMAScript number-to-string algorithm, minimal string escaping, no insignificant whitespace. It is tested against the RFC's examples and with property tests. Documents are canonicalised before hashing, diffing and storage. For Git export (`SCH-006`) the same ordering and number format are written with two-space indentation, so files diff line by line and hash identically after re-canonicalisation.

### Code generation

`tools/cmd/schemagen` uses only the standard library (tools policy) and writes:

| Output | Location |
|---|---|
| Go structs, enums and unions with `x-` extension maps and deterministic JSON marshalling | `backend/internal/schema/*_gen.go` |
| Dart immutable classes with `fromJson`/`toJson` and sealed unions | `packages/plux_flutter/lib/src/schema/*.g.dart` |
| TypeScript types and discriminated unions | `studio/packages/schema/src/*.gen.ts` |

It also generates the widget, action and limits registries (ADR-0010). Output is deterministic and committed; `make gen-check` fails when it is stale (`CI-003`). An in-house generator was preferred to quicktype because it needs no Node toolchain in the Go and Dart jobs, supports the profile's unions and extensions precisely, and produces code that follows the project's standards.

### Identifiers

UUIDv7 (RFC 9562) parsing, validation and generation live in `backend/internal/schema/uuid7` (standard library, clock and entropy injected). The compiler never generates identifiers; generation is used only when copying templates with fresh IDs (`SCH-031`).

### Property tests

`pgregory.net/rapid` (v1.3.0, MPL-2.0, test-only) generates inputs and shrinks failures to minimal cases. It is used for canonicalisation, compiler determinism and PXL laws (`QA-002`). `testing/quick` is frozen and does not shrink.

### Migrations

`schemaVersion` is required in every document (`SCH-000`). Migrations are Go functions registered per version step, applied in order to the parsed JSON before validation, deterministic and covered by golden tests from every released version to the current one (`SCH-043`). Version 1.0.0 is the first released schema.

## Consequences

- **Positive:** complete, standard validation with precise paths; canonical hashes that other implementations can reproduce; idiomatic generated code in three languages from one source; no Node dependency outside Studio.
- **Negative:** we maintain a code generator and must keep the schema within its profile; one more Go dependency to track.
- **Follow-up:** Dart and TypeScript validation where needed (Studio, P11) uses the same schemas.

## Options in detail

### Option 2 — in-house validator

No dependency, but full 2020-12 semantics (`unevaluatedProperties`, dynamic references, annotation collection) are hard to get right, and a subset validator would diverge from the schema standard that Studio and third parties will use.

### Option 3 — third-party generators

quicktype covers all three languages but requires Node in every job that runs `make gen`, and its Dart output relies on `dynamic` casts. Per-language generators produce three different interpretations of the schema, with uneven support for unions and extension properties.

### Option 4 — reference canonicalisation library

Correct, but a dependency for roughly two hundred lines of well-specified code that the RFC's test vectors fully cover.
