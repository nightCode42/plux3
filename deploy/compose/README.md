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

Addresses, all bound to `127.0.0.1`: server <https://localhost:8080> (TLS 1.3, see [Development TLS](#development-tls)), Grafana
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

## Development TLS

The API terminates TLS itself, as a production installation with `server.tls` does
(`SEC-040`): TLS 1.3 only, with `Strict-Transport-Security`, on <https://localhost:8080>
(still bound to `127.0.0.1`). `init-secrets.sh` needs `openssl` and, on every run, makes
sure these exist:

| File | What |
|---|---|
| `.secrets/ca/ca.crt`, `ca.key` | A development CA, valid 1 year. Its name constraints allow only `localhost`, `plux-server`, `127.0.0.1`, `::1` and `10.0.2.2`, so trusting it cannot vouch for any other site. The key never leaves `.secrets/ca`. |
| `.secrets/tls/server.crt`, `server.key` | The server certificate for those names, ECDSA P-256, valid 90 days. Mounted read-only into `plux-server`. The key is kept when the certificate is renewed. |
| `.secrets/tls/ca.crt` | A copy of the CA certificate for clients and Prometheus (which scrapes `/metrics` over HTTPS with it). |

A certificate within 30 days of expiry is re-issued by the next `make compose-up`; restart
the server (`docker compose -f deploy/compose/compose.yaml restart plux-server`) to load
it. When the CA itself expires, delete `.secrets/ca` and run `make compose-up`, then trust
the new CA. The script refuses to run when `plux-server.yaml` sets `production: true`: the
self-signed certificate is for development only, and production configures its own.

The stack's HSTS policy lasts 10 minutes and has no `includeSubDomains`. Browsers apply HSTS
to a host on every port, so the production default (two years) would make them force HTTPS
on <http://localhost:3000> (Grafana) and <http://localhost:9090> too.

### Trusting the CA

Command-line checks need only the file: `curl --cacert deploy/compose/.secrets/tls/ca.crt
-v https://localhost:8080/readyz`. `make` runs the `plux` CLI with `SSL_CERT_FILE`, which Go
honours on Linux; on macOS and Windows trust the CA as below first. Browsers and apps need it
in the operating system's store:

- **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain deploy/compose/.secrets/tls/ca.crt`
- **Debian, Ubuntu:** `sudo cp deploy/compose/.secrets/tls/ca.crt /usr/local/share/ca-certificates/plux-dev-ca.crt && sudo update-ca-certificates`
- **Fedora, RHEL:** copy it to `/etc/pki/ca-trust/source/anchors/` and run `sudo update-ca-trust`
- **iOS simulator:** `xcrun simctl keychain booted add-root-cert deploy/compose/.secrets/tls/ca.crt`
- **Android emulator:** `adb push deploy/compose/.secrets/tls/ca.crt /sdcard/` and install it under Settings,
  Security, Encryption and credentials, Install a certificate, CA certificate. The starter app's debug
  build trusts user-installed CAs (its `network_security_config.xml`); release and profile builds do not.
  `make dev-app` runs `adb reverse`, so `https://localhost:8080` works; `10.0.2.2` is in the certificate too.

Remove the CA again with the platform's reverse of the command above when you delete `.secrets/`.

### The runtime's pins

Debug builds with an `https` endpoint may leave `pins` empty (`SEC-041`), which is what the dev
app does: the platform's trust in the CA above is the check. To exercise pinning, pin the
server key, which survives certificate renewals:

```sh
openssl x509 -in deploy/compose/.secrets/tls/server.crt -pubkey -noout |
  openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | openssl base64
```

The runtime wants at least two distinct pins; make the second from a spare key with
`openssl ecparam -name prime256v1 -genkey -noout | openssl pkey -pubout -outform DER | openssl dgst -sha256 -binary | openssl base64`.

## Files

| File | Purpose |
|---|---|
| `compose.yaml` | The stack |
| `plux-server.yaml` | The server's configuration inside the stack, including `server.tls` |
| `init-secrets.sh` | Generates the credentials and the development CA and certificate |
| `compose.dev.yaml` | Overlay for `make dev`: rebuilds and restarts the server on source changes |
| `compose.test.yaml` | Overlay for `make compose-test`: publishes the stores on high local ports |
| `compose.load.yaml`, `plux-server.load.yaml` | Overlay for the load test (`NFR-020`): trusts Docker's private ranges as proxies so the generator can name each device's address. **Never use it outside a load test.** |
| `otel-collector.yaml`, `prometheus.yml`, `grafana/` | Telemetry pipeline |

## Production

The stack signs with the file backend, which refuses to sign for an environment marked
production (`SEC-056`). A production installation configures Vault Transit in
`plux-server.yaml`, a managed PostgreSQL and S3 store, and its own certificate in `server.tls` (or a TLS 1.3 proxy with `server.behindTLSProxy`);
see the [server runbook](../../docs/runbooks/server.md).
