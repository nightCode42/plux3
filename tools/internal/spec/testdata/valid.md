# Sample Specification

1. [Conventions](#1-conventions)
2. [Sync — rules](#2-sync--rules)

## 1. Conventions

| Area | Meaning | Section |
|---|---|---|
| `SYN` | Device sync | §2 |
| `QA` | Verification | §2 |

## 2. Sync — rules

> **SYN-000** `P3` **MUST** — Formats evolve additively only.

| ID | Phase | Priority | Requirement | Status |
|---|---|---|---|---|
| `SYN-001` | P3 | MUST | The runtime **MUST** sync all plugins; see `SYN-000`. | DONE |
| `SYN-002` | P3 | SHOULD | Manual sync **SHOULD** exist. | SPEC |
| `QA-001` | P0 | — | Withdrawn: superseded by `SYN-001`. | WITHDRAWN |

| ID | Phase | Priority | Metric | Target | Status |
|---|---|---|---|---|---|
| `QA-002` | P1 | MUST | Latency | ≤ 50 ms | WIP |

```text
`XYZ-999` inside a code fence is ignored.
```
