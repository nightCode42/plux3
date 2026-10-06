// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/codegen"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// templateMain is lib/main.dart as flutter create writes it, shortened.
const templateMain = `import 'package:flutter/material.dart';

void main() {
  runApp(const MyApp(title: 'Demo (1)'));
}

class MyApp extends StatelessWidget {
  const MyApp({super.key, required this.title});
  final String title;
  @override
  Widget build(BuildContext context) => MaterialApp(title: title);
}
`

func hostApp(t *testing.T, deps, main string) string {
	t.Helper()
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, "pubspec.yaml"), "name: host\nversion: 1.0.0+1\n\ndependencies:\n  flutter:\n    sdk: flutter\n"+deps+"\ndev_dependencies:\n  flutter_test:\n    sdk: flutter\n")
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o750); err != nil {
		t.Fatal(err)
	}
	if main != "" {
		putFile(t, filepath.Join(dir, "lib", "main.dart"), main)
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestInitHost sets a Flutter app up as a Plux host: dependencies, the
// router adapter, plux.yaml, the configuration with the environment's
// root keys, the runtime's start in a plain main; a second run changes
// nothing, and the server checks of plux doctor follow.
// Verifies: HST-032, SEC-051.
func TestInitHost(t *testing.T) { //nolint:paralleltest // the environment is process-wide
	t.Setenv(tokenEnv, "plux_pat_test")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	_, url := newFake(t)
	host := hostApp(t, "  go_router: ^18.0.0\n", templateMain)
	if err := os.MkdirAll(filepath.Join(host, "android", "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(host, "android", "app", "build.gradle.kts"), "android {\n  defaultConfig {\n    applicationId = \"dev.plux.host\"\n  }\n}\n")
	args := []string{"init", "--server", url, "--app", "demo", "--env", "production", "--packages", "plux_media", "-C", host}
	code, out, stderr := cli(t, t.TempDir(), args...)
	if code != exitOK {
		t.Fatalf("init: %d %s %s", code, out, stderr)
	}
	pubspec := read(t, filepath.Join(host, "pubspec.yaml"))
	if !strings.Contains(pubspec, "dependencies:\n  plux_flutter: "+runtimeConstraint+"\n  plux_go_router: "+adapterConstraint+"\n  plux_media: "+optionalConstraint+"\n  flutter:") {
		t.Errorf("pubspec.yaml:\n%s", pubspec)
	}
	cfg, err := readHostConfig(host)
	if err != nil || cfg.AppID != "dev.plux.host" || len(cfg.Slots) != 0 {
		t.Errorf("plux.yaml: %+v %v", cfg, err)
	}
	options := read(t, filepath.Join(host, "lib", "plux", "plux_options.g.dart"))
	for _, want := range []string{
		"static const appId = 'a1';", "static const endpoint = '" + url + "';", "static const environment = 'production';",
		"keyId: 'k1'", "publicKey: Uint8List.fromList([0x01, 0x02])",
		"devicePackages: [PluxMedia()],",
	} {
		if !strings.Contains(options, want) {
			t.Errorf("the options lack %q:\n%s", want, options)
		}
	}
	main := read(t, filepath.Join(host, "lib", "main.dart"))
	for _, want := range []string{
		"import 'package:flutter/material.dart';\nimport 'package:plux_flutter/plux_flutter.dart';\nimport 'plux/plux_options.g.dart';\n",
		"Future<void> main() async {\n  WidgetsFlutterBinding.ensureInitialized();\n  await " + initCall + "(PluxOptions.config());\n  runApp(PluxScope(child: const MyApp(title: 'Demo (1)')));\n}\n\nclass MyApp",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.dart lacks %q:\n%s", want, main)
		}
	}
	if !strings.Contains(out, "ok   app") {
		t.Errorf("no doctor checks:\n%s", out)
	}

	before := map[string]string{}
	for _, f := range []string{"pubspec.yaml", "plux.yaml", "lib/plux/plux_options.g.dart", "lib/main.dart"} {
		before[f] = read(t, filepath.Join(host, f))
	}
	code, out, _ = cli(t, t.TempDir(), args...)
	if code != exitOK || strings.Count(out, "unchanged") != 2 || !strings.Contains(out, "kept") || !strings.Contains(out, "already starts Plux") {
		t.Errorf("a second run: %d\n%s", code, out)
	}
	for f, content := range before {
		if read(t, filepath.Join(host, f)) != content {
			t.Errorf("a second run changed %s", f)
		}
	}
}

// TestInitHostCases covers a main plux init must not rewrite, an
// environment with no key yet, and a directory holding a project and an
// app.
// Verifies: HST-032.
func TestInitHostCases(t *testing.T) { //nolint:paralleltest // the environment is process-wide
	t.Setenv(tokenEnv, "plux_pat_test")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	_, url := newFake(t)
	busy := "import 'package:flutter/material.dart';\n\nvoid main() {\n  setUp();\n  runApp(const App());\n}\n"
	host := hostApp(t, "  auto_route: ^11.0.0\n", busy)
	code, out, _ := cli(t, t.TempDir(), "init", "--server", url, "--app", "demo", "--env", "fresh", "-C", host)
	if code != exitOK || !strings.Contains(out, "main does more than runApp") || !strings.Contains(out, "await "+initCall+"(PluxOptions.config());") ||
		!strings.Contains(out, "has signed nothing yet") {
		t.Errorf("init: %d\n%s", code, out)
	}
	if read(t, filepath.Join(host, "lib", "main.dart")) != busy {
		t.Error("a main doing more than runApp was rewritten")
	}
	if !strings.Contains(read(t, filepath.Join(host, "pubspec.yaml")), "plux_auto_route: "+adapterConstraint) {
		t.Error("no auto_route adapter")
	}
	if !strings.Contains(read(t, filepath.Join(host, "lib", "plux", "plux_options.g.dart")), "rootKeys = <PluxPublicKey>[\n  ];") {
		t.Error("an environment with no key yet")
	}
	arrow := hostApp(t, "", "import 'package:flutter/widgets.dart';\n\nvoid main() => runApp(const Text('x', textDirection: TextDirection.ltr));\n")
	if code, _, _ := cli(t, t.TempDir(), "init", "--server", url, "--app", "demo", "--env", "production", "-C", arrow); code != exitOK {
		t.Errorf("an arrow main: %d", code)
	}
	if got := read(t, filepath.Join(arrow, "lib", "main.dart")); !strings.Contains(got, "runApp(PluxScope(child: const Text('x', textDirection: TextDirection.ltr)));\n}") {
		t.Errorf("an arrow main:\n%s", got)
	}
	both := hostApp(t, "", "")
	putFile(t, filepath.Join(both, "app.json"), "{}")
	if code, _, stderr := cli(t, t.TempDir(), "init", "--server", url, "--app", "demo", "-C", both); code != exitUsage || !strings.Contains(stderr, "separate directories") {
		t.Errorf("a project and an app together: %d %s", code, stderr)
	}
	noMain := hostApp(t, "", "")
	if code, out, _ := cli(t, t.TempDir(), "init", "--server", url, "--app", "demo", "--env", "production", "-C", noMain); code != exitOK || !strings.Contains(out, "not found; add") {
		t.Errorf("no main.dart: %d\n%s", code, out)
	}
}

// TestInitConstraints keeps the constraints plux init adds in step with
// the packages' versions.
// Verifies: HST-032.
func TestInitConstraints(t *testing.T) {
	t.Parallel()
	version := regexp.MustCompile(`(?m)^version: (\d+\.\d+)\.\d+`)
	constraints := map[string]string{"plux_flutter": runtimeConstraint, "plux_go_router": adapterConstraint, "plux_auto_route": adapterConstraint}
	for _, p := range codegen.OptionalPackages() {
		constraints[p.Name] = optionalConstraint
	}
	for pkg, constraint := range constraints {
		m := version.FindStringSubmatch(read(t, filepath.Join("..", "..", "..", "packages", pkg, "pubspec.yaml")))
		if m == nil || !strings.HasPrefix(constraint, "^"+m[1]+".") {
			t.Errorf("%s is %v, plux init adds %s", pkg, m, constraint)
		}
	}
}

// TestOptionsGolden writes the configuration of fixed values to its
// golden in the runtime's tests, where Flutter's analyser compiles it.
// Verifies: HST-032.
func TestOptionsGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keys := []*pluxv1.PublicKey{{KeyId: "k1", Algorithm: "ed25519", Role: "targets", PublicKey: []byte{0xab, 0x01}}}
	if _, err := writeOptions(dir, "demo", "01e0c450-6c00-7000-8000-000000000001", "https://plux.example.com", "production", keys, nil); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(dir, "lib", "plux", "plux_options.g.dart"))
	path := filepath.Join("..", "..", "..", "packages", "plux_flutter", "test", "codegen", "plux_options.g.dart")
	if *updateGolden {
		putFile(t, path, got)
		return
	}
	if want, err := os.ReadFile(path); err != nil || !bytes.Equal(want, []byte(got)) { //nolint:gosec // the test's own golden
		t.Errorf("%s differs (%v); run go test ./cmd/plux -run TestOptionsGolden -update", path, err)
	}
}

// editJSON rewrites the JSON document at path with edit.
func editJSON(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, path, string(data))
}

