// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"slices"
	"testing"
	"testing/fstest"
)

// Verifies: TST-001.
func TestMatch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{DefaultTests, "tests/a.scenario.yaml", true},
		{DefaultTests, "tests/a.scenario.json", true},
		{DefaultTests, "tests/deep/er/a.scenario.yaml", true},
		{DefaultTests, "tests/a.scenario.yml", false},
		{DefaultTests, "app.json", false},
		{DefaultTests, "other/a.scenario.yaml", false},
		{"**/*.scenario.yaml", "a.scenario.yaml", true},
		{"tests/*.yaml", "tests/x/a.yaml", false},
		{"tests/?.yaml", "tests/a.yaml", true},
		{"tests/?.yaml", "tests/ab.yaml", false},
	} {
		got, err := Match(c.pattern, c.name)
		if err != nil || got != c.want {
			t.Errorf("Match(%q, %q) = %v, %v; want %v", c.pattern, c.name, got, err, c.want)
		}
	}
	if _, err := Match("tests/{a,b", "tests/a"); err == nil {
		t.Error("an unclosed brace was accepted")
	}
	if _, err := Match("tests/[", "tests/a"); err == nil {
		t.Error("a malformed segment was accepted")
	}
}

// Verifies: TST-001.
func TestFindListsMatchesSortedAndSkipsHiddenDirectories(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"tests/b.scenario.yaml":        {},
		"tests/a/z.scenario.json":      {},
		"tests/a.scenario.yaml":        {},
		".git/tests/x.scenario.yaml":   {},
		"tests/readme.md":              {},
		"plugins/p/tests/x.scenario.y": {},
	}
	got, err := Find(fsys, DefaultTests)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tests/a.scenario.yaml", "tests/a/z.scenario.json", "tests/b.scenario.yaml"}
	if !slices.Equal(got, want) {
		t.Errorf("Find = %v, want %v", got, want)
	}
}
