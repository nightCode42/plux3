---
title: Plux
description: Server-driven UI and plugins for Flutter.
template: splash
---

Plux is a server-driven UI and plugin platform for Flutter. You design apps, plugins,
pages and actions; the Plux Server validates, compiles and signs them into bundles with
binary deltas; the `plux_flutter` runtime syncs every plugin at app start, verifies it,
and renders it as native widgets.

## Start here

- [Concepts](../../../../docs/guides/concepts.md): apps, plugins, pages, releases and what the
  runtime does with them.
- [Publish a page and see it on a device](../../../../docs/guides/first-release.md): the
  Phase 3 loop on your machine, from `make dev` to an offline relaunch.
- [Run it locally](../../../../docs/guides/run-locally.md): everything you can run on your
  machine — the stack and app, the CLI, tests, end-to-end runs, benchmarks and size.
- [Host app guide](../../../../docs/guides/host-app.md): add the runtime to a Flutter app, start
  it, show published pages, mix them with native screens and widgets, hand it links and
  notifications, embed it in native Android and iOS apps, and control sync, theme and consent.
- [Routing guide](../../../../docs/guides/routing.md): route names, parameters and results,
  the host's screens, guards, links and notifications.
- [Typed API and host setup](../../../../docs/guides/typed-api.md): `plux init`, `plux codegen`
  and the native catalogue.
- [No-code apps](../../../../docs/guides/no-code-apps.md): generate, build and ship the store
  project of an app built entirely in Plux with `plux create`.
- [Actions](../../../../docs/guides/actions.md): triggers, repeated triggers, retries,
  optimistic updates, errors and flows.
- [State](../../../../docs/guides/state.md): scopes, computed entries, watchers, persistence,
  migrations and state shared with the host.
- [Forms](../../../../docs/guides/forms.md): typed fields, validators, inputs and submitting.
- [Data](../../../../docs/guides/data.md): REST and GraphQL sources, mutations, live streams
  and offline work.
- [Database](../../../../docs/guides/database.md): collections, watched queries, migrations
  and the key-value store.
- [Animation](../../../../docs/guides/animation.md): implicit motion, timelines, gestures,
  page transitions, Lottie and Rive.
- [Testing](../../../../docs/guides/testing.md): declarative scenarios with `plux test`, and
  mocking a backend.
- The [starter app](https://github.com/nightCode42/plux3/tree/main/apps/starter) is a complete
  host to copy from, and the reference apps
  [Plux Bank](https://github.com/nightCode42/plux3/tree/main/apps/plux_bank) and
  [Plux Express](https://github.com/nightCode42/plux3/tree/main/apps/plux_express) show the
  features of Phase 5 in two real flows; the ten-minute quick start arrives with Phase 10.

## Reference

- [Widgets](../../../../docs/reference/widgets.md), [PXL](../../../../docs/reference/pxl.md),
  [document model](../../../../docs/reference/document-model.md),
  [errors](../../../../docs/reference/errors.md), [limits](../../../../docs/reference/limits.md),
  [CLI](../../../../docs/reference/cli.md) and the [API](../../../../docs/reference/api.md).
- [Specification](../../../../docs/requirements.md) and
  [architecture decisions](../../../../docs/adr/README.md).
- [Security](../../../../docs/security/README.md) and the
  [threat model](../../../../docs/security/threat-model.md),
  [runbooks](../../../../docs/runbooks/README.md)
  and [benchmarks](../../../../docs/benchmarks/README.md).
