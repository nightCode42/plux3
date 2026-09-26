// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// parseString parses an in-memory document and fails the test on error.
func parseString(t *testing.T, text string) *Document {
	t.Helper()
	doc, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

// parseFile parses a document from disk and fails the test on error.
func parseFile(t *testing.T, path string) *Document {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	doc, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse %s: %v", path, err)
	}
	return doc
}

// TestParseExtractsEveryRequirementForm checks table rows, block-quoted
// rules, multi-column tables and withdrawn rows.
func TestParseExtractsEveryRequirementForm(t *testing.T) {
	t.Parallel()

	doc := parseFile(t, filepath.Join("testdata", "valid.md"))

	got := map[string]string{}
	for _, r := range doc.Requirements {
		got[r.ID] = r.Phase + " " + r.Priority + " " + r.Status
	}
	want := map[string]string{
		"SYN-000": "P3 MUST SPEC",
		"SYN-001": "P3 MUST DONE",
		"SYN-002": "P3 SHOULD SPEC",
		"QA-001":  "P0 — WITHDRAWN",
		"QA-002":  "P1 MUST WIP",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements = %v, want %v", got, want)
	}
	if !doc.Areas["SYN"] || !doc.Areas["QA"] {
		t.Errorf("areas = %v, want SYN and QA", doc.Areas)
	}
}

// TestLintAcceptsConsistentDocument checks that a clean document yields no
// problems, and that identifiers inside code fences are ignored.
func TestLintAcceptsConsistentDocument(t *testing.T) {
	t.Parallel()

	doc := parseFile(t, filepath.Join("testdata", "valid.md"))

	if problems := doc.Lint(); len(problems) != 0 {
		t.Fatalf("Lint() = %v, want no problems", problems)
	}
}

// TestLintReportsEveryDefect checks each class of defect Lint detects,
// including the withdrawal rules of QA-073.
// Verifies: QA-073.
func TestLintReportsEveryDefect_QA_073(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"duplicate id", "| `SYN-001` | P3 | MUST | a | SPEC |\n| `SYN-001` | P3 | MUST | b | SPEC |", "duplicates the identifier"},
		{"unknown area", "| `ABC-001` | P3 | MUST | a | SPEC |", `uses area "ABC"`},
		{"phase out of range", "| `SYN-001` | P16 | MUST | a | SPEC |", "outside P0–P15"},
		{"withdrawn with priority", "| `SYN-001` | P3 | MUST | Withdrawn: x. | WITHDRAWN |", "still has priority"},
		{"withdrawn without rationale", "| `SYN-001` | P3 | — | Gone. | WITHDRAWN |", "without a \"Withdrawn: <rationale>\""},
		{"missing priority", "| `SYN-001` | P3 | — | a | SPEC |", "has no priority"},
		{"undefined reference", "| `SYN-001` | P3 | MUST | see `SYN-404` | SPEC |", "undefined requirement SYN-404"},
		{"missing anchor", "[x](#nowhere)", "missing heading #nowhere"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := parseString(t, "| `SYN` | Sync | §1 |\n\n"+tt.body+"\n")

			problems := doc.Lint()

			if len(problems) != 1 || !strings.Contains(problems[0].Message, tt.want) {
				t.Fatalf("Lint() = %v, want one problem containing %q", problems, tt.want)
			}
		})
	}
}

// TestSlugMatchesGitHubAnchors checks the heading-to-anchor conversion used
// for table-of-contents links.
func TestSlugMatchesGitHubAnchors(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"8. Widget Catalogue — the Layered Model": "8-widget-catalogue--the-layered-model",
		"Appendix E — Binding Expressions (PXL)":  "appendix-e--binding-expressions-pxl",
		"34. Decisions, Editions and Risks":       "34-decisions-editions-and-risks",
		"snake_case and Ünïcode":                  "snake_case-and-ünïcode",
	}
	for heading, want := range tests {
		if got := Slug(heading); got != want {
			t.Errorf("Slug(%q) = %q, want %q", heading, got, want)
		}
	}
}

// TestProjectSpecificationIsConsistent lints the real specification, so a
// broken reference or malformed row fails CI.
// Verifies: QA-073.
func TestProjectSpecificationIsConsistent_QA_073(t *testing.T) {
	t.Parallel()

	doc := parseFile(t, filepath.Join("..", "..", "..", "docs", "requirements.md"))

	if len(doc.Requirements) < 600 {
		t.Fatalf("parsed %d requirements, want the full specification", len(doc.Requirements))
	}
	for _, p := range doc.Lint() {
		t.Errorf("docs/requirements.md %s", p)
	}
}
