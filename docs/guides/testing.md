<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Testing Guide

How to test what a Plux project does without a device or a server: declarative scenarios
that `plux test` runs headlessly on the real runtime (`TST-001`, `TST-002`), with data
sources mocked. The design is [ADR-0052](../adr/0052-plux-test-and-import-tools.md); the
file format is [`scenario.schema.json`](../../schema/json/scenario.schema.json).

## 1. Give nodes a test ID

Scenarios find nodes by their `testId`, never by text or position, so a translation or a
layout change does not break them:

```json
{"type": "TextButton", "testId": "place-profile",
 "slots": {"child": {"type": "Text", "props": {"data": "Profile"}}}}
```

## 2. Write a scenario

Scenario files live in the project's `tests/` folder as `*.scenario.yaml` or
`*.scenario.json`, or wherever `tests` in `plux.yaml` points. The starter app's:

```yaml
schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: opening the host's profile from a place
    page: place
    given:
      params:
        name: Harbour
      state:
        counter: 3
    steps:
      - tap: place-profile
    expect:
      - navigatedTo: profile
      - stateEquals:
          counter: 3
```

A scenario opens one `page` (or runs a plugin `flow`) and has three parts:

| Part | Holds |
|---|---|
| `given` | The page's `params`, exposed app `state`, and per data source a mock state (`loading`, `empty`, `error`, `success`) or an inline `mock` value |
| `steps` | `tap`, `enterText` (`testId`, `text`), `scroll` (`testId`, `dx`, `dy`), `waitFor` (a node becoming visible, with a timeout), and `trigger` (a host event into the app) |
| `expect` | `visible`, `notVisible`, `textEquals`, `navigatedTo` (a route), `actionCalled` and `functionCalled` (name, arguments, times), `stateEquals` (exposed app state) |

The compiler checks every scenario against the project before anything runs: an unknown
page, route, test ID, state entry or data source is an error at its line and column
(`PLX-1270`–`PLX-1276`).

## 3. Run it

```bash
plux test -C path/to/project --junit build/plux-test.xml
```

`plux test` compiles the project, signs the release with a key made for this run and
discarded after it, and runs each scenario as one Flutter widget test with the real
runtime, from a baseline, with no server. Network requests are not made: every data source
answers from its mock. The exit code is 0 when every scenario passed, 1 when one failed
or a file has errors, and 2 for a usage error. The JUnit XML is what CI reads; reporting to
Studio arrives with Studio (`P11`).

`--flutter` names the Flutter executable when it is not on the path.

## 4. Mock a backend

Two tools help when the backend is not ready:

- **`plux import openapi`** and **`plux import graphql`** write typed data sources from the
  backend's description, with mocks generated from its examples ([data guide](data.md)).
- **`plux mock <openapi-file>`** serves that description's examples, or values generated
  from its schemas with a fixed seed, on the loopback interface, so a development build can
  run against it (`TST-004`).

## 5. End to end

Scenarios test one page or flow in isolation. The reference apps (`apps/plux_bank`,
`apps/plux_express`) show the other end: their flows run against a real Plux server and
the reference backend [`test/refapi`](../../test/refapi/README.md), under `flutter test`,
on an Android emulator and on an iOS simulator ([e2e](../../test/e2e/README.md)).
