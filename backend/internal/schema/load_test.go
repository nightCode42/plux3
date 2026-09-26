// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"bytes"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// TestLoadsTheConformanceProject_SCH_006 checks that the shared example
// project loads from the Git layout without a single diagnostic, with one
// document per file.
// Verifies: SCH-006, SCH-001, SCH-020, SCH-021, SCH-022, SCH-023.
func TestLoadsTheConformanceProject_SCH_006(t *testing.T) {
	t.Parallel()
	p, diags := newLoader(t).Load(os.DirFS(exampleDir))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", list(diags))
	}
	if p.App.Doc == nil || p.Theme.Doc == nil || p.NativeCatalogue == nil || p.TranslationKeys == nil || p.Assets == nil {
		t.Fatal("a root document is missing")
	}
	if len(p.Plugins) != 1 || len(p.Plugins[0].Pages) != 2 || len(p.Plugins[0].Graphs) != 2 {
		t.Fatalf("plugins = %d", len(p.Plugins))
	}
	if len(p.Translations) != 3 || p.Translations[0].Doc.Locale != "am-ET" {
		t.Errorf("translations not loaded in file order")
	}
	if len(p.Templates) != 1 || len(p.AssetFiles["images/logo.png"]) == 0 {
		t.Errorf("templates or assets missing")
	}
	calc := p.Plugins[0].Pages[0]
	if calc.Doc.Key != "calculator" || calc.Doc.Route != "loan-calculator" || calc.Doc.PageKind != PageKindScreen {
		t.Errorf("calculator page decoded wrongly: %+v", calc.Doc)
	}
	body := calc.Doc.Root.Slots["body"]
	if body.One == nil || body.One.Type != "Padding" || body.One.Slots["child"].One.Children[0].TestID != "amount" {
		t.Errorf("node tree decoded wrongly")
	}
}

// TestSourcesAreCanonicalAndHashed_SCH_003 checks that every document is
// kept as its RFC 8785 form with its SHA-256.
// Verifies: SCH-003.
func TestSourcesAreCanonicalAndHashed_SCH_003(t *testing.T) {
	t.Parallel()
	p, _ := newLoader(t).Load(os.DirFS(exampleDir))
	for _, src := range []*Source{p.App.Source, p.Theme.Source, p.Plugins[0].Source, p.Plugins[0].Pages[0].Source} {
		data, err := os.ReadFile(exampleDir + "/" + src.File)
		if err != nil {
			t.Fatal(err)
		}
		want, err := jcs.Canonicalize(data, 512)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(src.Canonical, want) || src.Hash != sha256.Sum256(want) {
			t.Errorf("%s: canonical bytes or hash differ", src.File)
		}
	}
}

// TestExtensionPropertiesArePreservedAndIgnored_SCH_004 checks that x-
// properties pass validation anywhere, stay in the canonical source and do
// not reach the decoded types.
// Verifies: SCH-004.
func TestExtensionPropertiesArePreservedAndIgnored_SCH_004(t *testing.T) {
	t.Parallel()
	fsys := exampleFS(t)
	edit(t, fsys, "plugins/loans/pages/calculator.page.json", func(doc map[string]any) {
		doc["x-studio"] = map[string]any{"zoom": 2}
		at(t, doc, "root")["x-note"] = "reviewed"
		at(t, doc, "security")["x-audit"] = true
	})

	p, diags := newLoader(t).Load(fsys)

	if len(diags) != 0 {
		t.Fatalf("x- properties rejected:\n%s", list(diags))
	}
	src := p.Plugins[0].Pages[0].Source
	for _, want := range []string{`"x-studio":{"zoom":2}`, `"x-note":"reviewed"`, `"x-audit":true`} {
		if !strings.Contains(string(src.Canonical), want) {
			t.Errorf("canonical source lost %s", want)
		}
	}
}

