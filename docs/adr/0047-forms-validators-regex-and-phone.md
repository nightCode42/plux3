# 0047. Forms and validators, with one regex engine and one phone table for Go and Dart

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `STA-020`, `PXL-001`, `PXL-003`, `PXL-006`, `PXL-007`, `CMP-002`, `RT-061`, `LIM-001`

## Context and problem

`STA-020` asks for forms with first-class state and a set of built-in validators: required,
length, range, regex, email, phone number by region (E.164, default region from the device
locale), IBAN with its checksum, date range, decimal precision, custom PXL, and
asynchronous validators with debouncing. PXL's registry already declares the groups this
needs: `pxl.regex.v1` (`matches`, "a bounded RE2 subset") and `pxl.phone.v1` (`isPhone`).

Two of these cannot simply use a platform library:

- **Regex.** PXL must behave identically in the Go evaluator and the Dart VM (`PXL-007`),
  and every evaluation must terminate within its budget (`PXL-001`). Dart's `RegExp` is a
  backtracking engine with exponential cases and a syntax that differs from Go's `regexp`.
- **Phone numbers.** Validation by region needs numbering metadata. The reference source is
  Google's libphonenumber (Apache-2.0), whose full libraries are large and exist as
  separate ports per language.

The maintainer decided (plan D7, accepted in B2): one bounded, linear-time engine for an
RE2 subset, specified once, written for Go and Dart and proved by shared conformance
vectors; phone validation from a compact table generated from libphonenumber's metadata,
vendored with the maintainer's approval, with a size check; phone formatting stays P8's.

## Decision drivers

- **Identical results** in Go and Dart (`PXL-007`), proved by the same vectors.
- **Linear time and bounded memory**: no input can make a match slow (`PXL-001`), whatever
  the pattern.
- **Patterns checked at publish** (`PXL-003`): the runtime parses no expression source.
- **Size** (`RT-061`): the IPA budget (3 MiB) is the tightest, and the core ships in every
  host app.
- **Licences and provenance** of vendored data are recorded (`dependencies.md`, REUSE).

## Considered options

1. **Own RE2-subset engine in Go and Dart; phone table generated from libphonenumber's
   metadata.**
2. **Platform engines**: Go's `regexp` and Dart's `RegExp`, with patterns restricted by a
   validator; a phone library per language.
3. **Regex and phone validation on the server only**, through asynchronous validators.

## Decision

Chosen option: **1.** Option 2 cannot be proved identical — the two engines differ in
syntax, Unicode classes and, on Dart's side, in worst-case time — and the Dart ports of
libphonenumber either embed the full metadata or call platform code. Option 3 makes basic
field validation depend on the network and on a server the app may not have.

### Forms (`STA-020`)

- A form is declared on a page or a component: its fields, each with a type, an initial
  value and its validators. Its state lives in the page's or component's scope
  ([ADR-0046](0046-state-engine.md)): each field's value, `dirty` and `touched` flags and
  error messages, and the form's submit status (`idle`, `submitting`, `succeeded`,
  `failed`). Bindings read it like any other state.
- Input widgets bound to a field write its value through the state engine and mark it dirty
  and, on blur, touched.
- `validateForm` runs every validator, marks every field touched and returns whether the
  form is valid. `submitForm` validates, fails with a `validation` error carrying the
  field errors when the form is invalid, and otherwise sets `submitting` and returns the
  typed values for the next steps. `resetForm` restores the initial values and clears the
  flags.
- Messages come from the validator's declared message, or from the runtime's built-in
  strings until P8 brings localisation.

### Validators

| Validator | Rule |
|---|---|
| required, length, range | On the field's type; length counts Unicode code points |
| regex | `pxl.regex.v1`, below |
| email | One fixed pattern of the regex engine, the same in Go and Dart, documented in the forms guide; deliverability is not checked |
| phone | `pxl.phone.v1`, below; the region is the field's, or the device locale's region |
| IBAN | The structure of ISO 13616 (country, check digits, up to 30 characters), the country's length, and the mod-97 checksum |
| date range, decimal precision | On `date` and `decimal` values, using PXL's exact decimals (`PXL-005`) |
| custom | A PXL predicate over the value and the form |
| asynchronous | A graph — an `apiCall`, or a function call from P7 — run after the field has been quiet for its debounce time, with `restart` semantics so a stale result is discarded; `submitForm` waits for pending results |

The IBAN country lengths are a small table. Its source and that source's terms of use are
recorded when R3 adds it; the table is kept by hand and covered by tests.

### The regex engine (`pxl.regex.v1`)

