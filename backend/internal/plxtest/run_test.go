// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const starterDir = "../../../schema/testdata/documents/starter"

// starter is the conformance project with its scenario files replaced by
// scenarios, by path.
func starter(t *testing.T, scenarios map[string]string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	root := os.DirFS(starterDir)
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, "tests/") {
			return err
		}
		data, err := fs.ReadFile(root, p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, text := range scenarios {
		m[p] = &fstest.MapFile{Data: []byte(text)}
	}
	return m
}

// entropy is a key's worth of fixed bytes: the generated files are
// compared, so the key of the run is fixed.
func entropy() io.Reader { return bytes.NewReader(bytes.Repeat([]byte("plux-test"), 8)) }

func prepare(t *testing.T, project fs.FS) *Plan {
	t.Helper()
	plan, err := Prepare(Options{Project: project, Compiler: compiler.DefaultOptions(), Entropy: entropy(), Runtime: Dependency{Version: "^0.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func codes(plan *Plan) []plxerr.Code {
	var out []plxerr.Code
	for _, f := range plan.Findings {
		out = append(out, f.Code)
	}
	return out
}

const placeScenario = `schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: opens the profile
    page: place
    given:
      params:
        name: Harbour
      state:
        counter: 3
    steps:
      - tap: place-profile
    expect:
      - navigatedTo: profile
      - stateEquals:
          counter: 3
`

// Verifies: TST-002.
func TestPrepareStarterGeneratesADeterministicSignedTestProject(t *testing.T) {
	t.Parallel()
	project := os.DirFS(starterDir)
	plan := prepare(t, project)
	if !plan.Runnable() {
		t.Fatalf("not runnable: %v %v", plan.Compile, plan.Findings)
	}
	if len(plan.Cases) != 1 || plan.Cases[0].File != "tests/place.scenario.yaml" || plan.Cases[0].Line == 0 {
		t.Fatalf("cases = %+v", plan.Cases)
	}
	again := prepare(t, project)
	for name, data := range plan.Files {
		if !bytes.Equal(data, again.Files[name]) {
			t.Errorf("%s differs between two preparations", name)
		}
	}
	if len(plan.Files) != len(again.Files) {
		t.Error("the file sets differ")
	}
	for _, f := range []string{"assets/plux/baseline.json", "assets/plux/keys.json", "assets/plux/bundles/_app.pxb", "assets/plux/bundles/welcome.pxb", "pubspec.yaml", "test/plux_harness.dart", "test/plux_project.dart"} {
		if _, ok := plan.Files[f]; !ok {
			t.Errorf("no %s in %v", f, slices.Sorted(keys(plan.Files)))
		}
	}
	var base struct {
		Bundles []struct{ Signature, KeyID string }
		Keys    []struct{ KeyID, PublicKey string }
	}
	if err := json.Unmarshal(plan.Files["assets/plux/baseline.json"], &base); err != nil {
		t.Fatal(err)
	}
	if len(base.Keys) != 1 || len(base.Bundles) != 2 || base.Bundles[0].KeyID != base.Keys[0].KeyID || base.Bundles[0].Signature == "" {
		t.Errorf("baseline = %+v", base)
	}
	if strings.Contains(string(plan.Files["test/plux_harness.dart"]), "DataMocks") {
		t.Error("the harness selects data mocks although no scenario uses one")
	}
}

func keys(m map[string][]byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// Verifies: TST-002.
func TestGeneratedFilesMatchGolden(t *testing.T) {
	t.Parallel()
	plan := prepare(t, os.DirFS(starterDir))
	var b strings.Builder
	for _, name := range slices.Sorted(keys(plan.Files)) {
		if strings.HasPrefix(name, BaselineDir+"/bundles/") || strings.HasPrefix(name, BaselineDir+"/assets/") {
			continue
		}
		b.WriteString("=== " + name + " ===\n" + string(plan.Files[name]))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	golden := filepath.Join("testdata", "starter.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden) //nolint:gosec // the test's own golden
	if err != nil || string(want) != b.String() {
		t.Errorf("the generated project differs from %s (%v); review, then run go test ./internal/plxtest -update", golden, err)
	}
}

// Verifies: TST-001, TST-002.
func TestGeneratedTestsCoverEveryStepAndExpectation(t *testing.T) {
	t.Parallel()
	scenarios := `schemaVersion: 1.0.0
kind: scenarios
scenarios:
  - name: everything
    page: place
    given:
      params:
        name: Harbour
      state:
        counter: 1
      dataSources:
        places:
          state: error
    steps:
      - tap: place-profile
      - enterText:
          testId: search
          text: "it's $5"
      - scroll:
          testId: list
          dy: -300.5
      - waitFor:
          testId: done
      - trigger:
          event: placeShared
          payload:
            name: Harbour
    expect:
      - visible: a
      - notVisible: b
      - textEquals:
          testId: c
          text: d
      - navigatedTo: profile
      - actionCalled:
          name: navigate
          args:
            route: profile
          times: 1
      - functionCalled:
          name: score
      - stateEquals:
          counter: 1
`
	proj := starter(t, map[string]string{"tests/all.scenario.yaml": scenarios})
	plan := prepare(t, proj)
	// The project declares no data source "places": only the references
	// are reported; generation itself is exercised through Generate.
	if !slices.Contains(codes(plan), plxerr.ScenarioReferenceUnknown) {
		t.Fatalf("findings = %v", plan.Findings)
	}
	f, findings := parse(t, "tests/all.scenario.yaml", scenarios)
	if len(findings) > 0 {
		t.Fatal(findings)
	}
	files, cases, err := Generate(Input{Release: &Release{AppID: "app", KeyID: "k", PublicKey: []byte{1, 2}, Files: map[string][]byte{}}, Files: []*File{f}, Runtime: Dependency{Path: "/src/plux_flutter"}})
	if err != nil || len(cases) != 1 {
		t.Fatalf("%v %v", err, cases)
	}
	test := string(files["test/01_tests_all_scenario_yaml_test.dart"])
	for _, want := range []string{
		"await s.tap('place-profile', at: 'tests/all.scenario.yaml:",
		`await s.enterText('search', 'it\'s \$5', at:`,
		"await s.scroll('list', 0, -300.5, at:",
		"await s.waitFor('done', 5000, at:",
		"await s.trigger('placeShared', <String, Object?>{'name': json('\"Harbour\"'), }, at:",
		"s.visible('a', at:", "s.notVisible('b', at:", "s.textEquals('c', 'd', at:", "s.navigatedTo('profile', at:",
		"s.actionCalled('navigate', <String, Object?>{'route': json('\"profile\"'), }, 1, at:",
		"s.functionCalled('score', <String, Object?>{}, null, at:",
		"s.stateEquals('counter', json('1'), at:",
		"dataSources: {'places': 'error', }",
		"} finally {\n        await s.finish();",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("the test lacks %q:\n%s", want, test)
		}
	}
	if h := string(files["test/plux_harness.dart"]); !strings.Contains(h, "DataMocks({for") || strings.Contains(h, "{{") {
		t.Errorf("harness: data mocks missing or a placeholder left")
	}
	if p := string(files["pubspec.yaml"]); !strings.Contains(p, "path: '/src/plux_flutter'") {
		t.Errorf("pubspec: %s", p)
	}
}

// Verifies: TST-001.
func TestPrepareReportsWhatTheProjectLacksAndWhatCannotRunYet(t *testing.T) {
	t.Parallel()
	const head = "schemaVersion: 1.0.0\nkind: scenarios\nscenarios:\n"
	for name, c := range map[string]struct {
		text string
		want []plxerr.Code
		line int
	}{
		"unknown page": {head + "  - name: n\n    page: nowhere\n    expect:\n      - visible: a\n", []plxerr.Code{plxerr.ScenarioReferenceUnknown}, 5},
		"state that is not exposed": {
			head + "  - name: n\n    page: place\n    given:\n      state:\n        nothing: 1\n    expect:\n      - visible: a\n",
			[]plxerr.Code{plxerr.ScenarioReferenceUnknown},
			8,
		},
		"unknown state expectation": {head + "  - name: n\n    page: place\n    expect:\n      - stateEquals:\n          nothing: 1\n", []plxerr.Code{plxerr.ScenarioReferenceUnknown}, 8},
		"a flow":                    {head + "  - name: n\n    flow: require-sign-in\n    expect:\n      - visible: a\n", []plxerr.Code{plxerr.ScenarioUnsupported}, 5},
		"a mock value": {
			head + "  - name: n\n    page: place\n    given:\n      dataSources:\n        places:\n          state: success\n          mock: []\n    expect:\n      - visible: a\n",
			[]plxerr.Code{plxerr.ScenarioReferenceUnknown, plxerr.ScenarioUnsupported},
			8,
		},
	} {
		plan := prepare(t, starter(t, map[string]string{"tests/x.scenario.yaml": c.text}))
		if plan.Runnable() || plan.Files != nil {
			t.Errorf("%s: a plan with errors is runnable", name)
		}
		if got := codes(plan); !slices.Equal(got, c.want) {
			t.Errorf("%s: codes = %v, want %v", name, got, c.want)
		}
		if plan.Findings[0].Line != c.line {
			t.Errorf("%s: line = %d, want %d (%s)", name, plan.Findings[0].Line, c.line, plan.Findings[0])
		}
	}
}

// Verifies: TST-002.
func TestPrepareWithoutScenariosOrWithABrokenProject(t *testing.T) {
	t.Parallel()
	plan := prepare(t, starter(t, nil))
	if plan.Runnable() || len(plan.Findings) != 1 || plan.Findings[0].Code != plxerr.ScenarioFilesNone || HasErrors(plan.Findings) {
		t.Errorf("no scenario files: %+v", plan.Findings)
	}
	broken := starter(t, map[string]string{"tests/x.scenario.yaml": placeScenario}).(fstest.MapFS)
	delete(broken, "theme.json")
	plan = prepare(t, broken)
	if plan.Runnable() || !plan.Compile.HasErrors() {
		t.Errorf("a project that does not compile is runnable: %v", plan.Compile)
	}
	custom, err := Prepare(Options{Project: starter(t, map[string]string{"specs/p.scenario.yaml": placeScenario}), Compiler: compiler.DefaultOptions(), Tests: "specs/*.scenario.yaml", Entropy: entropy()})
	if err != nil || !custom.Runnable() || custom.Cases[0].File != "specs/p.scenario.yaml" {
		t.Errorf("custom pattern: %+v %v", custom, err)
	}
}

// Verifies: TST-002.
func TestPlanWriteCreatesTheProject(t *testing.T) {
	t.Parallel()
	plan := prepare(t, os.DirFS(starterDir))
	dir := t.TempDir()
	if err := plan.Write(dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range plan.Files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) //nolint:gosec // the test's own directory
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// fakeFlutter writes an executable that prints output and exits with code.
func fakeFlutter(t *testing.T, output string, code int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "flutter")
	script := "#!/bin/sh\ncat \"" + filepath.Join(dir, "out.txt") + "\"\necho \"log line\" >&2\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test double
		t.Fatal(err)
	}
	return path
}

// Verifies: TST-002.
func TestFlutterReturnsTheReportOfAFailingRunAndRejectsASilentOne(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	out, err := Flutter(context.Background(), fakeFlutter(t, report, 1), t.TempDir(), &log)
	if err != nil || !strings.Contains(string(out), `"type":"done"`) || !strings.Contains(log.String(), "log line") {
		t.Errorf("failing run: %v %q %q", err, out, log.String())
	}
	if _, err := Flutter(context.Background(), fakeFlutter(t, "", 1), t.TempDir(), io.Discard); err == nil {
		t.Error("a run that reported nothing and failed was accepted")
	}
	if _, err := Flutter(context.Background(), filepath.Join(t.TempDir(), "missing"), t.TempDir(), io.Discard); err == nil {
		t.Error("a missing Flutter was accepted")
	}
}

func lookFlutter() (string, error) { return exec.LookPath("flutter") }

// Verifies: TST-001, TST-002.
// The scenario file of the starter project runs end to end: compiled,
// signed, generated, run by Flutter on the real runtime, and reported. It
// needs the Flutter SDK, which the other tests do not.
func TestFlutterRunsTheStarterScenario(t *testing.T) {
	t.Parallel()
	flutter, err := lookFlutter()
	if err != nil {
		t.Skip("flutter is not installed: ", err)
	}
	runtime, err := filepath.Abs("../../../packages/plux_flutter")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(Options{Project: os.DirFS(starterDir), Compiler: compiler.DefaultOptions(), Entropy: entropy(), Runtime: Dependency{Path: runtime}})
	if err != nil || !plan.Runnable() {
		t.Fatalf("%v %+v", err, plan)
	}
	dir := t.TempDir()
	if err := plan.Write(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Flutter(context.Background(), flutter, dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ParseReport(bytes.NewReader(out), plan.Cases)
	if err != nil {
		t.Fatal(err)
	}
	if sum := Summarize(results); !sum.OK() || sum.Passed != 1 {
		t.Errorf("results = %+v", results)
	}
}
