// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package jcs

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// testdata is the shared conformance directory (QA-003).
var testdata = filepath.Join("..", "..", "..", "..", "schema", "testdata", "jcs")

// depth is a generous nesting limit for tests.
const depth = 64

// readJSON decodes a testdata file.
func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

// TestCanonicalizeMatchesSharedVectors_SCH_003 runs the RFC 8785 examples and
// the project's own vectors.
// Verifies: SCH-003, QA-003.
func TestCanonicalizeMatchesSharedVectors_SCH_003(t *testing.T) {
	t.Parallel()
	var file struct {
		Vectors []struct{ Name, Input, Canonical string } `json:"vectors"`
	}
	readJSON(t, "vectors.json", &file)
	if len(file.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			got, err := Canonicalize([]byte(v.Input), depth)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != v.Canonical {
				t.Errorf("got  %s\nwant %s", got, v.Canonical)
			}
		})
	}
}

// TestFormatNumberMatchesRFCAppendixB_SCH_003 checks the ECMAScript number
// serialisation against RFC 8785 Appendix B.
// Verifies: SCH-003.
func TestFormatNumberMatchesRFCAppendixB_SCH_003(t *testing.T) {
	t.Parallel()
	var file struct {
		Numbers []struct{ Bits, Canonical string } `json:"numbers"`
		Invalid []string                           `json:"invalid"`
	}
	readJSON(t, "numbers.json", &file)
	for _, n := range file.Numbers {
		bits, err := strconv.ParseUint(n.Bits, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatNumber(math.Float64frombits(bits))
		if err != nil || got != n.Canonical {
			t.Errorf("0x%s: got %q, %v; want %q", n.Bits, got, err, n.Canonical)
		}
	}
	for _, b := range file.Invalid {
		bits, _ := strconv.ParseUint(b, 16, 64)
		if _, err := FormatNumber(math.Float64frombits(bits)); err == nil {
			t.Errorf("0x%s accepted", b)
		}
	}
}

// TestFormatWritesIndentedCanonicalOrder_SCH_006 checks the Git-layout form:
// canonical order and numbers, two-space indentation, final newline.
// Verifies: SCH-006, SCH-003.
func TestFormatWritesIndentedCanonicalOrder_SCH_006(t *testing.T) {
	t.Parallel()
	in := `{"b":[1.50,{"y":true,"x":null}],"a":{},"c":[],"d":"é"}`
	want := "{\n  \"a\": {},\n  \"b\": [\n    1.5,\n    {\n      \"x\": null,\n      \"y\": true\n    }\n  ],\n  \"c\": [],\n  \"d\": \"é\"\n}\n"

	got, err := Format([]byte(in), depth)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	canon, err := Canonicalize(got, depth)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := Canonicalize([]byte(in), depth)
	if string(canon) != string(direct) {
		t.Errorf("formatted file does not canonicalise to the same bytes")
	}
}

// TestParseRejectsNonInteroperableJSON_SCH_003 checks every rejection of the
// strict parser, each with an offset.
// Verifies: SCH-003.
func TestParseRejectsNonInteroperableJSON_SCH_003(t *testing.T) {
	t.Parallel()
	bs := `\`
	tests := []struct {
		name, input, msg string
	}{
		{"empty", ``, "unexpected end"},
		{"trailing data", `{} {}`, "after the JSON value"},
		{"duplicate key", `{"a":1,"a":2}`, "duplicate member"},
		{"duplicate after unescape", `{"a":1,"` + bs + `u0061":2}`, "duplicate member"},
		{"unpaired high surrogate", `"` + bs + `ud800"`, "unpaired surrogate"},
		{"unpaired low surrogate", `"` + bs + `udc00"`, "unpaired surrogate"},
		{"high followed by non-low", `"` + bs + `ud800` + bs + `u0041"`, "unpaired surrogate"},
		{"invalid UTF-8", "\"\xff\"", "invalid UTF-8"},
		{"raw control character", "\"\x01\"", "must be escaped"},
		{"bad escape", `"` + bs + `x"`, "invalid escape"},
		{"truncated unicode escape", `"` + bs + `u12`, "truncated"},
		{"non-hex unicode escape", `"` + bs + `uzzzz"`, "invalid"},
		{"unterminated escape", `"` + bs, "unterminated escape"},
		{"unterminated string", `"abc`, "unterminated string"},
		{"integer beyond double precision", `9007199254740993`, "would change to 9007199254740992"},
		{"negative integer beyond double precision", `-12345678901234567`, "would change to -12345678901234568"},
		{"integer written out beyond 1e21", `1000000000000000000000`, "would change to 1e+21"},
		{"number overflow", `1e400`, "not a finite"},
		{"leading zero", `01`, "after the JSON value"},
		{"bare minus", `-`, "invalid number"},
		{"missing fraction digits", `1.`, "after '.'"},
		{"missing exponent digits", `1e+`, "exponent"},
		{"missing colon", `{"a" 1}`, "expected ':'"},
		{"missing member name", `{1:2}`, "member name"},
		{"missing comma", `[1 2]`, "expected ','"},
		{"unclosed array", `[1,`, "unexpected end"},
		{"unclosed object", `{"a":1`, "unexpected end"},
		{"unknown literal", `nul`, "unexpected character"},
		{"too deep", strings.Repeat("[", depth+1) + strings.Repeat("]", depth+1), "nesting deeper"},
		{"too deep object", strings.Repeat(`{"a":`, depth+1) + "1" + strings.Repeat("}", depth+1), "nesting deeper"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tt.input), depth)
			var syn *SyntaxError
			if !errors.As(err, &syn) {
				t.Fatalf("err = %v, want *SyntaxError", err)
			}
			if !strings.Contains(syn.Msg, tt.msg) || !strings.Contains(err.Error(), "offset") {
				t.Errorf("err = %v, want message containing %q", err, tt.msg)
			}
		})
	}
}

