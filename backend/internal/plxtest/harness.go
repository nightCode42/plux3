// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"bytes"
	_ "embed" // the Dart support file is a template beside this package
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/codegen"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

//go:embed templates/plux_harness.dart.tmpl
var harnessTemplate string

// Dependency says where the generated project gets plux_flutter from: a
// version constraint from pub.dev, or a local checkout of the runtime.
type Dependency struct {
	// Version is a pub constraint such as ^0.3.0; used when Path is empty.
	Version string
	// Path is the directory of a plux_flutter checkout.
	Path string
}

// Input is everything the test project is generated from.
type Input struct {
	Release *Release
	// Natives is the project's native catalogue, or nil. The harness
	// stands in for what the host app registers: its routes show their
	// name, its slots are empty, its actions return nothing.
	Natives *schema.NativeCatalogueDocument
	Files   []*File
	Runtime Dependency
}

// Case is one generated test: a scenario of a file.
type Case struct {
	// File is the scenario file's path in the project.
	File string
	// Name is the scenario's name.
	Name string
	// Line is where the scenario starts in its file.
	Line int
}

// TestName is the name Flutter's reporter gives the test: the file as its
// group, then the scenario.
func (c Case) TestName() string { return c.File + " " + c.Name }

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// Generate writes the Flutter test project: its pubspec, the release it
// tests, the harness and one test file per scenario file. The files and
// the cases are in a fixed order for the same input, so generation is
// deterministic.
func Generate(in Input) (map[string][]byte, []Case, error) {
	files := map[string][]byte{}
	for p, data := range in.Release.Files {
		files[BaselineDir+"/"+p] = data
	}
	files["pubspec.yaml"] = []byte(pubspec(in.Runtime))
	hasMocks := slices.ContainsFunc(in.Files, func(f *File) bool {
		return slices.ContainsFunc(f.Doc.Scenarios, func(s schema.Scenario) bool { return s.Given != nil && len(s.Given.DataSources) > 0 })
	})
	files["test/plux_harness.dart"] = []byte(harness(hasMocks))
	files["test/plux_project.dart"] = []byte(project(in))
	var cases []Case
	for i, f := range in.Files {
		code, fc, err := scenarioFile(f)
		if err != nil {
			return nil, nil, err
		}
		cases = append(cases, fc...)
		slug := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(f.Path), "_"), "_")
		files[fmt.Sprintf("test/%02d_%s_test.dart", i+1, slug)] = code
	}
	return files, cases, nil
}

func pubspec(dep Dependency) string {
	runtime := "  plux_flutter: " + dep.Version + "\n"
	if dep.Path != "" {
		runtime = "  plux_flutter:\n    path: " + codegen.DartString(dep.Path) + "\n"
	}
	return `# Written by plux test; it is rewritten on every run, so do not edit it.
name: plux_scenarios
description: The Flutter project plux test runs a Plux project's scenarios in.
publish_to: none
environment:
  sdk: ^3.13.0
  flutter: ">=3.47.0"
dependencies:
  flutter:
    sdk: flutter
  http: ^1.6.0
` + runtime + `dev_dependencies:
  flutter_test:
    sdk: flutter
`
}

func harness(mocks bool) string {
	r := strings.NewReplacer(
		"{{BASELINE_DIR}}", BaselineDir,
		"{{DATA_MOCKS_IMPORT}}", dartIf(mocks, "import 'package:plux_flutter/src/data/mocks.dart' show DataMockState, DataMocks;\n"),
		"{{DATA_MOCKS_ARG}}", dartIf(mocks, "          dataMocks: DataMocks({for (final e in dataSources.entries) e.key: DataMockState.values.byName(e.value)}, true),\n"),
	)
	return r.Replace(harnessTemplate)
}

