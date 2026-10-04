# Server Runbook

Failure modes introduced in P2 and P4 (DoD-9), how each shows itself and what to do. Alert rules
and dashboards for these arrive in P9 (`OBS-004`); until then the signals are `/readyz`,
the metrics of [Appendix G.1](../requirements.md#appendix-g--metrics-and-telemetry-events),
the JSON logs and the audit log. Background: [server.md](../reference/server.md).

Every internal failure a client sees is `PLX-8090` with an incident ID; search the logs
for that ID first — it is on the one log line that has the cause.

## A dependency is down

| Symptom | Likely cause | Action |
|---|---|---|
| `/readyz` reports `postgres: unavailable`; calls fail with `PLX-8090` | PostgreSQL unreachable, out of connections or disk | Check the database first; the server reconnects on its own. `database.maxConnections` × replicas must stay under PostgreSQL's `max_connections`. `/livez` stays healthy by design, so replicas are not restarted in a loop. |
| The server does not start: "the database role is a superuser" or "has BYPASSRLS" | It connects as a role that row-level security does not bind, so tenants would not be isolated | Connect as an ordinary role that owns the schema (`CREATE ROLE plux LOGIN NOSUPERUSER NOBYPASSRLS …`, `ALTER SCHEMA public OWNER TO plux`); keep the administrator for PostgreSQL itself. |
| `/readyz` reports `objectStorage: unavailable`; publishes fail, bundle downloads fail | S3 store unreachable, bucket missing, credentials rotated | Check the endpoint, bucket and `PLUX_OBJECT_STORAGE_*`. Nothing is lost: a publish is a durable job and retries. |
| `/readyz` reports `cache: unavailable`; calls rejected | Valkey down | Rate limits and short-lived caches live there; restart Valkey. Its contents are disposable (the Compose stack keeps none on disk). |
| Sign-in with single sign-on fails with `PLX-8091` | The OIDC provider or the path to it is down, or the SSRF guard refused its address | Check the provider; a provider on a private address is refused on purpose (`SEC-105`). Password sign-in still works where it is enabled. |

## Publishing and signing

| Symptom | Likely cause | Action |
|---|---|---|
| Publishes stay queued | No process runs the `worker` role, or it cannot reach the database | Check `server.roles` includes `worker` on at least one replica, and its logs. |
| Publish fails with `PLX-8051` | The publish found warnings nobody acknowledged | Expected behaviour: fix the warnings or publish again acknowledging them. |
| Publishes stay at the `assets` stage, then fail with `PLX-8053` | Image assets of the app are still `pending`: asset jobs are slow, failing, or no worker runs them | Check the worker's logs for `asset.process` jobs and the assets' processing state; raise `publish.assetWait` only if transcoding is legitimately slow. |
| Release creation fails with `PLX-8050` or `PLX-8052` | A plugin was built against other app-level sources, or has no published version | Publish the plugin again (or delete it), then create the release. |
| Signing fails for a production environment with the file backend | The file backend never signs for production (`SEC-056`) | Configure Vault Transit (`signing.backend: vault`); the file backend is for development only. |
| Vault signing fails | Token expired or lacks the policy, Transit key missing | Check `PLUX_SIGNING_VAULT_TOKEN` and its policy (`sign` on the environment keys, `encrypt`/`decrypt` on `wrapKey`). An api-only replica must have no `sign` permission. |
| Publishing warns `PLX-8054` | The release uses a native route, slot or custom action a host build's catalogue lacks or declares otherwise (`WGT-032`) | Devices of that build keep the newest release compatible with them (`REL-080`). Upload the catalogue of a build that has the entry (`plux native sync`), or acknowledge the warning; `ReleaseService.GetCompatibility` lists the builds held back. |
| `plux native sync` fails with `PLX-8032` (resource exists) | The build already has a stored catalogue with other content; a catalogue never changes once stored | A changed host gets a new build string (the `pubspec.yaml` version, or `--build`). The same content again is accepted. |
| `plux native sync` fails with `PLX-8030` (permission denied) | The caller is not a publisher of the app | Grant the publisher role; viewers may read catalogues, not upload them. |

## Manifests and devices

| Symptom | Likely cause | Action |
|---|---|---|
| Devices report an expired manifest | The worker has not re-signed within the seven-day lifetime — worker down for days, or signing failing | Fix the worker or signing (above); the maintenance sweep re-signs every channel within two days of expiry, hourly and at start. Promoting any release re-signs at once. |
| A new release or rollback reaches devices up to a second late | The per-replica manifest cache (1 s) | Expected (`REL-006` allows a minute). |
| Delta requests fail with `PLX-3011` or `PLX-3012` | A stored delta or base bundle is corrupt or truncated | Check object storage integrity. Deltas are content-addressed and computed again on the first request that misses one, so deleting a corrupt delta object is safe. (The runtime's fallback to a full bundle arrives in P3.) |
| Many `PLX-8040` responses | Rate limits, per address, account or device | If clients sit behind a proxy, list the proxy in `server.trustedProxies`, or every client shares its address. Never trust a range clients can send from. Limits tighten through `limits:`; they never exceed the registry maximum. |
| A device secret was leaked | Device credentials are not hardware-bound, and P2 has no revocation call; both arrive in P6 | Access tokens expire within 15 minutes, but the secret keeps minting new ones. Until P6 there is no per-device remedy; a device can only read its app's manifests and bundles and send telemetry — see the P2 residual risks in the [threat model](../security/threat-model.md). |

## Editing and data

| Symptom | Likely cause | Action |
|---|---|---|
| Editors see `EDITING_LOCK_HELD` | Someone else holds the plugin's lock | Locks expire two minutes after the last heartbeat; a user with `plugin.lock.override` can take over, which is audited. |
| Database grows | Retention settings too long, or the maintenance sweep not running | Check a worker runs; the sweep runs hourly and at start. `retention.*` sets how long history, trash and development releases are kept. |
| An audit write fails with a trigger error | Something tried to change or delete audit rows | Audit rows are append-only by design; investigate who attempted it. |

## Upgrades

Migrations are expand-only, so version N-1 keeps running while N migrates
(`DEP-030`). Upgrade by rolling replicas; with `database.migrateOnStart` the first
new replica migrates under an advisory lock, or run `plux-server migrate` first. A
migration that fails leaves nothing applied; roll the replicas back to N-1, which works
against the unchanged schema. Release notes name any step beyond this.
