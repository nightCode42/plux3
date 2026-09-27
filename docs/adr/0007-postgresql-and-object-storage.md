# 0007. PostgreSQL as system of record and job queue; S3-compatible object storage

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `SRV-020`, `SRV-021`, `SRV-022`, `SRV-023`, `SRV-024`, `SRV-030`, `SRV-031`, `SEC-102`, `DEP-002`, `DEP-041`, `QA-005`

## Context and problem

The server stores two very different things: structured, transactional state (organisations,
apps, plugins, drafts, versions, releases, audit) and large immutable artifacts (bundles,
deltas, assets). It also needs durable background work for the publish pipeline. A
self-hoster must be able to run the whole thing from one Compose file (`DEP-002`), and a
regulated customer must be able to prove tenant isolation (`SRV-022`).

## Decision drivers

- One database a self-hoster already knows how to operate, back up and restore.
- Transactional guarantees across a write, its snapshot and its audit record.
- Type-checked SQL rather than a runtime-reflective ORM.
- Tenant isolation provable at the database, beneath the application (`SRV-022`, `SEC-102`).
- Artifacts servable by any CDN and by the server itself (`SRV-023`, `DEP-041`).
- No additional infrastructure for the job queue (`SRV-024`).

## Considered options

1. **PostgreSQL 16+ with `pgx` and `sqlc`; River for jobs; S3-compatible object storage behind an interface.**
2. PostgreSQL with an ORM (GORM, Ent, Bun).
3. PostgreSQL plus a dedicated queue (Redis/NATS/RabbitMQ) and a document store for drafts.
4. Artifacts stored as large objects in PostgreSQL.

## Decision

Chosen option: **1**, as the specification requires (`SRV-020`, `SRV-023`, §6.2).

### Access layer

`pgx/v5` is the driver and pool. Every statement is written as SQL in
`backend/internal/storage/queries/` and compiled by `sqlc` into typed Go, committed and
checked by `make gen-check`. Documents are stored as `JSONB` holding the canonical bytes
produced by `schema/jcs` (`SRV-020`, `SCH-003`), so a document's hash is a property of the
row and not of how Go re-serialised it; relational columns hold the metadata queries filter
and order by. Nothing in a domain package writes SQL (L-2).

### Migrations

Migrations are numbered SQL files applied on start under a PostgreSQL advisory lock, so
several replicas starting at once converge without racing (`SRV-021`). Each migration is
expand-only: add, backfill, then contract in a later release, so an N-1 binary keeps working
against the new schema and a rolling upgrade never needs downtime (`DEP-030`). A test
applies every migration to an empty database, then asserts the resulting schema matches a
committed snapshot, so a migration cannot drift from what the queries expect.

### Tenant isolation

Every table holding tenant data carries `organization_id NOT NULL`, and row-level security
is enabled on it with a policy bound to `current_setting('plux.organization_id')`. The pool
sets that setting per transaction from the authenticated principal, and the application
layer authorises first (`SEC-102`); RLS is the second barrier, not the first. A test walks
the catalogue and fails when a tenant table has no `organization_id`, no enabled RLS or no
policy, so the guarantee cannot be lost by adding a table.

### Jobs

River runs the publish, delta, asset, retention and scheduling jobs in the same PostgreSQL
database (`SRV-024`). A job is enqueued in the same transaction as the state change that
causes it, so a publish is never half-recorded; retries, backoff, uniqueness keys and job
state are rows a self-hoster can inspect with `psql`. Jobs run only in the `worker` role.

### Object storage

Bundles, deltas, assets and exports are written under content-addressed keys —
`<kind>/<sha256[0:2]>/<sha256>` — so an object is immutable, deduplicated and cacheable
for ever (`REL-024`). The `storage.Objects` interface has two backends: `s3`
(`aws-sdk-go-v2`, which talks to MinIO, AWS S3 and every other S3-compatible service) and
`filesystem` for development and tests. When a CDN base URL is configured the server hands
out that URL; otherwise it issues short-lived signed URLs, or serves the bytes itself with
range support (`SRV-023`, `DEP-041`).

### Cache

Valkey holds only derived, expendable state: rate-limit counters, single-flight markers for
on-demand deltas (`REL-022`) and, from P6, the DPoP replay cache. The `cache.Cache`
interface has an in-memory backend for single-node installs and a Valkey backend reached
with a small RESP client in `internal/cache/resp`; nothing that matters survives only there.

### Testing

Integration tests run against a real PostgreSQL (`QA-005`) — the local server in
development, a service container in CI — selected by `PLUX_TEST_DATABASE_URL`; each test
gets its own schema and rolls back. Object storage is tested against an in-process S3 stub
for the common path and against MinIO in CI.

## Consequences

- **Positive:** one datastore to operate and back up; transactional writes across state, snapshot, audit and job; isolation provable in the database; artifacts servable by any CDN.
- **Negative:** PostgreSQL becomes the scaling bottleneck before anything else, and the job queue competes with request traffic for connections; `sqlc` must be pinned and regenerated with every query change.
- **Follow-up:** analytics storage stays out of this decision — `ANL-010` chooses between PostgreSQL and ClickHouse in P9.

## Options in detail

### Option 2 — an ORM

Faster to write at first, but it hides the SQL that RLS, `JSONB` containment queries and
keyset pagination depend on, and it makes query cost invisible in review. `sqlc` gives the
same type safety with the SQL in plain sight.

### Option 3 — a separate queue and document store

Adds two systems a self-hoster must run, back up and secure, and loses the one property
that matters most here: enqueuing a job in the same transaction as the write that causes it.

### Option 4 — artifacts in PostgreSQL

Keeps the deployment to one system, but bundles and deltas are exactly the workload object
storage and CDNs exist for: immutable, content-addressed, range-read, cached at the edge.
Storing them in the database would put device download traffic on the primary. Rejected.
