# CLI Reference

`plux` is the command-line interface for developers and CI (spec §22.1). It is a single Go binary, `backend/cmd/plux`. This page documents the commands that exist; later phases add the commands `CLI-003` names for them.

## Install

Every `backend/v*` release ships `plux` for Linux, macOS and Windows on amd64 and arm64, built reproducibly, with a `SHA256SUMS` file signed by the release workflow and SLSA provenance (`CLI-001`, `CI-004`).

| Method | Command |
|---|---|
| Install script (Linux, macOS) | `curl -fsSL https://raw.githubusercontent.com/nightCode42/plux3/main/scripts/install.sh \| sh` — checks the archive against `SHA256SUMS` and the signature with cosign; `PLUX_VERSION`, `PLUX_INSTALL_DIR` (default `~/.local/bin`) adjust it |
| Homebrew, Scoop | The release carries `plux.rb` and `plux.json`; the maintainer publishes them to the tap and bucket |
| Container | `docker run ghcr.io/nightcode42/plux:<version>` — distroless, signed with cosign, SBOM and provenance attached |
| From source | `go install github.com/nightCode42/plux3/backend/cmd/plux@v<version>` (Go maps `v<version>` to the `backend/v<version>` tag) |

To verify a download by hand:

```bash
cosign verify-blob --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "https://github.com/nightCode42/plux3/.github/workflows/release.yml@refs/tags/backend/v<version>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
```

## Commands

| Command | Does |
|---|---|
| `plux login [--server url]` | Signs in with the OAuth 2.0 device authorization grant: prints a code and a URL, waits (`--timeout`, default 10 minutes) until someone approves the code in Studio, and stores the token (below) (`CLI-002`). |
| `plux logout [--server url]` | Forgets the stored token for the server. |
| `plux whoami` | Shows the signed-in user and their organisations; with `--org`, their permissions there. |
| `plux init --server url --org id --app id-or-key [-C dir] [--env key]` | Writes `plux.json` into the project: server, organisation, app and the environment publishes go to (default `development`). It holds no secret and is meant to be committed. |
| `plux doctor [-C dir]` | Checks, in order, the project (`plux.json`, and that it compiles), the configuration, the server's `/readyz`, the credential, the sign-in and the app. |
| `plux validate [--json] <project-dir>` | Compiles a project in the [Git layout](document-model.md#1-project-layout) and reports every diagnostic, without a server. |
| `plux build [--dev] [--json] -o <out-dir> <project-dir>` | Compiles the project and writes its bundles, without a server (below). |
| `plux diff [-C dir]` | Lists the files the local project adds, changes or lacks against the server's drafts; JSON documents compare by their canonical form. Exits 1 when there are differences. |
| `plux publish [-C dir] [--env key] [--release] [--promote env[/channel]] [--wait duration] [--notes text] [--acknowledge-warnings] [--no-import]` | Uploads the project as the app's drafts (unless `--no-import`), publishes the app bundle and every plugin to the environment, waits for each job and prints its diagnostics; with `--release` or `--promote` it creates a release of exactly the versions just published and promotes it. A promotion then waits, up to `--wait` (default `2m`; `0` does not wait), until the worker has signed the channel's manifest for it, so devices are served the release and `pull` and `keys` see it at once; it fails if the manifest is not signed in time (a server with the worker role signs it). Exits 1 when a publish fails. |
| `plux pull [-C dir] [--env key] [--channel key] [-o dir]` | Downloads the release a channel points at (default `production/production`) into the host app as its baseline (`CLI-004`, `SYN-007`): `<dir>/bundles/<plugin>.pxb` (the app bundle is `_app.pxb`), `<dir>/keys.json` with the environment's root public keys, and `<dir>/baseline.json`, which lists every bundle with its plugin key, version, bundle hash and the signature publish made over that hash (`keyId`, `algorithm`, `signature` in base64), so the runtime verifies the baseline before loading it (ADR-0029). `<dir>/assets/<sha256>` holds the asset files the release uses. `-o` defaults to `assets/plux`, relative to the working directory: run it from the host app, and list `assets/plux/`, `assets/plux/bundles/` and `assets/plux/assets/` under `flutter: assets:` in its `pubspec.yaml` (Flutter's asset directories are not recursive; [apps/starter](../../apps/starter/pubspec.yaml) shows it). Every bundle is checked against its bundle hash before it is written. |
| `plux release list [-C dir] [--env key]` | Lists releases, newest first. |
| `plux release promote --env key [--channel key] [--wait duration] <sequence>` | Points a channel (default `production`) at a release and waits, as `publish --promote` does, for its signed manifest. |
| `plux release rollback --env key [--channel key] [--notes text] [--wait duration] <to-sequence>` | Creates a new release with the content of an earlier one, points the channel at it (`REL-006`) and waits for its signed manifest. |
| `plux export [-C dir] [-o dir]` | Writes the server's drafts in the Git layout. |
| `plux import [-C dir]` | Replaces the server's drafts with the local project. |
| `plux keys [-C dir] [--env key]` | Lists the public keys an environment's manifests and bundles are signed with. |
| `plux completion bash\|zsh\|fish\|powershell` | Prints a shell completion script (`CLI-008`), e.g. `source <(plux completion bash)`. |
| `plux version` | Prints the version, commit, commit date, Go version and platform of the binary. |
| `plux help` | Lists the commands. `plux <command> -h` lists a command's flags. |

