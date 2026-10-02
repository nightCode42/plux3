# Navigation Reference

How Plux routes are opened, shown and closed: by name, through one navigation delegate,
with typed parameters and results. The design is
[ADR-0040](../adr/0040-navigation-delegate-and-router-adapters.md). The actions that
navigate are in the [action engine reference](action-engine.md).

## 1. Opening a route

Every route is addressed by its app-wide name only. No caller knows which plugin holds it
(`NAV-001`, `NAV-003`).

| From | How |
|---|---|
| Native code | `Plux.open<T>(context, 'loan-calculator', params: {...})` |
| A declarative pages list | `Plux.pageFor('loan-calculator', params: {...})`, a `PluxPage` (`NAV-006`) |
| Inside a native widget tree | `PluxView('loan-calculator', params: {...})`, which embeds the page and owns no route |
| A plugin page | the `navigate`, `openDialog` and `openBottomSheet` actions |

Every navigation follows the same path:

1. The name is resolved to a page of the active release.
2. Its guards decide whether it opens, redirects or shows its fallback (§4).
3. Its parameters are checked on entry.
4. The navigation delegate changes the stack.

**Presentation.** `Plux.open`, `PluxPage` and `navigate` show a page as its page kind says:

| Page kind | Shown as |
|---|---|
| `screen` | a page |
| `dialog` | a dialog |
| `bottomSheet` | a modal bottom sheet |
| `fullscreenDialog` | a full-screen dialog |

`openDialog` presents any page as a dialog, except a full-screen dialog page, which keeps its
kind. `openBottomSheet` presents any page as a sheet.

**Stack operations (`NAV-005`).** `navigate`'s `mode` is one of:

- `push`, the default;
- `replace`;
- `clearAndPush`;
- `popUntil`, which pops until `until`, or else the target route, is on top, or only the
  first route is left.

`pop` pops the page's own route with an optional result. A page embedded with `PluxView`
owns no route, so its `pop` is refused with `PLX-4102`.

## 2. Parameters and results

**Parameters (`NAV-007`).** A page's parameters are checked against its declaration when it
is entered:

- a missing required parameter, a parameter of the wrong type, or a parameter the page does
  not declare shows the page's error fallback and reports `PLX-4101`;
- an optional parameter that is absent takes its default.

The host passes values in Dart form. `DateTime`, `Duration` and `Color` are accepted where
the type asks for them; every other value is passed in the JSON form of the
[document model](document-model.md#3-types-and-values).

**Results (`NAV-003`).** A page declares the type it returns with `result`.

- `pop`'s result is checked against that type before it leaves the page. A value of the
  wrong type is reported (`PLX-5003`), and the page pops with no result.
- `Plux.open<T>` completes with the result in its JSON form when that is a `T`. Decimals,
  dates and colours arrive as strings. Another value is reported (`PLX-5003`) and completes
  with null.
- In a plugin graph, the output of `openDialog` and `openBottomSheet` is the result, typed
  by the presented page's declaration.

## 3. Unknown routes (`NAV-011`)

A name no page has is reported with `PLX-4100`, and shows the first that exists of:

1. the page the app document names in `navigation.notFound`;
2. `PluxConfig.notFoundBuilder(context, route)`;
3. Plux's own `PluxNotFoundPage`.

A `navigate` step whose target is not a page names a native route, because the compiler
checked it ([ADR-0041](../adr/0041-native-catalogue-and-host-builds.md)). It opens the
route the host registers in `PluxConfig.nativeRoutes`, or that its router adapter
discovered, with its parameters checked against the native catalogue; a route the host
does not register fails the step with `PLX-4200`. Its result reaches a graph through
`openDialog` or `openBottomSheet`, typed by the catalogue's result and checked when the
screen returns it.

## 4. Guards (`NAV-009`)

A page is entered only when these allow it, in this order:

1. **Kill switch.** A plugin switched off shows its fallback page, before anything else
   runs (`RT-022`). A fallback page that has guards, or asks for an assurance level, gives
   way to the generic fallback: the kill switch never opens a guarded page.
2. **Assurance level.** A page whose `security.requiresAssurance` is above `AL0` shows
   its fallback. The runtime knows no higher level until attestation arrives in P6
   (`SEC-007`), so such a page fails closed.
3. **Guard graphs.** Each graph of `routeOptions.guards` runs in order and returns a
   `GuardResult` with `stop`: `allow` lets the next guard run, `fallback` shows the
   page's fallback, and a redirect opens its `route` instead, whose own guards then run.

A refusal shows the page's fallback, as a failing page does, and reports `PLX-4102`. So do
a guard that fails or ends without a result (guards fail closed), and redirects that come
back to a route already visited.

**What a guard reads.** Its page's parameters and declared initial state (`params`,
`page`), `device`, `user`, with `user.authenticated` from the auth delegate, and `flags`.
App and plugin state arrive in P5 with the actions that set them. A guard may emit host
events but not navigate: its navigation steps fail with `PLX-4102`, and a redirect is one
of its results. A redirect's `params` are text, converted by the target page's parameter
types as a deep link's are; one that does not convert is refused.

