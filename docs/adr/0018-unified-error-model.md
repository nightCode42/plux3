# 0018. Unified error model with registered codes and reasons

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `DX-003`, `CMP-004`, `SCH-040`, `SRV-006`, `ACT-020`

## Context and problem

Errors in Plux cross many boundaries: the compiler reports problems in documents, the CLI prints them, Studio underlines them, the server returns them over ConnectRPC, the runtime reports them from devices, and documentation explains them. If each component invents its own errors, users see inconsistent messages, programs branch on text, and documentation drifts. `DX-003` requires a stable code, a clear message, the likely cause, a suggested fix and a documentation link for **every** error and diagnostic. How is that model defined once and enforced everywhere?

## Decision drivers

- Stable identifiers that programs can branch on, independent of wording.
- Diagnostics precise enough for editors: document, JSON path, character range, machine-applicable fix (`CMP-004`, `SCH-040`).
- One registry, with the published catalogue generated from it and pinned by a test.
- Nothing sensitive in any error (`SEC-092`).
- Usable from Go now and from Dart and TypeScript later without re-typing the catalogue.

## Considered options

1. **A Go registry of codes and reasons in `backend/internal/plxerr`, exported as generated documentation and a machine-readable catalogue for other languages.**
2. A language-neutral data file as the source, generated into every language.
3. Plain Go errors with messages, documented by hand.

## Decision

Chosen option: **1**, as spec Appendix F prescribes ("generated from the registry in `backend/internal/plxerr`").

### Vocabulary

| Concept | Form | Example |
|---|---|---|
| Code | `PLX-NNNN`, grouped by range (Appendix F) | `PLX-1001` |
| Reason | `UPPER_SNAKE_CASE`, one per code | `UNKNOWN_PROPERTY` |
| Severity | `error`, `warning`, `info` | `error` |
| Definition | code, reason, default severity, title, cause, fix | registered once |

Codes and reasons are permanent: a retired code keeps its entry, marked deprecated, and is never reassigned.

### Diagnostics

A diagnostic is a finding about a document:

| Field | Meaning |
|---|---|
| `code`, `reason`, `severity` | From the definition; severity may be raised by policy, never lowered below the definition's. |
| `file` | The document's path in the project layout (`SCH-006`). |
| `path` | JSON Pointer (RFC 6901) to the offending value. |
| `range` | For PXL: start and end offsets, in Unicode code points, inside the expression string (`CMP-004`). |
| `message` | What is wrong, with the specific values involved. |
| `cause`, `fix` | From the definition, specialised where useful. |
| `patch` | Optional machine-applicable fix as JSON Patch (RFC 6902) operations on the document. |
| `docURL` | The code's entry in the published catalogue. |
| `related` | Other locations involved, e.g. the first declaration of a duplicate key. |

Diagnostics are sorted by file, path, range and code, so output is deterministic. The CLI prints them as `file#path: severity PLX-NNNN message` followed by the fix, and as JSON with `--json`.

### Errors

`plxerr.Error` is the Go error type for failures that are not document findings: it carries a code, reason, message and structured details, wraps its cause (`errors.Is`/`errors.As` work), and compares by code. The server translates it into a Connect code and `google.rpc.ErrorInfo` only in handlers (`SRV-006`, P2).

### Catalogue

`go generate` in `plxerr` writes `docs/reference/errors.md` — one anchored entry per code with its reason, severity, cause and fix — and `schema/errors.json` for the Dart and TypeScript generators. A test regenerates both and fails on any difference, so the published catalogue cannot drift from the registry. `docURL` points at the entry's anchor.

### Rules

- Messages never contain secrets, tokens, keys or values of fields tagged `sensitive`; they name paths and types, not sensitive contents.
- Programs branch on codes and reasons, never on messages.
- A registry test enforces unique codes and reasons, codes inside their area's range, a non-empty cause and fix for every definition, and British spelling in the text.

## Consequences

- **Positive:** every error and diagnostic has the same shape in every tool; documentation is always complete; editors can apply fixes automatically.
- **Negative:** every new failure mode needs a registry entry and a catalogue update in the same change.
- **Follow-up:** Dart and TypeScript error types generated from `schema/errors.json` (P3, P11); Connect translation (P2).

## Options in detail

### Option 1 — Go registry, generated catalogue

Follows the specification, keeps the registry next to the code that raises most errors, and still gives other languages a machine-readable source.

### Option 2 — neutral data file

Symmetric across languages, but the Go code would depend on generated constants for every error it raises, and the specification already places the registry in `plxerr`.

### Option 3 — plain errors, hand-written docs

No upfront cost, but codes would be ad hoc, messages would become the API, and documentation would drift — contrary to `DX-003`.
