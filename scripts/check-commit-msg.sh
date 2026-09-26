#!/usr/bin/env bash
# scripts/check-commit-msg.sh
# Validates a commit message against the Conventional Commits spec.
# https://www.conventionalcommits.org
#
# Usage: check-commit-msg.sh <path-to-commit-message-file>
# Called by pre-commit (commit-msg stage) and by CI for every commit in a PR.

set -euo pipefail

MSG_FILE="${1:?usage: $0 <commit-message-file>}"
MAX_HEADER_LENGTH=72
TYPES='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
PATTERN="^(${TYPES})(\([a-z0-9._/-]+\))?!?: [^ ](.*[^.])?$"

# Strip comment lines that git adds to the editor template, and CRLF endings.
MSG=$(grep -v '^#' "${MSG_FILE}" | tr -d '\r' || true)
HEADER=$(printf '%s\n' "${MSG}" | head -n 1)
SECOND_LINE=$(printf '%s\n' "${MSG}" | sed -n '2p')

# Let git-generated messages through: merges, reverts, and autosquash markers.
if printf '%s' "${HEADER}" | grep -qE '^(Merge |Revert "|(fixup|squash|amend)! )'; then
  exit 0
fi

errors=()
if ! printf '%s' "${HEADER}" | grep -qE "${PATTERN}"; then
  errors+=("Header does not match <type>(<scope>): <description>")
fi
if [ "${#HEADER}" -gt "${MAX_HEADER_LENGTH}" ]; then
  errors+=("Header is ${#HEADER} characters; the maximum is ${MAX_HEADER_LENGTH}")
fi
if [ -n "${SECOND_LINE}" ]; then
  errors+=("Separate the header from the body with a blank line")
fi

if [ "${#errors[@]}" -eq 0 ]; then
  exit 0
fi

cat >&2 <<EOF

  ✗  Commit message does not follow Conventional Commits.

$(printf '     - %s\n' "${errors[@]}")

  Format:  <type>(<optional-scope>)<optional-!>: <description>

  Rules:
    - type is one of: ${TYPES//|/, }
    - scope is lowercase, e.g. compiler, runtime, studio, sync, deps
    - description starts right after ": " and does not end with a period
    - whole header is at most ${MAX_HEADER_LENGTH} characters
    - a body, if present, follows a blank line

  Breaking change: append ! after type/scope
    feat(api)!: rename ListRequest.prefix to selector

  Examples:
    feat(compiler): add deterministic FlatBuffers encoder
    fix(sync): verify section hashes after patching
    test(pxl): add conformance vectors for decimals
    chore(deps): bump golangci-lint to v2.13.2

  Your header: "${HEADER}"

EOF
exit 1
