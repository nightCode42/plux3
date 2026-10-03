<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# No-Code Apps Guide

An app built entirely in Plux still needs a store binary: a Flutter project with its name,
icons, permissions and the runtime wired in. `plux create` generates that project from the
app's release, ready to build and run unchanged (`GEN-001`, `GEN-002`;
[ADR-0024](../adr/0024-no-code-generated-projects.md)). Its flags are in the
[CLI reference](../reference/cli.md).

## 1. Generate the project

Publish and promote a release first; the project embeds it, so its first launch needs no
network. Then:

```sh
plux create shop --env production --channel production \
  --name "Shop" --application-id com.acme.shop --icon icon.png --splash '#0B6E4F' \
  --out ./shop-app
```

`--zip shop.zip` writes one zip instead, and `--git <remote> --branch main` commits the
project and pushes it with your own `git` and credentials; Plux stores none (`GEN-003`).
The same release and flags always give the same files.

## 2. What it holds

- The Android and iOS projects, with the icons and launch screens made from your PNG, and
  the permissions and usage descriptions the plugins' device APIs need — nothing more.
- Push and deep links when the app document declares them: the iOS entitlement and
  background mode, the Android notification permission, intent filters, associated domains
  and URL schemes.
- `lib/main.dart`, which starts Plux with the environment's root keys and shows the entry
  route, and the release under `assets/plux/`.
- `integration_test/app_test.dart`, which launches the app on a device and waits for the
  entry page.
- `scripts/build_android.sh` and `scripts/build_ios.sh`, and GitHub Actions and GitLab CI
  templates for signed release builds. They read their secrets — keystore, certificates,
  store credentials — by name; set them in your CI.

`plux.shell.json` records the shell settings the project was made from.

## 3. Build and ship

```sh
cd shop-app
flutter test integration_test/app_test.dart -d <device>   # the entry page renders
scripts/build_android.sh                                  # a signed App Bundle
scripts/build_ios.sh                                      # a signed IPA, on macOS
```

On your machine, `android/key.properties` names the upload keystore (without it the bundle
is signed with the debug key), and `ios/ExportOptions.plist` the iOS signing; the project's
README says how.

Everything the app shows comes from its releases from then on: a new release reaches
installed apps at their next start, with no rebuild (`GEN-005`). Generate the project again
only for what lives in the binary — the name, the icon, new device permissions — and, before
each store release, so a fresh install starts from a recent release.

## 4. Add native code later

The project is a normal Flutter app. A team can add its own screens, widgets and actions,
register them in `PluxConfig` and run `plux native scan` and `sync`; plugins then use them
like any host's ([typed API guide](typed-api.md) §3, `GEN-006`).
