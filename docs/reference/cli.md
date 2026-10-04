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
| `plux init --server url --org id --app id-or-key [-C dir] [--env key]` | In a Plux project, or an empty directory a project export will fill: writes `plux.json`, naming the server, organisation, app and the environment publishes go to (default `development`). It holds no secret and is meant to be committed. In a Flutter app — a `pubspec.yaml` on the Flutter SDK and no `app.json` — sets the app up as a Plux host (`HST-032`, below). A directory holding both is a usage error. |
| `plux doctor [-C dir]` | Checks, in order, the project (`plux.json`, and that it compiles), the configuration, the server's `/readyz`, the credential, the sign-in and the app. |
| `plux validate [--json] <project-dir>` | Compiles a project in the [Git layout](document-model.md#1-project-layout) and reports every diagnostic, without a server. |
| `plux build [--dev] [--json] -o <out-dir> <project-dir>` | Compiles the project and writes its bundles, without a server (below). |
| `plux codegen [--host dir] [-o file] <project-dir>` | Compiles the project, without a server, and writes its typed Dart API into the host app (`--host`, default `.`) as `lib/plux/plux.g.dart` (`-o`, relative to the host) (`HST-030`): `PluxScreens` (a builder per routed page, with typed parameters and its typed result: `push`, `view`, `page`), `PluxComponents` (a view builder per exported component, with typed props), a class per host event and `PluxHostEvents` streams, `PluxAppState` (a typed handle per exposed state entry) and `PluxFlags`, plus a class per declared type. Misuse is a compile error in the host. The output is deterministic; an unchanged API leaves the file untouched. It reads a local project; reading a pulled release instead is planned with the quick start (P10). |
| `plux diff [-C dir]` | Lists the files the local project adds, changes or lacks against the server's drafts; JSON documents compare by their canonical form. Exits 1 when there are differences. |
| `plux publish [-C dir] [--env key] [--release] [--promote env[/channel]] [--wait duration] [--notes text] [--acknowledge-warnings] [--no-import]` | Uploads the project as the app's drafts (unless `--no-import`), publishes the app bundle and every plugin to the environment, waits for each job and prints its diagnostics; with `--release` or `--promote` it creates a release of exactly the versions just published and promotes it. A promotion then waits, up to `--wait` (default `2m`; `0` does not wait), until the worker has signed the channel's manifest for it, so devices are served the release and `pull` and `keys` see it at once; it fails if the manifest is not signed in time (a server with the worker role signs it). Exits 1 when a publish fails. |
| `plux pull [-C dir] [--env key] [--channel key] [-o dir]` | Downloads the release a channel points at (default `production/production`) into the host app as its baseline (`CLI-004`, `SYN-007`): `<dir>/bundles/<plugin>.pxb` (the app bundle is `_app.pxb`), `<dir>/keys.json` with the environment's root public keys, and `<dir>/baseline.json`, which lists every bundle with its plugin key, version, bundle hash and the signature publish made over that hash (`keyId`, `algorithm`, `signature` in base64), so the runtime verifies the baseline before loading it (ADR-0029). `<dir>/assets/<sha256>` holds the asset files the release uses. `-o` defaults to `assets/plux`, relative to the working directory: run it from the host app, and list `assets/plux/`, `assets/plux/bundles/` and `assets/plux/assets/` under `flutter: assets:` in its `pubspec.yaml` (Flutter's asset directories are not recursive; [apps/starter](../../apps/starter/pubspec.yaml) shows it). Every bundle is checked against its bundle hash before it is written. |
| `plux release list [-C dir] [--env key]` | Lists releases, newest first. |
| `plux release promote --env key [--channel key] [--wait duration] <sequence>` | Points a channel (default `production`) at a release and waits, as `publish --promote` does, for its signed manifest. |
| `plux release rollback --env key [--channel key] [--notes text] [--wait duration] <to-sequence>` | Creates a new release with the content of an earlier one, points the channel at it (`REL-006`) and waits for its signed manifest. |
| `plux export [-C dir] [-o dir]` | Writes the server's drafts in the Git layout. |
| `plux import [-C dir]` | Replaces the server's drafts with the local project. |
| `plux keys [-C dir] [--env key]` | Lists the public keys an environment's manifests and bundles are signed with. |
| `plux native scan [--host dir] [-o file] [--build id]` | Writes the host app's native catalogue, `plux.catalogue.json`, by static analysis, with no change to its code (`CLI-006`, [ADR-0041](../adr/0041-native-catalogue-and-host-builds.md)): it runs `dart run plux_native_scan` in the host project (`--host`, default `.`), which needs `plux_native_scan` as a dev dependency, with the slot classes `plux.yaml` lists and the host build (below). The scanner's problems, such as a type with no document type, are printed with their locations; its exit code is the command's. |
| `plux native sync [--build id] [--host dir] [--catalogue file] [-C dir]` | Uploads the catalogue of one host build (`NativeCatalogueService`). A build's catalogue never changes: the same content again changes nothing, and other content is refused (`PLX-8032`), so a new build needs its own identifier. Needs the `release.publish` permission. |
| `plux create <app> --out dir \| --zip file \| --git remote [--branch name] [--env key] [--channel key] [--name text] [--application-id id] [--bundle-id id] [--version x.y.z+n] [--icon file.png] [--splash #RRGGBB] [--plux-path dir]` | Generates the ready-to-build Flutter project of a no-code app (`GEN-001`, [ADR-0024](../adr/0024-no-code-generated-projects.md), below), written into a new or empty directory, as one zip, or committed and pushed to a Git remote and branch (default `main`) with your own `git` and credentials (`GEN-003`); a remote that starts with `-` or holds spaces, or a branch name git would refuse, is rejected before anything runs. |
| `plux completion bash\|zsh\|fish\|powershell` | Prints a shell completion script (`CLI-008`), e.g. `source <(plux completion bash)`. |
| `plux version` | Prints the version, commit, commit date, Go version and platform of the binary. |
| `plux help` | Lists the commands. `plux <command> -h` lists a command's flags. |

