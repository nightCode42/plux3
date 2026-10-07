# 0051. Device actions, optional device packages and the capability model

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `SEC-080`, `RT-060`, `RT-061`, `WGT-032`, `HST-031`, `DAT-030`, `GEN-001`, `ACT-020`, `SEC-091`, `GOV-032`

## Context and problem

Appendix D's P5 catalogue has ten actions that touch the device: `haptic`,
`copyToClipboard`, `share`, `openUrl`, `requestPermission`, `pickImage`, `capturePhoto`,
`pickFile`, `scanCode` and `getLocation`, plus the feedback actions `showSnackbar` and
`showToast`. Each needs a platform API, most need a third-party plugin, and several need a
platform permission. `RT-060` names `plux_media` and `plux_scanner` among the optional
packages, but none for location.

`SEC-080` requires plugins to perform only operations their declared capabilities cover
(network domains, functions, device APIs, native routes), the host to approve the
capability set per app, and undeclared operations to be blocked and reported. Plugins have
declared their requested capabilities since P1 (`SCH-021`: `networkDomains`, `functions`
and the `deviceApi` values `camera`, `photos`, `files`, `location`, `contacts`,
`biometrics`, `notifications`, `clipboard`, `share`, `haptics`), but nothing yet approves
or enforces them.

The maintainer decided (plan D4, D12, accepted in B2; spec 1.3.0):

- placement: `haptic`, `copyToClipboard`, `openUrl`, `share` and `requestPermission` in the
  core; `pickImage`, `capturePhoto` and `pickFile` in `plux_media`; `scanCode` in
  `plux_scanner`; `getLocation` in a new `plux_location`, added to `RT-060`;
- `share` and `requestPermission` use the runtime's own Kotlin and Swift code or
  `share_plus` and `permission_handler`, decided after a size measurement;
- a device action whose package the host lacks fails with a typed `permission` error, and
  publishing warns when a targeted host build lacks a package a release uses;
- the approved capability set is the app document's optional `capabilities`, checked at
  publish; `PluxConfig.allowedCapabilities` narrows it at run time; undeclared operations
  are blocked and reported; governance approval of the set waits for P9's approval engine.

## Decision drivers

- **Deny by default** (`SEC-080`, AGENTS §5): an operation runs only if every party
  allowed it.
- **Apps pay only for what they use** (`RT-060`, `RT-061`): third-party plugins, native
  code and permissions stay out of the core unless every app needs them.
- **Nothing fails silently**: a missing package or permission is a typed error at run time
  and a warning at publish.
- **Security is not an edition feature** (`GOV-032`).

## Considered options

1. **Approval in the app document, narrowed by the host at run time, enforced at publish
   and on the device; device actions placed by the rule below.**
2. **Approval in `PluxConfig` only**, enforced on the device.
3. **Approval in the app document only**, enforced at publish.

## Decision

Chosen option: **1** (plan D12, option (c)). Option 2 lets a release that asks for more
than the host allows reach devices and fail there; option 3 gives a host build no way to
refuse a capability it does not want, for example a white-label build without location.

### Where a capability ships: the package placement rule

A capability ships in an optional package (`RT-060`) when it:

1. adds a third-party or native dependency;
2. needs a platform permission; or
3. adds a measurable size cost that most apps would not use.

Otherwise it belongs in the core. The core's budgets (`RT-061`: ≤ 4 MiB per App Bundle
download and ≤ 10 MiB per APK, per ABI; ≤ 3 MiB for the thinned IPA) are measured at every
milestone, and an exception to the rule is an ADR. The rule is restated in
`packages/AGENTS.md`.

Applied to P5:

| Action | Package | Platform API or library | Why |
|---|---|---|---|
| `showSnackbar`, `showToast` | core | Flutter's `ScaffoldMessenger` and an overlay | No dependency, no permission |
| `haptic` | core | Flutter's `HapticFeedback` | Flutter's own services |
| `copyToClipboard` | core | Flutter's `Clipboard` | Flutter's own services; refused on pages marked `secure` (`SEC-091`) |
| `openUrl` | core | `url_launcher` (publisher flutter.dev) | The exception below |
| `share`, `requestPermission` | core | The runtime's own Kotlin and Swift code, or `share_plus` and `permission_handler` | Decided below |
| `pickImage`, `capturePhoto`, `pickFile` | `plux_media` | `image_picker`, `file_picker` | Third-party plugins; camera and photo permissions |
| `scanCode` | `plux_scanner` | `mobile_scanner` | Third-party plugin, a vision model; camera permission |
| `getLocation` | `plux_location` | `geolocator` | Third-party plugin; location permission |

**`openUrl`, the exception.** `url_launcher` is a plugin with native code, so the rule
would put it in a package. The maintainer placed it in the core (plan D4): it is published
by the Flutter team, needs no permission, and nearly every app opens links. This ADR
records the exception.

**`share` and `requestPermission`.** Both are thin calls to one platform API (the share
sheet; the permission prompt), so the runtime's own code in `PluxFlutterPlugin` is
preferred: it adds no dependency and only the permissions an app declares, and so follows
the rule. `permission_handler` also needs host build configuration on iOS to leave out the
permissions an app does not use. R8 measures both ways on the size jobs' blank app and
records the result, and the choice, as a revision of this ADR before either lands; choosing
`share_plus` or `permission_handler` for the core would be a further exception, recorded
in that revision.

