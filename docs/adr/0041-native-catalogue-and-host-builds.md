# 0041. Native catalogue and host builds: one registration, a static scanner, validation per build

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `WGT-030`, `WGT-032`, `WGT-033`, `HST-001`, `HST-031`, `ACT-060`, `CLI-006`, `NAV-002`, `REL-080`, `SCH-032`

## Context and problem

Plugins use what the host app's native code provides. There are three kinds:

- **native routes**, as navigation targets with typed parameters and results (`NAV-002`);
- **native slots**, existing widgets placed inside plugin pages (`WGT-030`, `WGT-033`);
- **custom actions**, host functions called from action graphs (`ACT-060`).

The spec asks for these things:

- Hosts expose them without changing existing code, beyond one registration point at
  start-up (`HST-031`).
- `plux native scan` builds the host's catalogue by static analysis, with slot props read
  by the Dart analyzer from constructor parameters (`WGT-030`, `CLI-006`).
- `plux native sync` uploads the catalogue for one host build.
- A publish is validated against the catalogue of every host build the release targets.
  A build that lacks something gets the newest compatible release (`WGT-032`,
  `REL-080`).

What exists today:

- The catalogue document (`native-catalogue.schema.json`, `SCH-032`) has the host build,
  routes with parameters and a result, slots with props and typed events, and actions with
  inputs and an output.
- The compiler reads a project's catalogue to check `navigate` steps to native routes,
  slot nodes and `callNative` steps.
- The app bundle's `Meta.native_catalogue` names the catalogue it was compiled against.
- Devices report a free-form `hostBuild` string (`PluxConfig.hostBuild`).

Nothing yet stores catalogues per build, registers anything at run time, or scans a host.

## Decision drivers

- **No change to existing host code** beyond the start-up registration (`HST-031`).
- **Static analysis only.** The scanner never runs host code (`CLI-006`).
- **Deterministic output**, so a committed catalogue changes only when the host does.
- **Development-only tooling.** `analyzer` must never reach a shipped package (allowlist,
  P4 plan A5).
- **A device never gets a release it cannot run** (`REL-080`). The publisher sees how
  many devices a release leaves behind.
- **Untrusted input at the boundary.** Plugins are remote content. Values crossing into
  host code are checked against the catalogue's types at run time as well as at compile
  time.
- **Tenant isolation.** Catalogues are tenant data, under row-level security like every
  tenant table (`SRV-022`).

## Considered options

For the scanner, which carries most of the risk:

1. **A Dart package built on `analyzer`, `plux_native_scan`, run with `dart run` in the
   host project by the Go CLI.**
2. **A Dart parser in Go**, inside the CLI.
3. **Ask the running app.** A debug build reports its registrations to the CLI.

## Decision

Chosen option: **1.**

- `WGT-030` names the Dart analyzer.
- Option 2 would have to resolve Dart types, imports and generated code, which only the
  analyzer does correctly.
- Option 3 runs host code, needs a device, and cannot see slot constructor types.

### One registration point (`HST-031`, `ACT-060`)

Everything is registered in the `PluxConfig` passed to `Plux.initialize`:

```dart
Plux.initialize(PluxConfig(
  hostBuild: '1.4.0+52',
  nativeRoutes: {
    'profile': PluxNativeRoute<ProfileParams, bool>(
      params: ProfileParams.fromJson,
      builder: (context, p) => ProfileScreen(userId: p.userId),
    ),
  },
  nativeSlots: {
    'MapCard': PluxNativeSlot(
      (context, slot) => MapCard(
        zoom: slot['zoom']! as double,
        onPan: (offset) => slot.emit('onPan', offset),
      ),
    ),
  },
  nativeActions: {
    'openScanner': PluxNativeAction<ScanIn, String>(
      input: ScanIn.fromJson,
      handler: (scan) => scanner.scan(prompt: scan.prompt),
    ),
  },
  router: PluxGoRouter(myGoRouter), // optional: routes discovered by plux_go_router
));
```

Existing widgets, routes and functions are referenced, never modified. A route's and an
action's type arguments are what `plux native scan` reads; `params` and `input` build them
from the JSON form the catalogue already checked, and a result or output may be converted
back with `result` or `output`. A slot's builder reads its props and emits its events; the
scanner reads the props from the widget's constructor. Calling a constructor tear-off with
named arguments built at run time would break in builds made with `--obfuscate`, so slots
take a builder.

