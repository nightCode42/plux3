# Publish a Page and See It on a Device

A walkthrough of the Phase 3 loop on your machine: run a Plux server, run the starter host
app on an emulator, change a published page, and watch the device pick up the new release.
It takes about fifteen minutes, most of it the first build. Background: [concepts](concepts.md).

## 1. Prerequisites

- Docker, for the server stack.
- Go and Flutter at the versions the repository pins (`make help` lists the setup targets;
  `make setup` installs the rest).
- An Android emulator, an iOS simulator or a device, running.

## 2. Start the stack and the starter app

```bash
make dev
```

On the first run this builds and starts the server, PostgreSQL and object storage, seeds an
organisation with an administrator, publishes the starter project to its `starter` app and
promotes it to `staging`, pulls that release into the app as its baseline
(`apps/starter/assets/plux`), and starts the app under `flutter run`. The seeded credentials
are in `deploy/compose/.secrets/dev.env`, readable only by you.

The app shows a native home screen with a Plux page inside it — text, an icon and an image,
rendered from the release — and a tile with the sync state. Tap **Open welcome** to see
the same page pushed with `Plux.open`.

## 3. Change the page

The starter project is [`schema/testdata/documents/starter`](../../schema/testdata/documents/starter).
Copy it, and change the page's text in `translations/en.json`:

```bash
cp -r schema/testdata/documents/starter /tmp/my-starter
$EDITOR /tmp/my-starter/translations/en.json   # change "Welcome to Plux"
```

Validate it without a server — the compiler reports every problem with its JSON path:

```bash
cd backend && go run ./cmd/plux validate /tmp/my-starter
```

## 4. Publish and promote

```bash
. ../deploy/compose/.secrets/dev.env
PLUX_TOKEN=$PLUX_DEV_TOKEN go run ./cmd/plux publish -C /tmp/my-starter \
  --server http://localhost:8080 --org "$PLUX_DEV_ORGANIZATION" --app "$PLUX_DEV_STARTER" \
  --env staging --promote staging
```

The server imports the project as drafts, compiles and signs a new version of the app and
the `welcome` plugin, creates a release of exactly those versions, points the `staging`
channel at it and waits until the worker has signed the channel's manifest.

## 5. See it on the device

Tap the sync tile. The runtime fetches the manifest, downloads the changed bundles as
deltas, verifies them and stages the release; the tile says so. The page on screen does not
change under you: the release activates when no Plux page is shown, or at the next start —
press `R` in the `flutter run` terminal to restart, and the new text appears.

Stop the server (`docker compose -f deploy/compose/compose.yaml stop`) and restart the app:
it still renders the new release, from its store, offline.

## 6. What to try next

- Break the page — give a required prop a value of the wrong type — and publish: the compiler
  refuses it with a diagnostic and nothing reaches the device.
- Roll back with `plux release rollback --env staging <sequence>`: devices move to a new,
  higher sequence with the older content.
- Read the [host app guide](host-app.md) to add the runtime to your own app, and the
  [runtime runbook](../runbooks/runtime.md) for what can go wrong on devices.
