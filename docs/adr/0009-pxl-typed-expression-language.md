# 0009. PXL: a typed expression language compiled to bytecode

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `PXL-001`–`PXL-007`, `CMP-004`, `CMP-022`, `CMP-023`, `SEC-054`, `QA-002`, `QA-003`

## Context and problem

Bindings, conditions and action inputs need small computations — `form.loan.valid && page.amount > 0d`, `item.status ?? "pending"` — evaluated on the device thousands of times per second (`PXL-004`: ≤ 2 µs for a typical binding). The runtime may not download executable code (`SEC-054`), may not parse source on the device (`PXL-003`), must never hang or crash (`PXL-001`), and financial values must be exact (`PXL-005`). Should Plux embed an existing scripting engine, adopt an existing expression language, or define its own?

## Decision drivers

- Pure, deterministic, total evaluation within an operation budget (`PXL-001`).
- Static types checked against the schemas available at each use site (`PXL-002`), with errors mapped to exact character ranges (`CMP-004`).
- Compact bytecode produced at publish time; a small VM on the device (`PXL-003`).
- Exact decimal and money arithmetic with explicit rounding (`PXL-005`).
- Identical results in Go, Dart and, later, the Studio language service (`PXL-007`).
- Store-policy safety: the device interprets data, it never compiles code (`SEC-054`).

## Considered options

1. **PXL: a CEL-like language, type-checked in Go, compiled to typed stack bytecode, interpreted by a VM in each runtime.**
2. Google CEL itself (cel-go on the server, a Dart port on the device).
3. An embedded JavaScript or Lua engine.
4. JSONLogic or a similar JSON-encoded rule format.

## Decision

Chosen option: **1**. The full language reference is `docs/reference/pxl.md`; the decisions it rests on are:

### Syntax

The grammar of spec Appendix E.1, plus:

- **Macros** in receiver form, as in CEL: `list.map(x, expr)`, `list.filter(x, pred)`, `list.any(x, pred)`, `list.all(x, pred)`, `list.sortBy(x, key)`. The first argument names a variable bound inside the second. These are the only receiver-style calls; every other function is called by name (`len(s)`) or by namespace (`format.money(m)`).
- `in` tests list membership and map keys.
- Literals: 64-bit integers, doubles (`1.5`, `2e3`), decimals with the `d` suffix (`12.50d`), strings in single or double quotes with `\n \t \\ \" \' \u{…}` escapes, `true`, `false`, `null`.

### Types

The value types of `SCH-010` — `string`, `int`, `double`, `bool`, `decimal`, `money`, `date`, `dateTime`, `duration`, `color`, enums, `list<T>`, `map<string,T>`, named object types — each optionally nullable (`T?`). There is no dynamic type.

- Member access on a nullable value is a compile error; `?.` propagates `null` and `??` removes it.
- `int` widens implicitly to `double` and to `decimal`; `decimal` and `double` never convert implicitly (`double(d)`, `decimal(x)`). Collections never convert: a `list<int>` is not a `list<double>`.
- An enum compares with a string literal naming one of its members; the checker rejects any other string.
- `now` is frozen for the whole evaluation.

### Semantics that must match in every language

- `int` is 64-bit; overflow and division by zero are typed errors, never wrap-around.
- `decimal` has arbitrary precision. `+`, `-` and `*` are exact; `/` on decimals is a compile error with a fix suggesting `div(a, b, scale, mode)`, because every division that loses precision must state its rounding (`PXL-005`). Rounding modes: `halfEven` (default for every rounding, also of doubles), `halfUp`, `down`, `up`, `ceiling`, `floor`.
- `money` is a decimal plus an ISO 4217 code; `+` and `-` require the same currency (a compile error for constants, a typed error otherwise); `round(m)` uses the currency's minor units.
- `double` operations that produce NaN or an infinity return a typed error, so every result is serialisable.
- Strings are sequences of Unicode code points: `len`, `substring` and indices count code points, never UTF-16 units; `upper` and `lower` use simple (one-to-one) case mapping.
- Dates are proleptic Gregorian; date-times carry their own UTC offset; there is no time-zone database, so results never depend on the device.

### Bytecode and VM

