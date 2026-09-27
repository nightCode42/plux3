// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package jcs

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// genValue generates JSON trees in the shape Parse returns, up to depth d.
func genValue(d int) *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any {
		kind := rapid.IntRange(0, 6).Draw(t, "kind")
		if d == 0 && kind >= 5 {
			kind = rapid.IntRange(0, 4).Draw(t, "leaf")
		}
		switch kind {
		case 0:
			return nil
		case 1:
			return rapid.Bool().Draw(t, "bool")
		case 2:
			n := rapid.Int64Range(-(1<<53-1), 1<<53-1).Draw(t, "int")
			return json.Number(strconv.FormatInt(n, 10))
		case 3:
			f := rapid.Float64().Filter(func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }).Draw(t, "float")
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
		case 4:
			return rapid.String().Draw(t, "string")
		case 5:
			return rapid.SliceOfN(genValue(d-1), 0, 4).Draw(t, "array")
		default:
			return rapid.MapOfN(rapid.String(), genValue(d-1), 0, 4).Draw(t, "object")
		}
	})
}

// render writes v as JSON with shuffled member order and random whitespace,
// escaping characters at random, to exercise every input form.
func render(t *rapid.T, v any) string {
	ws := func() string { return rapid.SampledFrom([]string{"", " ", "\n", "\t ", "\r\n  "}).Draw(t, "ws") }
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		keys = rapid.Permutation(keys).Draw(t, "order")
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = ws() + renderString(t, k) + ws() + ":" + ws() + render(t, x[k]) + ws()
		}
		return "{" + strings.Join(parts, ",") + ws() + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = ws() + render(t, e) + ws()
		}
		return "[" + strings.Join(parts, ",") + ws() + "]"
	case string:
		return renderString(t, x)
	default:
		out, err := json.Marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
}

// renderString escapes each character either minimally or as \uXXXX.
func renderString(t *rapid.T, s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		escape := r < 0x20 || r == '"' || r == '\\' || rapid.Bool().Draw(t, "escape")
		switch {
		case !escape:
			b.WriteRune(r)
		case r >= 0x10000:
			hi, lo := utf16Units(r)[0], utf16Units(r)[1]
			b.WriteString(`\u` + hex4(hi) + `\u` + hex4(lo))
		default:
			b.WriteString(`\u` + hex4(uint16(r))) //nolint:gosec // G115: r < 0x10000.
		}
	}
	b.WriteByte('"')
	return b.String()
}

// hex4 formats a code unit as four hexadecimal digits.
func hex4(u uint16) string {
	s := strconv.FormatUint(uint64(u), 16)
	return strings.Repeat("0", 4-len(s)) + s
}

// TestCanonicalFormIsInvariantUnderRepresentation_QA_002 states: the
// canonical form depends only on the JSON data model value, not on member
// order, whitespace or escaping.
// Verifies: QA-002, SCH-003.
func TestCanonicalFormIsInvariantUnderRepresentation_QA_002(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		v := genValue(3).Draw(t, "value")
		want, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		text := render(t, v)

		got, err := Canonicalize([]byte(text), depth)
		if err != nil {
			t.Fatalf("Canonicalize(%s): %v", text, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("got %s, want %s for %s", got, want, text)
		}
	})
}

// TestCanonicalizeIsIdempotentAndValid_QA_002 states: canonicalising a
// canonical text returns it unchanged, and the result is valid JSON that
// Format reproduces.
// Verifies: QA-002, SCH-003.
func TestCanonicalizeIsIdempotentAndValid_QA_002(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		v := genValue(3).Draw(t, "value")
		once, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := Canonicalize(once, depth)
		if err != nil || !bytes.Equal(once, twice) {
			t.Fatalf("not idempotent: %s → %s (%v)", once, twice, err)
		}
		if !json.Valid(once) {
			t.Fatalf("invalid JSON: %s", once)
		}
		pretty, err := Format(once, depth)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Canonicalize(pretty, depth)
		if err != nil || !bytes.Equal(back, once) {
			t.Fatalf("Format does not round-trip: %s", pretty)
		}
	})
}

// TestFormatNumberRoundTrips_QA_002 states: the canonical number text parses
// back to the same double and follows the ECMAScript output grammar.
// Verifies: QA-002, SCH-003.
func TestFormatNumberRoundTrips_QA_002(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		f := rapid.Float64().Filter(func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }).Draw(t, "f")
		s, err := FormatNumber(f)
		if err != nil {
			t.Fatal(err)
		}
		back, err := strconv.ParseFloat(s, 64)
		if err != nil || back != f {
			t.Fatalf("%v → %q → %v (%v)", f, s, back, err)
		}
		if !numberGrammar.MatchString(s) {
			t.Fatalf("%q does not match the output grammar", s)
		}
	})
}

// FuzzCanonicalize checks that arbitrary input never panics and that every
// accepted input canonicalises to a valid, idempotent text (QA-004).
func FuzzCanonicalize(f *testing.F) {
	for _, seed := range []string{`{}`, `[1,"a",null]`, `{"b":1,"a":[true,{"c":-0.5e3}]}`, `"😀"`, `1e308`, `{"a":1,"a":2}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Canonicalize(data, depth)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("invalid output %q for %q", out, data)
		}
		again, err := Canonicalize(out, depth)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent for %q: %q, %v", data, again, err)
		}
	})
}
