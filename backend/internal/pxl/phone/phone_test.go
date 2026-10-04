// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package phone

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/pxl/regex"
)

var update = flag.Bool("update", false, "rewrite schema/testdata/phone/examples.json")

var (
	metadataPath = filepath.Join("..", "..", "..", "..", "schema", "pxl", "phone.json")
	examplesPath = filepath.Join("..", "..", "..", "..", "schema", "testdata", "phone", "examples.json")
)

// metadata is the part of schema/pxl/phone.json the tests read.
type metadata struct {
	Territories []struct {
		ID          string `json:"id"`
		CountryCode int    `json:"countryCode"`
		Types       []struct {
			Type    string `json:"type"`
			Example string `json:"example"`
		} `json:"types"`
	} `json:"territories"`
}

// examples is the shared golden of results over inputs derived from the
// metadata's example numbers.
type examples struct {
	Description string   `json:"description"`
	Variants    []string `json:"variants"`
	Results     string   `json:"results"`
}

// variants derives the inputs from one example (schema/testdata/phone).
var variants = []struct {
	name  string
	input func(id string, cc int, ex string) (string, string)
}{
	{"national", func(id string, _ int, ex string) (string, string) { return ex, id }},
	{"international", func(_ string, cc int, ex string) (string, string) { return "+" + strconv.Itoa(cc) + " " + ex, "" }},
	{"idd", func(_ string, cc int, ex string) (string, string) { return "00" + strconv.Itoa(cc) + ex, "DE" }},
	{"longer", func(id string, _ int, ex string) (string, string) { return ex + "0", id }},
	{"shorter", func(id string, _ int, ex string) (string, string) { return ex[:len(ex)-1], id }},
}

func loadMetadata(t *testing.T) metadata {
	t.Helper()
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var m metadata
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestExamples_PXL_006 validates every example number of the metadata —
// libphonenumber's own valid numbers — in national, international and
// IDD form, and compares all derived results with the golden the Dart
// runtime checks too.
//
// Verifies: PXL-006, PXL-007, STA-020.
func TestExamples_PXL_006(t *testing.T) {
	m := loadMetadata(t)
	var results strings.Builder
	for _, x := range m.Territories {
		for _, ty := range x.Types {
			if ty.Example == "" {
				continue
			}
			for _, v := range variants {
				number, region := v.input(x.ID, x.CountryCode, ty.Example)
				if x.ID == "001" && v.name != "international" && v.name != "idd" {
					region = "ZZ"
				}
				ok := IsValid(number, region)
				if (v.name == "international" || v.name == "idd" || v.name == "national" && x.ID != "001") && !ok {
					t.Errorf("%s %s example %s, %s form %q in %q: invalid", x.ID, ty.Type, ty.Example, v.name, number, region)
				}
				results.WriteByte("01"[boolIndex(ok)])
			}
		}
	}
	got := examples{
		Description: "pxl.phone.v1 results over the example numbers of schema/pxl/phone.json, shared by Go and Dart: for each territory in order, each type with an example, each variant in order, 1 if isPhone holds. Written by the Go test with -update.",
		Variants:    variantNames(), Results: results.String(),
	}
	if *update {
		out, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(examplesPath, append(out, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(examplesPath)
	if err != nil {
		t.Fatal(err)
	}
	var want examples
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if want.Results != got.Results || strings.Join(want.Variants, ",") != strings.Join(got.Variants, ",") {
		t.Error("results differ from schema/testdata/phone/examples.json; run with -update and review")
	}
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

func variantNames() []string {
	out := make([]string, len(variants))
	for i, v := range variants {
		out[i] = v.name
	}
	return out
}

// TestTablePatternsCompile checks that every metadata pattern is in the
// pxl.regex.v1 subset, and reports the largest program.
func TestTablePatternsCompile(t *testing.T) {
	largest := 0
	for line := range strings.SplitSeq(strings.TrimSpace(table), "\n") {
		f := strings.Split(line, " ")
		for i, s := range f {
			if i < 3 || i == 6 || i == 8 || i == 9 || i >= 10 && i%2 == 0 || s == "~" {
				continue
			}
			re, err := regex.Compile(s, regex.Trusted)
			if err != nil {
				t.Errorf("%s field %d %q: %v", f[0], i, s, err)
				continue
			}
			largest = max(largest, re.Size())
		}
	}
	t.Logf("largest program: %d instructions", largest)
	if Version == "" {
		t.Error("no version")
	}
}

// TestIsValid covers the input syntax and each parsing path of
// schema/pxl/phone.md §3.
//
// Verifies: STA-020.
func TestIsValid_STA_020(t *testing.T) {
	for _, c := range []struct {
		number, region string
		want           bool
	}{
		{"07400 123456", "GB", true},          // national prefix 0 stripped
		{"7400 123456", "GB", true},           // national significant number
		{"+44 7400 123456", "", true},         // international, no region needed
		{"+44 (0) 7400 123456", "US", true},   // "(0)" is the national prefix after the code
		{"0044 7400 123456", "DE", true},      // IDD 00 from Germany
		{"011 44 7400 123456", "US", true},    // IDD 011 from the US
		{"44 7400 123456", "GB", true},        // calling code without "+"
		{"+44 7400 12345", "", false},         // too short
		{"+44 7400 1234567", "", false},       // too long
		{"+1 201-555-0123", "", true},         // NANPA, US
		{"(201) 555-0123", "us", true},        // region in lower case
		{"201.555.0123", "CA", true},          // a US number from Canada's default
		{"+1 613 555 0123", "", true},         // NANPA, Canada
		{"+800 1234 5678", "", true},          // non-geographic code
		{"+0 123", "", false},                 // codes do not start with 0
		{"+999 123456", "", false},            // unknown code
		{"+44", "", false},                    // nothing after the code
		{"7400 123456", "", false},            // national number without a region
		{"7400 123456", "XX", false},          // unknown region
		{"7400 123456", "GBR", false},         // not a region code
		{"7400 123456 ext. 12", "GB", false},  // extensions are not read
		{"74OO 123456", "GB", false},          // letters
		{"7400+123456", "GB", false},          // "+" after digits
		{"", "GB", false},                     // empty
		{"+", "GB", false},                    // only a plus
		{strings.Repeat("1", 251), "", false}, // over 250 code points
		{"030 123456", "DE", true},            // Berlin, national prefix 0
		{"00 7 400 123456", "GB", false},      // IDD, then no valid number for +7
		{"0 11 15-2345-6789", "AR", true},     // Argentine mobile, transform rule
	} {
		if got := IsValid(c.number, c.region); got != c.want {
			t.Errorf("IsValid(%q, %q) = %v, want %v", c.number, c.region, got, c.want)
		}
	}
	if !Known("gb") || Known("001") || Known("") {
		t.Error("Known")
	}
}
