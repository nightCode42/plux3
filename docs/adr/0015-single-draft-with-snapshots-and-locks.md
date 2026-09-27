# 0015. Single draft with snapshots and exclusive plugin locks instead of branching

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `SRV-030`, `SRV-031`, `SRV-040`, `SRV-041`, `SRV-042`, `GOV-031`, `SEC-140`, `REL-001`

## Context and problem

Several people work on one app. Studio autosaves as they type, and a page's validity
depends on the rest of the plugin — its state, its parameters, its action graphs — so a
half-merged page is not merely untidy, it fails to compile. Git's answer is branching and
merging; a design tool's answer is one shared document. Which model does Plux use for
drafts, and how is concurrent editing prevented from destroying work?

## Decision drivers

- A draft must always be in a state that can be validated and published (`SRV-050`).
- Autosave must be cheap: Studio sends what changed, not the document (`SRV-030`).
- Nothing a user did may be lost, including work interrupted by a lock takeover (`SRV-041`).
- History must be inspectable, comparable and restorable (`SRV-031`).
- Every change must be attributable (`SEC-140`).
- The model must survive being replaced by real-time co-editing in P14 without a rewrite of
  the storage layer.

## Considered options

1. **One draft per plugin, a linear append-only history of snapshots, and an exclusive editing lock.**
2. Git-like branches per user with a merge step before publishing.
3. Last-write-wins on shared documents, with no lock.
4. CRDT co-editing from the start.

## Decision

Chosen option: **1**, as the specification requires (§11.3).

### Drafts and writes

Each plugin has exactly one draft: the current version of each of its documents. A write
carries the document's revision number; the server applies it only if the revision still
matches, and otherwise returns a conflict the client resolves by re-reading (`SRV-030`).
Partial writes are RFC 6902 JSON Patch documents, so an autosave that moves one node sends
a few hundred bytes. The patch is applied to the stored canonical JSON, the result is
re-canonicalised (`SCH-003`) and structurally validated before the revision is incremented;
an invalid patch is rejected whole, so the draft is never left broken by a write.

App-level documents — theme, translations, data sources, native catalogue, shared state and
shared components — form a second draft owned by the app rather than by any plugin.

### Snapshots

Every accepted write appends a snapshot of the documents it touched: the writer, the time,
the reason, and the content, compressed and deduplicated by content hash so that an
unchanged document costs one reference (`SRV-031`). Snapshots are listable, comparable and
restorable — restoring writes the old content forward as a new snapshot, never rewrites
history. Retention is at least 90 days, and the snapshot a version was published from is
kept for ever, so any release can be opened as it was authored (`REL-001`).

Because a snapshot is content-addressed, it is also what a lock takeover preserves and what
the trash restores (`GOV-031`): deleting a plugin moves it to the trash with its draft and
history intact for 30 days.

### Locks

Editing a plugin requires its lock (`SRV-040`). A lock is acquired explicitly, belongs to
one user and one session, is renewed by a heartbeat every 30 seconds and expires 2 minutes
after the last heartbeat; every reader sees who holds it and when it expires. App-level
documents have their own lock, so theming and page editing do not block each other
(`SRV-042`).

Another user may **request** the lock, which notifies the holder; a user with
`plugin.lock.override` may **take** it (`SRV-041`). A takeover snapshots the current draft
first, so the previous holder's unsaved work is recoverable, and writes an audit record
with both parties (`SEC-140`). Writes without the lock are refused with `PLX-8020`.

Locks are rows in PostgreSQL, taken and renewed in a transaction; they are correct across
replicas without a distributed lock service, and a crashed `api` replica costs at most the
expiry window. They are advisory over authorisation, never a substitute for it: holding a
lock never grants a permission the caller lacks (`SEC-102`).

### Why not branches

The unit of publication is the plugin version, and the unit of activation is the app
release (ADR-0020). A branch would have to be validated against the rest of the app to be
meaningful, which is the same work as publishing, and merging two visual edits of one page
has no safe automatic answer — the conflict is not textual. One draft plus a lock makes the
contention visible to people, who resolve it in seconds, instead of hiding it until a merge.

## Consequences

- **Positive:** the draft is always publishable; autosave is small and attributable; nothing is lost, including on takeover; history is inspectable without a version-control system; the storage model is a plain linear log.
- **Negative:** only one person edits a plugin at a time, which is friction on a large app — mitigated by splitting work across plugins and by the app-level lock being separate; long-lived experimental work has no home until it can be a separate plugin.
- **Follow-up:** P14 adds CRDT co-editing (`COL-001`) and supersedes the exclusive lock for editing, keeping snapshots, revisions and the audit trail as they are.

## Options in detail

### Option 2 — branches and merges

Familiar to developers and useless to designers: a visual merge of two edits to the same
node tree cannot be resolved automatically, and a branch that is not continuously validated
against its app is a branch that fails at publish time. It also multiplies the storage and
the validation cost by the number of open branches.

### Option 3 — last write wins

Cheapest to build, and it silently discards work. Rejected outright: `SRV-041` requires
that even a forced takeover preserve what the previous holder had typed.

### Option 4 — CRDTs now

The right end state and the P14 plan, but it is a large piece of work whose value appears
only once Studio exists (P11). Building it now would delay the backend by months and would
still need snapshots, revisions and audit underneath it. Deferred deliberately.
