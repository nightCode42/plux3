<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Forms

Forms and validators (`STA-020`, ADR-0047), as the compiler checks them and the runtime runs
them from P5 R3. A page or component that declares `forms` requires the feature `forms.v1`,
first in runtime 0.3.0; a regex validator also requires `pxl.regex.v1`, a phone validator
`pxl.phone.v1`.

## Declaring a form

A page or component lists its forms under `forms`. Each form has a `name` and `fields`; each
field has a `name`, a `type`, an `initial` value (required unless the type is nullable,
`PLX-1165`) and `validators`, run in order. Form and field names are unique within their
owner (`PLX-1166`).

```json
"forms": [
  {
    "name": "signup",
    "fields": [
      {
        "name": "name",
        "type": "string",
        "initial": "",
        "validators": [
          { "kind": "required", "message": "Enter your name" },
          { "kind": "length", "min": 2, "max": 20 },
          { "kind": "regex", "pattern": "^[A-Za-z ]+$" },
          { "kind": "async", "$graph": "<graph id>", "debounceMs": 300 }
        ]
      }
    ]
  }
]
```

## Validators

| Kind | Options | Applies to |
|---|---|---|
| `required` | — | any field |
| `length` | `min`, `max` | strings and lists |
| `range` | `min`, `max` | numbers and numeric strings |
| `regex` | `pattern`: a `pxl.regex.v1` literal, compiled at publish (`PLX-1162`) | strings |
| `email` | — | strings |
| `phone` | `region`: ISO 3166-1; the device locale's region when absent. A literal region must be known (`PLX-1163`) | strings |
| `iban` | — (country length and the mod-97 checksum) | strings |
| `dateRange` | `min`, `max` | `date` and `dateTime` |
| `decimalPrecision` | `maxScale`, `maxIntegerDigits` | numbers and numeric strings |
| `custom` | `rule`: a PXL expression over `value` (the field's value) and `form` (the form's values), true when valid | any field |
| `async` | `$graph`: a flow returning an error message or null; `debounceMs` | any field |

Each validator may carry a `message`; without one, the runtime shows its built-in message
(localised from P8). A validator whose kind does not fit the field's type is `PLX-1160`;
options that do not fit the kind are `PLX-1161`. An asynchronous validator's graph must
return a nullable string (`PLX-1164`); it runs on the owner's action engine once the field
has stayed unchanged for `debounceMs`, and the result of a check overtaken by a newer value
is discarded. A failing check reports `PLX-5352` and leaves the field invalid.

## Form state

A form's state lives in its owner's scope under the form's name (`STA-001`): `values`,
`errors`, `dirty`, `touched`, `status` (`idle`, `submitting`, `succeeded`, `failed`),
`valid` and `validating`. Bindings read it like any state, with fine-grained rebuilds
(`STA-010`). An input writes its field with `setState` on `<form>.values.<field>` and marks
it touched on `<form>.touched.<field>`; the compiler refuses writes to any other form path
(`PLX-1167`).

## Actions

- `validateForm` runs every validator and records the errors; its output is whether the
  form is valid, and the run continues on its `valid` or `invalid` branch.
- `submitForm` validates, then sets `status` to `submitting` and outputs the values; the step
  fails with a validation error (`PLX-5350`) when the form is invalid, and `status` becomes
  `succeeded` or `failed` when the run ends.
- `resetForm` restores the initial values and clears errors, `dirty` and `touched`.

A form action that names a form its scope does not declare fails with `PLX-5351`.
