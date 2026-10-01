// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/tools/internal/spec"
)

// writeFiles creates the given files under a fresh temporary directory and
// returns its path.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestScanFindsEveryEvidenceForm checks the three ways a test or CI file can
// cite a requirement, and that unrelated files, dependency directories and
// identifiers inside string literals are ignored.
// Verifies: QA-071.
func TestScanFindsEveryEvidenceForm_QA_071(t *testing.T) {
	t.Parallel()
	root := writeFiles(t, map[string]string{
		"backend/sync_test.go":             "// Verifies: SYN-005, SYN-006.\nfunc TestActivationIsAtomic_SYN_005(t *testing.T) {}\n",
		"packages/p/test/sync_test.dart":   "testWidgets('keeps last known good [SYN-006]', (t) async {});\n",
		"studio/packages/b/src/x.test.ts":  "test('contrast [A11Y-003]', () => {});\n",
		".github/workflows/ci.yml":         "      # Verifies: CI-006.\n",
		"Makefile":                         "# Verifies: CI-002.\n",
		"mk/go.mk":                         "# Verifies: CI-003.\n",
		"mk/old/go.mk":                     "# Verifies: SYN-001. (not a fragment)\n",
		"backend/x.mk":                     "# Verifies: SYN-001. (not a fragment)\n",
		"backend/sync.go":                  "// Verifies: SYN-001. (not a test file)\n",
		"studio/node_modules/x/y.test.ts":  "test('x [SYN-001]', () => {});\n",
		"backend/testdata/fixture_test.go": "// Verifies: SYN-001.\n",
	})

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []Evidence{
		{ID: "CI-006", Path: ".github/workflows/ci.yml", Line: 1, Kind: KindCI},
		{ID: "CI-002", Path: "Makefile", Line: 1, Kind: KindCI},
		{ID: "SYN-005", Path: "backend/sync_test.go", Line: 1, Kind: KindTest},
		{ID: "SYN-006", Path: "backend/sync_test.go", Line: 1, Kind: KindTest},
		{ID: "SYN-005", Path: "backend/sync_test.go", Line: 2, Kind: KindTest},
		{ID: "CI-003", Path: "mk/go.mk", Line: 1, Kind: KindCI},
		{ID: "SYN-006", Path: "packages/p/test/sync_test.dart", Line: 1, Kind: KindTest},
		{ID: "A11Y-003", Path: "studio/packages/b/src/x.test.ts", Line: 1, Kind: KindTest},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Scan() =\n%v\nwant\n%v", got, want)
	}
}

// specDoc parses a small specification for report tests.
func specDoc(t *testing.T) *spec.Document {
	t.Helper()
	doc, err := spec.Parse(strings.NewReader(strings.Join([]string{
		"| `SYN` | Sync | §1 |",
		"| `SYN-001` | P3 | MUST | a | DONE |",
		"| `SYN-002` | P3 | MUST | b | DONE |",
		"| `SYN-003` | P3 | SHOULD | c | DONE |",
		"| `SYN-004` | P3 | — | Withdrawn: gone. | WITHDRAWN |",
		"| `SYN-005` | P4 | MUST | d | SPEC |",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestViolationsFlagUnverifiedDoneRequirements checks the strict-mode rules:
// every DONE MUST requirement needs evidence, and evidence may only cite
// defined, active requirements.
// Verifies: QA-070.
func TestViolationsFlagUnverifiedDoneRequirements_QA_070(t *testing.T) {
	t.Parallel()
	evidence := []Evidence{
		{ID: "SYN-001", Path: "a_test.go", Line: 1, Kind: KindTest},
		{ID: "SYN-004", Path: "b_test.go", Line: 2, Kind: KindTest},
		{ID: "SYN-999", Path: "c_test.go", Line: 3, Kind: KindTest},
	}

	got := Build(specDoc(t), evidence).Violations()

	want := []string{
		"SYN-002 is DONE but no test or CI check verifies it",
		"c_test.go:3 cites SYN-999, which the specification does not define",
		"b_test.go:2 cites SYN-004, which is WITHDRAWN",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Violations() =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteMarkdownSummarisesPhasesAndEvidence checks the report layout.
// Verifies: QA-070.
func TestWriteMarkdownSummarisesPhasesAndEvidence_QA_070(t *testing.T) {
	t.Parallel()
	report := Build(specDoc(t), []Evidence{{ID: "SYN-001", Path: "a_test.go", Line: 7, Kind: KindTest}})
	var out bytes.Buffer

	if err := report.WriteMarkdown(&out); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"| P3 | 0 | 0 | 3 | 1 |",
		"| P4 | 1 | 0 | 0 | 0 |",
		"| `SYN-001` | P3 | MUST | DONE | `a_test.go:7` (test) |",
		"| `SYN-002` | P3 | MUST | DONE | — |",
		"## Violations",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report is missing %q\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "SYN-005") {
		t.Errorf("report lists SYN-005, which has neither evidence nor progress")
	}
}