With a router adapter (ADR-0040), the host's existing routes are discovered from the
router and need no registration: named `GoRoute`s, and `auto_route`'s routes by name. `plux_flutter` cannot name a router's type without
depending on it, so the adapter wraps the router (`PluxGoRouter(myGoRouter)`,
`PluxAutoRoute(myRouter)`) behind the core's `PluxRouterAdapter` interface: its navigation
delegate, the routes it discovers and its navigator key (maintainer, P4 plan A22).

### `plux.yaml` and the scanner (`WGT-030`, `CLI-006`)

`plux.yaml`, at the host project's root, lists the class names of the slot widgets, and
optionally the host build ID. `plux init` writes it (R8).

`plux native scan` runs `dart run plux_native_scan` in the host project.
`plux_native_scan` is a development-only package built on `analyzer`, which no shipped
package depends on. It reads:

- **Routes.**
  - `GoRoute(name: …)` declarations and `auto_route` annotations;
  - or the `nativeRoutes` registration;
  - parameter and result types from the route's type arguments.
- **Slots.** The constructor parameters of each class that `plux.yaml` lists:
  - the parameter types become props;
  - callback parameters named `on…` become typed events.
- **Actions.** The `nativeActions` registration, with input and output types from its
  type arguments.

Dart types map to the document type system. A type with no mapping is reported with its
source location, and the entry is left out, never guessed.

As built in R6:

- `plux native scan` reads `plux.yaml` and the host's `pubspec.yaml` and passes the slot
  classes, the host build, an optional `appId` and an optional `catalogueId` to
  `dart run plux_native_scan`, so the scanner depends on `analyzer` and `path` alone.
  `plux.yaml` keeps to top-level `key: value` lines and lists of names, which the Go CLI
  reads without a YAML library ([CLI reference](../reference/cli.md)).
- Routes come from named `GoRoute`s, whose path parameters are strings; from auto_route
  `@RoutePage` classes, named as auto_route names them (`ProfilePage` → `ProfileRoute`),
  whose `@PathParam` and `@QueryParam` parameters are theirs (auto_route declares no
  result type); and from `nativeRoutes`, where `PluxNativeRoute<P, R>` takes its parameters
  from the constructor of the class `P` and its result from `R`. A registration wins over a
  discovered route of the same name, as at run time.
- "Left out" is per entry: an optional parameter, or a slot prop, of a type with no
  mapping is left out (the host's builder supplies such a prop); a route or action that
  requires one, or an auto_route page that requires a typed argument, is left out
  entirely, since plugins could not open or call it. Slot events take the callback's one
  parameter as payload; a callback with more is left out.
- The catalogue keeps the identifier of the file it replaces, so scanning unchanged code
  writes the same bytes. The output is
`plux.catalogue.json`, a native catalogue document. It is deterministic: keys and entries
are sorted, and it holds no timestamps and no absolute paths. Fixture host apps
(`go_router`, `auto_route`, plain, and slots with every parameter shape) test the scanner.

### Host builds and `plux native sync`

A **host build** is identified by the string devices report as `PluxConfig.hostBuild`. By
default `plux native scan` and `sync` take the host's `pubspec.yaml` version, for example
`1.4.0+52`, unless `--build` or `plux.yaml`'s `hostBuild` names another. The runtime cannot
read the host's `pubspec.yaml`, so `PluxConfig.hostBuild` has no default: the host passes
the same string (the code R8 generates does). The catalogue's `host.version` and
`host.build` describe it.

`plux native sync --build <id>` uploads the catalogue through a new
`NativeCatalogueService`:

| Call | Does | Permission |
|---|---|---|
| `UploadNativeCatalogue(app, host_build, catalogue)` | Stores the catalogue of one build | `release.publish` |
| `GetNativeCatalogue(app, host_build)` | Returns it | `app.read` |
| `ListHostBuilds(app)` | Lists builds with their upload times and device counts | `app.read` |

- **Permission.** An upload changes which releases devices of that build receive, as a
  publish does, so it requires `release.publish`. No new permission is added.
- **Immutability.** A build's catalogue cannot change once uploaded, because its native
  code does not change after it ships. Uploading the same content again is a no-op, and
  different content for an existing build is refused (`PLX-8032`).
- **Storage.** Catalogues are stored canonicalised (RFC 8785, ADR-0025) with their
  SHA-256, per app and build, under row-level security. This is a new migration.
- **Size.** The request size is bounded by `api.requestSize`. No new limit is needed.

The proto and the migration land in R6 with the code that serves them. `buf breaking`
passes, because both are additions.

### Validation at publish (`WGT-032`) and compatibility (`REL-080`)

**The project's catalogue** is the one the developer builds against, and the compiler
checks against it. Everything a plugin uses that it lacks is an error, with its JSON path,
as today.

**At publish and at release**, the server checks every native route, slot and custom
action that the release's plugins use against the catalogue of every host build of the
app.

- A build whose catalogue lacks a used entry, or declares it with other types, is
  **incompatible**. The diagnostics name the entry, the build and the using document's
  JSON path.
- REL-080's compatibility check (P2/P3: runtime versions) also counts host builds.
  - An incompatible build's devices keep receiving the newest compatible release.
  - The publisher sees how many devices that affects before approving.
- Devices that report no host build, or a build with no catalogue, are judged by their
  runtime version alone, as today.

As built in R6:

- Each publish records the native entries its draft uses, with the types it was compiled
  against, and a release records its versions' together. A build whose catalogue arrives
  after a release is judged by them, without compiling again.
- A publish warns with `PLX-8054` at each use a build lacks, in the draft's own files; the
  publisher acknowledges it as any warning.
- A build's fallback is the newest earlier release it can run; in a production
  environment, only a release once promoted to production.
- The worker signs, with each channel manifest, a manifest of the fallback release for
  every build that cannot run the channel's release. A device of that build receives it
  while the channel manifest it was signed with is the newest; a build with no fallback
  receives the channel's. Uploading a catalogue has every channel of the app signed again.
- `GetCompatibility` lists the builds that cannot run a release, with their devices,
  fallback and missing entries, and counts their devices among the incompatible ones.
- The device's token carries the build it registered or last reported, so choosing its
  manifest costs no query.

### Runtime

The app bundle carries the declarations of the catalogue it was compiled against — each
route's parameters and result, each slot's props and events in the catalogue's order, each
custom action's inputs and output — as optional fields of its schemas section (maintainer,
P4 plan A24). Slot nodes address props and events by index into them, and every value
crossing into host code is checked against them on the way in and on the way out. A host
handler, route conversion or slot builder that throws reports `PLX-4205` with the
exception's type only, since its message may hold the user's data.

