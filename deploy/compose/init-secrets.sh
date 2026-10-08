#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0

# Generates the Compose stack's credentials into .secrets/ on first run,
# and its development TLS certificate (see dev_tls below).
# Nothing here is ever committed (.gitignore), and nothing has a default:
# every password is 32 random bytes from the operating system.
set -eu
cd "$(dirname "$0")"
umask 077

credentials() {
  mkdir .secrets
  rand() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
  admin=$(rand) pg=$(rand) s3key=$(rand | cut -c1-20) s3secret=$(rand) grafana=$(rand) keycloak=$(rand)
  # The image's own user is the administrator; plux-server connects as the
  # role postgres-init.sh creates, which row-level security binds.
  printf 'POSTGRES_USER=postgres\nPOSTGRES_DB=plux\nPOSTGRES_PASSWORD=%s\nPLUX_APP_PASSWORD=%s\n' "$admin" "$pg" > .secrets/postgres.env
  printf 'PLUX_DATABASE_URL=postgres://plux:%s@postgres:5432/plux?sslmode=disable\nPLUX_OBJECT_STORAGE_ACCESS_KEY_ID=%s\nPLUX_OBJECT_STORAGE_SECRET_ACCESS_KEY=%s\n' \
    "$pg" "$s3key" "$s3secret" > .secrets/plux-server.env
  printf '{"identities":[{"name":"plux","credentials":[{"accessKey":"%s","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' \
    "$s3key" "$s3secret" > .secrets/s3.json
  printf 'GF_SECURITY_ADMIN_USER=admin\nGF_SECURITY_ADMIN_PASSWORD=%s\n' "$grafana" > .secrets/grafana.env
  printf 'KC_BOOTSTRAP_ADMIN_USERNAME=admin\nKC_BOOTSTRAP_ADMIN_PASSWORD=%s\n' "$keycloak" > .secrets/keycloak.env
  # The S3 config is read by the SeaweedFS container's user.
  chmod 644 .secrets/s3.json
  echo "Generated credentials in $(pwd)/.secrets (Grafana admin password in grafana.env)."
}

# The API serves TLS 1.3 here as it does in production (SEC-040). The
# certificate is development-only: a local CA (1 year, constrained to
# localhost, the service name and the Android emulator's host address, so
# trusting it on a machine cannot vouch for any other site) signs a server
# certificate (90 days) whose key is kept across renewals, so a pin of it
# stays valid. The CA key stays in .secrets/ca and is never mounted into a
# container; .secrets/tls holds what the server and Prometheus read. A
# production installation uses its own certificate, so this refuses to run
# for a configuration that declares itself production.
dev_tls() {
  if grep -Eq '^[[:space:]]*production:[[:space:]]*true' plux-server.yaml; then
    echo "init-secrets.sh: plux-server.yaml sets production: true; the self-signed development certificate is refused. Configure server.tls with your own certificate." >&2
    exit 1
  fi
  command -v openssl >/dev/null 2>&1 || { echo "init-secrets.sh: openssl is required to create the development TLS certificate" >&2; exit 1; }
  mkdir -p .secrets/ca .secrets/tls
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  month=2592000
  renew=0
  if [ ! -s .secrets/ca/ca.crt ] || ! openssl x509 -checkend "$month" -noout -in .secrets/ca/ca.crt >/dev/null; then
    cat > "$tmp/ca.cnf" <<'CNF'
[req]
distinguished_name = dn
prompt = no
x509_extensions = ext
[dn]
O = Plux development
CN = Plux development CA
[ext]
basicConstraints = critical,CA:TRUE,pathlen:0
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
nameConstraints = critical,permitted;DNS:localhost,permitted;DNS:plux-server,permitted;IP:127.0.0.1/255.255.255.255,permitted;IP:10.0.2.2/255.255.255.255,permitted;IP:::1/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff
CNF
    openssl ecparam -name prime256v1 -genkey -noout -out .secrets/ca/ca.key
    openssl req -new -x509 -sha256 -days 365 -key .secrets/ca/ca.key -config "$tmp/ca.cnf" -out .secrets/ca/ca.crt
    renew=1
    echo "Generated the development CA .secrets/ca/ca.crt (valid 1 year): trust it as described in README.md."
  fi
  if [ "$renew" = 0 ] && [ -s .secrets/tls/server.crt ] && openssl x509 -checkend "$month" -noout -in .secrets/tls/server.crt >/dev/null; then
    return 0
  fi
  cat > "$tmp/leaf.cnf" <<'CNF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = localhost
[ext]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = serverAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid
subjectAltName = @san
[san]
DNS.1 = localhost
DNS.2 = plux-server
IP.1 = 127.0.0.1
IP.2 = ::1
IP.3 = 10.0.2.2
CNF
  [ -s .secrets/tls/server.key ] || openssl ecparam -name prime256v1 -genkey -noout -out .secrets/tls/server.key
  openssl req -new -key .secrets/tls/server.key -config "$tmp/leaf.cnf" -out "$tmp/leaf.csr"
  openssl x509 -req -sha256 -days 90 -in "$tmp/leaf.csr" -CA .secrets/ca/ca.crt -CAkey .secrets/ca/ca.key \
    -CAcreateserial -extfile "$tmp/leaf.cnf" -extensions ext -out .secrets/tls/server.crt 2>/dev/null
  cp .secrets/ca/ca.crt .secrets/tls/ca.crt
  # The server runs as another user (65532) than the one who ran this
  # script, so, like s3.json, its files must be world-readable; the
  # directory above them (.secrets, mode 0700) keeps other users out.
  chmod 755 .secrets/tls
  chmod 644 .secrets/tls/server.key .secrets/tls/server.crt .secrets/tls/ca.crt
  echo "Issued the development server certificate .secrets/tls/server.crt (valid 90 days; restart plux-server to load a renewed one)."
}

[ -d .secrets ] || credentials
dev_tls
