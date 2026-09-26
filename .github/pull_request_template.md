## What

<!-- What does this change do? -->

## Why

<!-- The problem it solves, or the requirement IDs it implements (e.g. SYN-005). -->

## How it was verified

<!-- The make targets and tests run, and any manual checks. -->

## Checklist

<!-- Tick what applies. Leave an item unticked and append "N/A" when it does not apply to this change. -->

- [ ] `make check` (or the gates of the changed components) passes locally
- [ ] Docs, doc comments, component `AGENTS.md` and `docs/WORKLOG.md` updated where behaviour or rules changed
- [ ] ADR added for any new design decision or dependency
- [ ] New files carry SPDX headers (`make reuse-lint`)

**Implements requirements** (`feat` and `fix` only):

- [ ] Tests name the requirement IDs they verify (`QA-071`)
- [ ] `Status` updated in `docs/requirements.md` and `make trace` passes (`QA-070`, `QA-072`)