- **Native routes (`NAV-002`).** `navigate` to a native route opens it through the
  registration, with its parameters checked. Its typed result reaches a graph through a
  presenting step, `openDialog` or `openBottomSheet`, whose output the compiler types by the
  catalogue's result: Appendix D gives `navigate` no output. A route that is not
  registered reports `PLX-4200` and takes the step's `onError`; a result of another type
  fails the step with a `validation` error.
- **Slots (`WGT-033`).** A slot node builds the registered widget with props from
  bindings, which update reactively like any node.
  - Layout is constraints in, size out.
  - Events start graphs, with the event's payload as `event`.
  - A failure is contained by the page's boundary (`RT-020`).
  - A slot that is not registered shows the neutral placeholder and reports `PLX-4201`.
- **`callNative` (`ACT-060`).** Inputs are checked against the action's declared types
  before host code runs, and the output after it. A step named after the custom action is
  compiled as `callNative`, and either form types `steps.<id>.output` by the catalogue's
  output.
  - An unregistered action reports `PLX-4202`.
  - A value of the wrong type fails the step with a `validation` error.
  - A host handler's exception becomes the step's error. It never escapes the run
    (ADR-0039).
- **Older runtimes** render an unknown slot as the placeholder of `WGT-014`, and have no
  way to run `callNative` or navigate. No required feature is needed.

## Consequences

- **Positive.**
  - Hosts integrate with one registration and one command.
  - Catalogues are reproducible files that can be reviewed in the host's repository.
  - No release reaches a build that cannot run it.
  - Values crossing into host code are checked on both sides.
- **Negative.**
  - The scanner follows `analyzer`'s API, so its version is pinned and fixture tests run
    on every change to it.
  - A host that builds many versions uploads one catalogue per build. CI templates from
    `plux create` do this (ADR-0024).
- **Follow-up.**
  - R6 delivers the registration, slot nodes, `callNative`, the scanner, the CLI
    commands, the service, the migration, publish validation and per-build compatibility.
  - Studio's catalogue view of slots and actions is P11's (`WGT-030`, `ACT-060`).

## Options in detail

### Option 2: a Dart parser in Go

There is no `dart run` step and no Dart SDK needed by the CLI. But resolving a type
through imports, `part` files, typedefs and generated code is the analyzer's job, and a
partial reimplementation would mis-type props silently. The spec names the analyzer.

### Option 3: the running app reports its registrations

The registrations would be exact. But it needs a device or an emulator, runs host code,
and still cannot derive slot props from constructors, so it fails `CLI-006`'s "static
analysis".
