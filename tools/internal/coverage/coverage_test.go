// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package coverage

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// write creates a file with the given content, creating parent directories.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadGoProfileMapsImportPathsToRepoPaths checks module-path rewriting
// and that a block reported by several test binaries counts once.
func TestReadGoProfileMapsImportPathsToRepoPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, "backend", "go.mod"), "module example.com/plux/backend\n\ngo 1.27.0\n")
	write(t, filepath.Join(root, "backend", "coverage.out"), strings.Join([]string{
		"mode: atomic",
		"example.com/plux/backend/internal/pxl/vm.go:1.1,2.2 3 1",
		"example.com/plux/backend/internal/pxl/vm.go:3.1,4.2 2 0",
		"example.com/plux/backend/internal/pxl/vm.go:3.1,4.2 2 4",
		"example.com/plux/backend/cmd/plux/main.go:1.1,2.2 5 0",
	}, "\n"))

	got, err := ReadGoProfile(filepath.Join(root, "backend", "coverage.out"), root)
	if err != nil {
		t.Fatal(err)
	}

	want := []FileCoverage{
		{Path: "backend/internal/pxl/vm.go", Total: 5, Covered: 5},
		{Path: "backend/cmd/plux/main.go", Total: 5, Covered: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadGoProfile() = %+v, want %+v", got, want)
	}
}

// TestReadLCOVResolvesRelativeAndAbsolutePaths checks the path handling for
// the Flutter and Bun coverage outputs.
func TestReadLCOVResolvesRelativeAndAbsolutePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	abs := filepath.Join(root, "packages", "p", "lib", "b.dart")
	lcov := filepath.Join(root, "packages", "p", "coverage", "lcov.info")
	write(t, lcov, "SF:lib/a.dart\nLF:10\nLH:8\nend_of_record\nSF:"+abs+"\nLF:4\nLH:1\nend_of_record\n")

	got, err := ReadLCOV(lcov, root)
	if err != nil {
		t.Fatal(err)
	}

	want := []FileCoverage{
		{Path: "packages/p/lib/a.dart", Total: 10, Covered: 8},
		{Path: "packages/p/lib/b.dart", Total: 4, Covered: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadLCOV() = %+v, want %+v", got, want)
	}
}

// TestEvaluateAppliesTotalAndPackageFloors checks totals, stricter package
// floors, and floors declared for packages that do not exist yet.
func TestEvaluateAppliesTotalAndPackageFloors(t *testing.T) {
	t.Parallel()
	files := []FileCoverage{
		{Path: "backend/internal/pxl/vm.go", Total: 100, Covered: 84},
		{Path: "backend/cmd/plux/main.go", Total: 100, Covered: 90},
	}
	rule := Rule{Floor: 80, Packages: map[string]float64{
		"backend/internal/pxl":      85,
		"backend/internal/compiler": 85,
	}}

	got := Evaluate(rule, files)

	want := []Result{
		{Scope: "total", Floor: 80, Percent: 87, Present: true},
		{Scope: "backend/internal/compiler", Floor: 85, Present: false},
		{Scope: "backend/internal/pxl", Floor: 85, Percent: 84, Present: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %+v, want %+v", got, want)
	}
	var out bytes.Buffer
	ok, err := WriteTable(&out, "go", got)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("WriteTable() passed, want failure for backend/internal/pxl")
	}
	for _, want := range []string{"| `total` | 87.0% | 80% | pass |", "not present yet", "| `backend/internal/pxl` | 84.0% | 85% | **FAIL** |"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table is missing %q:\n%s", want, out.String())
		}
	}
}

// TestProjectCoverageConfigMatchesSpecification pins the repository's floors
// to QA-001: 85% for the critical packages, 80% for other Go and Dart code,
// 70% for Studio.
// Verifies: QA-001.
func TestProjectCoverageConfigMatchesSpecification_QA_001(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{"go": 80, "dart": 80, "studio": 70}
	for kind, floor := range want {
		if cfg[kind].Floor != floor {
			t.Errorf("%s floor = %v, want %v", kind, cfg[kind].Floor, floor)
		}
	}
	critical := map[string][]string{
		"go":   {"compiler", "pxl", "bundle", "delta", "dpop", "approval"},
		"dart": {"sync", "security", "bundle", "pxl"},
	}
	for kind, names := range critical {
		for _, name := range names {
			if !hasPackageFloor(cfg[kind], name, 85) {
				t.Errorf("%s has no 85%% floor for a %q package", kind, name)
			}
		}
	}
}

// hasPackageFloor reports whether rule has the given floor for a prefix
// ending in name.
func hasPackageFloor(rule Rule, name string, floor float64) bool {
	for prefix, f := range rule.Packages {
		if strings.HasSuffix(prefix, "/"+name) && f == floor {
			return true
		}
	}
	return false
}

// TestLoadConfigRejectsInvalidFloors checks configuration validation.
func TestLoadConfigRejectsInvalidFloors(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"floor above 100": `{"go": {"floor": 101}}`,
		"zero floor":      `{"go": {"floor": 0}}`,
		"bad package":     `{"go": {"floor": 80, "packages": {"x": -1}}}`,
		"unknown field":   `{"go": {"floor": 80, "flor": 1}}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "coverage.json")
			write(t, path, content)

			if _, err := LoadConfig(path); err == nil {
				t.Fatalf("LoadConfig(%s) succeeded, want an error", content)
			}
		})
	}
}
