#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# scripts/session-start.sh
# Claude Code SessionStart hook. In remote (web) sessions it installs the
# toolchains pinned in the Makefile so `make check` runs as in CI.
# Local sessions are left alone: developers run `make setup` themselves.

set -euo pipefail

[ "${CLAUDE_CODE_REMOTE:-}" = "true" ] || exit 0

cd "${CLAUDE_PROJECT_DIR:-$(dirname "$0")/..}"

FLUTTER_VERSION="$(sed -n 's/^FLUTTER_VERSION *[:?]*= *//p' Makefile | head -n1)"
FLUTTER_DIR="${HOME}/.cache/plux/flutter-${FLUTTER_VERSION}"

if [ -n "${FLUTTER_VERSION}" ] && [ ! -x "${FLUTTER_DIR}/bin/flutter" ]; then
  mkdir -p "$(dirname "${FLUTTER_DIR}")"
  git clone --quiet --depth 1 --branch "${FLUTTER_VERSION}" \
    https://github.com/flutter/flutter.git "${FLUTTER_DIR}"
fi


GOBIN_DIR="$(go env GOPATH)/bin"
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  echo "export PATH=\"${FLUTTER_DIR}/bin:${GOBIN_DIR}:\$HOME/.local/bin:\$PATH\"" >> "${CLAUDE_ENV_FILE}"
fi
export PATH="${FLUTTER_DIR}/bin:${GOBIN_DIR}:${HOME}/.local/bin:${PATH}"

make setup

# Integration tests need PostgreSQL (QA-005). Where the container has it,
# start it and make the test role and database the tests expect: an
# ordinary role, since the server refuses one that bypasses row-level
# security (SRV-022). Idempotent; skipped when PostgreSQL is absent.
if command -v pg_ctlcluster >/dev/null 2>&1; then
  service postgresql start >/dev/null 2>&1 || true
  for _ in $(seq 20); do pg_isready -q -h 127.0.0.1 && break; sleep 1; done
  su postgres -c "psql -q -v ON_ERROR_STOP=1" <<'SQL' || true
SELECT 'CREATE ROLE plux LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD ''plux'''
  WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'plux') \gexec
SELECT 'CREATE DATABASE plux_test OWNER plux'
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'plux_test') \gexec
SQL
  url="postgres://plux:plux@127.0.0.1:5432/plux_test?sslmode=disable"
  if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    echo "export PLUX_TEST_DATABASE_URL=\"${url}\"" >> "${CLAUDE_ENV_FILE}"
  fi
fi
