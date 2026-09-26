# Contributing to Plux

Thank you for your interest. `docs/requirements.md` is the source of truth for what Plux must do; read the relevant section before changing anything.

## Workflow

1. Open or pick an issue; for larger changes, discuss the approach first.
2. Run `make setup` once, then branch from `main`: `<type>/<short-kebab-description>` (e.g. `feat/delta-patcher`).
3. Read the working agreement ([AGENTS.md](AGENTS.md)) and the handbook pages its routing table lists for your change.
4. Commit with [Conventional Commits](https://www.conventionalcommits.org) — enforced by `scripts/check-commit-msg.sh`.
5. Run `make check` (or the gates of the component you changed).
6. Open a pull request using the template; the **CI OK** check must pass. Pull requests are squash-merged, so the PR title becomes the commit message.

The full process, including releases, is in [docs/engineering/workflow.md](docs/engineering/workflow.md).

## Requirements traceability

- Tests name the requirement IDs they verify (`QA-071`).
- A pull request implementing a requirement updates its `Status` in the same change (`QA-072`).
- Decisions that are hard to reverse get an ADR in `docs/adr/`.

## Licensing of contributions

Plux is open core ([ADR-0022](docs/adr/0022-open-core-licensing.md)). Contributions are accepted under the [Contributor License Agreement](docs/legal/CLA.md), which permits dual licensing (`GOV-034`). The signing process will be linked here before external contributions are accepted. Every new file carries SPDX headers matching [REUSE.toml](REUSE.toml).

## Code of conduct

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