// TestParseAcceptsTheFullGrammar checks the value shapes Parse returns.
func TestParseAcceptsTheFullGrammar(t *testing.T) {
	t.Parallel()
	bs := `\`
	in := `{"s":"a` + bs + `"` + bs + bs + bs + `/` + bs + `b` + bs + `f` + bs + `n` + bs + `r` + bs + `t` + bs + `ud83d` + bs + `ude00é",` +
		`"n":[0,-1.5e-3,2E+2,9007199254740991],"t":true,"f":false,"z":null,"o":{},"a":[]}`
	v, err := Parse([]byte(in), depth)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if got := m["s"]; got != "a\"\\/\b\f\n\r\t😀é" {
		t.Errorf("s = %q", got)
	}
	nums, _ := m["n"].([]any)
	if len(nums) != 4 || nums[1] != json.Number("-1.5e-3") {
		t.Errorf("n = %v", m["n"])
	}
	if m["t"] != true || m["f"] != false || m["z"] != nil {
		t.Errorf("literals = %v %v %v", m["t"], m["f"], m["z"])
	}
	if _, err := Parse([]byte("1"), 0); err == nil {
		t.Error("non-positive depth accepted")
	}
}

// TestMarshalAcceptsDecodedTreesAndRejectsOthers checks Marshal's inputs.
func TestMarshalAcceptsDecodedTreesAndRejectsOthers(t *testing.T) {
	t.Parallel()
	got, err := Marshal(map[string]any{"b": 2.5, "a": []any{json.Number("1e2"), "x"}})
	if err != nil || string(got) != `{"a":[100,"x"],"b":2.5}` {
		t.Errorf("got %s, %v", got, err)
	}
	for _, bad := range []any{struct{}{}, json.Number("x"), math.Inf(1), []any{math.NaN()}, map[string]any{"a": int64(1)}} {
		if _, err := Marshal(bad); err == nil {
			t.Errorf("Marshal(%v) accepted", bad)
		}
	}
}

// TestCompareUTF16OrdersByCodeUnits checks the member-name order of RFC 8785
// §3.2.3, which differs from code-point order for supplementary characters.
func TestCompareUTF16OrdersByCodeUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "b", -1},
		{"ab", "a", 1},
		{"😀", "דּ", -1}, // U+1F600 (D83D DE00) sorts before U+FB33.
		{"", "😀", 1},
		{"x😀", "x😁", -1},
		{"same", "same", 0},
	}
	for _, tt := range tests {
		got := CompareUTF16(tt.a, tt.b)
		if sign(got) != tt.want {
			t.Errorf("CompareUTF16(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// sign returns -1, 0 or 1.
func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}

// numberGrammar is the ECMAScript output shape of FormatNumber.
var numberGrammar = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?(e[+-][1-9][0-9]*)?$|^-?0\.[0-9]*[1-9]$`)

// TestFormatNumberShapes checks fixed/exponential switch-over points.
func TestFormatNumberShapes(t *testing.T) {
	t.Parallel()
	tests := map[float64]string{
		1e21: "1e+21", 1e20: "100000000000000000000", 123e18: "123000000000000000000",
		1e-6: "0.000001", 1e-7: "1e-7", 1.5e-7: "1.5e-7", 0.1: "0.1", -2.5: "-2.5", 5: "5", 12.34: "12.34",
	}
	for f, want := range tests {
		got, err := FormatNumber(f)
		if err != nil || got != want {
			t.Errorf("FormatNumber(%v) = %q, %v; want %q", f, got, err, want)
		}
		if !numberGrammar.MatchString(got) {
			t.Errorf("%q does not match the output grammar", got)
		}
	}
	if got, _ := FormatNumber(math.Copysign(0, -1)); got != "0" {
		t.Errorf("negative zero = %q", got)
	}
}
