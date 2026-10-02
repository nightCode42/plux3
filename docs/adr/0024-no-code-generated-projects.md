# 0024. No-code generated projects: a deterministic Go generator over a versioned shell spec

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `GEN-001`, `GEN-002`, `GEN-003`, `GEN-005`, `GEN-006`; `GEN-004` (P11)

## Context and problem

A no-code app still needs a native shell to reach the stores. `GEN-001` asks for a
complete, ready-to-build Flutter project, containing:

- the app's name, bundle and application IDs, icons and splash screen;
- the runtime, wired in with the signing root keys and configuration;
- an embedded baseline release whose entry is the app's default plugin;
- platform permissions and usage descriptions derived from the plugins' declared
  capabilities;
- push configuration and build scripts.

The other requirements:

- The project builds and runs unchanged with the pinned Flutter, proved in CI
  (`GEN-002`).
- It is delivered to a directory, as a zip, or by a push to a Git repository, with CI
  templates for signed release builds (`GEN-003`).
- Everything except the shell ships as Plux releases (`GEN-005`).
- A team may later add native code without losing anything (`GEN-006`).

`plux create` delivers this in P4. Studio's download arrives in P11 (`GEN-001`). In P11
Studio also manages shell settings and detects when a change needs a new store build
(`GEN-004`).

The maintainer decided (P4 plan D5):

- the generator is a Go library;
- in P4 the CLI writes a directory, a zip, or pushes with the developer's own `git`;
- the server endpoint for Studio reuses the same library in P11.

## Decision drivers

- **One generator for the CLI now and the server later.** The server has no Flutter or
  Dart SDK and must not need one.
- **Deterministic output.** The same input gives the same files and the same zip bytes,
  so generated projects can be reviewed, diffed and cached (in the spirit of `CMP-002`).
- **No new dependency** ([dependencies.md](../engineering/dependencies.md)). The Go
  standard library covers templates, images, zips and running `git`.
- **No secrets in output.** CI templates name secrets and contain none. `--git` never
  stores credentials.
- **Shell changes are detectable later.** `GEN-004` compares shell settings, so P4 must
  record them in a stable form.
- **Least privilege on the device.** A generated app asks only for the permissions its
  plugins declare (`SEC-080`).

## Considered options

1. **A Go library with templates embedded in the binary,** rendered from a typed, versioned
   shell spec.
2. **Run `flutter create`, then patch its output.**
3. **A Dart template generator** (for example `mason` bricks).

## Decision

Chosen option: **1.**

- Option 2 needs the Flutter SDK wherever the generator runs, P11's server included, and
  its output varies with the Flutter version and the machine.
- Option 3 adds a dependency that is not on the allowlist, and a Dart runtime on the
  server.

### The library

The library is a Go package, `backend/internal/generator`, under Apache-2.0 like the
compiler (ADR-0022). The CLI uses it in P4 and the server in P11.

Its input is a **`ShellSpec`**, a typed, versioned record of everything that needs a new
store build:

- name, bundle and application IDs, version and build number;
- the icon and splash sources and colours;
- permissions and usage descriptions;
- optional packages;
- push configuration;
- deep-link hosts and schemes.

`plux create` derives a `ShellSpec` from the app document (name, icon, `push`,
`navigation.deepLinks`) and the release's plugins (capabilities), plus flags or a
`shell.json` for what the document does not hold, such as identifiers. The spec it used
is written into the project as `plux.shell.json`. A later regeneration can then compare
against it, and P11's shell-update detection (`GEN-004`) compares `ShellSpec`s. Its
`version` field changes only additively.

### Templates

The project's files are templates embedded with `embed`, rendered with `text/template`:

- `pubspec.yaml`;
- `lib/main.dart`, which calls `Plux.initialize` with the root keys and the app's
  configuration;
- the Android Gradle project;
- the iOS Xcode project and CocoaPods files;
- `plux.yaml`.

The templates are written against the pinned Flutter: their Gradle, Kotlin, Xcode and
deployment-target settings match that Flutter's own templates. The `GEN-002` job builds a
generated project on every change to the generator and on every Flutter pin bump, so any
drift fails CI.

Generated projects are ordinary embedded-mode Flutter apps (`GEN-006`):

- `main.dart` uses the same `Plux.initialize` as any host;
- `PluxConfig` holds the one registration point for native routes, slots and actions
  (ADR-0041);
- the starter's predictive-back setting is included (ADR-0040).

### What the generator derives

**Icons and splash:**

- One source PNG is resized for Android's mipmap densities and the iOS `AppIcon` set by
  an in-house area-averaging resize over `image` and `image/png`. It is deterministic
  and needs no image library.
- The splash uses each platform's native launch screen: a colour and the centred icon. It
  needs no splash package.

**Permissions:**

- One table maps each `deviceApi` capability to its Android permissions and its iOS
  `Info.plist` usage-description keys.
- A test checks that every member of the `deviceApi` enum is mapped.
- A generated app declares only what its plugins' capabilities need.

**Baseline and keys:**

- The release's bundles, signatures and assets are fetched as `plux pull` fetches them,
  and embedded as the app's baseline (`SYN-007`). Its entry is the app's default plugin.
- The signing root public keys come from `plux keys`.

**Push and deep links:**

