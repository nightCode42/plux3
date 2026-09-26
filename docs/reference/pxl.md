# PXL Language Reference

PXL is the typed expression language of Plux bindings, conditions and action inputs (spec §14.2, Appendix E, [ADR-0009](../adr/0009-pxl-typed-expression-language.md)). It is pure, deterministic and total: no loops, no assignments, no I/O, and every evaluation ends within an operation budget with a value or a typed error (`PXL-001`). Expressions are type-checked and compiled to bytecode at publish time (`PXL-002`, `PXL-003`); runtimes only evaluate bytecode. The Go implementation is `backend/internal/pxl`, the Dart runtime's is `packages/plux_flutter/lib/src/pxl`; the conformance vectors in `schema/testdata/pxl/` define the behaviour every implementation must reproduce (`PXL-007`). Every function is listed in the [standard library reference](pxl-stdlib.md).

## 1. Syntax

```ebnf
expr     = or [ "?" expr ":" expr ] ;
or       = and { "||" and } ;
and      = equality { "&&" equality } ;
equality = relation { ( "==" | "!=" ) relation } ;
relation = coalesce { ( "<" | "<=" | ">" | ">=" | "in" ) coalesce } ;
coalesce = additive { "??" additive } ;
additive = mult { ( "+" | "-" ) mult } ;
mult     = unary { ( "*" | "/" | "%" ) unary } ;
unary    = [ "!" | "-" ] unary | member ;
member   = primary { "." ident [ call ] | "?." ident | "[" expr "]" } ;
primary  = literal | ident [ call ] | "(" expr ")" | "[" [ exprs ] "]" | "{" [ entry { "," entry } ] "}" ;
call     = "(" [ exprs ] ")" ;
exprs    = expr { "," expr } ;
entry    = expr ":" expr ;
literal  = int | double | decimal | string | "true" | "false" | "null" ;
int      = digit { digit } ;
double   = digit { digit } ( "." digit { digit } [ exponent ] | exponent ) ;
exponent = ( "e" | "E" ) [ "+" | "-" ] digit { digit } ;
decimal  = digit { digit } [ "." digit { digit } ] "d" ;
string   = '"' { char | escape } '"' | "'" { char | escape } "'" ;
escape   = "\n" | "\t" | "\r" | "\\" | '\"' | "\'" | "\u{" hex { hex } "}" ;
ident    = letter { letter | digit } ;   (* ASCII letters and "_"; not true, false, null, in *)
```

- **Calls.** `name(args)` calls a standard-library function; `format.name(args)` calls a function of the `format` namespace. `list.name(x, expr)` is a **macro** — `map`, `filter`, `any`, `all` or `sortBy` — where `x` names a variable bound to each item inside `expr`; it may not hide a root or an outer variable. No other receiver calls exist.
- **Literals.** Integers are 64-bit; `-9223372036854775808` is accepted. A decimal literal has no exponent (`12.50d`). Strings may not span lines; `\u{…}` takes one to six hex digits of a Unicode scalar value.
- **Limits.** An expression has at most `pxl.expressionLength` code points and a syntax tree at most `pxl.nestingDepth` deep ([limits](limits.md)); beyond them the compiler reports `PLX-2016`.

## 2. Types

