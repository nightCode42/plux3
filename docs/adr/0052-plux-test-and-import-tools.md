# 0052. Plux Test and the import tools: generated Flutter tests, YAML scenarios, OpenAPI and GraphQL libraries

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `TST-001`, `TST-002`, `TST-004`, `DAT-002`, `DAT-080`, `CLI-001`, `CMP-002`, `SEC-052`

## Context and problem

P5 brings Plux Test and the import tools:

- **`TST-001`:** declarative scenarios per page or flow — given parameters, state and
  data-source mocks; steps (tap by `testId`, enter text, scroll, wait for, trigger an
  event); expectations (visible, text equals, navigated to a route, an action or function
  called with arguments, a state value) — in YAML or in Studio.
- **`TST-002`:** `plux test` runs them headlessly in CI with Flutter's test harness and the
  real runtime, and reports JUnit XML and to Studio.
- **`TST-004`** (`SHOULD`, delivered by B1): mock servers generated from imported OpenAPI
  definitions.
- **`DAT-002`:** REST sources imported from OpenAPI 3.x and GraphQL sources from a schema,
  producing typed operations.

The CLI parses `plux.yaml` with the standard library (P4 plan A28), which handles that
file's small fixed format but not general YAML; OpenAPI documents are often YAML. The
maintainer decided (plan D8, D9, accepted in B2): libraries for OpenAPI and GraphQL, each
with its licence checked; scenarios in YAML or JSON validated by a new JSON Schema, with a
maintained YAML library; `plux test` writes a Flutter test harness around the real runtime,
as the generator does ([ADR-0024](0024-no-code-generated-projects.md)), runs `flutter
test` and writes JUnit XML; reporting to Studio waits for P11, so `TST-002` stays `WIP`.

## Decision drivers

- **The real runtime, unmodified**: scenarios exercise what devices run, including
  verification (`SEC-052`).
- **Deterministic output** of generated harnesses and imported documents (`CMP-002`), so
  they can be reviewed and committed.
- **Clear diagnostics**: a scenario or import error names its file and line.
- **Few, maintained, permissively licensed dependencies** in the CLI (Apache-2.0,
  `dependencies.md`).

## Considered options

1. **A generated Flutter test harness run by `flutter test`; scenarios validated by JSON
   Schema; `goccy/go-yaml`, `kin-openapi` and `gqlparser` in the CLI.**
2. **A scenario interpreter inside the runtime**, driven by the CLI over a debug channel.
3. **In-house YAML, OpenAPI and GraphQL parsers.**

## Decision

Chosen option: **1.** Option 2 adds test-only code paths to the shipped runtime and needs a
running app. Option 3 means writing and maintaining three parsers of large, evolving
specifications, where maintained, permissively licensed libraries exist.

### Scenarios (`TST-001`)

- A scenario file is YAML or JSON with one or more scenarios: the page or flow under test,
  its `given` (parameters, state values, and for each data source a mock and its state,
  `DAT-080`), its steps and its expectations, with the vocabulary of `TST-001`.
- A new JSON Schema in `schema/json/` defines the format; R9 fixes it, and it evolves
  additively like the other document schemas (`SCH-000`). YAML is decoded to the same data
  model and validated by the same schema, with errors mapped back to the file's line and
  column.
- Scenarios live in the project beside its documents, found through `plux.yaml`.

### `plux test` (`TST-002`)

1. Compile the project with the real compiler into a test release, signed with a key
   generated for the run, whose public key the harness embeds as its root key, so the
   runtime verifies the release exactly as on a device.
2. Generate a Flutter test project: one `testWidgets` per scenario that starts the real
   runtime on the release, applies `given`, drives the steps through `WidgetTester` (finding
   nodes by `testId`), and checks the expectations. Calls to actions and functions are
   read from the run's traces ([ADR-0045](0045-action-engine-completion.md)); routes from
   the navigation delegate; state through the runtime's state API. Generation is
   deterministic.