// TestStructuralValidationReportsExactPaths_SCH_040 checks the mapping of
// schema failures to codes and JSON Pointers, including through unions.
// Verifies: SCH-040, SCH-004, SCH-011, SCH-002.
func TestStructuralValidationReportsExactPaths_SCH_040(t *testing.T) {
	t.Parallel()
	const page = "plugins/loans/pages/calculator.page.json"
	tests := []struct {
		name string
		edit func(t *testing.T, doc map[string]any)
		code plxerr.Code
		path string
	}{
		{"unknown top-level property", func(t *testing.T, d map[string]any) { d["colour"] = "red" }, plxerr.UnknownProperty, "/colour"},
		{"unknown nested property", func(t *testing.T, d map[string]any) { at(t, d, "root")["colour"] = "red" }, plxerr.UnknownProperty, "/root/colour"},
		{"unknown property inside a slot", func(t *testing.T, d map[string]any) { at(t, d, "root/slots/appBar")["bogus"] = 1 }, plxerr.UnknownProperty, "/root/slots/appBar/bogus"},
		{"missing property", func(t *testing.T, d map[string]any) { delete(d, "pageKind") }, plxerr.MissingProperty, ""},
		{"wrong JSON type", func(t *testing.T, d map[string]any) { d["key"] = 7 }, plxerr.WrongJSONType, "/key"},
		{"enum value", func(t *testing.T, d map[string]any) { d["pageKind"] = "popup" }, plxerr.InvalidEnumValue, "/pageKind"},
		{"invalid identifier", func(t *testing.T, d map[string]any) { at(t, d, "root")["id"] = "n_scaffold" }, plxerr.InvalidFormat, "/root/id"},
		{"invalid key", func(t *testing.T, d map[string]any) { d["route"] = "Loan_Calculator" }, plxerr.InvalidFormat, "/route"},
		{"binding with wrong type", func(t *testing.T, d map[string]any) {
			at(t, d, "root/slots/appBar/slots/title/props")["data"] = map[string]any{"$expr": 5}
		}, plxerr.WrongJSONType, "/root/slots/appBar/slots/title/props/data/$expr"},
		{"binding with extra key", func(t *testing.T, d map[string]any) {
			at(t, d, "root/slots/appBar/slots/title/props")["data"] = map[string]any{"$expr": "a", "extra": 1}
		}, plxerr.UnknownProperty, "/root/slots/appBar/slots/title/props/data/extra"},
		{"two binding keys", func(t *testing.T, d map[string]any) {
			at(t, d, "root/slots/appBar/slots/title/props")["data"] = map[string]any{"$expr": "a", "$token": "color.primary"}
		}, plxerr.InvalidStructure, "/root/slots/appBar/slots/title/props/data"},
		{"literal object with a $ key", func(t *testing.T, d map[string]any) {
			at(t, d, "root/slots/appBar/slots/title/props")["data"] = map[string]any{"$unknown": 1}
		}, plxerr.InvalidStructure, "/root/slots/appBar/slots/title/props/data"},
		{"node with type and component", func(t *testing.T, d map[string]any) {
			at(t, d, "root")["component"] = map[string]any{"id": "01928c3a-7b2e-7c4d-8e5f-0123456789ab", "version": 1}
		}, plxerr.InvalidStructure, "/root"},
		{"node with children and slots", func(t *testing.T, d map[string]any) { at(t, d, "root")["children"] = []any{} }, plxerr.InvalidStructure, "/root"},
		{"handler without graph or steps", func(t *testing.T, d map[string]any) {
			at(t, d, "lifecycle")["onEnter"] = map[string]any{"concurrency": "drop"}
		}, plxerr.InvalidStructure, "/lifecycle/onEnter"},
		{"bad concurrency", func(t *testing.T, d map[string]any) {
			at(t, d, "lifecycle/onEnter")["concurrency"] = "debounce:0"
		}, plxerr.InvalidFormat, "/lifecycle/onEnter/concurrency"},
		{"bad prop name", func(t *testing.T, d map[string]any) {
			at(t, d, "root/slots/appBar/slots/title/props")["Data"] = "x"
		}, plxerr.InvalidFormat, "/root/slots/appBar/slots/title/props/Data"},
		{"state entry without default or computed", func(t *testing.T, d map[string]any) {
			delete(at(t, d, "state/0"), "default")
		}, plxerr.InvalidStructure, "/state/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := exampleFS(t)
			edit(t, fsys, page, func(doc map[string]any) { tt.edit(t, doc) })

			p, diags := newLoader(t).Load(fsys)

			d := wantDiagnostic(t, diags, tt.code, page, tt.path)
			if d.Message == "" || d.DocURL == "" || d.Cause == "" {
				t.Errorf("incomplete diagnostic %+v", d)
			}
			if p.Plugins[0].Pages[0].Doc != nil {
				t.Error("an invalid document was decoded")
			}
		})
	}
}