// TestHostPackages checks the optional packages plux init adds: those
// named, and those the project's device actions need, sorted once each.
// Verifies: RT-060, HST-032.
func TestHostPackages(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS(filepath.Join("..", "..", "..", "schema", "testdata", "documents", "widgets"))); err != nil {
		t.Fatal(err)
	}
	editJSON(t, filepath.Join(project, "plugins", "gallery", "pages", "material.page.json"), func(doc map[string]any) {
		var node any = doc
		for _, k := range strings.Split("root/slots/body/slots/child/children/6/events/onPressed/steps/0", "/") {
			switch n := node.(type) {
			case map[string]any:
				node = n[k]
			case []any:
				i, err := strconv.Atoi(k)
				if err != nil {
					t.Fatal(err)
				}
				node = n[i]
			}
		}
		step, ok := node.(map[string]any)
		if !ok {
			t.Fatal("the gallery page has no step to edit")
		}
		step["action"], step["input"] = "capturePhoto", map[string]any{}
	})
	addCamera := func(doc map[string]any) {
		caps, _ := doc["capabilities"].(map[string]any)
		apis, _ := caps["deviceApis"].([]any)
		caps["deviceApis"] = append(apis, "camera")
	}
	editJSON(t, filepath.Join(project, "plugins", "gallery", "plugin.json"), addCamera)
	editJSON(t, filepath.Join(project, "app.json"), addCamera)

	for _, tc := range []struct {
		name, list, project string
		want                []string
		err                 string
	}{
		{name: "none"},
		{name: "named", list: "plux_rive, plux_lottie,plux_rive", want: []string{"plux_lottie", "plux_rive"}},
		{name: "unknown", list: "plux_camera", err: `unknown package "plux_camera"`},
		{name: "project", list: "plux_media,plux_lottie", project: project, want: []string{"plux_lottie", "plux_media"}},
		{name: "project without device actions", project: projectDir},
		{name: "not a directory", project: filepath.Join(project, "app.json"), err: "is not a directory"},
	} {
		got, err := hostPackages(tc.list, tc.project)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v %v, want %v", tc.name, got, err, tc.want)
		}
	}
}

