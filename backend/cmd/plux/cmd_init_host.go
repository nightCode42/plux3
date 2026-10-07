// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/codegen"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// The constraints plux init adds, in step with the runtime this CLI
// generates code for (a test keeps them in step with the packages).
const (
	runtimeConstraint  = "^0.3.0"
	adapterConstraint  = "^0.1.0"
	optionalConstraint = "^0.1.0"
)

// initCall is the runtime's start-up call, as the Dart API spells it.
const initCall = "Plux.initialize" //nolint:misspell // the runtime's API name

// optionsFile is the configuration plux init writes into a host app.
const optionsFile = "lib/plux/plux_options.g.dart"

// flutterSDK matches pubspec.yaml's `sdk: flutter` line of a Flutter app.
var flutterSDK = regexp.MustCompile(`(?m)^\s+sdk:\s*flutter\s*$`)

// dirKind says what plux init sets up in dir (maintainer, P4 R8): a host
// Flutter app (a pubspec.yaml on the Flutter SDK and no app.json), else a
// Plux project, as before, including an empty directory a project export
// will fill. A directory holding both is a usage error.
func dirKind(dir string) (host bool, err error) {
	_, appErr := os.Stat(filepath.Join(dir, "app.json"))
	pubspec, pubErr := os.ReadFile(filepath.Join(dir, "pubspec.yaml")) //nolint:gosec // the developer's own project
	flutter := pubErr == nil && flutterSDK.Match(pubspec)
	if flutter && appErr == nil {
		return false, usageError(dir + " holds both a Plux project (app.json) and a Flutter app (pubspec.yaml): keep them in separate directories")
	}
	return flutter, nil
}

// hostStep is what one step of plux init did to a host app.
type hostStep struct {
	File   string `json:"file"`
	Result string `json:"result"`
}

