#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0

# Generates the Compose stack's credentials into .secrets/ on first run.
# Nothing here is ever committed (.gitignore), and nothing has a default:
# every password is 32 random bytes from the operating system.
set -eu
cd "$(dirname "$0")"
[ -d .secrets ] && exit 0
umask 077
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
