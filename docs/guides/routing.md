<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Routing Guide

How a Plux app's pages reach each other and the host's own screens: by an app-wide route
name, with typed parameters and results, through guards, from links and notifications. The
examples are the starter app's documents
([`schema/testdata/documents/starter`](../../schema/testdata/documents/starter)); every
detail is in the [navigation reference](../reference/navigation.md), and the actions in the
[action engine reference](../reference/action-engine.md).

## 1. Name a page

A page with a `route` can be opened from anywhere in the app, by that name alone; no caller
knows which plugin holds it (`NAV-001`). Route names are unique across the app and the host's
native catalogue. Parameters are declared with their types, and the compiler checks every
caller against them:

```json
{
  "key": "place", "kind": "page", "pageKind": "screen", "route": "place",
  "params": [{"name": "name", "type": "string", "required": true}],
  "root": {"type": "Text", "props": {"data": {"$expr": "\"Place: \" + params.name"}}}
}
```

A page that returns something declares its `result` type, and returns it with the `pop`
action (`NAV-003`).

## 2. Open it

From a plugin page, with the `navigate` action. Its `mode` pushes (the default), replaces,
clears the stack or pops back to a route (`NAV-005`); `openDialog` and `openBottomSheet`
present a page and give its result to the next steps as `steps.<id>.output`:

```json
{"events": {"onPlace": {"steps": [
  {"action": "navigate", "id": "open", "input": {"route": "place", "params": {"name": {"$expr": "event"}}}}
]}}}
```

From the host's code, with `Plux.open(context, 'place', params: {'name': 'Harbour'})`, or
typed, `PluxScreens.place(name: 'Harbour').push(context)`, which `plux codegen` writes
([typed API guide](typed-api.md)). Inside a native screen, `PluxView('place', inputs: …)`
shows the page in place, with no route of its own ([host app guide](host-app.md) §3).

## 3. Open the host's screens

The host's screens are routes too: its native catalogue declares them with their parameters
and result, and a plugin page opens them with the same `navigate` (`NAV-002`). The starter's
place page opens the host's profile screen:

```json
{"action": "navigate", "id": "go", "input": {"route": "profile", "params": {"name": {"$expr": "params.name"}}}}
```

The host registers the screen once, in `PluxConfig.nativeRoutes`, or its `go_router` or
`auto_route` routes are discovered ([host app guide](host-app.md) §3). Its custom actions are
called the same way, by name, as steps.

## 4. Guard a page

A page's `routeOptions.guards` names action graphs that decide whether it opens (`NAV-009`).
A guard returns a `GuardResult` with `stop`: `allow`, `fallback`, or a redirect to another
route. The starter's account page lets signed-in users in and sends the others to sign in:

```json
{"key": "require-sign-in", "kind": "actionGraph", "output": "GuardResult", "steps": [
  {"action": "condition", "id": "gate", "input": {"when": {"$expr": "user.authenticated"}},
   "branches": {"then": "yes", "else": "no"}},
  {"action": "stop", "id": "yes", "input": {"result": "allow"}},
  {"action": "stop", "id": "no", "input": {"result": {"decision": "redirect", "route": "sign-in"}}}
]}
```

`user.authenticated` comes from the host's auth delegate, and `user.<name>` from the user
context it sets (`HST-010`, `HST-011`). Guards fail closed: one that fails, or ends without a
result, shows the page's fallback.

## 5. Links and notifications

The app document maps links to routes. Links of the form `https://<host>/p/<route>?…` always
work for the app's hosts; patterns name the others, and query parameters fill the remaining
parameters (`NAV-008`):

```json
"navigation": {"deepLinks": {"schemes": ["plux-starter"],
  "routes": [{"path": "/places/{name}", "route": "place"}]}},
"push": {"enabled": true}
```

`plux-starter://places/Lighthouse` then opens the place page. The host hands Plux the links
the platform delivers and the notifications the user opens, with `Plux.handleDeepLink` and
`Plux.handlePushPayload` ([host app guide](host-app.md) §3); a link is untrusted input, and
one that maps nothing is reported and left to the host.

## 6. When a route is wrong

A name no page has shows the app's `navigation.notFound` page, the host's builder or Plux's
own, and is reported (`PLX-4100`, `NAV-011`); parameters that do not match are refused on
entry (`PLX-4101`). Neither reaches the host as an exception.

## 7. Shells and tabs

`navigation.shells` declares tabbed shells; `PluxShell('main')` shows one, each tab with its
own stack, and the `switchTab` action selects a tab. Transitions follow the platform, or
each route's `routeOptions.transition` (`NAV-010`).
