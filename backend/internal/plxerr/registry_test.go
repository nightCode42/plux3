// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// reasonPattern is the form of every reason.
var reasonPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)

// TestRegistryDefinitionsAreComplete_DX_003 checks that every registered code
// has a unique reason, a valid severity, a title, a cause and a fix, lies in
// an area of Appendix F, and that the registry is sorted.
// Verifies: DX-003.
func TestRegistryDefinitionsAreComplete_DX_003(t *testing.T) {
	t.Parallel()
	defs := Definitions()
	if len(defs) == 0 {
		t.Fatal("registry is empty")
	}
	reasons := map[Reason]Code{}
	for i, d := range defs {
		if i > 0 && defs[i-1].Code >= d.Code {
			t.Errorf("%s: registry not strictly ascending after %s", d.Code, defs[i-1].Code)
		}
		if prev, dup := reasons[d.Reason]; dup {
			t.Errorf("%s: reason %s already used by %s", d.Code, d.Reason, prev)
		}
		reasons[d.Reason] = d.Code
		if !reasonPattern.MatchString(string(d.Reason)) {
			t.Errorf("%s: reason %q is not UPPER_SNAKE_CASE", d.Code, d.Reason)
		}
		if d.Severity < SeverityInfo || d.Severity > SeverityError {
			t.Errorf("%s: invalid severity %d", d.Code, d.Severity)
		}
		if d.Title == "" || strings.HasSuffix(d.Title, ".") {
			t.Errorf("%s: title %q must be non-empty without a final period", d.Code, d.Title)
		}
		for name, text := range map[string]string{"cause": d.Cause, "fix": d.Fix} {
			if !strings.HasSuffix(text, ".") {
				t.Errorf("%s: %s %q must be a complete sentence", d.Code, name, text)
			}
		}
		if !inSomeArea(d.Code) {
			t.Errorf("%s: outside every area of Appendix F", d.Code)
		}
	}
}

// inSomeArea reports whether c lies in one of the Appendix F ranges.
func inSomeArea(c Code) bool {
	for _, a := range Areas() {
		if c >= a.First && c <= a.Last {
			return true
		}
	}
	return false
}

// TestRegistryContainsAppendixFExamples_DX_003 pins the example codes the
// specification names in Appendix F to their meaning.
// Verifies: DX-003.
func TestRegistryContainsAppendixFExamples_DX_003(t *testing.T) {
	t.Parallel()
	want := map[Code]Reason{
		1001: "UNKNOWN_PROPERTY",
		1102: "UNRESOLVED_REFERENCE",
		1203: "ROUTE_PARAMETER_MISSING",
		1310: "PAGE_NODE_BUDGET",
		1401: "ACCESSIBLE_NAME_MISSING",
		1500: "SECRET_LIKE_VALUE",
		2001: "PXL_SYNTAX_ERROR",
		2002: "PXL_TYPE_MISMATCH",
		2010: "PXL_BUDGET_EXCEEDED",
		3010: "UNSUPPORTED_REQUIRED_FEATURE",
		9100: "CLI_CONFIGURATION_INVALID",
	}
	for code, reason := range want {
		def, ok := Lookup(code)
		if !ok {
			t.Errorf("%s is not registered", code)
			continue
		}
		if def.Reason != reason {
			t.Errorf("%s reason = %s, want %s", code, def.Reason, reason)
		}
	}
}

// TestLookupReportsUnknownCodes checks the miss path of Lookup.
func TestLookupReportsUnknownCodes(t *testing.T) {
	t.Parallel()
	for _, c := range []Code{0, 999, 1999, 65535} {
		if _, ok := Lookup(c); ok {
			t.Errorf("Lookup(%s) found a definition", c)
		}
	}
}

// TestDefinitionsReturnsACopy checks that callers cannot alter the registry.
func TestDefinitionsReturnsACopy(t *testing.T) {
	t.Parallel()
	defs := Definitions()
	defs[0].Title = "changed"
	if Definitions()[0].Title == "changed" {
		t.Fatal("Definitions exposes the registry")
	}
}

// TestPublishedCatalogueMatchesRegistry_DX_003 fails when the generated
// catalogue files are stale; run `make gen` to update them.
// Verifies: DX-003.
func TestPublishedCatalogueMatchesRegistry_DX_003(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	js, err := RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string][]byte{CatalogueMarkdownPath: RenderMarkdown(), CatalogueJSONPath: js} {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v (run `make gen`)", rel, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is stale; run `make gen`", rel)
		}
	}
}

// TestCatalogueHasOneAnchoredEntryPerCode checks that every code's DocURL
// points at a heading in the Markdown catalogue.
// Verifies: DX-003.
func TestCatalogueHasOneAnchoredEntryPerCode(t *testing.T) {
	t.Parallel()
	md := string(RenderMarkdown())
	for _, d := range Definitions() {
		heading := "\n### " + d.Code.String() + "\n"
		if strings.Count(md, heading) != 1 {
			t.Errorf("%s: catalogue has %d headings, want 1", d.Code, strings.Count(md, heading))
		}
		if want := CatalogueURL + "#" + strings.ToLower(d.Code.String()); d.Code.DocURL() != want {
			t.Errorf("%s: DocURL = %s, want %s", d.Code, d.Code.DocURL(), want)
		}
	}
}
