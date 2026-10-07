<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Forms Guide

How a Plux page collects input: a form of typed fields with validators, inputs bound to
it, and actions that validate and submit it (`STA-020`). The example is Plux Bank's
transfer form
([`transfer.page.json`](../../schema/testdata/documents/plux_bank/plugins/banking/pages/transfer.page.json)).
Every option is in the [forms reference](../reference/forms.md). The design is
[ADR-0047](../adr/0047-forms-validators-regex-and-phone.md).

## 1. Declare the form

A page or component declares its forms. Each field has a type, an initial value and
validators, which run in order and stop at the first that fails:

```json
"forms": [{"name": "transfer", "fields": [
  {"name": "toIban", "type": "string", "initial": "", "validators": [
    {"kind": "required", "message": "Enter the recipient's IBAN"},
    {"kind": "iban", "message": "Enter a valid IBAN"}]},
  {"name": "amount", "type": "string", "initial": "", "validators": [
    {"kind": "required", "message": "Enter an amount"},
    {"kind": "decimalPrecision", "maxIntegerDigits": 9, "maxScale": 2, "message": "Use at most two decimals"},
    {"kind": "custom", "message": "The amount must be above zero and within your balance",
     "rule": {"$expr": "int(mul(decimal(value), 100d)) > 0 && int(mul(decimal(value), 100d)) <= form.balanceCents"}}]},
  {"name": "reference", "type": "string", "initial": "", "validators": [
    {"kind": "length", "max": 40, "message": "Use at most 40 characters"}]},
  {"name": "balanceCents", "type": "int", "initial": 0}
]}]
```

The built-in validators are `required`, `length`, `range`, `regex`, `email`, `phone` (by
region, the device's by default), `iban` (with its checksum), `dateRange`,
`decimalPrecision`, `custom` (PXL over `value` and `form`) and `async`: a flow that checks
a value against a server, run once the field has stopped changing for `debounceMs`. A
custom rule can read other fields, as the amount reads `form.balanceCents`.

## 2. Bind the inputs

The form's state lives in its owner's scope: `values`, `errors`, `dirty`, `touched`,
`status`, `valid` and `validating`. An input shows a value and writes it back, and marks
the field touched:

```json
{"type": "TextFormField", "testId": "toIban",
 "props": {"value": {"$expr": "page.transfer.values.toIban"}},
 "events": {
   "onChanged": {"steps": [{"id": "write", "action": "setState",
     "input": {"path": "page.transfer.values.toIban", "value": {"$expr": "event"}}}]},
   "onEditingComplete": {"steps": [{"id": "touch", "action": "setState",
     "input": {"path": "page.transfer.touched.toIban", "value": true}}]}}}
```

Show a field's error once it is touched, with
`page.transfer.touched.toIban ? page.transfer.errors.toIban : null`. Inside a `FormScope`
naming the form, the subtree reads the form as `form` instead.

## 3. Submit

`submitForm` validates every field. If the form is valid, it sets `status` to `submitting`
and outputs the values, which the next step sends. If the form is invalid, it takes the
step's `invalid` branch when one is wired, and fails with a `validation` error otherwise:

```json
{"id": "submit", "action": "submitForm", "input": {"form": "transfer"},
 "onSuccess": "send", "onError": "invalid"}
```

`status` becomes `succeeded` or `failed` when the run ends, so a button can show progress
with `page.transfer.status == "submitting"`. Taps `drop` while a run is active, so a
double tap never submits twice ([actions guide](actions.md)). `validateForm` validates
without submitting, and `resetForm` restores the initial values.

## 4. Regular expressions and phone numbers

`regex` validators and PXL's `matches` use a bounded, linear-time subset of RE2, checked
when the release is published, so no pattern can hang a device. Phone numbers are
validated by region with libphonenumber's metadata. Both are runtime features
(`pxl.regex.v1`, `pxl.phone.v1`) that the release requires when it uses them.
