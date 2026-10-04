<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# `pxl.regex.v1` — the PXL regular-expression subset

The one specification of PXL's regular expressions (`PXL-006`), implemented by Go
(`backend/internal/pxl/regex`) and Dart (`packages/plux_flutter/lib/src/pxl/regex.dart`)
and proved identical by the vectors in `schema/testdata/regex/engine.json`. It is a strict
subset of RE2: every pattern accepted here means the same in Go's `regexp`, which the Go fuzz
test `FuzzRegex` checks. Matching is linear: no backtracking, no backreferences, no
lookaround.

## 1. Patterns

A pattern is a sequence of Unicode code points, valid UTF-8 (Go) or without unpaired
surrogates (Dart), of at most `pxl.regexPatternLength` code points.

```ebnf
regex       = alternation ;
alternation = sequence { "|" sequence } ;            (* branches may be empty *)
sequence    = { anchor | atom [ quantifier ] } ;
anchor      = "^" | "$" ;                             (* start / end of the input *)
atom        = char | "." | class | perl | escape | "(" [ "?:" ] alternation ")" ;
quantifier  = "*" | "+" | "?" | "{" count "}" | "{" count ",}" | "{" count "," count "}" ;
count       = "0" | nonzero { digit } ;               (* no leading zeros; ≤ pxl.regexRepeat *)
class       = "[" [ "^" ] item { item } "]" ;
item        = point [ "-" point ] | perl ;
perl        = "\d" | "\D" | "\w" | "\W" | "\s" | "\S" ;
escape      = "\" punct | "\t" | "\n" | "\r" | "\f" | "\v" | "\x" hex hex | "\x{" hex { hex } "}" ;
```

- `char` is any code point but `\ . [ ] ( ) { } | * + ? ^ $`; `punct` is ASCII
  punctuation. `\x{…}` has one to six hex digits, at most U+10FFFF, not a surrogate.
- In a class, `point` is a code point other than `\`, `[`, `]`, or an escape; `-` is literal
  only first or last; a range needs `lo ≤ hi` and two code points (not a `perl` class).
- `(` … `)` captures, numbered by its opening parenthesis from 1; `(?:` … `)` does not.
  Groups nest at most 100 deep.
- Rejected, among others: any other `(?` form (flags, named groups, lookaround), `\b`,
  `\p{…}`, `\1`, lazy or possessive quantifiers (`*?`, `*+`), a quantifier after a
  quantifier or an anchor, an unescaped `{`, `}` or `]` outside a class, `[]`.

Errors carry a kind — `syntax`, `patternLength`, `programSize`, `repeat` — and the code-point
offset of the problem. At publish time they are `PLX-2020` (syntax) and `PLX-2022` (limits);
a pattern of `matches` must be a string literal (`PLX-2021`).

## 2. Meaning

- `.` is any code point but U+000A; `[^…]`, `\D`, `\W`, `\S` include U+000A.
- `\d` = `[0-9]`, `\w` = `[0-9A-Za-z_]`, `\s` = `[\t\n\f\r ]` — ASCII, as RE2. Literals and
  ranges are code points; there is no case folding.
- `^` holds only at the start of the input, `$` only at its end.
- `x{n}` is n copies of x; `x{n,}` is n−1 copies followed by `x+` (`x*` for n = 0);
  `x{n,m}` is n copies followed by m−n nested optional copies: `x{1,3}` = `x(x(x)?)?`.
- When x can match the empty string, `x*` means `(x+)?`.
- Matches are **leftmost-first**: among the matches starting at the leftmost position, the
  first in priority order wins — the left branch of `|` first, quantifiers greedy. Group
  positions are those of that match, as Go's `regexp` reports them; a group that did not
  take part is −1.

## 3. Program and its size

The pattern compiles to a program of these instructions: consume one code point (a literal or
a class), assert start or end, save a position, split (two continuations, the first
preferred), match. Its size is |p| + 3 (two saves for the whole match and the match), where:

| Node | Size |
|---|---|
| empty | 0 |
| literal, class, `.`, `^`, `$` | 1 |
| `( x )` | \|x\| + 2 |
| concatenation | sum of its parts |
| alternation of k branches | sum of the branches + (k − 1) |
| `x*` | \|x\| + 1, or \|x\| + 2 when x can match empty |
| `x+`, `x?` | \|x\| + 1 |
| `x{n,}`, n ≥ 1 | (n − 1)·\|x\| + \|x\| + 1 |
| `x{n,m}` | n·\|x\| + (m − n)·(\|x\| + 1) |

The size must not exceed `pxl.regexProgramSize`; it is computed before the program is built.

## 4. Matching

A Pike VM runs the program over the input's code points with one ordered thread list per
position, visiting each instruction at most once per position: O((n + 1)·m) steps for n code
points and m instructions, with memory bounded by m. Three modes: **search** (`matches`; a
new lowest-priority thread starts at every position until a match is found), **prefix**
(threads start only at position 0) and **full** (a match counts only at the end of the input).

## 5. In PXL

`matches(s, pattern)` searches `s`; anchor with `^` and `$` to match all of it. The call costs
the operation budget the size of its arguments (PXL's cost model) plus (n + 1)·m, the VM's
bound. A pattern rejected at run time — possible only for malformed bytecode — fails with
`invalidArgument`. The feature `pxl.regex.v1` is first in runtime 0.3.0.
