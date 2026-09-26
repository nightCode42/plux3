@AGENTS.md

## Claude Code

- `AGENTS.md` (imported above) is the canonical working agreement; this file only adds Claude Code specifics.
- After a context compaction or a resumed session, repeat the session protocol in `AGENTS.md` §2 before continuing — do not rely on the compaction summary for rules, spec details or project state.
- Project rules belong in this repository, not in personal memory. If a rule is missing or wrong here, propose a change to `AGENTS.md` or the handbook instead of remembering it privately.
- When working inside a component that has its own `AGENTS.md` (`backend/`, `packages/`, `studio/`, `tools/`), read it before editing.
- Run `make help` to discover tasks; prefer Makefile targets over ad-hoc commands so local runs match CI.
