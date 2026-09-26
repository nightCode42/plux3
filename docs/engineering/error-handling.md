# Error Handling

How errors are modelled, propagated and reported across Plux. The binding decision is ADR-0018 (planned for P1); this document states the model it will implement and the rules that apply from P0.

---

## 1. One model, three languages

Every component — server, CLI, runtime, Studio — speaks the same error vocabulary:

| Field | Meaning |
|---|---|
| **Code** | A stable `PLX-NNNN` code from spec Appendix F, grouped by range (schema, compiler, sync, runtime, actions, security, functions, governance, tooling). |
| **Reason** | A stable `UPPER_SNAKE_CASE` identifier, e.g. `MANIFEST_SIGNATURE_INVALID`. Programs branch on the reason, never on the message. |
| **Message** | A human-readable explanation for developers. |
| **Fix** | A suggested fix and a documentation link where one exists (`DX-003`). |
| **Details** | Structured context: JSON path, requirement ID, retry information. Never secrets or sensitive values. |

## 2. Rules from P0

- Errors carry context when they cross a boundary: wrap in Go (`fmt.Errorf("pkg.Func: %w", err)`), rethrow with a typed cause in Dart and TypeScript.
- Never compare error message strings; branch on types, reasons or codes.
- Log an error once, at the edge that handles it — never log and return.
- Messages never contain secrets, tokens, keys or fields tagged `sensitive`.
- Command-line tools use exit codes consistently: `0` success, `1` a check failed, `2` usage or I/O error.

## 3. At the edges (from P1 and P2)

- Server: domain errors carry a registered reason and are translated only in the ConnectRPC handlers into a Connect code, `google.rpc.ErrorInfo` (reason, domain `plux.dev`, metadata) and the Plux code (`SRV-006`).
- Compiler: diagnostics are structured — code, severity, JSON path, range, message, fix (`CMP-004`).
- Runtime: errors are typed (`network`, `http`, `timeout`, `validation`, `function`, `permission`, `cancelled`, `custom`) and routed to `onError` handlers (`ACT-020`); failures render fallbacks and are reported, never thrown into the host app (`RT-020`).

## 4. The catalogue

The catalogue of codes and reasons is generated from the registry in code and published in the documentation; a test pins the published catalogue to the registry, so they cannot drift.