// TestLayoutRulesAreEnforced_SCH_006 checks every rule of the Git layout.
// Verifies: SCH-006, SCH-002.
func TestLayoutRulesAreEnforced_SCH_006(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		change     func(t *testing.T, fsys fstest.MapFS)
		code       plxerr.Code
		file, path string
	}{
		{"file not named after its key", func(t *testing.T, f fstest.MapFS) {
			f["plugins/loans/pages/calc.page.json"] = f["plugins/loans/pages/calculator.page.json"]
			delete(f, "plugins/loans/pages/calculator.page.json")
		}, plxerr.InvalidProjectLayout, "plugins/loans/pages/calc.page.json", "/key"},
		{"wrong document kind for the location", func(t *testing.T, f fstest.MapFS) {
			f["plugins/loans/pages/set-amount.page.json"] = f["plugins/loans/actions/set-amount.graph.json"]
		}, plxerr.InvalidProjectLayout, "plugins/loans/pages/set-amount.page.json", "/kind"},
		{"unexpected file name", func(t *testing.T, f fstest.MapFS) {
			f["plugins/loans/pages/notes.json"] = &fstest.MapFile{Data: []byte("{}")}
		}, plxerr.InvalidProjectLayout, "plugins/loans/pages/notes.json", ""},
		{"page not listed in the plugin", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, "plugins/loans/plugin.json", func(d map[string]any) { d["pages"] = d["pages"].([]any)[:1] })
		}, plxerr.InvalidProjectLayout, "plugins/loans/pages/result.page.json", "/id"},
		{"listed page without a file", func(t *testing.T, f fstest.MapFS) {
			delete(f, "plugins/loans/pages/result.page.json")
		}, plxerr.UnresolvedReference, "plugins/loans/plugin.json", "/pages/1"},
		{"plugin directory not listed in the app", func(t *testing.T, f fstest.MapFS) {
			f["plugins/cards/plugin.json"] = f["plugins/loans/plugin.json"]
		}, plxerr.InvalidProjectLayout, "plugins/cards/plugin.json", ""},
		{"plugin key differs from its directory", func(t *testing.T, f fstest.MapFS) {
			f["plugins/cards/plugin.json"] = f["plugins/loans/plugin.json"]
		}, plxerr.InvalidProjectLayout, "plugins/cards/plugin.json", "/key"},
		{"listed plugin without a directory", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, "app.json", func(d map[string]any) { d["plugins"] = []any{"loans", "cards"} })
		}, plxerr.InvalidProjectLayout, "app.json", "/plugins/1"},
		{"translations file not named after its locale", func(t *testing.T, f fstest.MapFS) {
			f["translations/de.json"] = f["translations/de-DE.json"]
			delete(f, "translations/de-DE.json")
		}, plxerr.InvalidProjectLayout, "translations/de.json", "/locale"},
		{
			"missing asset file", func(t *testing.T, f fstest.MapFS) { delete(f, "assets/images/logo.png") },
			plxerr.InvalidProjectLayout, "assets/index.json", "/assets/0/file",
		},
		{
			"missing theme", func(t *testing.T, f fstest.MapFS) { delete(f, "theme.json") },
			plxerr.InvalidProjectLayout, "theme.json", "",
		},
		{
			"no project", func(t *testing.T, f fstest.MapFS) { delete(f, "app.json") },
			plxerr.ProjectNotFound, "app.json", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := exampleFS(t)
			tt.change(t, fsys)

			_, diags := newLoader(t).Load(fsys)

			wantDiagnostic(t, diags, tt.code, tt.file, tt.path)
		})
	}
}

