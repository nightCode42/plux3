#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The test database, run by `make test-db` and `make test-db-down`
# (docs/engineering/testing.md): a throwaway PostgreSQL container with
# the plux_test database owned by the role plux, which is neither a
# superuser nor BYPASSRLS, so row-level security binds it (SRV-022). The
# tests create and migrate their own schemas in it. Prints the
# PLUX_TEST_DATABASE_URL to export.
#
#   test-db.sh up     start it, or reuse it when it runs
#   test-db.sh down   remove it and its data
set -eu

name=plux-test-db
port=${TEST_DB_PORT:-55432}
# The image the Compose stack pins (deploy/compose/compose.yaml).
image=$(sed -n 's/^ *image: \(postgres:.*\)$/\1/p' "$(dirname "$0")/../deploy/compose/compose.yaml")
url="postgres://plux:plux@127.0.0.1:$port/plux_test?sslmode=disable"

case "${1:-}" in
up)
	if [ -z "$(docker ps -q -f "name=^$name\$")" ]; then
		docker rm -f "$name" >/dev/null 2>&1 || true
		docker run -d --name "$name" -p "127.0.0.1:$port:5432" \
			-e POSTGRES_PASSWORD=postgres "$image" >/dev/null
		tries=0
		until docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres 2>/dev/null; do
			tries=$((tries + 1))
			[ "$tries" -lt 60 ] || { echo "✗ PostgreSQL did not start; see: docker logs $name" >&2; exit 1; }
			sleep 1
		done
		docker exec -i "$name" psql -q -v ON_ERROR_STOP=1 -U postgres <<'SQL'
CREATE ROLE plux LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB PASSWORD 'plux';
CREATE DATABASE plux_test OWNER plux;
SQL
	fi
	echo "Test database ready. Export it for the tests:"
	echo "  export PLUX_TEST_DATABASE_URL=$url"
	;;
down)
	docker rm -f "$name" >/dev/null 2>&1 || true
	echo "Test database removed."
	;;
*)
	echo "usage: test-db.sh up|down" >&2
	exit 2
	;;
esac