func dartIf(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

// project is test/plux_project.dart: the app, the key to trust and the
// stand-ins for what a host app registers.
func project(in Input) string {
	var b strings.Builder
	b.WriteString(`// Written by plux test; do not edit.
// ignore_for_file: implementation_imports

import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The ID of the app under test.
const pluxTestAppId = ` + codegen.DartString(in.Release.AppID) + `;

/// The public key of this run, the only root key the runtime trusts.
final pluxTestRootKeys = <PluxPublicKey>[
  PluxPublicKey(
    keyId: ` + codegen.DartString(in.Release.KeyID) + `,
    algorithm: 'ed25519',
    role: 'targets',
    publicKey: Uint8List.fromList([`)
	for i, by := range in.Release.PublicKey {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(int(by)))
	}
	b.WriteString("]),\n  ),\n];\n\n")
	var routes, slots, actions []string
	if n := in.Natives; n != nil {
		for _, r := range n.Routes {
			routes = append(routes, r.Name)
		}
		for _, s := range n.Slots {
			slots = append(slots, s.Type)
		}
		for _, a := range n.Actions {
			actions = append(actions, a.Name)
		}
	}
	slices.Sort(routes)
	slices.Sort(slots)
	slices.Sort(actions)
	b.WriteString("/// Native routes show their name.\nfinal pluxTestNativeRoutes = <String, PluxNativeRoute<Object?, Object?>>{\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "  %s: PluxNativeRoute<Object?, Object?>(\n    params: (json) => json,\n    builder: (context, params) => Scaffold(\n      body: Center(child: Text(%s)),\n    ),\n  ),\n",
			codegen.DartString(r), codegen.DartString("native route "+r))
	}
	b.WriteString("};\n\n/// Native slots are empty.\nfinal pluxTestNativeSlots = <String, PluxNativeSlot>{\n")
	for _, s := range slots {
		fmt.Fprintf(&b, "  %s: PluxNativeSlot((context, slot) => const SizedBox.shrink()),\n", codegen.DartString(s))
	}
	b.WriteString("};\n\n/// Native actions return nothing.\nfinal pluxTestNativeActions = <String, PluxNativeAction<Object?, Object?>>{\n")
	for _, a := range actions {
		fmt.Fprintf(&b, "  %s: PluxNativeAction<Object?, Object?>(\n    input: (json) => json,\n    handler: (input) => null,\n  ),\n", codegen.DartString(a))
	}
	b.WriteString("};\n")
	return b.String()
}

// scenarioFile is the test file of one scenario file.
func scenarioFile(f *File) ([]byte, []Case, error) {
	var b bytes.Buffer
	b.WriteString("// Written by plux test from " + f.Path + "; do not edit.\n\n")
	b.WriteString("import 'package:flutter_test/flutter_test.dart';\n\nimport 'plux_harness.dart';\n\nvoid main() {\n")
	fmt.Fprintf(&b, "  group(%s, () {\n", codegen.DartString(f.Path))
	var cases []Case
	for i, s := range f.Doc.Scenarios {
		line, _ := f.Locate(pointer("scenarios", strconv.Itoa(i)))
		cases = append(cases, Case{File: f.Path, Name: s.Name, Line: line})
		if err := writeScenario(&b, f, i, s); err != nil {
			return nil, nil, err
		}
	}
	b.WriteString("  });\n}\n")
	return b.Bytes(), cases, nil
}

// writeScenario writes the widget test of the scenario with index i.
func writeScenario(b *bytes.Buffer, f *File, i int, s schema.Scenario) error {
	at := func(more ...string) string {
		line, col := f.Locate(pointer(append([]string{"scenarios", strconv.Itoa(i)}, more...)...))
		return fmt.Sprintf("%s:%d:%d", f.Path, line, col)
	}
	fmt.Fprintf(b, "    testWidgets(%s, (tester) async {\n", codegen.DartString(s.Name))
	b.WriteString("      final s = await ScenarioSession.start(\n        tester,\n")
	fmt.Fprintf(b, "        page: %s,\n", codegen.DartString(s.Page))
	if g := s.Given; g != nil {
		writeGiven(b, g)
	}
	b.WriteString("      );\n      try {\n")
	for j, st := range s.Steps {
		code, err := step(st, at("steps", strconv.Itoa(j)))
		if err != nil {
			return fmt.Errorf("%s: %w", at("steps", strconv.Itoa(j)), err)
		}
		b.WriteString("        " + code + "\n")
	}
	for j, e := range s.Expect {
		code, err := expectation(e, at("expect", strconv.Itoa(j)))
		if err != nil {
			return fmt.Errorf("%s: %w", at("expect", strconv.Itoa(j)), err)
		}
		b.WriteString("        " + code + "\n")
	}
	b.WriteString("      } finally {\n        await s.finish();\n      }\n    });\n")
	return nil
}