- A program is a constant pool of typed values, an instruction stream of one-byte opcodes with fixed-width little-endian operands, its maximum stack depth, its result type and its **read set** — the exact state paths it reads (`CMP-023`), from which the runtime subscribes only to what the binding uses.
- Opcodes are **typed** (`ADD_INT`, `ADD_DEC`, `ADD_MONEY`, …): the checker resolves every overload, so the VM never inspects types to dispatch and cannot meet an operand of an unexpected type.
- Standard-library functions and macros have permanent numeric IDs from `schema/pxl/stdlib.json`.
- Every instruction costs one operation; functions over strings and collections cost in proportion to their input and result, as the cost model of `docs/reference/pxl.md` specifies exactly, so Go and Dart exhaust a budget at the same instruction. When the budget (default 10,000, from the limits registry) is exhausted, evaluation stops with a typed error (`PXL-001`). Strings, collections and decimals produced are bounded by the same registry (`pxl.stringLength`, `pxl.collectionSize`, `pxl.decimalDigits`).
- Programs are verified when decoded — operands in range, jumps onto instruction boundaries, argument counts, a stack depth no larger than the code — and the VM checks operand types, so malformed bytecode fails with a typed error instead of crashing. Opcodes, constant tags and error kinds are defined once in `schema/pxl/bytecode.json` and generated for Go and Dart.
- Programs are stored in the `pxl` section and referenced by content-addressed IDs (ADR-0002).
- The VM is data-driven and has no access to I/O, the clock (except the frozen `now`) or platform APIs (`SEC-054`).

### Implementations

- **Go** (`backend/internal/pxl`): lexer, parser with byte ranges, type checker, bytecode emitter, and an evaluator used for constant folding (`CMP-022`) and server-side checks.
- **Dart** (`plux_flutter/lib/src/pxl`): bytecode decoder and VM.
- **TypeScript**: the Studio language service is the Go implementation compiled to WebAssembly (`PXL-008`, P11), so the checker is never re-implemented.
- A shared conformance suite in `schema/testdata/pxl/` — expression, typed environment, inputs, expected value or error, expected bytecode and read set — runs in every implementation (`PXL-007`, `QA-003`).

### Standard library tiers

Every function in Appendix E is type-checked from P1. Functions whose evaluation needs data that arrives in later phases — locale-aware `format.*`, `t` and `plural` (CLDR, P8), `toCalendar` and `fromCalendar` (P8), `isPhone` and `format.phone` (phone metadata, P5) and `matches` (a bounded regular-expression engine shared by Go and Dart, P5) — belong to named groups. A program that calls one adds the group's feature (for example `pxl.format.v1`) to the bundle's `required_features`, so a runtime that cannot evaluate it refuses the bundle cleanly instead of failing at render time (`BND-008`). The compiler never folds these calls.

## Consequences

- **Positive:** errors surface at publish time with exact ranges; the device runs a small, fast, typed interpreter with nothing to exploit; money is exact everywhere; one conformance suite keeps Go and Dart honest.
- **Negative:** we own a language, its checker and two interpreters; the standard library must be implemented twice and kept identical.
- **Follow-up:** the Dart VM's performance target (`PXL-004`, P5); the Studio language service (`PXL-008`, P11); the later-phase library groups above.

## Options in detail

### Option 1 — PXL

Small enough to specify completely, with the typed opcodes and bounded evaluation the device needs. The cost is ownership of two interpreters, contained by the conformance suite.

### Option 2 — Google CEL

A proven, non-Turing-complete design with good Go support, and the inspiration for PXL's syntax. Rejected: there is no maintained Dart implementation, CEL's `dyn` type and protobuf-centred type system do not match `SCH-010`, it has no exact decimal or money type, and cel-go's checked AST is not a compact, versioned bytecode we could evaluate on the device.

### Option 3 — JavaScript or Lua

Powerful and familiar, but embedding a general-purpose engine contradicts `SEC-054` and store policies, is not total, and adds megabytes to every host app (`RT-061`).

### Option 4 — JSONLogic

Data-only and easy to evaluate, but untyped, verbose to write and read, without exact decimals, and with no source ranges for errors.
