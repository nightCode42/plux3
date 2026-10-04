// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package regex

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite the expected results of schema/testdata/regex/engine.json")

// vectorPath holds the engine vectors shared with the Dart runtime.
var vectorPath = filepath.Join("..", "..", "..", "..", "schema", "testdata", "regex", "engine.json")

type vectorFile struct {
	Description string        `json:"description"`
	Limits      vectorLimits  `json:"limits"`
	Cases       []*vectorCase `json:"cases"`
}

type vectorLimits struct {
	PatternLength int64 `json:"patternLength"`
	ProgramSize   int64 `json:"programSize"`
	Repeat        int64 `json:"repeat"`
}

type vectorCase struct {
	Pattern string         `json:"pattern"`
	Error   string         `json:"error,omitempty"`
	Offset  *int           `json:"offset,omitempty"`
	Size    int            `json:"size,omitempty"`
	Groups  int            `json:"groups,omitempty"`
	Inputs  []*vectorInput `json:"inputs,omitempty"`
}

type vectorInput struct {
	Input  string `json:"input"`
	Find   []int  `json:"find"`
	Prefix []int  `json:"prefix"`
	Full   bool   `json:"full"`
}

// TestVectors runs the shared engine vectors: errors with their offsets,
// program sizes, and the search, prefix and full-match results; every
// expected search result is also checked against Go's regexp, whose
// leftmost-first semantics the subset shares.
//
// Verifies: PXL-006, PXL-007.
func TestVectors_PXL_006(t *testing.T) {
	data, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatal(err)
	}
	lim := Limits(f.Limits)
	for _, c := range f.Cases {
		re, cerr := Compile(c.Pattern, lim)
		if *update {
			fill(c, re, cerr)
			continue
		}
		if c.Error != "" {
			if cerr == nil || cerr.Kind.String() != c.Error || c.Offset == nil || cerr.Offset != *c.Offset {
				t.Errorf("%q: got %v, want %s at %v", c.Pattern, cerr, c.Error, c.Offset)
			}
			continue
		}
		if cerr != nil {
			t.Errorf("%q: %v", c.Pattern, cerr)
			continue
		}
		if re.Size() != c.Size || re.Groups() != c.Groups {
			t.Errorf("%q: size %d groups %d, want %d %d", c.Pattern, re.Size(), re.Groups(), c.Size, c.Groups)
		}
		std := regexp.MustCompile(c.Pattern)
		for _, in := range c.Inputs {
			if got := re.Find(in.Input); !slices.Equal(got, in.Find) {
				t.Errorf("%q on %q: find %v, want %v", c.Pattern, in.Input, got, in.Find)
			}
			if got := toRunes(in.Input, std.FindStringSubmatchIndex(in.Input)); !slices.Equal(got, in.Find) {
				t.Errorf("%q on %q: Go regexp finds %v, the vector %v", c.Pattern, in.Input, got, in.Find)
			}
			if got := re.Prefix(in.Input); !slices.Equal(got, in.Prefix) {
				t.Errorf("%q on %q: prefix %v, want %v", c.Pattern, in.Input, got, in.Prefix)
			}
			if got := re.FullMatch(in.Input); got != in.Full {
				t.Errorf("%q on %q: full %v, want %v", c.Pattern, in.Input, got, in.Full)
			}
			if got := re.MatchString(in.Input); got != (in.Find != nil) {
				t.Errorf("%q on %q: match %v", c.Pattern, in.Input, got)
			}
		}
	}
	if *update {
		out, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorPath, append(out, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// fill writes a case's expected results from this engine (-update).
func fill(c *vectorCase, re *Regexp, err *Error) {
	if err != nil {
		c.Error, c.Offset, c.Size, c.Groups, c.Inputs = err.Kind.String(), &err.Offset, 0, 0, nil
		return
	}
	c.Error, c.Offset, c.Size, c.Groups = "", nil, re.Size(), re.Groups()
	for _, in := range c.Inputs {
		in.Find, in.Prefix, in.Full = re.Find(in.Input), re.Prefix(in.Input), re.FullMatch(in.Input)
	}
}

// toRunes converts byte offsets of s to code-point offsets.
func toRunes(s string, idx []int) []int {
	if idx == nil {
		return nil
	}
	out := make([]int, len(idx))
	for i, b := range idx {
		out[i] = -1
		if b >= 0 {
			out[i] = utf8.RuneCountInString(s[:b])
		}
	}
	return out
}

// TestSizeMatchesFormula checks the specified size formula against the
// emitted program, including saturation above the limit.
func TestSizeMatchesFormula(t *testing.T) {
	lim := Limits{PatternLength: 1000, ProgramSize: 100000, Repeat: 1000}
	for _, p := range []string{"", "a", "a|b|", "(a)*", "(a*)*", "a{3,7}", "(?:ab){2,}", "x{0}", "x{0,0}y", "(^$)+", "[a-c]?d*"} {
		re, err := Compile(p, lim)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		tree, _, _ := parse([]rune(p), lim)
		if want := sizeOf(tree, lim.ProgramSize) + 3; int64(re.Size()) != want {
			t.Errorf("%q: %d instructions, formula %d", p, re.Size(), want)
		}
	}
	if _, err := Compile("((a{1000}){1000}){1000}", lim); err == nil || err.Kind != ErrProgramSize {
		t.Errorf("explosive nesting: %v", err)
	}
	if _, err := Compile(strings.Repeat("(", MaxNesting+1)+strings.Repeat(")", MaxNesting+1), lim); err == nil || err.Kind != ErrSyntax {
		t.Errorf("deep nesting: %v", err)
	}
	if (&Error{Kind: 9}).Kind.String() != "ErrorKind(9)" || (&Error{Kind: ErrRepeat, Message: "m"}).Error() != "regex: repeat at 0: m" {
		t.Error("error strings")
	}
}

// fuzzLimits keep fuzzed programs small enough to run quickly.
var fuzzLimits = Limits{PatternLength: 64, ProgramSize: 2000, Repeat: 20}

// FuzzRegex compiles arbitrary patterns: Compile never panics, rejects
// only with an error, and every accepted pattern matches exactly as Go's
// regexp does — the same leftmost-first match and groups — within the
// O(n·m) bound, since the subset is a subset of RE2.
//
// Verifies: PXL-001, PXL-006.
func FuzzRegex(f *testing.F) {
	for _, s := range []string{"a(b|c)*d", "^\\d{2,4}$", "[^a-z]+", "(a|ab)(c|bcd)(d*)", "(|a)*", "(a*)+", "x?y{0,3}", "[\\w.-]+@[\\w-]+\\.\\w{2,}", "\\x{1F600}|é", "(?:)", "a|", "[-a]"} {
		f.Add(s, "xabcdabbcd é 12345 ab@cd.ef")
	}
	f.Fuzz(func(t *testing.T, pattern, input string) {
		re, err := Compile(pattern, fuzzLimits)
		if err != nil {
			if re != nil || err.Kind == 0 {
				t.Fatalf("%q: %v", pattern, err)
			}
			return
		}
		if int64(re.Size()) > fuzzLimits.ProgramSize {
			t.Fatalf("%q: size %d above the limit", pattern, re.Size())
		}
		std, serr := regexp.Compile(pattern)
		if serr != nil {
			t.Fatalf("%q is accepted but Go's regexp rejects it: %v", pattern, serr)
		}
		if len(input) > 256 {
			input = input[:256]
		}
		want := toRunes(input, std.FindStringSubmatchIndex(input))
		if got := re.Find(input); !slices.Equal(got, want) {
			t.Fatalf("%q on %q: %v, Go's regexp %v", pattern, input, got, want)
		}
		if got := re.MatchString(input); got != (want != nil) {
			t.Fatalf("%q on %q: match %v", pattern, input, got)
		}
		anchored := regexp.MustCompile(`^(?:` + pattern + `)$`)
		if got := re.FullMatch(input); got != anchored.MatchString(input) {
			t.Fatalf("%q on %q: full %v", pattern, input, got)
		}
	})
}