**Where guards run.** `Plux.open`, `navigate`, deep links and push payloads run the guards
before the route is pushed, so the stack holds the route they enter. `PluxView`, a
`PluxPage` and a shell's tabs run them in the page's place before anything of it builds,
and show the page they enter there, a redirect's target included, since the host owns
that part of the stack.

Every guard run records `action_run` with the trigger `guard` ([action engine](action-engine.md#6-telemetry)).

## 5. Deep links and push payloads (`NAV-008`)

`Plux.handleDeepLink(uri)` opens the page a link names, and `Plux.handlePushPayload(data)`
the page a notification names. The host calls them: with the link the platform delivered,
and from its own push SDK when the user opens a notification. Plux ships no push SDK. Both
open their route on the navigator of `PluxConfig.navigatorKey`, through the route's guards,
and complete with whether a route opened.

**Links.** The app document's `navigation.deepLinks` lists the app's hosts, its custom
schemes and its path patterns:

- An `https` link must name one of the hosts; any other scheme must be one of the custom
  schemes. A custom scheme's authority is the path's first segment, so `acme://items/42`
  and `acme:///items/42` both name `/items/42`.
- `/p/<route-name>` names the route itself, on every app.
- Otherwise the first pattern whose segments match: literal segments are equal, and a
  `{name}` segment captures that parameter, percent-decoded. A trailing slash is ignored.
- Query parameters fill the route's other parameters by name; a path parameter wins over
  a query parameter of the same name. Names the route does not declare, such as a
  campaign's, are left out.
- Text is converted by each parameter's declared type; text that does not convert shows
  the page's error fallback (`PLX-4101`).

A link nothing maps opens nothing and reports `PLX-4103`, naming only its scheme, host and
path, since a query can carry user data. A link whose percent-encoding does not decode
maps nothing. Deep links open plugin pages only: the host routes its own links.

**Push payloads.** The app document's `push` names the payload key, `plux` by default. Under
it, as an object or as that object's JSON text (the form FCM data messages carry), the
payload holds `{"route": …, "params": {…}}`. A dotted key reaches into nested objects. The
route opens as a link's does.

## 6. The user context (`HST-011`)

`Plux.setUserContext(PluxUser(id: …, attributes: {…}))` sets the pseudonymous user and their
attributes as text. Each attribute the app document's `userContext` declares is converted
to its declared type and read as `user.<name>`; one that is not set reads as null. An
attribute the app does not declare, or whose text does not convert, is left out and
reported once with `PLX-4204`, by name only. No attribute, and not the user's ID, reaches
telemetry; attributes the app declares non-sensitive may from P9, for rollouts and
experiments.

## 7. The navigation delegate

`PluxConfig.navigationDelegate` is the seam every navigation goes through. Plux describes the
route as a `PluxRouteSpec`: its name, presentation, transition, whether it is dismissible,
and a builder. The delegate changes the stack: `push`, `replace`, `clearAndPush`, `popUntil`
and `pop`.

The default, `PluxNavigatorDelegate`, drives the nearest `Navigator` with the plain API. It
needs no router and works in any app, `MaterialApp.router` apps included. `plux_go_router`
and `plux_auto_route` provide delegates for their routers (P4 R5).

## 8. Transitions (`NAV-010`)

A page's `routeOptions.transition` picks how its page route moves in:

| Transition | Motion |
|---|---|
| `platform` | The host theme's; on Android the predictive-back transition, which follows the back gesture on Android 14 and later |
| `fade` | Cross-fade |
| `slideLeft`, `slideRight`, `slideUp`, `slideDown` | Slides in, from the right, the left, the bottom or the top |
| `scale` | Grows from the centre |
| `sharedAxis` | Fades in while moving along the horizontal axis |
| `none` | No animation |

Predictive back needs `android:enableOnBackInvokedCallback="true"` on the host's
`<application>`, as the starter app sets. Custom timelines arrive in P5 with animations.

## 9. Shells and tabs

`PluxShell('main')` shows the shell of that key from the app document's
`navigation.shells`:

- a bottom navigation bar of its tabs, with labels and icons evaluated over the app scope;
- one nested `Navigator` per tab, which starts at the tab's `initialRoute` and keeps its
  stack while other tabs are shown;
- the system back gesture pops the current tab's stack first.

`switchTab` selects a tab of the enclosing shell. Without a shell holding that tab, it fails
with `PLX-4102`.

## 10. Host events (`HST-013`)

`Plux.events` is a stream of `PluxHostEvent`: the events plugins emit with `emitHostEvent`,
each with its declared name and its payload in JSON form. The app document declares them in
`hostEvents`. `plux codegen` generates a typed class per event (P4 R8).

## 11. Telemetry (`NAV-012`)

Every page that is shown records `screen_view` when it is removed, with:

- its route and plugin;
- the route that was shown before it;
- how long it was shown.

Parameters are never recorded.