Flags come before positional arguments. Every server command accepts `--server`, `--org` and `--json`; those that act on an app also accept `--app` and `-C <project-dir>` (default `.`).

**Setting up a host app (`plux init` in a Flutter app).** It fetches the environment's
root public keys (`--env`, as `plux keys`), then:

1. adds `plux_flutter` to `pubspec.yaml`'s dependencies, and `plux_go_router` or
   `plux_auto_route` when the app depends on `go_router` or `auto_route`;
2. writes `plux.yaml` when the app has none, with the Android `applicationId` as `appId`
   when it finds one;
3. writes `lib/plux/plux_options.g.dart`: `PluxOptions` with the app, server and
   environment, the root keys embedded (`SEC-051`), and `PluxOptions.config()`;
4. calls `Plux.initialize(PluxOptions.config())` in `lib/main.dart` and wraps the app in
   a `PluxScope`, when `main` does nothing but `runApp(…)`; otherwise it prints the exact
   lines to add and leaves `main.dart` as it is;
5. runs the server checks of `plux doctor`.

Each step leaves what it already finished as it is, so a second run changes nothing. An
environment that has signed nothing yet has no key to embed: promote a release to it and
run `plux init` again. Until `plux_flutter` and the adapters are published, point the
added dependencies at this repository with `dependency_overrides`.

**Generating a no-code app (`plux create`).** It pulls the channel's release (default
`production/production`) as `plux pull` does, and reads the shell from it: the app's
name and entry route, push and deep links from the app bundle, and the device APIs the
plugins request. Flags give what a release does not hold: the identifiers (default
`com.example.<package>`; set your own before publishing), the version (default
`1.0.0+1`), the icon (a square PNG of 1024 to 4096 pixels; default a disc on the splash
colour) and the splash colour (default `#FFFFFF`). The project holds:

- the Android and iOS projects of the pinned Flutter, with the icons and launch screens,
  the permissions and usage descriptions the device APIs need, push (iOS entitlement and
  background mode, Android notification permission) and deep links (intent filters,
  associated domains, URL schemes);
- `lib/main.dart`, which starts Plux and shows the entry page, and
  `lib/plux/plux_options.g.dart` with the environment's root keys;
- the release embedded under `assets/plux/`, `plux.yaml` and `plux.shell.json`, the
  shell settings it was generated from;
- `integration_test/app_test.dart`, which waits for the entry page on a device;
- `scripts/build_android.sh` and `scripts/build_ios.sh`, and GitHub Actions and GitLab
  CI templates for signed release builds, which read their secrets by name and hold none.

The same input gives the same files and the same zip bytes. Until `plux_flutter` is
published, `--plux-path` points the project at a checkout of it.

**Host builds and `plux.yaml`.** The host build is the string the host's devices report as
`PluxConfig.hostBuild`: `--build`, else `plux.yaml`'s `hostBuild`, else the `version` of
the host's `pubspec.yaml`, such as `1.4.0+52`; pass the same string to `PluxConfig`.
`plux.yaml`, at the host project's root, is read without a YAML library, so it keeps to
top-level `key: value` lines and lists of names:

```yaml
slots:            # the widget classes plugins may place as native slots (WGT-030)
  - MapCard
hostBuild: 1.4.0+52   # optional
appId: com.example.shop   # optional: the catalogue's host.appId
catalogueId: 01f0c450-6c00-7000-8000-000000000003   # optional: keeps the catalogue's ID
```

The catalogue keeps the ID of the file it replaces, so scanning unchanged code writes the
same bytes.

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
