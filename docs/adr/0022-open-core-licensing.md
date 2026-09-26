# 0022. Open-core licensing: Apache-2.0 client side, AGPL-3.0 server and Studio, commercial enterprise edition

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `GOV-030`, `GOV-032`, `GOV-033`, `GOV-034`; spec §34.2

## Context and problem

Plux should be free to use and to study, so that developers adopt it and the engineering is visible in full. It should also be sustainable: organisations that need enterprise capabilities or cannot accept copyleft should be able to buy a commercial licence, and a hosted derivative should not be offered by a third party without contributing back. Code that ships inside customers' mobile apps must never impose copyleft obligations on those apps. How should the repository be licensed?

## Decision drivers

- Zero friction for the runtime inside third-party apps.
- The whole codebase stays public and readable.
- Protection of a future hosted offering against closed forks.
- A clear, auditable boundary between open and commercial code.
- Security capabilities available to everyone (`GOV-032`).
- Licensing that tools can verify automatically.

## Considered options

1. **Open core with dual licensing:** Apache-2.0 for client-side code and libraries, AGPL-3.0-only for the server and Studio, a commercial licence for `ee/`.
2. Everything under Apache-2.0.
3. Source-available licence for everything (e.g. Business Source License or Functional Source License).
4. Proprietary.

## Decision

Chosen option: **1**.

| Part | Licence |
|---|---|
| Runtime packages (`packages/`), host apps (`apps/`), CLI (`backend/cmd/plux`), compiler, schema, SDKs, repository tooling (`tools/`), documentation | Apache-2.0 |
| Server (`backend/cmd/plux-server` and server-only packages), Studio (`studio/`) | AGPL-3.0-only |
| Enterprise edition (`ee/`, from P9) | Commercial; source visible, production use requires a licence |

The licence of every path is declared in `REUSE.toml` and in SPDX headers, and CI verifies it with `reuse lint` (REUSE specification 3.3). Packages linked by the CLI or embeddable by third parties are Apache-2.0 even when they live in the backend module; the AGPL server may use them, not the reverse. Security capabilities are never placed in `ee/` (`GOV-032`). Contributions are accepted under a Contributor License Agreement that permits dual licensing (`GOV-034`, [CLA](../legal/CLA.md)).

## Consequences

- **Positive:** apps can embed the runtime freely; the full source stays public; a hosted fork must publish its changes; companies that cannot accept AGPL, or need enterprise features, have a commercial path; licensing is machine-verified on every change.
- **Negative:** two open licences to explain; the licence boundary constrains package dependencies (Apache code must not import AGPL code); the CLA adds a step for external contributors.
- **Follow-up:** before accepting external contributions, publish the CLA process (`GOV-034`); before P9, write the commercial licence for `ee/` and the offline licence-file mechanism (`GOV-030`, `GOV-033`); have the model reviewed by a lawyer before the first public release.

## Options in detail

### Option 2 — Apache-2.0 for everything

Maximum adoption and simplicity. Rejected: nothing prevents a closed, hosted derivative, and there is no incentive for organisations to buy a commercial licence.

### Option 3 — source-available

Protects commercial interests strongly. Rejected: not open source by the OSI definition, which reduces adoption and trust with the developers and companies Plux targets.

### Option 4 — proprietary

Rejected: hides the engineering and removes the adoption path for solo developers and startups.