### Optional device packages

- Each package registers its action handlers with the runtime at start-up, through the
  one registration point (`PluxConfig`, `HST-031`), and uses only `plux_flutter`'s public
  API.
- An action whose handler is not registered fails with a typed `permission` error naming
  the missing package (`ACT-020`), and is reported.
- `plux native scan` records the Plux packages a host build installs in its native
  catalogue, and publishing warns when a release uses an action, widget or adapter that a
  targeted build lacks, as it does for native entries (`WGT-032`,
  [ADR-0041](0041-native-catalogue-and-host-builds.md)).
- Each package's size per ABI is measured when it is added and recorded in the size
  journey; it is not counted against the core's budget.

### The capability model (`SEC-080`)

An operation runs only if it is in all of:

1. **the plugin's request** — its `capabilities`, compiled into its signed bundle;
2. **the app's approved set** — the app document's new optional `capabilities` (spec 1.3.0),
   in the signed app bundle;
3. **the host's allowed set** — `PluxConfig.allowedCapabilities`, when the host gives one.

- **At publish**, a plugin that requests a device API or network domain outside the app's
  approved set is refused with a registered error naming each one. An app with no
  `capabilities` approves nothing, so approval is always explicit; the CLI can propose the
  field from its plugins' requests, for the developer to review and commit.
- **On the device**, every device action, every request (`DAT-030`,
  [ADR-0048](0048-data-layer.md)) and every navigation to a native route checks the three
  sets before it acts. A refusal is a typed `permission` error, reported with the plugin,
  the operation and the missing capability, never with a payload.
- **Native routes, slots and custom actions** are approved by the host's own registration:
  the host registers them, its build's catalogue records them, and publishing validates a
  release against it (`WGT-032`). The approved set does not repeat them.
- **`openUrl`** opens an `http` or `https` URL only on a domain in the plugin's
  `networkDomains`; a link into the app goes through Plux routing (`NAV-008`). Other
  schemes (`tel:`, `mailto:`) need an additive declaration in the plugin's capabilities,
  whose shape R0's schema change fixes.
- **Platform permissions.** Device actions that need one ask for it on first use, or
  through `requestPermission`; a denial is a typed `permission` error. Generated projects
  already derive the usage descriptions and manifest entries from the plugins' declared
  capabilities (`GEN-001`); a host build whose platform files lack one gets the same typed
  error, and the host guide lists what each package needs.
- **Functions** join the model in P7, and **governance approval** of the app's set (who may
  change it, with what review) in P9's approval engine. `SEC-080` stays `WIP` until then.

### Dependencies (approved by the maintainer, 2026-10-04, B2)

Each is checked before use (`dependencies.md` §1); version, maintenance and size are
recorded here when its milestone adds it.

| Library | Package | Licence | Check before use |
|---|---|---|---|
| `url_launcher` (flutter.dev) | core | BSD-3-Clause | Its federated platform packages' licences |
| `share_plus` (fluttercommunity.dev) | core, if chosen | BSD-3-Clause | Only if R8 chooses it |
| `permission_handler` (baseflow.com) | core, if chosen | MIT | Only if R8 chooses it; its iOS build configuration |
| `image_picker` (flutter.dev) | `plux_media` | BSD-3-Clause | Whether its Android implementation carries code under another licence (Apache-2.0 is expected), recorded from its `LICENSE` files |
| `file_picker` | `plux_media` | MIT | Its platform dependencies |
| `mobile_scanner` | `plux_scanner` | BSD-3-Clause | On Android it uses Google's ML Kit barcode scanning, whose SDK and model are distributed under Google's ML Kit terms, not an open-source licence. R8 records whether the model is bundled or downloaded through Google Play services, and puts acceptance of those terms to the maintainer before the package is added. On iOS it uses Apple's Vision framework. |
| `geolocator` (baseflow.com) | `plux_location` | MIT | Its platform packages' licences |

`mobile_scanner` is preferred to scanners that bundle their own copy of a vision model or a
proprietary SDK, because it uses the platform's model where one exists.

## Consequences

- **Positive.**
  - Capabilities are enforced three times, from three owners, and every refusal is typed
    and reported.
  - The core keeps only what every app uses; device plugins and their permissions arrive
    only with the package that needs them.
  - Missing packages are found at publish, not by users.
- **Negative.**
  - App developers maintain an approved set beside their plugins' requests; the CLI's
    proposal reduces, but does not remove, that work.
  - The scanner on Android depends on terms the maintainer must accept.
- **Follow-up.**
  - R0: the app document's `capabilities` and the scheme declaration (schema).
  - R4: domain enforcement in the data layer.
  - R8: the device actions, the three packages, the capability checks, the scan of
    installed packages and the publish warning; the `share`/`requestPermission` revision.
  - P7: function capabilities. P9: governance approval.

## Options in detail

### Option 2: `PluxConfig` only

The host lists what it allows; plugins asking for more fail on devices. Nothing checks a
release before it reaches devices, and a release could ship features no host build allows.

### Option 3: the app document only

Publishing checks the plugins against the approved set, but a host build cannot narrow it,
so every build of an app must allow everything any build allows.