3. Run `flutter test` with the Flutter SDK the developer has, headless on the host.
4. Write JUnit XML (standard library `encoding/xml`), one test case per scenario, with the
   failing expectation and its file and line.

Data sources use their mocks; no network is reached. Reporting to Studio arrives in P11,
so `TST-002` stays `WIP`; authoring in Studio is P11's too, so `TST-001` stays `WIP`.

### Import (`DAT-002`)

- **`plux import openapi <file>`** reads OpenAPI 3.x in JSON or YAML with `kin-openapi`
  and writes a data-source document: one typed operation per path and method, request and
  response types converted from the document's schemas to Plux types, base URLs as
  environment variables. A construct with no Plux type is reported, with its location, and
  the operation is left out rather than typed loosely. `kin-openapi`'s coverage of
  OpenAPI 3.1 is checked in R9; if it falls short of `DAT-002`'s "3.x", the gap is put to
  the maintainer.
- **`plux import graphql <schema>`** reads the schema (SDL) with `gqlparser` and the
  operations in the given `.graphql` documents, validates the operations against the
  schema, and writes one typed operation each. Operations are not invented from the
  schema: a GraphQL operation is a choice of fields, which only its author can make.
- Output is canonical JSON, sorted, so a re-import of an unchanged source changes nothing.

### `plux mock` (`TST-004`)

`plux mock <openapi-file>` serves responses for every operation from the document's
examples, or from values generated from its schemas with a fixed seed, on a standard
library HTTP server that listens on the loopback interface unless told otherwise.

### Dependencies (approved by the maintainer, 2026-10-04, B2)

All are Go modules of the `backend` module, used by the CLI only, never by `plux-server`'s
request path. Each is checked before use (`dependencies.md` §1); versions and transitive
modules, with their licences, are recorded when R9 adds them, and any licence CI's
dependency review cannot validate is put to the maintainer before `ci.yml` changes.

| Module | Licence | Used for |
|---|---|---|
| `github.com/getkin/kin-openapi` | MIT | Parsing and validating OpenAPI documents |
| `github.com/vektah/gqlparser/v2` | MIT | Parsing GraphQL schemas and operations |
| `github.com/goccy/go-yaml` | MIT | Decoding YAML scenarios, with positions for diagnostics |

**Why `goccy/go-yaml`.** It is maintained, MIT-licensed, and keeps each node's position, so
a JSON Schema error found on the decoded data can be reported at its line in the YAML
file. `gopkg.in/yaml.v3` (MIT and Apache-2.0) is the best-known alternative, but its
original repository is no longer maintained (its continuation is `go.yaml.in/yaml/v3`,
which R9 may compare). `sigs.k8s.io/yaml`, already allowed for the server's configuration,
converts YAML to JSON and drops positions, so diagnostics could name only a JSON path.

## Consequences

- **Positive.**
  - Scenarios test the shipped runtime, including verification, with no test hooks in it.
  - Imports and harnesses are deterministic and reviewable.
  - Three small, permissive libraries replace three parsers.
- **Negative.**
  - `plux test` needs a Flutter SDK on the machine, as `plux create`'s projects do.
  - Three new modules in the CLI's dependency tree, each with transitive modules to track.
- **Follow-up.**
  - R9: the scenario schema, `plux test`, JUnit XML, the imports, `plux mock`; the starter
    and reference apps carry scenarios that CI runs.
  - P11: authoring in and reporting to Studio.

## Options in detail

### Option 2: an interpreter in the runtime

The CLI would drive a running app over a debug connection. It needs a device or emulator
for every run, adds test-only code to the runtime, and duplicates what `flutter test` and
`WidgetTester` already do.

### Option 3: in-house parsers

The CLI's `plux.yaml` reader works because that file's format is small and fixed. YAML in
general, OpenAPI's schema dialects and GraphQL's grammar and validation rules are each a
large specification; writing them would cost more than the libraries' maintenance and
would be less correct.