// TestParsingProblemsAreDiagnostics_SCH_003 checks malformed files: syntax
// errors with positions, duplicate keys and non-object documents.
// Verifies: SCH-003, SCH-040.
func TestParsingProblemsAreDiagnostics_SCH_003(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		data string
		code plxerr.Code
		msg  string
	}{
		"syntax error":   {"{\n  \"kind\": \"theme\",\n  oops\n}", plxerr.InvalidJSON, "line 3, column 3"},
		"duplicate key":  {`{"kind":"theme","kind":"theme"}`, plxerr.InvalidJSON, "duplicate member"},
		"not an object":  {`["theme"]`, plxerr.WrongJSONType, "JSON object"},
		"unknown kind":   {`{"kind":"widget"}`, plxerr.InvalidProjectLayout, `"widget"`},
		"no version":     {`{"kind":"theme"}`, plxerr.MissingProperty, "schemaVersion"},
		"future version": {`{"kind":"theme","schemaVersion":"9.0.0"}`, plxerr.UnsupportedSchemaVersion, "9.0.0"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fsys := exampleFS(t)
			fsys["theme.json"] = &fstest.MapFile{Data: []byte(tt.data)}

			_, diags := newLoader(t).Load(fsys)

			var got *plxerr.Diagnostic
			for i := range diags {
				if diags[i].File == "theme.json" {
					got = &diags[i]
				}
			}
			if got == nil || got.Code != tt.code || !strings.Contains(got.Message, tt.msg) {
				t.Fatalf("got %v, want %s containing %q\n%s", got, tt.code, tt.msg, list(diags))
			}
		})
	}
}

// TestSizeLimitsComeFromTheRegistry_SCH_005 checks the document, depth,
// plugin and page limits, tightened through the registry (LIM-001).
// Verifies: SCH-005, LIM-001.
func TestSizeLimitsComeFromTheRegistry_SCH_005(t *testing.T) {
	t.Parallel()
	tighten := func(t *testing.T, k limits.Key, v int64) limits.Set {
		t.Helper()
		s, err := limits.Defaults().Tighten(k, limits.ScopeInstallation, v)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	v, err := sharedValidator()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		lim        limits.Set
		code       plxerr.Code
		file, want string
	}{
		{"file size", tighten(t, limits.DocumentFileSize, 100), plxerr.LimitExceeded, "app.json", "document.fileSize"},
		{"JSON depth", tighten(t, limits.DocumentJSONDepth, 4), plxerr.InvalidJSON, "plugins/loans/pages/calculator.page.json", "nesting deeper than 4"},
		{"pages per plugin", tighten(t, limits.PluginPages, 1), plxerr.LimitExceeded, "plugins/loans/pages", "plugin.pages = 1"},
		{"plugins per app", tighten(t, limits.AppPlugins, 1), plxerr.LimitExceeded, "plugins", "app.plugins = 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := exampleFS(t)
			if tt.name == "plugins per app" {
				fsys["plugins/cards/plugin.json"] = fsys["plugins/loans/plugin.json"]
			}

			_, diags := NewLoader(v, DefaultMigrator(), tt.lim).Load(fsys)

			found := false
			for _, d := range diags {
				found = found || d.Code == tt.code && d.File == tt.file && strings.Contains(d.Message, tt.want)
			}
			if !found {
				t.Fatalf("want %s in %s mentioning %q:\n%s", tt.code, tt.file, tt.want, list(diags))
			}
		})
	}
}

// TestParseDocumentValidatesOneEditedPage_SCH_042 checks the single-document
// entry point used for incremental validation.
// Verifies: SCH-042.
func TestParseDocumentValidatesOneEditedPage_SCH_042(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	data, err := os.ReadFile(exampleDir + "/plugins/loans/pages/calculator.page.json")
	if err != nil {
		t.Fatal(err)
	}
	src, diags := l.ParseDocument("calculator.page.json", data, "")
	if len(diags) != 0 || src == nil || src.Kind != KindPage {
		t.Fatalf("valid page: %v\n%s", src, list(diags))
	}
	_, diags = l.ParseDocument("calculator.page.json", bytes.Replace(data, []byte(`"screen"`), []byte(`"popup"`), 1), KindPage)
	wantDiagnostic(t, diags, plxerr.InvalidEnumValue, "calculator.page.json", "/pageKind")
}