- Push configuration is present only when the app declares `push.enabled`:
  - on iOS, the push entitlement and the remote-notification background mode;
  - on Android, the notification permission.
  - No push SDK is added (P4 plan D6).
- Deep-link hosts and schemes become Android intent filters, iOS associated domains and
  URL schemes.

**Build scripts and CI templates (`GEN-003`):**

- GitHub Actions and GitLab CI pipelines build signed Android and iOS releases, and
  upload the build's native catalogue (`plux native sync`, ADR-0041).
- They read the keystore, the signing certificates and the Plux token from secrets named
  in the template's README. The generated files contain no secret.

### Determinism

- Files are written in sorted path order, with LF line endings and fixed file modes.
- Zip entries carry the zip format's earliest timestamp and no extra fields.
- Nothing reads the clock, the environment or the machine.
- A test generates the sample app twice and compares the bytes of the files and of the
  zip.

### Outputs (`GEN-003`)

`plux create <app> --out <dir> | --zip <file> | --git <remote> [--branch <name>]`:

- **`--out`** writes into a new or empty directory, and refuses a non-empty one.
- **`--zip`** writes one archive. Entry paths are relative, with no `..` and no absolute
  paths.
- **`--git`**:
  - writes into a temporary directory;
  - commits with the developer's own `git` as a subprocess, with arguments, never through
    a shell;
  - pushes to the given remote and branch with the developer's own credential helpers.
  - The generator never sees, stores or logs a credential.
  - In P11 the server uses the same library, with server-held credentials under P9's
    secret handling.

### What P4 leaves to P11

- Studio's download endpoint, which wraps the zip output (`GEN-001` through Studio).
- Managing shell settings in Studio, and detecting a "shell update required" by comparing
  the stored `ShellSpec` with the new one (`GEN-004`). P4 makes the `ShellSpec` stable,
  versioned and recorded in every project, so P11 has a baseline to compare against.

### Proofs

- **`GEN-002`.** A CI job generates the sample app, builds it with the pinned Flutter for
  Android and iOS, and launches it on an emulator and a simulator inside the existing
  device jobs. It checks that the first page renders.
- **`GEN-005`.** The test publishes a new plugin release, and the generated app shows it
  without a rebuild.
- **`GEN-006`.** The test adds a native route and a slot to the generated project, runs
  `plux native scan`, and uses both from a plugin.

### As built in R9

**Where the shell comes from.**

- `plux create <app>` reads the shell from the release it embeds, not from the app
  document: the app bundle gives the name, entry route, push and deep links, and the
  plugin bundles give their device APIs. Flags give the rest — identifiers, version, icon,
  splash colour. No `shell.json` input was needed.
- Identifiers default to `com.example.<package>`.
- An app without an icon gets a generated one: a disc on the splash colour.

**Templates.**

- They are the starter's platform projects, made with the pinned Flutter (a test keeps
  the templates' Flutter version and GitHub action pins in step with the repository's).
- `RunnerTests` runs a build's Dart integration tests under XCTest (ADR-0035), so the
  project's `integration_test/app_test.dart`, which waits for the entry page, runs on a
  simulator as on an emulator.

**Generated files.**

- `lib/main.dart` hands the links the app is opened with to `Plux.handleDeepLink`.
- `lib/plux/plux_options.g.dart` is the file `plux init` writes, now with the channel.
- Release signing on Android reads `android/key.properties` when present. The iOS
  script uses `ios/ExportOptions.plist`, which the CI templates write, or Xcode's
  automatic signing.
- The push entitlement is `development`; App Store signing with a distribution profile
  uses the profile's.
- `--plux-path` makes the project depend on a checkout of `plux_flutter` until it is
  published.

**Proofs.** `TestGeneratedAppAgainstTheServer` (`make e2e-starter`) proves `GEN-002`,
`GEN-005` and `GEN-006` against a server built from source:

1. It publishes the starter fixture and generates its project.
2. A team adds a native route and a slot, and `plux native scan` finds both.
3. A newer release changes a title and adds a page that uses the slot and the route.
4. The generated app then runs:
   - from its embedded release, whose title it shows: the newer release was already
     published, so a first sync would have shown the new one;
   - after a relaunch, from the newer release, without a rebuild;
   - through the page that uses the native code.
5. In the device jobs, the same test runs the project's own launch test on the emulator
   or simulator.

## Consequences

- **Positive.**
  - One deterministic generator serves the CLI and, later, the server.
  - Generated projects are plain embedded-mode apps.
  - Shell settings are recorded for P11's detection.
  - No dependency is added.
- **Negative.**
  - The templates are ours to keep in step with Flutter's. The build-and-launch job makes
    drift visible, but updating them is work at every Flutter pin bump.
  - The in-house resize is simpler than an image library's, which is enough for icons.
- **Follow-up.**
  - R9 builds the library, the CLI outputs, the job, and the `GEN-005` and `GEN-006`
    tests (done, *As built in R9*).
  - P11 builds the endpoint and `GEN-004`.

## Options in detail

### Option 2: `flutter create`, then patch

The output would always be the current Flutter's template. But the Flutter SDK would be
needed on every machine that generates, P11's server included. The patches break when the
template changes, and the output is not reproducible across machines.

### Option 3: a Dart template generator

It would fit Flutter developers' tools. But it is a new dependency outside the allowlist,
it needs a Dart runtime on the server in P11, and Go's `text/template` already covers the
need.
