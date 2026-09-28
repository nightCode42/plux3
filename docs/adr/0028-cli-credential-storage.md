# 0028. The CLI keeps its token in the OS keychain

- **Status:** Accepted
- **Date:** 2026-09-28
- **Requirements:** `CLI-002`, `CLI-007`, `SRV-064`

## Context and problem

`plux login` obtains an access token through the OAuth 2.0 device authorization grant
(`CLI-002`), and every later command needs it. The working agreement forbids persisting
tokens unencrypted (AGENTS.md §5). Where does the CLI keep the token between runs?

## Decision drivers

- No token in a plain file where the operating system offers protected storage.
- CI must work without any stored state (`SRV-064`) and never block on a prompt (`CLI-007`).
- The CLI is a static, cgo-free binary for Linux, macOS and Windows (`CLI-001`).

## Considered options

1. The OS credential store through `github.com/zalando/go-keyring`, with a user-only file where no store exists, and `PLUX_TOKEN` overriding both.
2. The same stores reached by calling the platform tools (`security`, `secret-tool`, PowerShell) directly.
3. A file readable only by the user (mode 0600), as `gh` and `gcloud` do by default.
4. Nothing stored: `plux login` prints the token for the user to export.

## Decision

Chosen option: **1**, the approach most widely used CLIs take (maintainer, 2026-09-28).

- `PLUX_TOKEN` wins when set; CI uses it, with a scoped or workload-identity token.
- Otherwise the token lives in macOS Keychain, Windows Credential Manager or the Linux
  Secret Service, under service `plux` and the server URL as the account.
- Where no store is reachable — typically headless Linux without a keyring daemon — the
  token is written to `<user config dir>/plux/credentials.json` with mode 0600, and the CLI
  says so on every login. This is the one place a token is persisted outside protected
  storage; it is the documented exception to the AGENTS.md rule, limited to the user's own
  CLI credential on the user's own machine.
- `plux logout` removes the token from wherever it is and revokes nothing server-side that
  the user did not ask for; the token's own expiry still applies.

go-keyring is pure Go (it adds `godbus/dbus` on Linux and `danieljoos/wincred` on Windows,
and calls `/usr/bin/security` on macOS), so the binary stays static and cgo-free.

## Consequences

- **Positive:** tokens are protected by the OS on desktops; CI needs no state; no cgo.
- **Negative:** three new modules; headless Linux falls back to a file.
- **Follow-up:** none.

## Options in detail

Option 2 avoids the dependency but needs `secret-tool` installed on Linux and has no
direct route to Windows Credential Manager. Option 3 breaks the working agreement on every
machine. Option 4 is safe but makes the CLI tedious for people.
