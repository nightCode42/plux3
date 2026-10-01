# Run It Locally

Everything you can run on your machine, in one place. Each section says what it is for and
the command to run; the linked pages have the details. Every task is a `make` target, run
from the repository root, and `make help` lists them all.

## 1. Set up once

- Install Go (the toolchain in `backend/go.mod`), Flutter 3.47, Bun 1.3, Python 3, GNU Make 4
  or later, and Docker.
- On Windows, use Git Bash with GNU Make 4 (`winget install ezwinports.make`); GnuWin32's
  make 3.81 is refused. On macOS, `brew install make` and run `gmake`, or put its `gnubin`
  first on your `PATH`.
- Install the pinned tools and git hooks: `make setup`.
- Only for running tests: start the test database with `make test-db` (Docker), and export
  the `PLUX_TEST_DATABASE_URL` it prints. Each test creates and migrates its own schema in
  it. Remove it with `make test-db-down`. Running the stack and the app (§2) does not need
  it: the stack's server migrates its own database on start.

## 2. The whole loop: server, app, publish, update

Start here. It shows everything Phase 3 delivers.

1. Start an Android emulator, an iOS simulator or a phone.
2. Run `make dev`. It starts the server stack, seeds the starter app, pulls its first
   release into the app and runs the app with hot reload (`r` reload, `R` restart,
   `q` quit).
3. In the app: a native screen with a Plux page inside it, a sync tile, **Open welcome**
   (the same page full screen), consent and theme switches, and the devtools overlay.
4. Change the page, publish it and see the device update: steps 3–5 of
   [Publish a page and see it on a device](first-release.md).
5. Stop the stack (`make compose-down`) and restart the app: it still renders the release,
   offline.

Also: `make dev-starter` points the app at the stack again and pulls a fresh baseline;
`make dev-app` runs only the app.

## 3. The server stack on its own

- Start and stop it: `make compose-up`, `make compose-down`. Seed the sample apps:
  `make compose-seed`.
- Addresses: API <http://localhost:8080>, Grafana <http://localhost:3000>, Prometheus
  <http://localhost:9090>, object storage <http://localhost:8180>.
- Seeded credentials: `deploy/compose/.secrets/dev.env`. More:
  [deploy/compose](../../deploy/compose/README.md).

## 4. The CLI

From `backend/`:

```bash
go run ./cmd/plux validate <project>        # compile locally; errors carry their JSON path
go run ./cmd/plux build <project> --json    # inspect the compiled bundles
go run ./cmd/plux publish …                 # also: keys, pull, release
```

`make build` puts the `plux` and `plux-server` binaries in `bin/`.

## 5. Tests and quality gates

- Everything CI runs: `make check`. By area: `make go-check`, `make dart-check`,
  `make studio-check`, `make repo-check`.
- Unit tests only: `make test`.
- Go: `make go-test-race`, `make go-cover` (with coverage floors),
  `make go-fuzz FUZZTIME=30s`, `make go-determinism`.
- Dart: `make dart-test`, `make dart-cover`, or `flutter test` in a package.
- Against the real stack (Docker): `make compose-test`.

How tests are organised: [testing](../engineering/testing.md).

## 6. End-to-end

- The starter app's flows against a server built from source, on this machine (about
  30 s): `make e2e-starter`.
- Old and new runtimes against old and new servers: `make compat`.
- On an emulator or simulator, as CI runs them: `make e2e-android`, `make e2e-ios` (slow;
  they create their own device). Details: [test/e2e](../../test/e2e/README.md).

## 7. Benchmarks and size

- Rendering and start-up, in profile mode: `make bench-runtime` (under xvfb on Linux).
- Against another commit, failing beyond 10%: `make bench-runtime-ab BASE=main`.
- On your own phone, for the reference-device numbers:
  [test/bench/runtime](../../test/bench/runtime/README.md).
- Sync on a simulated slow network: `make bench-sync`.
- What the runtime adds to an app: `make size-android` (Android SDK), `make size-ios`
  (Xcode). History: [size journey](../benchmarks/size.md).

## 8. Documentation site

Build it, checking every internal link: `make docs-site`. Preview it:
`cd site && bun run preview`.

## When something fails

- **A download fails while building the server image** (`make dev`, `make compose-up`):
  the build fetches pinned sources and retries; a network that keeps resetting connections
  (VPN, proxy) still stops it. Run the command again.
- **The app cannot reach the server.** On Android (emulator or a phone on USB),
  `make dev-app` forwards port 8080 with `adb reverse`; check that `adb` is on your path.
  On an iPhone, point the app at your machine:
  `make dev-starter DEV_ENDPOINT=http://<your machine's address>:8080`.
- **What can go wrong on a device:** [runtime runbook](../runbooks/runtime.md).
