# Contributing to Plux

Thank you for your interest. `docs/requirements.md` is the source of truth for what Plux must do; read the relevant section before changing anything.

## Workflow

1. Open or pick an issue; for larger changes, discuss the approach first.
2. Branch from `main`: `<type>/<short-kebab-description>` (e.g. `feat/delta-patcher`).
3. Commit with [Conventional Commits](https://www.conventionalcommits.org) — enforced by `scripts/check-commit-msg.sh`.
4. Install hooks once: `pre-commit install`.
5. Open a pull request using the template; all required checks must pass. Pull requests are squash-merged.

## Requirements traceability

- Tests name the requirement IDs they verify (`QA-071`).
- A pull request implementing a requirement updates its `Status` in the same change (`QA-072`).
- Decisions that are hard to reverse get an ADR in `docs/adr/`.

## Licensing of contributions

Contributions are accepted under a Contributor License Agreement that permits dual licensing (`GOV-034`). The CLA process will be published before external contributions are accepted.

## Code of conduct

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
