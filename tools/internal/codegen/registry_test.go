// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// loadRegistry loads the committed registry.
func loadRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRegistryFilesAreDeterministic renders the committed registry twice
// and checks every output's path, header and stability.
//
// Verifies: WGT-002, WGT-003.
func TestRegistryFilesAreDeterministic(t *testing.T) {
	t.Parallel()
	r := loadRegistry(t)
	first, err := RegistryFiles(r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RegistryFiles(loadRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		registry.LockFile: `"widget/Text": `,
		registryGoPath:    "var widgets = [...]Widget{",
		registryDartPath:  "const List<WidgetDescriptor> widgetDescriptors = [",
		renderDartPath:    "const Map<int, NodeBuilder> generatedBuilders = {",
		registryTSPath:    "export const widgets: readonly WidgetDescriptor[] = [",
		widgetsDocPath:    "# Widget Reference",
		actionsDocPath:    "# Action Reference",
		coveragePath:      "# Flutter Coverage Table",
	}
	if len(first) != len(want) {
		t.Fatalf("%d files, want %d", len(first), len(want))
	}
	for i, f := range first {
		snippet, ok := want[f.Path]
		if !ok {
			t.Errorf("unexpected file %s", f.Path)
			continue
		}
		if !bytes.Contains(f.Content, []byte(snippet)) {
			t.Errorf("%s lacks %q", f.Path, snippet)
		}
		if f.Path != registry.LockFile && !bytes.Contains(f.Content, []byte("DO NOT EDIT.")) {
			t.Errorf("%s lacks the generated-code marker", f.Path)
		}
		if !bytes.Equal(f.Content, second[i].Content) {
			t.Errorf("%s differs between runs", f.Path)
		}
	}
}

// TestCoverageTableListsEveryParameter checks that the table has a row for
// every covered parameter and reports exclusions with their reasons.
//
// Verifies: WGT-003.
func TestCoverageTableListsEveryParameter(t *testing.T) {
	t.Parallel()
	r := loadRegistry(t)
	table := string(coverageMarkdown(r))
	var rows int
	for _, c := range r.Coverage {
		rows += len(c.Params)
	}
	if got := strings.Count(table, "\n| `"); got < rows {
		t.Errorf("%d parameter rows, want at least %d", got, rows)
	}
	for _, want := range []string{
		"### Text · `Text`",
		"| `key` | `Key?` | excluded: non-serialisable |",
		"| `style` | `TextStyle?` | prop `style` |",
		"Flutter " + r.API.Flutter,
	} {
		if !strings.Contains(table, want) {
			t.Errorf("coverage table lacks %q", want)
		}
	}
}

func TestCollapseJSON(t *testing.T) {
	t.Parallel()
	in := `[
  {
    "name": "a, b",
    "tags": [
      "x",
      "y"
    ],
    "nested": {
      "list": [
        {
          "k": "v: w",
          "n": 1
        }
      ]
    }
  }
]`
	got := string(collapseJSON([]byte(in)))
	for _, want := range []string{`"tags": ["x", "y"],`, `{"k": "v: w", "n": 1}`, `"name": "a, b",`, `    "nested": {`} {
		if !strings.Contains(got, want) {
			t.Errorf("collapseJSON lacks %q:\n%s", want, got)
		}
	}
	var v any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("collapsed JSON is invalid: %v\n%s", err, got)
	}
	if got := spaceJSON(`{"a":"x,\"y\":z","b":[1,2]}`); got != `{"a": "x,\"y\":z", "b": [1, 2]}` {
		t.Errorf("spaceJSON = %s", got)
	}
}

func TestMarkdownHelpers(t *testing.T) {
	t.Parallel()
	one, two := 1.0, 2.0
	lo, hi := 1, 5
	tests := []struct {
		c    *registry.Constraints
		want string
	}{
		{nil, ""},
		{&registry.Constraints{}, ""},
		{&registry.Constraints{Min: &one, Max: &two}, "Must be at least 1 and at most 2."},
		{&registry.Constraints{MinLength: &lo, MaxLength: &hi, Pattern: "^a"}, "Must be length at least 1 and length at most 5 and matches `^a`."},
	}
	for _, tt := range tests {
		if got := mdConstraints(tt.c); got != tt.want {
			t.Errorf("mdConstraints = %q, want %q", got, tt.want)
		}
	}
	for c, want := range map[*registry.Children]string{
		{}:                   "",
		{Min: &lo}:           " (at least 1)",
		{Max: &hi}:           " (at most 5)",
		{Min: &lo, Max: &hi}: " (1 to 5)",
	} {
		if got := mdChildren(c); got != want {
			t.Errorf("mdChildren = %q, want %q", got, want)
		}
	}
	if got := mdCell("a|b\nc"); got != `a\|b c` {
		t.Errorf("mdCell = %q", got)
	}
	if got := goConstraints(&registry.Constraints{Min: &one, MaxLength: &hi, Pattern: "x"}); got != `Constraints{Min: Bound{Value: 1, Set: true}, MaxLength: Bound{Value: 5, Set: true}, Pattern: "x"}` {
		t.Errorf("goConstraints = %s", got)
	}
	if got := compactJSON(json.RawMessage(`{ "a" : 1 }`)); got != `{"a":1}` {
		t.Errorf("compactJSON = %s", got)
	}
	if got := ctorList("Image", []string{"", "network"}); got != "Image, Image.network" {
		t.Errorf("ctorList = %s", got)
	}
}
