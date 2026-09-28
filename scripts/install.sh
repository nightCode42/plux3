#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0

# Installs the plux CLI from a GitHub release, verified (CLI-001):
#
#   curl -fsSL https://raw.githubusercontent.com/nightCode42/plux3/main/scripts/install.sh | sh
#
# The archive's SHA-256 must match the release's SHA256SUMS, and
# SHA256SUMS must carry a Sigstore signature made by this repository's
# release workflow (CI-004), checked with cosign. Without cosign the
# script stops; PLUX_INSTALL_SKIP_SIGNATURE=1 skips that check and says so.
#
# PLUX_VERSION (default: the latest release), PLUX_INSTALL_DIR (default:
# $HOME/.local/bin) and PLUX_RELEASE_BASE_URL (a mirror of the release's
# files) adjust it.
set -eu

repo=${PLUX_RELEASE_REPO:-nightCode42/plux3}
dir=${PLUX_INSTALL_DIR:-$HOME/.local/bin}
fail() { echo "plux install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported system $(uname -s); on Windows use Scoop or the .zip release" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

version=${PLUX_VERSION:-}
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=50" |
    grep -o '"tag_name": *"backend/v[^"]*"' | head -n1 | sed 's/.*backend\/v//; s/"//')
  [ -n "$version" ] || fail "no release found"
fi

base=${PLUX_RELEASE_BASE_URL:-"https://github.com/$repo/releases/download/backend%2Fv$version"}
archive="plux_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for f in "$archive" SHA256SUMS SHA256SUMS.sigstore.json; do
  curl -fsSL -o "$tmp/$f" "$base/$f" || fail "could not download $f"
done

if command -v cosign >/dev/null 2>&1; then
  cosign verify-blob --bundle "$tmp/SHA256SUMS.sigstore.json" \
    --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/backend/v$version" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$tmp/SHA256SUMS" >/dev/null 2>&1 || fail "the signature of SHA256SUMS does not verify"
elif [ "${PLUX_INSTALL_SKIP_SIGNATURE:-}" = 1 ]; then
  echo "plux install: WARNING: cosign is not installed; the signature was not checked" >&2
else
  fail "cosign is needed to verify the release signature (https://docs.sigstore.dev/cosign/system_config/installation/); set PLUX_INSTALL_SKIP_SIGNATURE=1 to install with the checksum only"
fi

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$expected" ] || fail "$archive is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else actual=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1); fi
[ "$expected" = "$actual" ] || fail "$archive does not match its checksum"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$dir"
install -m 0755 "$tmp/plux_${version}_${os}_${arch}/plux" "$dir/plux"
echo "Installed plux $version to $dir/plux"
case ":$PATH:" in *":$dir:"*) ;; *) echo "Add $dir to your PATH." ;; esac
