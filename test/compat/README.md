<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Compatibility matrix (`QA-010`)

`make compat` runs [`run.sh`](run.sh): the starter app's end-to-end flows
([`apps/starter/integration_test/starter_flows.dart`](../../apps/starter/integration_test/starter_flows.dart),
driven by `backend/internal/server/starter_e2e_integration_test.go`) for every pair below.
Each run starts a server with both roles, publishes and promotes the
[starter fixture](../../schema/testdata/documents/starter) with the CLI, and runs the real
runtime under `flutter test`, which syncs, verifies and renders the release.

| Runtime | Server and compiler | Checks |
|---|---|---|
| this commit | this commit | the baseline pair (also `make e2e-starter`) |
| each of the last three `plux_flutter/v*` tags | this commit | released runtimes against the current server and new bundles |
| this commit | each of the last three `backend/v*` tags | the current runtime against bundles from released compilers |

A released side is checked out from its tag into a temporary `git worktree`, so each
runs its own flows and its own fixture exactly as released. Until three releases of a
component are tagged, the matrix runs the ones that exist (none before the first P3
tags), and `QA-010` stays `WIP`.

**Keeping the matrix meaningful.** The flows of one release run against the fixture of
another, so the starter fixture evolves additively: the welcome page's route, texts and
semantics labels that the flows look for are never changed or removed.

**Running it.** It needs `PLUX_TEST_DATABASE_URL` (a PostgreSQL database, as for the Go
integration tests; [testing.md](../../docs/engineering/testing.md)), Go and Flutter, and
the tags (`git fetch --tags`). CI runs it in the *Starter app end-to-end* job.