// initHost sets up a host Flutter app (HST-032): its dependencies, its
// plux.yaml, its configuration with the environment's root keys embedded,
// and the runtime's start in main.dart where main has the plain runApp
// shape.
// Every step leaves what it already finished unchanged, so a second run
// changes nothing; then it runs plux doctor's server checks.
func (e env) initHost(c common, envKey string, pkgs []string) int {
	if err := c.resolve(); err != nil {
		return e.fail("init", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("init", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("init", err)
	}
	ctx := context.Background()
	app, err := findApp(ctx, cl, c.app)
	if err != nil {
		return e.fail("init", err)
	}
	res, err := cl.manifest.GetRootKeys(ctx, connect.NewRequest(&pluxv1.GetRootKeysRequest{AppId: app.GetId(), Environment: envKey}))
	if err != nil {
		return e.fail("init", err)
	}
	var steps []hostStep
	for _, step := range []func() (hostStep, error){
		func() (hostStep, error) { return addDependencies(c.dir, pkgs) },
		func() (hostStep, error) { return writeHostConfig(c.dir) },
		func() (hostStep, error) {
			return writeOptions(c.dir, app.GetKey(), app.GetId(), c.server, envKey, res.Msg.GetKeys(), pkgs)
		},
		func() (hostStep, error) { return wireMain(c.dir) },
	} {
		s, err := step()
		if err != nil {
			return e.fail("init", err)
		}
		steps = append(steps, s)
	}
	var text strings.Builder
	for _, s := range steps {
		fmt.Fprintf(&text, "%-30s %s\n", s.File, s.Result)
	}
	if len(res.Msg.GetKeys()) == 0 {
		fmt.Fprintf(&text, "\nEnvironment %s has signed nothing yet, so no root key is embedded: promote a release to it and run plux init again.\n", envKey)
	}
	checks := e.doctorChecks(&c, false)
	failed := slices.ContainsFunc(checks, func(ch check) bool { return !ch.OK })
	text.WriteString("\n")
	for _, ch := range checks {
		mark := "ok  "
		if !ch.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&text, "%s %-10s %s\n", mark, ch.Name, ch.Detail)
	}
	text.WriteString("\nNext: flutter pub get, then plux codegen <project-dir> for typed routes.\n")
	code := e.emit(c.json, map[string]any{"ok": !failed, "steps": steps, "checks": checks, "keys": len(res.Msg.GetKeys())}, text.String())
	if failed && code == exitOK {
		return exitFailed
	}
	return code
}

// hostPackages are the optional packages plux init adds to a host app:
// those named in list, comma-separated, and those the device actions of
// the project in projectDir need (RT-060), sorted and without repeats. An
// unknown name or a project with errors is refused.
func hostPackages(list, projectDir string) ([]string, error) {
	var out []string
	for n := range strings.SplitSeq(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := codegen.PackageByName(n); !ok {
			var known []string
			for _, p := range codegen.OptionalPackages() {
				known = append(known, p.Name)
			}
			return nil, usageError(fmt.Sprintf("unknown package %q: the optional packages are %s", n, strings.Join(known, ", ")))
		}
		out = append(out, n)
	}
	if projectDir != "" {
		st, err := os.Stat(projectDir)
		if err != nil || !st.IsDir() {
			return nil, usageError(projectDir + " is not a directory")
		}
		res := compiler.Compile(os.DirFS(projectDir), options(false))
		if i := slices.IndexFunc(res.Diagnostics, func(d plxerr.Diagnostic) bool { return d.Severity == plxerr.SeverityError }); i >= 0 {
			return nil, fmt.Errorf("the project in %s has errors, the first %s: run plux validate %s", projectDir, res.Diagnostics[i], projectDir)
		}
		for _, u := range compiler.DeviceUses(res, nil) {
			out = append(out, u.Package)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// addDependencies adds plux_flutter to pubspec.yaml's dependencies, the
// router adapter of a go_router or auto_route app, and the optional
// packages pkgs.
func addDependencies(dir string, pkgs []string) (hostStep, error) {
	path := filepath.Join(dir, "pubspec.yaml")
	data, err := os.ReadFile(path) //nolint:gosec // the developer's own project
	if err != nil {
		return hostStep{}, fmt.Errorf("read pubspec.yaml: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	at := slices.IndexFunc(lines, func(l string) bool { return strings.TrimRight(l, " \r") == "dependencies:" })
	if at < 0 {
		return hostStep{}, errors.New("pubspec.yaml has no dependencies section")
	}
	has := func(pkg string) bool {
		return regexp.MustCompile(`(?m)^\s+` + pkg + `:`).Match(data)
	}
	var add []string
	if !has("plux_flutter") {
		add = append(add, "  plux_flutter: "+runtimeConstraint)
	}
	for router, adapter := range map[string]string{"go_router": "plux_go_router", "auto_route": "plux_auto_route"} {
		if has(router) && !has(adapter) {
			add = append(add, "  "+adapter+": "+adapterConstraint)
		}
	}
	for _, p := range pkgs {
		if !has(p) {
			add = append(add, "  "+p+": "+optionalConstraint)
		}
	}
	if len(add) == 0 {
		return hostStep{"pubspec.yaml", "unchanged"}, nil
	}
	slices.Sort(add)
	lines = slices.Insert(lines, at+1, add...)
	if err := writeFile(path, []byte(strings.Join(lines, "\n"))); err != nil {
		return hostStep{}, err
	}
	var names []string
	for _, a := range add {
		names = append(names, strings.TrimSpace(a))
	}
	return hostStep{"pubspec.yaml", "added " + strings.Join(names, ", ")}, nil
}

// applicationID finds the Android applicationId of the app, for the
// native catalogue's host identifier.
var applicationID = regexp.MustCompile(`applicationId\s*=?\s*"([^"]+)"`)

// writeHostConfig writes plux.yaml when the app has none.
func writeHostConfig(dir string) (hostStep, error) {
	path := filepath.Join(dir, hostConfigFile)
	if _, err := os.Stat(path); err == nil {
		return hostStep{hostConfigFile, "kept"}, nil
	}
	var b strings.Builder
	b.WriteString("# This app's Plux configuration (plux init); plux native scan and sync read it.\n")
	for _, gradle := range []string{"android/app/build.gradle.kts", "android/app/build.gradle"} {
		data, err := os.ReadFile(filepath.Join(dir, gradle)) //nolint:gosec // the developer's own project
		if m := applicationID.FindSubmatch(data); err == nil && m != nil {
			fmt.Fprintf(&b, "# The app's identifier, as the native catalogue names its host.\nappId: %s\n", m[1])
			break
		}
	}
	b.WriteString("# The widget classes plugins may place as native slots, one per line:\n#   - MapCard\nslots:\n")
	if err := writeFile(path, []byte(b.String())); err != nil {
		return hostStep{}, err
	}
	return hostStep{hostConfigFile, "written"}, nil
}

// writeOptions writes the generated configuration: the server, app and
// environment, and the environment's root keys embedded (SEC-051).
func writeOptions(dir, key, appID, server, envKey string, keys []*pluxv1.PublicKey, pkgs []string) (hostStep, error) {
	spec := codegen.OptionsSpec{AppKey: key, AppID: appID, Endpoint: server, Environment: envKey, Packages: pkgs}
	for _, k := range keys {
		spec.Keys = append(spec.Keys, codegen.RootKey{KeyID: k.GetKeyId(), Algorithm: k.GetAlgorithm(), Role: k.GetRole(), PublicKey: k.GetPublicKey()})
	}
	content := codegen.Options(spec)
	path := filepath.Join(dir, filepath.FromSlash(optionsFile))
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) { //nolint:gosec // the developer's own project
		return hostStep{optionsFile, "unchanged"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return hostStep{}, fmt.Errorf("create %s: %w", filepath.Dir(optionsFile), err)
	}
	if err := writeFile(path, content); err != nil {
		return hostStep{}, err
	}
	return hostStep{optionsFile, fmt.Sprintf("written (%d root keys)", len(keys))}, nil
}

// importLine matches an import directive on its own line.
var importLine = regexp.MustCompile(`(?m)^import .*$`)

// mainStart finds the start of `void main()`.
var mainStart = regexp.MustCompile(`(?m)^void\s+main\(\)\s*`)

// plainMain finds a main whose whole body runs one app — `void main() {
// runApp(…); }` or `void main() => runApp(…);` — and returns the span of
// the declaration and the argument of runApp.
func plainMain(src string) (start, end int, app string, ok bool) {
	m := mainStart.FindStringIndex(src)
	if m == nil {
		return 0, 0, "", false
	}
	rest := src[m[1]:]
	block := strings.HasPrefix(rest, "{")
	switch {
	case block:
		rest = strings.TrimLeft(rest[1:], " \t\r\n")
	case strings.HasPrefix(rest, "=>"):
		rest = strings.TrimLeft(rest[2:], " \t\r\n")
	default:
		return 0, 0, "", false
	}
	if !strings.HasPrefix(rest, "runApp(") {
		return 0, 0, "", false
	}
	open := len(src) - len(rest) + len("runApp")
	closing := matchParen(src, open)
	if closing < 0 {
		return 0, 0, "", false
	}
	tail := strings.TrimLeft(src[closing+1:], " \t")
	if !strings.HasPrefix(tail, ";") {
		return 0, 0, "", false
	}
	end = len(src) - len(tail) + 1
	if block {
		after := strings.TrimLeft(src[end:], " \t\r\n")
		if !strings.HasPrefix(after, "}") {
			return 0, 0, "", false
		}
		end = len(src) - len(after) + 1
	}
	return m[0], end, strings.TrimSpace(src[open+1 : closing]), true
}

// matchParen is the index of the bracket closing the one at open, or -1;
// it skips brackets inside string literals.
func matchParen(src string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// mainLines are what main must hold, printed when plux init cannot wire
// it safely.
//
//nolint:misspell // Plux.initialize is the runtime's API name.
const mainLines = `  import 'package:plux_flutter/plux_flutter.dart';
  import 'plux/plux_options.g.dart';

  Future<void> main() async {
    WidgetsFlutterBinding.ensureInitialized();
    await Plux.initialize(PluxOptions.config());
    runApp(PluxScope(child: MyApp()));
  }`

// wireMain starts the runtime in lib/main.dart when main has the plain
// runApp shape, and otherwise says which lines to add.
func wireMain(dir string) (hostStep, error) {
	const file = "lib/main.dart"
	path := filepath.Join(dir, filepath.FromSlash(file))
	data, err := os.ReadFile(path) //nolint:gosec // the developer's own project
	if err != nil {
		return hostStep{file, "not found; add to your main:\n" + mainLines}, nil //nolint:nilerr // reported as a step
	}
	src := string(data)
	if strings.Contains(src, initCall+"(") {
		return hostStep{file, "already starts Plux"}, nil
	}
	start, end, app, ok := plainMain(src)
	if !ok {
		return hostStep{file, "not changed: main does more than runApp; add:\n" + mainLines}, nil
	}
	body := "Future<void> main() async {\n  WidgetsFlutterBinding.ensureInitialized();\n  await " + initCall + "(PluxOptions.config());\n  runApp(PluxScope(child: " + app + "));\n}"
	src = src[:start] + body + src[end:]
	var imports []string
	for _, imp := range []string{"import 'package:plux_flutter/plux_flutter.dart';", "import 'plux/plux_options.g.dart';"} {
		if !strings.Contains(src, imp) {
			imports = append(imports, imp)
		}
	}
	if all := importLine.FindAllStringIndex(src, -1); len(all) > 0 {
		end := all[len(all)-1][1] + 1
		src = src[:end] + strings.Join(imports, "\n") + "\n" + src[end:]
	} else {
		src = strings.Join(imports, "\n") + "\n\n" + src
	}
	if err := writeFile(path, []byte(src)); err != nil {
		return hostStep{}, err
	}
	return hostStep{file, "starts Plux before runApp"}, nil
}
