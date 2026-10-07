<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Actions Guide

How a Plux page does things: action graphs that triggers start, with policies for repeated
triggers, retries, optimistic updates, error handling and reusable flows. The examples are
the `features`, `state` and `triggers` conformance projects
([`schema/testdata/documents`](../../schema/testdata/documents)) and the reference apps.
How the engine runs them is the [action engine reference](../reference/action-engine.md);
every action and its inputs are in the [action reference](../reference/actions.md). The
design is [ADR-0045](../adr/0045-action-engine-completion.md).

## 1. A graph

A graph is a list of typed steps. The first runs first; each names the next one, or a
branch, or the step to go to on an error (`ACT-001`). Inputs are PXL over the page, the
trigger's `event` and earlier steps' outputs (`steps.<id>.output`):

```json
{"steps": [
  {"id": "submit", "action": "submitForm", "input": {"form": "transfer"}, "onSuccess": "send", "onError": "invalid"},
  {"id": "send", "action": "apiCall", "input": {"operation": "api.transfer", "input": {"$expr": "…"}},
   "onSuccess": "done", "onError": "failed"},
  {"id": "done", "action": "navigate", "input": {"route": "result", "params": {"ok": true}}}
]}
```

The compiler checks every graph against the types of its inputs and outputs, and refuses
a cycle, including one through flows of other plugins. Every run is bounded in steps,
`forEach` items and time by the limits registry (`ACT-005`).

## 2. Triggers

Besides widget events (`onPressed`, `onChanged`, `onSubmitted`, `onRefresh`,
`onEndReached` and the others of `ACT-002`), a page declares lifecycle triggers
(`onInit`, `onEnter`, `onResume`, `onLeave`, `onDispose`), timers, state watchers and
data-source events. The app and plugins declare app lifecycle (`onAppResume`,
`onAppPause`), `onPushOpened`, host events, timers and watchers:

```json
"triggers": {
  "watch": [{"path": "app.pings", "handler": {"steps": [ … ]}}],
  "dataSources": {"courier": {"onSynced": {"steps": [ … ]}}}
}
```

## 3. Repeated triggers

A handler's `concurrency` says what happens when its trigger fires again while it runs
(`ACT-003`): `parallel`, `drop`, `restart`, `queue`, `debounce:<ms>` or `throttle:<ms>`.
Widget events and timers `drop` by default, so a double tap never submits twice. Watchers
`restart`, so the latest value wins. Lifecycle, push, host and data-source events `queue`,
so none is lost. A search box waits for the user to stop typing:

```json
"onChanged": {"concurrency": "debounce:300", "steps": [ … ]}
```

A run belongs to its page or component and is cancelled with it. Mark a handler
`detached` for work that must finish after the user leaves, such as saving a draft
(`ACT-004`).

## 4. Retries and timeouts

A step may retry on the error kinds it lists, with exponential backoff and jitter
(`ACT-006`), and set its own timeout, shorter than the registry's:

```json
{"id": "archive", "action": "invokeFunction", "input": { … },
 "retry": {"count": 2, "backoffMs": 100, "maxBackoffMs": 1000, "jitter": true, "on": ["network", "timeout"]},
 "timeoutMs": 5000, "onSuccess": "show", "onError": "stop"}
```

Retry only what is safe to repeat: a read, or a mutation the server de-duplicates.

## 5. Optimistic updates

`apiCall` takes `optimistic`: state entries to set at once, rolled back if the call fails
(`ACT-007`). `patchState` with `"optimistic": true` is rolled back if a later step of the
run fails. A value someone else wrote in the meantime is kept:

```json
{"id": "like", "action": "apiCall",
 "input": {"operation": "posts.like", "input": {"id": {"$expr": "params.id"}},
           "optimistic": {"page.liked": true}}}
```

## 6. Errors

A failure is typed: `network`, `http` (with its `status`), `timeout`, `validation`,
`function`, `permission`, `cancelled` or `custom` (`ACT-020`). It goes to the step's
`onError` first, then to the page's error handler, the plugin's and the app's. A handler
reads the error as `event` and may handle it or pass it on with `stop`. An error nobody
handles shows a themed message on the page and is reported. Plux Bank's login tells a wrong
password from a lost connection by `steps.login.error.status == 401`.

## 7. Flows

A plugin's action graphs with typed `inputs` and an `output` are flows. `callFlow` runs one
inside the current run, sharing its bounds and its cancellation. A graph marked
`"exported": true` can be called from other plugins, and a flow always runs with its own
plugin's state and capabilities (`ACT-061`).

## 8. See what ran

Every run is traced: its trigger, its steps with their timing, its status and its errors
(`ACT-030`). Debug builds also record values, except sensitive ones (`ACT-031`).
`plux_devtools` shows the traces, and each run is a task on the DevTools timeline.