Flags come before positional arguments. Every server command accepts `--server`, `--org` and `--json`; those that act on an app also accept `--app` and `-C <project-dir>` (default `.`).

`validate` and `build` work fully offline against a local directory, so CI can check a change without a server (`CLI-005`). They run the compiler described in [compiler.md](compiler.md) with the registry's default limits and record the binary's version in every bundle.

`build` writes the app bundle to `<out-dir>/app/<key>.pxb` and one bundle per plugin to `<out-dir>/plugins/<key>.pxb`. A release build also writes each bundle's source map beside it as `<key>.sourcemap` (`CMP-041`); `--dev` builds development bundles, which embed their source maps instead. Files are written through a temporary file and a rename, so a reader never sees a partial bundle. Nothing is written when the project has errors.

## Server, organisation and app

Each is taken from its flag, then an environment variable, then `plux.json` in the project directory:

| Value | Flag | Variable | `plux.json` |
|---|---|---|---|
| Server URL | `--server` | `PLUX_SERVER` | `server` |
| Organisation | `--org` | `PLUX_ORGANIZATION` | `organization` |
| App | `--app` | — | `app` |

## Credentials

Following [ADR-0028](../adr/0028-cli-credential-storage.md):

- `PLUX_TOKEN`, when set, is used and nothing is stored. CI sets it to a scoped personal access token or to a token exchanged from the CI provider's workload identity (`SRV-064`).
- Otherwise `plux login` stores the token in the OS credential store — macOS Keychain, Windows Credential Manager or the Linux Secret Service — under the service `plux`, with the server URL as the account.
- Where no credential store is reachable, typically headless Linux without a keyring daemon, the token goes to `<user config dir>/plux/credentials.json`, readable only by its owner (mode 0600), and `plux login` says so.

No command ever prompts: `login` prints its code and waits for approval elsewhere, so every command can run unattended (`CLI-007`).

## Output and exit codes

Without `--json`, results go to standard output and errors and diagnostics to standard error. Diagnostics are printed one per line as `file#pointer[start:end]: severity PLX-nnnn message`, followed by a count; `build` prints each written bundle's hash (`BND-005`), path and size.

With `--json`, standard output carries one JSON object and nothing else. For `validate` and `build`:

| Field | Content |
|---|---|
| `ok` | `true` when no error was reported |
| `errors`, `warnings` | Counts |
| `bundles` | `build` only: `role` (`app` or `plugin`), `key`, `id`, `development`, `file`, `sourceMap` (release builds), `size`, `hash` (hex), `features` (required features, sorted) |
| `diagnostics` | Every diagnostic in the form of [ADR-0018](../adr/0018-unified-error-model.md): `code`, `reason`, `severity`, `file`, `path`, `range`, `message`, `cause`, `fix`, `docURL` |

The server commands print what they did as one object: `publish` gives `ok`, one entry per publish (`plugin`, `state`, `version`, `diagnostics`) and `release` when one was made; `pull` gives the baseline (`app`, `environment`, `channel`, `releaseSequence`, `bundles`, `keys`); `diff` gives `changes` with `path` and `change`; `doctor` gives `ok` and `checks`.

| Exit code | Meaning |
|---|---|
| 0 | Success; warnings do not fail a command |
| 1 | The command ran and failed: the project has errors, a publish failed, `diff` found differences, a bundle did not match its hash, or the output could not be written |
| 2 | Usage error: an unknown command or flag, a missing argument or setting, or a project path that is not a directory |
| 3 | Not signed in, or the server refused the credential |
| 4 | The server could not be reached |
