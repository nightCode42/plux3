// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldens is where the generated libraries are kept: in the runtime's
// tests, which Flutter's analyser compiles and which run them against
// the routing project's bundles.
const goldens = "../../../packages/plux_flutter/test/codegen"

func compile(t *testing.T, project string) *schema.Project {
	t.Helper()
	res := compiler.Compile(os.DirFS(filepath.Join("..", "..", "..", "schema", "testdata", "documents", project)), compiler.DefaultOptions())
	if res.Diagnostics.HasErrors() {
		t.Fatalf("%s:\n%v", project, res.Diagnostics)
	}
	return res.Project
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldens, name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // the test's own golden
	if err != nil {
		t.Fatalf("%v (run go test ./internal/codegen -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the generated library; run go test ./internal/codegen -update and review the change", path)
	}
}

// TestConformanceProjects generates the library of each conformance
// project, twice, and compares it with its golden.
// Verifies: HST-030.
func TestConformanceProjects(t *testing.T) {
	t.Parallel()
	for project, file := range map[string]string{
		"routing": "routing.g.dart", "features": "features.g.dart",
		"loan-calculator": "loan_calculator.g.dart", "starter": "starter.g.dart",
	} {
		t.Run(project, func(t *testing.T) {
			t.Parallel()
			p := compile(t, project)
			first, err := Dart(p)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Dart(compile(t, project))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, again) {
				t.Error("two generations differ")
			}
			golden(t, file, first)
		})
	}
}

func yes() *bool { b := true; return &b }

// typesProject declares every type shape, reserved names and colliding
// type names.
func typesProject() *schema.Project {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	app := &schema.AppDocument{
		Key: "types",
		Types: []schema.TypeDecl{
			{Name: "Point", Fields: []schema.Field{{Name: "x", Type: "int"}, {Name: "y", Type: "int"}, {Name: "toJson", Type: "string?"}}},
			{Name: "Mode", Enum: []string{"fast", "in", "values", "name"}},
			{Name: "PluxFlags", Fields: []schema.Field{{Name: "on", Type: "bool"}}},
		},
		State: []schema.StateEntry{
			{Name: "day", Type: "date", Exposed: yes()},
			{Name: "tint", Type: "color", Exposed: yes()},
			{Name: "price", Type: "money", Exposed: yes()},
			{Name: "wait", Type: "duration", Exposed: yes()},
			{Name: "path", Type: "list<Point>", Exposed: yes()},
			{Name: "modes", Type: "map<string,Mode?>", Exposed: yes()},
			{Name: "seen", Type: "dateTime?", Exposed: yes()},
			{Name: "amount", Type: "decimal", Exposed: yes()},
			{Name: "ratio", Type: "double", Exposed: yes()},
			{Name: "hidden", Type: "int"},
		},
		HostEvents: []schema.HostEventDecl{
			{Name: "done", Fields: []schema.Field{{Name: "at", Type: "dateTime"}, {Name: "points", Type: "list<Point>?"}}},
			{Name: "default"},
		},
		Flags: []schema.FlagDecl{
			{Name: "rate", Type: schema.FlagTypeDouble, Default: raw("1")},
			{Name: "label", Type: schema.FlagTypeString, Default: raw(`"it's $5\n"`)},
			{Name: "limit", Type: schema.FlagTypeInt, Default: raw("3")},
		},
	}
	shop := schema.Plugin{
		Loaded: schema.Loaded[schema.PluginDocument]{Doc: &schema.PluginDocument{Key: "shop", Types: []schema.TypeDecl{
			{Name: "Point", Fields: []schema.Field{{Name: "lat", Type: "double"}}},
		}}},
		Pages: []schema.Loaded[schema.PageDocument]{
			{Doc: &schema.PageDocument{Route: "to-string", Result: "list<Point>", Params: []schema.Param{
				{Name: "at", Type: "Point", Required: yes()},
				{Name: "mode", Type: "Mode"},
				{Name: "tags", Type: "map<string,list<int>>", Required: yes()},
				{Name: "count", Type: "int", Required: yes(), Default: raw("2")},
				{Name: "when", Type: "date?"},
			}}},
			{Doc: &schema.PageDocument{Route: "class", Description: "A page\nwith a  long description."}},
			{Doc: &schema.PageDocument{Key: "inner"}},
		},
		Components: []schema.Loaded[schema.ComponentDocument]{
			{Doc: &schema.ComponentDocument{Key: "badge", Exported: yes(), Props: []schema.ComponentProp{
				{Name: "key", Type: "string", Required: yes()},
				{Name: "tint", Type: "color?"},
			}}},
			{Doc: &schema.ComponentDocument{Key: "internal"}},
		},
	}
	return &schema.Project{App: schema.Loaded[schema.AppDocument]{Doc: app}, Plugins: []schema.Plugin{shop}}
}

// TestEveryTypeShape generates the library of a project with every type
// shape; the runtime's tests compile it and round-trip its values.
// Verifies: HST-030.
func TestEveryTypeShape(t *testing.T) {
	t.Parallel()
	out, err := Dart(typesProject())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"static PluxScreen<List<ShopPoint>> toString$(", // an Object member gets a suffix
		"static PluxScreen<void> class$()",              // a keyword too
		"/// A page with a long description.",           // descriptions on one line
		"required String key$",                          // a prop named like a view parameter
		"final class PluxFlagsType {",                   // a type named like a generated class
		"final class ShopPoint {",                       // a plugin type another scope declares
		"in$('in'),",                                    // an enum member that is a keyword
		"values$('values'),",                            // or an enum's own member
		"final String? toJson$;",                        // a field named like a generated member
		"static String get label => plux.Plux.flag<String>('label') ?? 'it\\'s \\$5\\n';",
		"static double get rate => plux.Plux.flag<double>('rate') ?? 1.0;",
		"final class DefaultEvent {",
		"static Stream<DefaultEvent> get default$ =>",
		"'count': ?count", // a required parameter with a default may be left out
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the library lacks %q", want)
		}
	}
	for _, unwanted := range []string{"hidden", "inner", "internal"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("the library names %q, which the host cannot use", unwanted)
		}
	}
	golden(t, "types.g.dart", out)
}

// TestErrors reports what cannot be generated.
// Verifies: HST-030.
func TestErrors(t *testing.T) {
	t.Parallel()
	if _, err := Dart(nil); err == nil {
		t.Error("no project was accepted")
	}
	for _, bad := range []string{"", "list", "list<int", "map<int,string>", "widget", "int!", "list<>"} {
		if _, err := parseType(bad); err == nil {
			t.Errorf("type %q was accepted", bad)
		}
	}
	p := typesProject()
	p.App.Doc.State = append(p.App.Doc.State, schema.StateEntry{Name: "ghost", Type: "Ghost", Exposed: yes()})
	if _, err := Dart(p); err == nil || !strings.Contains(err.Error(), "Ghost") {
		t.Errorf("an undeclared type: %v", err)
	}
	p = typesProject()
	p.App.Doc.Flags[0].Default = json.RawMessage(`[1]`)
	if _, err := Dart(p); err == nil {
		t.Error("a list default for a flag was accepted")
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"loan-calculator": "loanCalculator", "a-1b": "a1b", "new": "new$", "to-string": "toString$"} {
		if got := camel(in); got != want {
			t.Errorf("camel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := upper("signedIn"); got != "SignedIn" {
		t.Errorf("upper = %q", got)
	}
	if got := member("onEvent", "onEvent"); got != "onEvent$" {
		t.Errorf("member = %q", got)
	}
}
