// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

func parse(t *testing.T, path, text string) (*File, []Finding) {
	t.Helper()
	v, err := schema.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	return ParseFile(v, limits.Defaults(), path, []byte(text))
}

const validYAML = `schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: opens the place
    page: place
    given:
      params:
        name: Harbour
      state:
        counter: 3
      dataSources:
        places:
          state: empty
    steps:
      - tap: place-profile
      - enterText:
          testId: search
          text: harbour
      - scroll:
          testId: list
          dy: -300
      - waitFor:
          testId: done
          timeoutMs: 2000
      - trigger:
          event: placeShared
          payload:
            name: Harbour
    expect:
      - visible: place-title
      - notVisible: spinner
      - textEquals:
          testId: place-title
          text: Harbour
      - navigatedTo: profile
      - actionCalled:
          name: navigate
          args:
            route: profile
      - functionCalled:
          name: score
          times: 2
      - stateEquals:
          counter: 3
`

// Verifies: TST-001.
func TestParseFileReadsEveryStepAndExpectation(t *testing.T) {
	t.Parallel()
	f, findings := parse(t, "tests/a.scenario.yaml", validYAML)
	if len(findings) > 0 {
		t.Fatalf("findings: %v", findings)
	}
	s := f.Doc.Scenarios[0]
	if s.Page != "place" || len(s.Steps) != 5 || len(s.Expect) != 7 {
		t.Fatalf("scenario = %+v", s)
	}
	if string(s.Given.Params["name"]) != `"Harbour"` || string(s.Given.State["counter"]) != "3" {
		t.Errorf("given = %+v", s.Given)
	}
	if s.Given.DataSources["places"].State != schema.ScenarioMockStateEmpty {
		t.Errorf("data source state = %s", s.Given.DataSources["places"].State)
	}
	if got := s.Steps[2].Scroll; got == nil || *got.Dy != -300 || got.Dx != nil {
		t.Errorf("scroll = %+v", got)
	}
}

// Verifies: TST-001.
func TestParseFileReadsJSON(t *testing.T) {
	t.Parallel()
	f, findings := parse(t, "tests/a.scenario.json", `{"schemaVersion":"1.0.0","kind":"scenarios","scenarios":[{"name":"n","page":"home","expect":[{"visible":"title"}]}]}`)
	if len(findings) > 0 || f == nil || f.Doc.Scenarios[0].Expect[0].Visible != "title" {
		t.Fatalf("findings %v, file %+v", findings, f)
	}
}

// Verifies: TST-001.
func TestParseFileMapsSchemaErrorsToLineAndColumn(t *testing.T) {
	t.Parallel()
	text := `schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: first
    page: home
    steps:
      - tap: title
        enterText:
          testId: field
          text: x
    expect:
      - visible: title
  - name: second
    page: home
    expect:
      - bogus: 1
`
	f, findings := parse(t, "tests/b.scenario.yaml", text)
	if f != nil {
		t.Fatal("a file with errors was accepted")
	}
	if len(findings) == 0 {
		t.Fatal("no finding")
	}
	var lines []int
	for _, fd := range findings {
		if fd.Code != plxerr.ScenarioFileInvalid {
			t.Errorf("code = %s, want %s", fd.Code, plxerr.ScenarioFileInvalid)
		}
		if fd.File != "tests/b.scenario.yaml" || fd.Column == 0 {
			t.Errorf("finding %+v has no position", fd)
		}
		lines = append(lines, fd.Line)
	}
	// Two tap keys in one step (line 7) and an unknown expectation (line 16).
	if !contains(lines, 7) || !contains(lines, 16) {
		t.Errorf("lines = %v, want 7 and 16 among them", lines)
	}
	if got := findings[0].String(); !strings.HasPrefix(got, "tests/b.scenario.yaml:") || !strings.Contains(got, "PLX-1270") {
		t.Errorf("String() = %q", got)
	}
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Verifies: TST-001.
func TestParseFileReportsSyntaxErrorsWithPosition(t *testing.T) {
	t.Parallel()
	_, findings := parse(t, "tests/c.scenario.yaml", "schemaVersion: 1.0.0\nkind: [scenarios\n")
	if len(findings) != 1 || findings[0].Code != plxerr.ScenarioSyntaxInvalid || findings[0].Line == 0 {
		t.Fatalf("findings = %v", findings)
	}
}

// Verifies: TST-001.
func TestParseFileRejectsDuplicateKeysAndNames(t *testing.T) {
	t.Parallel()
	_, findings := parse(t, "tests/d.scenario.yaml", "schemaVersion: 1.0.0\nkind: scenarios\nkind: scenarios\nscenarios: []\n")
	if len(findings) != 1 || findings[0].Code != plxerr.ScenarioSyntaxInvalid {
		t.Fatalf("duplicate key: %v", findings)
	}
	text := `schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: same
    page: home
    expect:
      - visible: a
  - name: same
    page: home
    expect:
      - visible: b
`
	_, findings = parse(t, "tests/d.scenario.yaml", text)
	if len(findings) != 1 || findings[0].Code != plxerr.ScenarioNameDuplicate || findings[0].Line != 8 {
		t.Fatalf("duplicate name: %v", findings)
	}
}

// Verifies: TST-001.
func TestParseFileNeedsPageOrFlow(t *testing.T) {
	t.Parallel()
	_, findings := parse(t, "tests/e.scenario.yaml", "schemaVersion: 1.0.0\nkind: scenarios\nscenarios:\n  - name: n\n    expect:\n      - visible: a\n")
	if len(findings) == 0 || findings[0].Line != 4 {
		t.Fatalf("findings = %v", findings)
	}
}

// Verifies: TST-001.
func TestLocateReturnsTheClosestEnclosingValue(t *testing.T) {
	t.Parallel()
	f, findings := parse(t, "tests/a.scenario.yaml", validYAML)
	if len(findings) > 0 {
		t.Fatal(findings)
	}
	for ptr, want := range map[string]int{
		"/scenarios/0":              4,
		"/scenarios/0/page":         5,
		"/scenarios/0/steps/1":      16,
		"/scenarios/0/expect/6":     43,
		"/scenarios/0/given/nope/x": 6,
		"":                          1,
	} {
		if line, _ := f.Locate(ptr); line != want {
			t.Errorf("Locate(%q) line = %d, want %d", ptr, line, want)
		}
	}
}