// writeGiven writes the named arguments of ScenarioSession.start that
// carry the preconditions of a scenario.
func writeGiven(b *bytes.Buffer, g *schema.ScenarioGiven) {
	if len(g.Params) > 0 {
		fmt.Fprintf(b, "        params: %s,\n", object(g.Params))
	}
	if len(g.State) > 0 {
		fmt.Fprintf(b, "        state: %s,\n", object(g.State))
	}
	if len(g.DataSources) > 0 {
		b.WriteString("        dataSources: {")
		for _, k := range sortedKeys(g.DataSources) {
			fmt.Fprintf(b, "%s: %s, ", codegen.DartString(k), codegen.DartString(string(g.DataSources[k].State)))
		}
		b.WriteString("},\n")
	}
}

func pointer(segs ...string) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString("/" + strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// literal is a JSON value as a Dart expression.
func literal(raw json.RawMessage) string {
	var c bytes.Buffer
	if err := json.Compact(&c, raw); err != nil {
		c.Reset()
		c.WriteString("null")
	}
	return "json(" + codegen.DartString(c.String()) + ")"
}

// object is a JSON object as a Dart expression: a map of its entries.
func object(m map[string]json.RawMessage) string {
	var b strings.Builder
	b.WriteString("<String, Object?>{")
	for _, k := range slices.Sorted(maps.Keys(m)) {
		b.WriteString(codegen.DartString(k) + ": " + literal(m[k]) + ", ")
	}
	b.WriteString("}")
	return b.String()
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// step is the Dart call of one scenario step; the schema guarantees that
// exactly one key is set.
func step(s schema.ScenarioStep, at string) (string, error) {
	where := ", at: " + codegen.DartString(at)
	switch {
	case s.Tap != "":
		return "await s.tap(" + codegen.DartString(s.Tap) + where + ");", nil
	case s.EnterText != nil:
		return "await s.enterText(" + codegen.DartString(s.EnterText.TestID) + ", " + codegen.DartString(s.EnterText.Text) + where + ");", nil
	case s.Scroll != nil:
		var dx, dy float64
		if s.Scroll.Dx != nil {
			dx = *s.Scroll.Dx
		}
		if s.Scroll.Dy != nil {
			dy = *s.Scroll.Dy
		}
		return "await s.scroll(" + codegen.DartString(s.Scroll.TestID) + ", " + number(dx) + ", " + number(dy) + where + ");", nil
	case s.WaitFor != nil:
		timeout := int64(5000)
		if s.WaitFor.TimeoutMs != nil {
			timeout = *s.WaitFor.TimeoutMs
		}
		return "await s.waitFor(" + codegen.DartString(s.WaitFor.TestID) + ", " + strconv.FormatInt(timeout, 10) + where + ");", nil
	case s.Trigger != nil:
		return "await s.trigger(" + codegen.DartString(s.Trigger.Event) + ", " + object(s.Trigger.Payload) + where + ");", nil
	}
	return "", errors.New("a step without a key")
}

// expectation is the Dart call of one expectation.
func expectation(e schema.ScenarioExpectation, at string) (string, error) {
	where := "at: " + codegen.DartString(at)
	call := func(method string, c *schema.ScenarioCall) string {
		times := "null"
		if c.Times != nil {
			times = strconv.FormatInt(*c.Times, 10)
		}
		return "s." + method + "(" + codegen.DartString(c.Name) + ", " + object(c.Args) + ", " + times + ", " + where + ");"
	}
	switch {
	case e.Visible != "":
		return "s.visible(" + codegen.DartString(e.Visible) + ", " + where + ");", nil
	case e.NotVisible != "":
		return "s.notVisible(" + codegen.DartString(e.NotVisible) + ", " + where + ");", nil
	case e.TextEquals != nil:
		return "s.textEquals(" + codegen.DartString(e.TextEquals.TestID) + ", " + codegen.DartString(e.TextEquals.Text) + ", " + where + ");", nil
	case e.NavigatedTo != "":
		return "s.navigatedTo(" + codegen.DartString(e.NavigatedTo) + ", " + where + ");", nil
	case e.ActionCalled != nil:
		return call("actionCalled", e.ActionCalled), nil
	case e.FunctionCalled != nil:
		return call("functionCalled", e.FunctionCalled), nil
	case len(e.StateEquals) > 0:
		var b strings.Builder
		for _, k := range sortedKeys(e.StateEquals) {
			b.WriteString("s.stateEquals(" + codegen.DartString(k) + ", " + literal(e.StateEquals[k]) + ", " + where + ");")
		}
		return b.String(), nil
	}
	return "", errors.New("an expectation without a key")
}
