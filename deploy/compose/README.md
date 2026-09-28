# Compose stack

A complete single-node Plux installation (`DEP-002`): `plux-server` running every role,
with PostgreSQL, SeaweedFS (the S3 store), Valkey, the OpenTelemetry Collector,
Prometheus and Grafana beside it. Every image is pinned by digest. Studio joins the stack
in P11.

## Use

| Command | What it does |
|---|---|
| `make compose-up` | Generates credentials on first run, builds the server image and starts the stack |
| `make dev` | The same with hot reload of the server (`compose watch`); on first run seeds an administrator and the loan calculator promoted to `staging` |
| `make compose-seed` | Starts the stack and seeds it once, without watching |
| `make compose-test` | Runs the Go integration and end-to-end tests against the stack's PostgreSQL, SeaweedFS and Valkey (`QA-005`) |
| `make compose-down` | Stops the stack; volumes are kept (`docker compose … down -v` deletes them) |

Addresses, all bound to `127.0.0.1`: server <http://localhost:8080>, Grafana
<http://localhost:3000>, Prometheus <http://localhost:9090>. The optional profiles
`keycloak` (an OpenID Connect provider on port 8180) and `ollama` start with
`docker compose --profile <name> …`.

## Secrets

`init-secrets.sh` writes random credentials into `.secrets/` (mode 0600, ignored by Git)
the first time it runs and never overwrites them. The seed step adds `.secrets/dev.env`
with the administrator's password, a CLI token and the IDs of the seeded organisation and
app — use them with `PLUX_TOKEN=… plux …`. Delete `.secrets/` together with the volumes to
start again from nothing.

PostgreSQL's own `postgres` user is the administrator. `postgres-init.sh` creates the role
`plux-server` connects as when the volume is first created: it owns the schema but is
neither a superuser nor `BYPASSRLS`, so row-level security binds it, and the server refuses
to start as a role that it would not bind (`SRV-022`). A stack created before this change
connects as a superuser and no longer starts: delete `.secrets/` and the volumes
(`docker compose -f deploy/compose/compose.yaml down -v`) and run `make compose-up` again.

## Files

| File | Purpose |
|---|---|
| `compose.yaml` | The stack |
| `plux-server.yaml` | The server's configuration inside the stack |
| `compose.dev.yaml` | Overlay for `make dev`: rebuilds and restarts the server on source changes |
| `compose.test.yaml` | Overlay for `make compose-test`: publishes the stores on high local ports |
| `compose.load.yaml`, `plux-server.load.yaml` | Overlay for the load test (`NFR-020`): trusts Docker's private ranges as proxies so the generator can name each device's address. **Never use it outside a load test.** |
| `otel-collector.yaml`, `prometheus.yml`, `grafana/` | Telemetry pipeline |

## Production

The stack signs with the file backend, which refuses to sign for an environment marked
production (`SEC-056`). A production installation configures Vault Transit in
`plux-server.yaml`, a managed PostgreSQL and S3 store, and TLS in front of port 8080;
see the [server runbook](../../docs/runbooks/server.md).
