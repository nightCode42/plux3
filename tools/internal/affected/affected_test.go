// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"Makefile", "Makefile", true},
		{"Makefile", "mk/Makefile", false},
		{"backend/**", "backend/go.mod", true},
		{"backend/**", "backend/internal/x/y.go", true},
		{"backend/**", "backend", true},
		{"backend/**", "backendx/y", false},
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/a/b.md", true},
		{"**/*.md", "docs/a/b.mdx", false},
		{"packages/*/LICENSE", "packages/p/LICENSE", true},
		{"packages/*/LICENSE", "packages/p/native/LICENSE", false},
		{"packages/**/native/**/LICENSE*", "packages/p/native/zstd/LICENSE", true},
		{"packages/**/native/**/LICENSE*", "packages/p/native/LICENSE.txt", true},
		{"packages/**/*.g.dart", "packages/p/lib/src/a.g.dart", true},
		{"packages/**/*.g.dart", "packages/p/lib/src/a.dart", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y/c", false},
	}
	for _, c := range cases {
		g, err := ParseGlob(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Match(c.path); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestParseGlobRejectsMalformedPatterns(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "/abs", "dir/", "a//b", "a/../b", "./a", "a**/b", "a/[", "a/**x"} {
		if _, err := ParseGlob(p); err == nil {
			t.Errorf("ParseGlob(%q) accepted a malformed pattern", p)
		}
	}
	if _, err := ParsePathSet([]string{"!a/**"}); err == nil {
		t.Error("ParsePathSet accepted an exclusion before any inclusion")
	}
}

func TestPathSetLastMatchWins(t *testing.T) {
	t.Parallel()
	s, err := ParsePathSet([]string{"packages/**", "!**/*.md", "packages/p/CHANGELOG.md"})
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		"packages/p/lib/a.dart":   true,
		"packages/p/README.md":    false,
		"packages/p/CHANGELOG.md": true,
		"apps/a/lib/a.dart":       false,
	} {
		if got := s.Contains(p); got != want {
			t.Errorf("Contains(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestEmbedPatternsReadsEveryForm(t *testing.T) {
	t.Parallel()
	src := "package p\n\n// A comment that mentions //go:embed is not a directive.\n" +
		"//go:embed schemas/*.json all:migrations\n//go:embed \"with space.txt\" `raw.bin`\nvar x string\n"
	got := embedPatterns([]byte(src))
	want := []string{"schemas/*.json", "migrations", "with space.txt", "raw.bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("embedPatterns() = %q, want %q", got, want)
	}
	for rest, want := range map[string]bool{
		"schemas/a.json":     true,
		"schemas/a.yaml":     false,
		"schemas/sub/a.json": false,
		"other/a.json":       false,
	} {
		if got := embedMatch("schemas/*.json", rest); got != want {
			t.Errorf("embedMatch(schemas/*.json, %q) = %v, want %v", rest, got, want)
		}
	}
	if !embedMatch("migrations", "migrations/001.sql") {
		t.Error("a directory pattern does not embed the files in it")
	}
}

const workflow = `name: CI
on:
  push:
env:
  A: 1

jobs:
  changes:
    runs-on: ubuntu-latest

  # The Go jobs.
  go-test:
    runs-on: ubuntu-latest
    steps:
      - run: make go-cover

      - run: make go-budgets
  dart:
    runs-on: ubuntu-latest # a comment
`

func TestWorkflowBlocksSplitsJobsAndAttachesTheirComments(t *testing.T) {
	t.Parallel()
	global, jobs := workflowBlocks(workflow)
	if !strings.HasSuffix(global, "jobs:\n") || strings.Contains(global, "runs-on") {
		t.Errorf("global = %q", global)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(jobs)), []string{"changes", "dart", "go-test"}) {
		t.Fatalf("jobs = %v", jobs)
	}
	if !strings.Contains(jobs["go-test"], "  # The Go jobs.\n  go-test:") || !strings.Contains(jobs["go-test"], "go-budgets") {
		t.Errorf("go-test block = %q", jobs["go-test"])
	}
	if strings.Contains(jobs["changes"], "Go jobs") {
		t.Errorf("the comment above go-test went to changes: %q", jobs["changes"])
	}

	g, changed := changedBlocks(workflow, strings.Replace(workflow, "make go-budgets", "make go-budgets -j2", 1))
	if g || !slices.Equal(changed, []string{"go-test"}) {
		t.Errorf("a step change: global %v, jobs %v", g, changed)
	}
	g, _ = changedBlocks(workflow, strings.Replace(workflow, "A: 1", "A: 2", 1))
	if !g {
		t.Error("an env change is not a change outside the job blocks")
	}
	g, changed = changedBlocks(workflow, workflow+"  studio:\n    runs-on: ubuntu-latest\n")
	if g || !slices.Equal(changed, []string{"studio"}) {
		t.Errorf("an added job: global %v, jobs %v", g, changed)
	}
}

// writeRepo writes files under a temporary directory.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fixture is a small repository: a Go workspace of two modules and a pub
// workspace of three packages.
func fixture(t *testing.T) string {
	t.Helper()
	return writeRepo(t, map[string]string{
		"go.work":                               "go 1.27\n\nuse (\n\t./srv\n\t./tools\n)\n",
		"srv/go.mod":                            "module example.com/srv\n",
		"srv/cmd/app/main.go":                   "package main\n\nimport _ \"example.com/srv/internal/core\"\n",
		"srv/internal/core/core.go":             "package core\n\nimport (\n\t\"fmt\"\n\t_ \"example.com/srv/internal/leaf\"\n)\n\n//go:embed data/*.json\nvar _ = fmt.Sprint\n",
		"srv/internal/core/x_test.go":           "package core\n\nimport _ \"example.com/srv/internal/testkit\"\n",
		"srv/internal/leaf/leaf.go":             "package leaf\n",
		"srv/internal/testkit/kit.go":           "package testkit\n",
		"srv/internal/other/other.go":           "package other\n",
		"srv/internal/core/testdata/golden.txt": "x\n",
		"srv/internal/core/data/a.json":         "{}\n",
		"srv/nested/go.mod":                     "module example.com/nested\n",
		"srv/nested/n.go":                       "package nested\n",
		"tools/go.mod":                          "module example.com/tools\n",
		"tools/cmd/t/main.go":                   "package main\n",
		"pubspec.yaml":                          "name: ws\nworkspace:\n  - apps/app\n  - packages/runtime\n  - packages/devtools\n",
		"apps/app/pubspec.yaml":                 "name: app\nresolution: workspace\ndependencies:\n  flutter:\n    sdk: flutter\n  runtime: any\ndev_dependencies:\n  devtools: any\n",
		"packages/runtime/pubspec.yaml":         "name: runtime\n# a comment\ndependencies:\n  http: ^1.0.0\n",
		"packages/devtools/pubspec.yaml":        "name: devtools\ndependencies:\n  runtime: any\n",
		"packages/alone/pubspec.yaml":           "name: alone\n",
		"ci/affected.json": `{
  "always": ["changes"],
  "everything": ["Makefile"],
  "noJob": ["docs/**", "**/*.md"],
  "jobs": {
    "build": {"go": ["srv/cmd/..."]},
    "test": {"goTest": ["srv/internal/core"]},
    "app": {"dart": ["apps/app"], "exclude": ["**/ios/**"], "make": ["app-check"]},
    "runtime": {"dart": ["packages/runtime"], "paths": ["mk/runtime.mk"], "skipOnPush": true},
    "alone": {"dart": ["packages/alone"]}
  }
}`,
	})
}

func load(t *testing.T, root string) *Rules {
	t.Helper()
	r, err := Load(root, filepath.Join(root, "ci", "affected.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSelectFollowsTheGoAndDartGraphs checks that a changed file runs
// exactly the jobs whose packages depend on it.
// Verifies: CI-002.
func TestSelectFollowsTheGoAndDartGraphs_CI_002(t *testing.T) {
	t.Parallel()
	rules := load(t, fixture(t))
	cases := []struct {
		file string
		want []string
	}{
		{"srv/internal/leaf/leaf.go", []string{"build", "test"}},     // a dependency of both
		{"srv/internal/core/x_test.go", []string{"test"}},            // tests are built only by goTest
		{"srv/internal/testkit/kit.go", []string{"test"}},            // a test-only import
		{"srv/internal/core/testdata/golden.txt", []string{"test"}},  // the target's testdata
		{"srv/internal/core/data/a.json", []string{"build", "test"}}, // embedded
		{"srv/go.sum", []string{"build", "test"}},                    // the module's dependencies
		{"go.work", []string{"build", "test"}},                       // the workspace
		{"srv/internal/other/other.go", nil},                         // nothing imports it: unknown, see below
		{"packages/runtime/lib/a.dart", []string{"app", "runtime"}},  // a dependency of app
		{"packages/runtime/test/a_test.dart", []string{"runtime"}},   // a dependency's tests
		{"packages/runtime/README.md", []string{"runtime"}},          // the package's own Markdown
		{"packages/devtools/lib/d.dart", []string{"app"}},            // a dev dependency
		{"apps/app/ios/Runner/AppDelegate.swift", nil},               // excluded: unknown
		{"pubspec.lock", []string{"app", "runtime"}},                 // the workspace's lockfile
		{"packages/alone/lib/a.dart", []string{"alone"}},             // outside the workspace
		{"mk/runtime.mk", []string{"runtime"}},                       // a path glob
	}
	for _, c := range cases {
		res := rules.Select(Change{Event: "pull_request", Files: []string{c.file}})
		if c.want == nil {
			if res.Everything == "" {
				t.Errorf("%s: selected %v; a file no job needs and no rule names must run everything", c.file, res.Selected())
			}
			continue
		}
		if res.Everything != "" || !slices.Equal(res.Selected(), c.want) {
			t.Errorf("%s: selected %v (everything: %q), want %v", c.file, res.Selected(), res.Everything, c.want)
		}
	}
}

// TestSelectRunsEverythingWhenInDoubt checks the cases that run every job.
// Verifies: CI-002.
func TestSelectRunsEverythingWhenInDoubt_CI_002(t *testing.T) {
	t.Parallel()
	rules := load(t, fixture(t))
	all := []string{"alone", "app", "build", "runtime", "test"}
	cases := []struct {
		name   string
		change Change
		want   []string
		why    string
	}{
		{"unknown file", Change{Event: "pull_request", Files: []string{"docs/a.md", "newdir/x"}}, all, "no rule in ci/affected.json names newdir/x"},
		{"everything file", Change{Event: "pull_request", Files: []string{"Makefile"}}, all, "Makefile changed"},
		{"workflow outside jobs", Change{
			Event: "pull_request", Files: []string{WorkflowPath}, BaseWorkflow: workflow,
			HeadWorkflow: strings.Replace(workflow, "A: 1", "A: 2", 1),
		}, all, "outside the job blocks"},
		{"workflow added", Change{Event: "pull_request", Files: []string{WorkflowPath}, HeadWorkflow: workflow}, all, "added or removed"},
		{"push", Change{Event: "push"}, []string{"alone", "app", "build", "test"}, "a push runs every job"},
		{"merge queue", Change{Event: "merge_group"}, []string{"alone", "app", "build", "test"}, "a merge group runs every job"},
		{"schedule", Change{Event: "schedule"}, all, "a schedule run"},
		{"manual", Change{Event: "workflow_dispatch"}, all, "a workflow dispatch run"},
	}
	for _, c := range cases {
		res := rules.Select(c.change)
		if !slices.Equal(res.Selected(), c.want) || !strings.Contains(res.Everything, c.why) {
			t.Errorf("%s: selected %v because %q; want %v because %q", c.name, res.Selected(), res.Everything, c.want, c.why)
		}
	}
}

func TestSelectRunsTheJobsWhoseWorkflowBlockChanged(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	rules := load(t, root)
	base := "on: push\njobs:\n  build:\n    runs-on: a\n  app:\n    runs-on: a\n"
	head := strings.Replace(base, "  app:\n    runs-on: a", "  app:\n    runs-on: b", 1)
	res := rules.Select(Change{Event: "pull_request", Files: []string{WorkflowPath}, BaseWorkflow: base, HeadWorkflow: head})
	if res.Everything != "" || !slices.Equal(res.Selected(), []string{"app"}) {
		t.Fatalf("selected %v (everything: %q), want [app]", res.Selected(), res.Everything)
	}
	if got := res.Reasons["app"]; len(got) != 1 || !strings.Contains(got[0], "block of "+WorkflowPath) {
		t.Errorf("reasons = %q", got)
	}
	if res = rules.Select(Change{Event: "pull_request", Files: []string{"docs/guide.md"}}); len(res.Selected()) != 0 ||
		!slices.Equal(res.Skipped, []string{"docs/guide.md"}) {
		t.Errorf("documentation selected %v, skipped %v", res.Selected(), res.Skipped)
	}
}

func TestSummaryListsReasonsAndSkippedJobs(t *testing.T) {
	t.Parallel()
	rules := load(t, fixture(t))
	res := rules.Select(Change{Event: "pull_request", Files: []string{"srv/internal/leaf/leaf.go", "docs/a.md"}})
	got := res.Summary(rules.Always())
	for _, want := range []string{
		"Always: changes.",
		"- **build**: srv/internal/leaf/leaf.go (the Go package srv/internal/leaf)",
		"- **test**: srv/internal/leaf/leaf.go (the Go package srv/internal/leaf)",
		"Not affected: alone, app, runtime.",
		"Changed files no job needs: docs/a.md.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if job := rules.Jobs()[1]; job.Name != "app" || !slices.Equal(job.Make, []string{"app-check"}) {
		t.Errorf("Jobs()[1] = %+v", job)
	}
}

func TestLoadRejectsBadRules(t *testing.T) {
	t.Parallel()
	for name, rules := range map[string]string{
		"unknown field":  `{"jobs": {"a": {"paths": ["x"], "path": ["y"]}}}`,
		"no roots":       `{"jobs": {"a": {"make": ["x"]}}}`,
		"bad glob":       `{"jobs": {"a": {"paths": ["/x"]}}}`,
		"unknown go pkg": `{"jobs": {"a": {"go": ["srv/nope"]}}}`,
		"bad noJob":      `{"noJob": ["!x"], "jobs": {}}`,
		"missing dart":   `{"jobs": {"a": {"dart": ["packages/nope"]}}}`,
	} {
		root := fixture(t)
		p := filepath.Join(root, "ci", "affected.json")
		if err := os.WriteFile(p, []byte(rules), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root, p); err == nil {
			t.Errorf("%s: Load accepted %s", name, rules)
		}
	}
}

func TestGoGraphSkipsNestedModulesAndReadsUseLines(t *testing.T) {
	t.Parallel()
	if got := workUses("go 1.27\nuse ./a // a\nuse (\n\t./b // b\n\t// c\n)\n"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("workUses = %q", got)
	}
	g, err := loadGoGraph(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.packages["srv/nested"]; ok {
		t.Error("a nested module was read as a package of srv")
	}
	if dir := g.resolve("example.com/srv/internal/leaf"); dir != "srv/internal/leaf" {
		t.Errorf("resolve = %q", dir)
	}
	if dir := g.resolve("example.com/srvx/y"); dir != "" {
		t.Errorf("resolve of another module = %q", dir)
	}
}
