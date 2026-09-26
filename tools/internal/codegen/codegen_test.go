// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamesFollowTheLanguageConventions checks identifier derivation.
func TestNamesFollowTheLanguageConventions(t *testing.T) {
	t.Parallel()
	tests := []struct{ key, goName, lower string }{
		{"pxl.operationBudget", "PXLOperationBudget", "pxlOperationBudget"},
		{"bundle.pageSectionSize", "BundlePageSectionSize", "bundlePageSectionSize"},
		{"document.jsonDepth", "DocumentJSONDepth", "documentJsonDepth"},
		{"on-enter", "OnEnter", "onEnter"},
		{"installation", "Installation", "installation"},
	}
	for _, tt := range tests {
		if got := GoName(tt.key); got != tt.goName {
			t.Errorf("GoName(%q) = %q, want %q", tt.key, got, tt.goName)
		}
		if got := LowerCamel(tt.key); got != tt.lower {
			t.Errorf("LowerCamel(%q) = %q, want %q", tt.key, got, tt.lower)
		}
	}
	if got := quoteDart(`it's $x\n`); got != `'it\'s \$x\\n'` {
		t.Errorf("quoteDart = %s", got)
	}
	if got := wrapComment("// ", "aaa bbb ccc", 9); got != "// aaa\n// bbb\n// ccc\n" {
		t.Errorf("wrapComment = %q", got)
	}
}

// writeLimits writes a registry file with the given entries.
func writeLimits(t *testing.T, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(path, []byte(`{"$schema":"x","limits":[`+entries+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// entry builds one registry entry, overriding fields in JSON.
func entry(key string, overrides ...string) string {
	fields := map[string]string{
		"key": `"` + key + `"`, "unit": `"count"`, "default": "10", "max": "20", "scopes": `["installation","app"]`,
		"enforcedBy": `["compiler"]`, "phase": `"P1"`, "requirements": `["SCH-005"]`, "description": `"Things."`,
	}
	for _, o := range overrides {
		k, v, _ := strings.Cut(o, "=")
		fields[k] = v
	}
	parts := make([]string, 0, len(fields))
	for _, k := range []string{"key", "unit", "default", "warning", "max", "scopes", "enforcedBy", "phase", "requirements", "description"} {
		if v, ok := fields[k]; ok {
			parts = append(parts, `"`+k+`":`+v)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// TestLoadLimitsValidatesEveryField_LIM_001 checks the registry rules.
// Verifies: LIM-001.
func TestLoadLimitsValidatesEveryField_LIM_001(t *testing.T) {
	t.Parallel()
	if _, err := LoadLimits(writeLimits(t, entry("a.one")+","+entry("b.two", "warning=5"))); err != nil {
		t.Fatalf("valid registry rejected: %v", err)
	}
	bad := map[string]string{
		"unsorted":          entry("b.two") + "," + entry("a.one"),
		"duplicate":         entry("a.one") + "," + entry("a.one"),
		"bad key":           entry("Aone"),
		"unknown unit":      entry("a.one", `unit="furlongs"`),
		"default above max": entry("a.one", "default=30"),
		"zero default":      entry("a.one", "default=0"),
		"warning too high":  entry("a.one", "warning=10"),
		"negative warning":  entry("a.one", "warning=-1"),
		"bad phase":         entry("a.one", `phase="P16"`),
		"no sentence":       entry("a.one", `description="things"`),
		"no requirements":   entry("a.one", "requirements=[]"),
		"bad requirement":   entry("a.one", `requirements=["x"]`),
		"unknown scope":     entry("a.one", `scopes=["planet"]`),
		"scope order":       entry("a.one", `scopes=["app","installation"]`),
		"no enforcer":       entry("a.one", "enforcedBy=[]"),
		"unknown field":     strings.TrimSuffix(entry("a.one"), "}") + `,"extra":1}`,
	}
	for name, entries := range bad {
		if _, err := LoadLimits(writeLimits(t, entries)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadLimits(writeLimits(t, "")); err == nil {
		t.Error("empty registry accepted")
	}
	if _, err := LoadLimits(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
}

// TestLimitsFilesRenderEveryLanguage_LIM_001 checks that each output names
// every key with the generated marker.
// Verifies: LIM-001, SCH-001.
func TestLimitsFilesRenderEveryLanguage_LIM_001(t *testing.T) {
	t.Parallel()
	limits, err := LoadLimits(writeLimits(t, entry("page.nodes", "warning=5")+","+entry("pxl.operationBudget", `unit="operations"`)))
	if err != nil {
		t.Fatal(err)
	}
	files, err := LimitsFiles(limits)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"backend/internal/schema/limits/limits_gen.go":       {"PageNodes Key = \"page.nodes\"", "UnitOperations", "Warning: 5"},
		"packages/plux_flutter/lib/src/schema/limits.g.dart": {"pageNodes('page.nodes'", "pxlOperationBudget("},
		"studio/packages/schema/src/limits.gen.ts":           {`key: "page.nodes"`, "AGPL-3.0-only"},
		"docs/reference/limits.md":                           {"| `page.nodes` | count | 10 | 5 |", "| `pxl.operationBudget` | operations | 10 | 80% |"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files", len(files))
	}
	for _, f := range files {
		for _, fragment := range append(want[f.Path], "Code generated by schemagen from schema/limits.json. DO NOT EDIT.") {
			if !strings.Contains(string(f.Content), fragment) {
				t.Errorf("%s lacks %q", f.Path, fragment)
			}
		}
	}
}

// TestWriteAllWritesOnlyChangedFiles checks idempotent writing.
func TestWriteAllWritesOnlyChangedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []File{{Path: "a/b.txt", Content: []byte("x")}, {Path: "c.txt", Content: []byte("y")}}
	changed, err := WriteAll(root, files)
	if err != nil || len(changed) != 2 {
		t.Fatalf("first write: %v, %v", changed, err)
	}
	files[1].Content = []byte("z")
	changed, err = WriteAll(root, files)
	if err != nil || len(changed) != 1 || changed[0] != "c.txt" {
		t.Fatalf("second write: %v, %v", changed, err)
	}
	if _, err := goFile("x.go", []byte("package")); err == nil {
		t.Error("invalid Go source accepted")
	}
}