// TestOptionsRegisterPackages checks that the generated configuration
// imports and registers each optional package, with a root navigator key
// for those that open pages of their own.
// Verifies: HST-032.
func TestOptionsRegisterPackages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := writeOptions(dir, "demo", "01e0c450-6c00-7000-8000-000000000001", "https://plux.example.com", "production", nil,
		[]string{"plux_scanner", "plux_media", "plux_lottie", "plux_db_drift"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(dir, "lib", "plux", "plux_options.g.dart"))
	for _, want := range []string{
		"import 'package:plux_db_drift/plux_db_drift.dart';\nimport 'package:plux_lottie/plux_lottie.dart';\nimport 'package:plux_media/plux_media.dart';\nimport 'package:plux_scanner/plux_scanner.dart';\n\n",
		"static final rootNavigatorKey = GlobalKey<NavigatorState>();",
		"        navigatorKey: navigatorKey ?? rootNavigatorKey,\n" +
			"        devicePackages: [PluxMedia(), PluxScanner(navigatorKey: navigatorKey ?? rootNavigatorKey)],\n" +
			"        nativeSlots: {...PluxLottie.slots},\n" +
			"        databaseAdapter: PluxDriftAdapter(),\n      );\n}\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plux_options.g.dart lacks\n%s\nin\n%s", want, got)
		}
	}
}