- **Syntax, an RE2 subset.** Literals and escapes; `.`; classes `[...]` with ranges and
  negation; `\d`, `\w`, `\s` and their negations, ASCII only; anchors `^`, `$`, `\A`,
  `\z` and `\b`; groups, capturing and `(?:…)`; alternation; the quantifiers `*`, `+`, `?`
  and `{n,m}`, greedy and lazy; the flag `(?i)`, with simple case folding. Matching is on
  Unicode code points. Backreferences, look-around and Unicode property classes are not
  in v1; RE2 has no backreferences or look-around either.
- **Semantics:** leftmost-first, as RE2 and Go's `regexp`.
- **Engine:** the pattern compiles to a small instruction program, run by a Pike VM — a
  Thompson NFA simulation that keeps at most one thread per instruction — so a match takes
  time linear in the input for a given program, and memory proportional to the program.
- **At publish.** A pattern must be a constant. The Go compiler parses it, refuses
  anything outside the subset with a compile error, bounds its program size, and stores
  the compiled program in the PXL program's constants. The Dart VM runs the program and
  never parses a pattern (`PXL-003`). The program format is part of the PXL bytecode
  contract and evolves additively.
- **Go.** The Go evaluator (constant folding, server-side validation) runs the same
  program with a Go implementation of the same VM, not with `regexp`, so both sides run
  one design.
- **Bounds.** Pattern length, program size and input length are registry limits fixed in
  R3 (`schema/limits.json`); each instruction executed counts against the PXL operation
  budget.
- **Proof.** One specification of syntax and semantics lives with the PXL reference.
  Conformance vectors — pattern, input, expected match and captures, or the expected
  compile error — sit with PXL's existing vectors in `schema/testdata/pxl/` and run in Go and Dart
  (`PXL-007`). A Go fuzz test compares the engine with Go's `regexp` on generated patterns
  inside the subset, so RE2's own behaviour is the oracle.

### Phone validation (`pxl.phone.v1`)

- **Source.** The metadata file of a pinned libphonenumber release (Apache-2.0), named by
  version and SHA-256 in a lock file. The file itself is not committed: it is larger than
  the hygiene hook's limit for added files and is needed only to regenerate.
- **Table.** A generator — standard library only, in `tools/` — reduces it to one compact
  table per region: the country calling code, the national prefix, the possible national
  lengths, and the leading digits of valid numbers derived from the metadata's patterns.
  The generator is deterministic (`CMP-002`) and writes the table for Go and for Dart; both
  outputs are committed as generated code with their licence recorded in `REUSE.toml`.
- **Validation.** `isPhone(s, region)` normalises `s` to E.164 using the region's calling
  code and national prefix, then checks the length and the leading digits. It is stricter
  than a length check and looser than libphonenumber's full pattern match; the forms guide
  says so. Shared vectors drawn from libphonenumber's own test numbers run in Go and Dart.
- **Size.** The Dart table's contribution to the core is measured in R3, before it lands,
  and recorded in the size journey; the 10% size gate (`QA-007`) applies as to any change.
- **Updates.** A metadata update is a pinned-version change, regenerated and reviewed like
  any vendored source.
- **Formatting stays P8's** (plan D7). The registry lists `format.phone` in the `phone`
  group, so a runtime declaring `pxl.phone.v1` would be expected to run it. R3 puts the
  choice to the maintainer before `pxl.phone.v1` ships: move `format.phone` to the
  `format` group, which P8 delivers, or implement it in P5.

## Consequences

- **Positive.**
  - Regex and phone results are identical on the server, in the compiler and on devices,
    and a pattern cannot make a device slow.
  - Bad patterns are publish-time errors.
  - The phone table carries only what validation needs.
- **Negative.**
  - Two implementations of one engine to maintain; the vectors and the fuzz oracle are what
    keep them equal.
  - The subset excludes features some developers expect (look-around, `\p{…}`); the
    compiler's message names the construct.
  - Phone validation is not libphonenumber's full validation.
- **Follow-up.**
  - R3: forms, validators, the engine in Go and Dart, the vectors and fuzz test, the phone
    generator and table, the IBAN table; the `format.phone` question above.
  - P8: phone and IBAN formatting, localised messages.

## Options in detail

### Option 2: platform engines and phone libraries

Restricting patterns to a common subset would still leave differences in case folding,
class definitions and match semantics between Go's `regexp` and Dart's `RegExp`, and Dart's
backtracking keeps exponential worst cases that a pattern validator cannot rule out in
general. A Dart phone library adds a dependency with its own copy of the metadata to every
host app, and the Go side would use a different port.

### Option 3: server-side validation

`STA-020` names these validators as built in, and forms must validate offline. Asynchronous
validators remain available for checks that need the server, such as whether an account
exists.
