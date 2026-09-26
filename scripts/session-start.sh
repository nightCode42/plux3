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
