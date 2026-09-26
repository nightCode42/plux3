# Workflow

How a change moves from an idea to `main`, and how releases are cut. The rules apply to every contributor, human or AI agent.

---

## 1. Roles

| Role | Responsibilities |
|---|---|
| **Maintainer** | Decides scope and priorities, resolves spec questions, reviews, merges, and signs release tags. |
| **Contributor or AI agent** | Implements a scoped change, runs the checks, updates documentation and the work log, and drafts the commit message and PR description. Commits or pushes only when the maintainer asks. |

## 2. Planning a change

- One branch implements **one slice of requirements** — small enough to review in one sitting, complete enough to be tested and useful on its own.
- Before writing code, identify the requirement IDs the slice covers and read their spec sections and the documents in the routing table (`AGENTS.md` §4).
- A decision the spec does not make is recorded as an ADR in the same PR — or escalated when it touches the triggers in `AGENTS.md` §7.
- Order of work: types and interfaces → tests → implementation → documentation.

## 3. Branches

- Created from an up-to-date `main`: `git switch main && git pull && git switch -c <branch>`.
- Named `<type>/<short-kebab-description>` with Conventional Commits types: `feat/delta-patcher`, `fix/manifest-expiry`, `docs/error-catalogue`, `ci/scorecard`.
- Short-lived: merged or closed within days.

## 4. Commits

- [Conventional Commits](https://www.conventionalcommits.org/), enforced by the `commit-msg` hook and in CI (`scripts/check-commit-msg.sh`):

```text
<type>(<scope>): <description>

<optional body: what and why, wrapped at 72 characters>

<optional footer: Refs: SYN-005, QA-009>
```

- Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Scopes are component or area names: `backend`, `compiler`, `runtime`, `sync`, `studio`, `schema`, `tools`, `ci`, `deps`.
- The header is at most 72 characters, imperative ("add", not "added"), with no trailing period.
- A breaking change to a public interface uses `!` and explains the migration in the body.
- Commits are signed and use the author's GitHub no-reply address.

## 5. Pull requests

- The PR title is a valid Conventional Commits header — it becomes the single commit on `main` when squash-merged. CI checks the title and every commit.
- The description follows the [pull request template](../../.github/pull_request_template.md).
- Target size: under ~400 changed lines excluding generated code, lockfiles and test data.
- The only required status check is **CI OK**, which fails if any CI job fails ([ci.md](ci.md)).
- Merged with **squash merge** only; the branch is deleted automatically.

## 6. Definition of done

A change is done when **all** of the following hold:

- [ ] `make check` (or the relevant component gates) passes locally, and CI OK is green.
- [ ] New behaviour is covered by tests that name their requirement IDs (`QA-071`).
- [ ] The `Status` of every implemented requirement is updated in `docs/requirements.md` in the same PR (`QA-072`), and `make trace` passes.
- [ ] Documentation — doc comments, handbook, component `AGENTS.md`, spec — is updated where behaviour or rules changed, with no document contradicting the code (`AGENTS.md` §3).
- [ ] A new design decision is recorded as an ADR.
- [ ] `docs/WORKLOG.md` reflects the new state and the next step.
- [ ] No `TODO` without an issue, no commented-out code, no debug output, no unrelated changes.

At the end of a phase, the phase Definition of Done in spec §5.3 applies as well.

## 7. Local tooling

The Makefile is the only entry point; `make help` lists every target.

| Command | When |
|---|---|
| `make setup` | Once after cloning: installs pinned tools and git hooks |
| `make check` | Before every push: every gate CI runs |
| `make go-check`, `make dart-check`, `make studio-check`, `make repo-check` | The gates of one area |
| `make gen` | After changing a contract; commit the result |
| `make trace` | To see which requirements are verified, and by what |
| `make secrets` | To scan the full history for secrets |

Git hooks (installed by `make setup`):

| Stage | Checks |
|---|---|
| `pre-commit` | File hygiene, private keys, secrets, Go/Dart/Studio formatting, specification consistency, no commits to `main` |
| `commit-msg` | Conventional Commits |
| `pre-push` | Go unit tests |

Hooks are never bypassed with `--no-verify`. A wrong hook is fixed in its own pull request.

## 8. Releases (`CI-008`)

- Components are versioned independently with [Semantic Versioning](https://semver.org/): `backend` (the `plux` and `plux-server` binaries), `plux_flutter`, `studio`.
- A release is a **signed, annotated tag** named `<component>/v<semver>`, created by the maintainer only:

```bash
git tag -s backend/v0.1.0 -m "backend v0.1.0"
git push origin backend/v0.1.0
```

- The release workflow verifies that the tag is annotated, that GitHub verifies its signature, and that the version matches the component manifest; it then generates release notes from Conventional Commits (`make release-notes COMPONENT=backend`) and publishes the GitHub release.
- Before tagging `plux_flutter`, bump `version` in its `pubspec.yaml` and `PluxRuntimeInfo.version` in the same PR.
- Each phase ends with tagged releases of the components it changed, once its Definition of Done is met.