Types are the `SCH-010` types of the [document model](document-model.md#3-types-and-values): `bool`, `int`, `double`, `string`, `decimal`, `money`, `date`, `dateTime`, `duration`, `color`, `asset`, `route`, enums, object types, `list<T>`, `map<string,T>`, each optionally nullable (`T?`). There is no dynamic type. The built-in enums are `RoundingMode` (`halfEven`, `halfUp`, `down`, `up`, `ceiling`, `floor`) and `DateStyle`.

- **Roots.** An expression can read the roots available at its use site (Appendix E.2) — for example `page`, `params`, `item`, `event` — with the types of their declarations; the compiler's table of roots per use site is in [compiler.md §3](compiler.md#3-scopes). `now` is always available: a `dateTime` frozen for the whole evaluation.
- **Nullability.** `a.b` on a nullable `a` is `PLX-2006`; write `a?.b`, which yields `null` when `a` is `null`, or give a default with `a ?? b`. Operators other than `==`, `!=` and `??` reject nullable operands.
- **Widening.** An `int` converts implicitly to `double` or `decimal` where the other operand, the branch or the parameter needs it. Nothing else converts implicitly: `double(d)`, `decimal(x)`, `int(x)`, `string(x)` are explicit, and collections never convert (`list<int>` is not a `list<double>`).
- **Enums** compare with, and are passed as, string literals naming one of their members (`tier == "gold"`); any other string is `PLX-2014`.
- **Unification.** The branches of `c ? a : b`, the items of a list literal and the values of a map literal must have one type after widening; `null` makes it nullable.

## 3. Operators

| Operator | Operands | Result |
|---|---|---|
| `!` | `bool` | `bool` |
| unary `-` | `int`, `double`, `decimal`, `money`, `duration` | same |
| `+` | numbers of one kind; `money` + `money`; `string` + `string`; `list<T>` + `list<T>`; `dateTime` + `duration` (either order); `duration` + `duration` | as operands |
| `-` | numbers; `money`; `dateTime` − `duration`; `dateTime` − `dateTime` → `duration`; `duration` − `duration` | as operands |
| `*` | numbers; `money` × `decimal` (either order; an `int` widens); `duration` × `int` (either order) | as operands |
| `/` | `int` (truncates toward zero), `double` | as operands |
| `%` | `int`, `double`: remainder of truncated division, with the dividend's sign | as operands |
| `<` `<=` `>` `>=` | two values of one ordered type: numbers, `money` of one currency, `string` (by code point), `date`, `dateTime` (by instant), `duration` | `bool` |
| `==` `!=` | two values of one type, or a value and `null` | `bool` |
| `in` | `T in list<T>`; `string in map<string,T>` (a key) | `bool` |
| `&&` `\|\|` | `bool`, short-circuit | `bool` |
| `??` | `T? ?? T` | `T` |
| `?:` | `bool ? T : T` | `T` |

Decimal division with `/` is `PLX-2007`: write `div(a, b, scale)` or `div(a, b, scale, mode)`, because every division that loses digits must state its rounding (`PXL-005`).

**Equality** is deep for lists, maps and objects; decimals compare numerically (`12.5d == 12.50d`), money values are equal when currency and amount are equal (different currencies are unequal, not an error), dateTimes when they denote the same instant, doubles by IEEE 754 (`-0.0 == 0.0`).

## 4. Values and semantics

- **int** is 64-bit two's complement; results outside that range, and division or remainder by zero, are errors — never wrap-around.
- **double** is IEEE 754 binary64; a result that is NaN or infinite is the error `nonFinite`, so every value is finite.
- **decimal** is exact with arbitrary precision and keeps its scale: `1.25d * 0.4d` is `0.500`. `+`, `-` and `*` are exact; `div` and `round` take a scale and a rounding mode, default `halfEven`. A decimal may have at most `pxl.decimalDigits` digits in plain notation.
- **money** is a decimal amount and an ISO 4217 code from `schema/pxl/currencies.json`. `+`, `-` and ordering require one currency (the error `currencyMismatch`, or `PLX-2008` when both are constants); `round(m)` and `div(m, d)` round to the currency's minor units.
- **Rounding.** Every rounding defaults to half-even, including `round(double)`, which returns an `int`.
- **Strings** are sequences of Unicode code points: `len`, indices and `substring` count code points; `upper` and `lower` use simple one-to-one case mapping and `trim` the White_Space property, from the Unicode version of the Go toolchain's tables, which the Dart runtime's generated tables reproduce. Strings compare by code point.
- **date** is a proleptic Gregorian date in years 1–9999; **dateTime** is an instant in milliseconds with the UTC offset it was written in; **duration** is milliseconds. There is no time-zone database: `addDays` on a `dateTime` adds 24-hour days in its offset, so results never depend on the device.
- **Collections.** Maps iterate in code-point order of their keys (`keys`, `values`, equality). Macros evaluate their expression for each item in order; `sortBy` is stable and ascending; `any` and `all` stop at the first item that decides the result.
- **Text forms** (`string(x)`): doubles as ECMAScript `Number.prototype.toString` (`1`, `0.30000000000000004`, `1e+21`); decimals in plain notation with their scale; money as `12.50 EUR`; dates, dateTimes (milliseconds only when not zero, `Z` for offset zero) and durations (`P1DT2H`, `PT0S`) in ISO 8601; colors as `#RRGGBBAA`.

## 5. Errors

Compile-time problems are diagnostics with a `PLX-2xxx` code and the code-point range of the offending text (`CMP-004`, [error catalogue](errors.md)). A constant subexpression that always fails is reported at publish time (`PLX-2011`, or `PLX-2010` when it exceeds the budget).

At run time an evaluation stops with one typed error:

| Kind | Cause |
|---|---|
| `budgetExceeded` | More operations than the budget allows |
| `overflow` | An `int` or `duration` result outside 64 bits |
| `divisionByZero` | Division or remainder by zero |
| `nonFinite` | A `double` result that is NaN or infinite |
| `currencyMismatch` | Money in different currencies combined or ordered |
| `unknownCurrency` | A currency code not in the ISO 4217 table |
| `indexOutOfRange` | An index outside a list or string (`at`, `first` and `last` return `null` instead) |
| `missingKey` | A map without the key (`m[k]`, `m.k`) |
| `invalidArgument` | A function argument outside its domain, such as an unparsable date |
| `sizeLimit` | A string, collection or decimal beyond the limits registry |
| `duplicateKey` | A map literal with a key twice |
| `unsupported` | A function of a feature group the runtime does not implement |
| `invalidProgram`, `invalidInput` | Malformed bytecode, or an input that does not match its declared type |

## 6. Cost model

The budget is `pxl.operationBudget` operations (default 10,000). Go and Dart count identically:

- every executed instruction costs 1;
- a standard-library call also costs the size of each string, list or map argument and of its result — code points or entries;
- string and list concatenation also cost the size of the result; a list or map literal the number of its entries;
- `==`, `!=` and `in` also cost the nested values they compare;
- `sortBy` also costs n·⌈log₂(n+1)⌉ for n items.

Produced strings, collections and decimals are bounded by `pxl.stringLength`, `pxl.collectionSize` and `pxl.decimalDigits`.

## 7. Feature groups

Functions of the `core` group run everywhere. Functions of later groups — `format` and `l10n` and `calendar` (P8), `phone` and `regex` (P5) — are type-checked from P1, but a program that calls one requires the group's feature (for example `pxl.format.v1`), reported as `PLX-2013`; the bundle lists it in `required_features`, so a runtime without it refuses the bundle cleanly (`BND-008`). Evaluators that do not implement a group return `unsupported`, and the compiler never folds such calls.

## 8. Bytecode

A program (`Program.Encode`) is, in order: the encoding version (1), the number of local slots, the maximum stack depth, the result type as a type expression, the constant pool, the read set and the code. Counts and lengths are unsigned LEB128, signed values zigzag LEB128, operands fixed-width little-endian and jump targets absolute code offsets. The opcodes, operand widths, constant tags, comparison kinds and error kinds are listed in `schema/pxl/bytecode.json` and generated for Go and Dart; their codes are permanent.

- **Typed opcodes.** The checker resolves every operator and overload, so the VM never dispatches on types: `ADD_INT`, `ADD_DEC`, `ADD_MONEY`, `CMP_DATETIME`, `CALL id argc`, and so on.
- **Macros** compile to loops over an iterator in a local slot (`ITER_INIT`, `ITER_NEXT`, `ITER_LIST`, `SORT_BY`).
- **Read set** (`CMP-023`): the state paths the program reads, such as `page.amount` or `items`, sorted and without paths another path covers, so the runtime re-evaluates a binding only when what it reads changes.
- **Constant folding** (`CMP-022`): the largest constant subtrees are evaluated at compile time by the same VM, within the same limits, and replaced by their values.
- **Verification.** `Decode` rejects malformed programs — unknown opcodes, operands out of range, jumps into instructions, wrong argument counts, strings that are not valid UTF-8, and a stack depth larger than the code — and the VM checks operand types, so a malformed program fails with `invalidProgram`, never with a crash.

The bundle stores the encoded program in its `pxl` section, addressed by the first eight bytes of the SHA-256 of the encoding ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)).

## 9. Conformance vectors

Each file in `schema/testdata/pxl/` declares an environment (`types`, `roots`), shared `inputs` in the literal forms of the document model, optional `limits`, and cases. A case gives an expression and exactly one expected outcome — a `value`, a run-time `error` kind, or compile-time `diagnostics` with codes and code-point ranges — plus goldens: the result `type`, the encoded `program` (base64), its disassembly (`asm`) and its `reads`. Go compiles each expression and checks the goldens and the outcome; the Dart VM (`packages/plux_flutter/test/pxl`) evaluates the golden program against the inputs. Constant subexpressions are folded at compile time, so `runtime.json` calls every core overload on root values: its programs exercise each implementation at run time in both languages. Goldens change only with `go test ./internal/pxl -run TestConformance -update` and are reviewed as a behaviour change.
