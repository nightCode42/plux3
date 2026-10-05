// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// formsDir is the conformance project of forms, whose bundles the
// runtime's form tests run.
var formsDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "forms")

const (
	formsApp  = "app.json"
	formsPage = "plugins/signup/pages/home.page.json"
	formsComp = "plugins/signup/components/newsletter.component.json"
	formsPl   = "plugins/signup/plugin.json"
)

// TestFormsGoldenBundles pins the bundles of the forms project byte for
// byte and checks the encoded forms: every validator kind, its options,
// the custom rule's program and the asynchronous validator's graph.
// Verifies: STA-020, CMP-002, QA-003.
func TestFormsGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(formsDir), DefaultOptions())
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	read := readAll(t, res)
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "forms", b.Key+".pxb"), b.Data)
	}
	var pg *fbs.Page
	for _, s := range read[1].Sections {
		if s.Kind == bundle.SectionPage {
			pg = fbs.GetRootAsPage(s.Data, 0)
		}
	}
	if pg == nil || pg.FormsLength() != 1 {
		t.Fatalf("page forms not encoded")
	}
	var f fbs.Form
	pg.Forms(&f, 0)
	str := func(i uint32) string { return string(pg.Strings(int(i))) }
	if str(f.Name()) != "signup" || f.FieldsLength() != 8 {
		t.Fatalf("form %s with %d fields", str(f.Name()), f.FieldsLength())
	}
	var kinds []fbs.ValidatorKind
	for i := range f.FieldsLength() {
		var fd fbs.FormField
		f.Fields(&fd, i)
		for j := range fd.ValidatorsLength() {
			var v fbs.FormValidator
			fd.Validators(&v, j)
			kinds = append(kinds, v.Kind())
			switch v.Kind() {
			case fbs.ValidatorKindRegex:
				if str(v.Pattern()) != "^[A-Za-z ]+$" || str(v.Message()) != "Letters only" {
					t.Errorf("regex validator %q %q", str(v.Pattern()), str(v.Message()))
				}
			case fbs.ValidatorKindDecimalPrecision:
				if v.MaxScale() != 2 || v.MaxIntegerDigits() != 6 {
					t.Errorf("decimal precision %d %d", v.MaxScale(), v.MaxIntegerDigits())
				}
			case fbs.ValidatorKindAsync:
				if v.Graph(nil) == nil || v.DebounceMs() != 300 {
					t.Errorf("async validator without its graph or debounce")
				}
			case fbs.ValidatorKindCustom:
				if v.Rule() == 0 {
					t.Errorf("custom validator without its rule")
				}
			case fbs.ValidatorKindLength:
				if v.Min(nil).I() != 2 || v.Max(nil).I() != 20 {
					t.Errorf("length bounds %d %d", v.Min(nil).I(), v.Max(nil).I())
				}
			}
		}
	}
	for k := fbs.ValidatorKindRequired; k <= fbs.ValidatorKindAsync; k++ {
		if !slices.Contains(kinds, k) {
			t.Errorf("no %s validator encoded", k)
		}
	}
}

// TestFormsFeatureIsRaised checks that forms need runtime 0.3.0: rejected
// under an older minRuntimeVersion, or raised as forms.v1 (BND-008).
// Verifies: STA-020, BND-008.
func TestFormsFeatureIsRaised(t *testing.T) {
	t.Parallel()
	m := project(t, formsDir)
	edit(t, m, formsApp, func(doc map[string]any) { doc["minRuntimeVersion"] = "0.2.0" })
	res := compileFS(m)
	wantDiag(t, res, plxerr.RuntimeTooOld, formsPage, "/forms")
	wantDiag(t, res, plxerr.RuntimeTooOld, formsComp, "/forms")

	m = project(t, formsDir)
	edit(t, m, formsApp, func(doc map[string]any) {
		doc["minRuntimeVersion"] = "0.2.0"
		doc["requiredFeatures"] = "raise"
	})
	res = compileFS(m)
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	if !slices.Contains(res.Plugins[0].Features, "forms.v1") {
		t.Errorf("plugin features %v lack forms.v1", res.Plugins[0].Features)
	}
}

// fieldOf returns field i of the page's form.
func fieldOf(doc map[string]any, i int) map[string]any {
	return doc["forms"].([]any)[0].(map[string]any)["fields"].([]any)[i].(map[string]any)
}

// setValidator replaces validator j of field i.
func setValidator(i, j int, v map[string]any) func(t *testing.T, m fstest.MapFS) {
	return onState(formsPage, func(doc map[string]any) {
		fieldOf(doc, i)["validators"].([]any)[j] = v
	})
}

// formSteps returns the steps of the page's child button at index i.
func formSteps(doc map[string]any, child int) []any {
	body := doc["root"].(map[string]any)["slots"].(map[string]any)["body"].(map[string]any)
	n := body["children"].([]any)[child].(map[string]any)
	return n["events"].(map[string]any)["onPressed"].(map[string]any)["steps"].([]any)
}

// TestInvalidForms checks the compile-time form diagnostics: validators
// on fields they do not apply to, missing and foreign options, bounds
// that cannot hold, patterns outside pxl.regex.v1, unknown phone
// regions, custom rules that are not predicates, asynchronous graphs
// that cannot validate, missing initial values, name conflicts, writes
// to form state other than values and touched flags, and form actions
// naming no form.
// Verifies: STA-020, PXL-003.
func TestInvalidForms(t *testing.T) {
	t.Parallel()
	const v0 = "/forms/0/fields/0/validators/0"
	ageButton := "/root/slots/body/children/6/events/onPressed/steps/0"
	cases := []stateCase{
		{
			name: "length on an int", edit: setValidator(4, 0, map[string]any{"kind": "length", "min": 1}),
			code: plxerr.FormValidatorNotApplicable, file: formsPage, ptr: "/forms/0/fields/4/validators/0/kind",
		},
		{
			name: "email on a date", edit: setValidator(6, 0, map[string]any{"kind": "email"}),
			code: plxerr.FormValidatorNotApplicable, file: formsPage, ptr: "/forms/0/fields/6/validators/0/kind",
		},
		{
			name: "regex without a pattern", edit: setValidator(0, 0, map[string]any{"kind": "regex"}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: v0,
		},
		{
			name: "required with a pattern", edit: setValidator(0, 0, map[string]any{"kind": "required", "pattern": "a"}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: v0 + "/pattern",
		},
		{
			name: "length min above max", edit: setValidator(0, 0, map[string]any{"kind": "length", "min": 5, "max": 2}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: v0 + "/min",
		},
		{
			name: "negative length", edit: setValidator(0, 0, map[string]any{"kind": "length", "min": -1}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: v0,
		},
		{
			name: "range min above max", edit: setValidator(4, 0, map[string]any{"kind": "range", "min": 100, "max": 3}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: "/forms/0/fields/4/validators/0/min",
		},
		{
			name: "decimal range min above max", edit: setValidator(5, 0, map[string]any{"kind": "range", "min": "10.5", "max": "3"}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: "/forms/0/fields/5/validators/0/min",
		},
		{
			name: "range bound of another type", edit: setValidator(4, 0, map[string]any{"kind": "range", "min": "ten"}),
			code: plxerr.ValueTypeMismatch, file: formsPage, ptr: "/forms/0/fields/4/validators/0/min",
		},
		{
			name: "date range min above max", edit: setValidator(6, 0, map[string]any{"kind": "dateRange", "min": "2027-01-01", "max": "2026-01-01"}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: "/forms/0/fields/6/validators/0/min",
		},
		{
			name: "date range bound that is not a date", edit: setValidator(6, 0, map[string]any{"kind": "dateRange", "min": "soon"}),
			code: plxerr.ValueTypeMismatch, file: formsPage, ptr: "/forms/0/fields/6/validators/0/min",
		},
		{
			name: "decimal precision without options", edit: setValidator(5, 0, map[string]any{"kind": "decimalPrecision"}),
			code: plxerr.FormValidatorOptions, file: formsPage, ptr: "/forms/0/fields/5/validators/0",
		},
		{
			name: "pattern outside the subset", edit: setValidator(0, 2, map[string]any{"kind": "regex", "pattern": "(?=a)"}),
			code: plxerr.FormPatternInvalid, file: formsPage, ptr: "/forms/0/fields/0/validators/2/pattern",
		},
		{
			name: "pattern above the repeat limit", edit: setValidator(0, 2, map[string]any{"kind": "regex", "pattern": "a{5000}"}),
			code: plxerr.FormPatternInvalid, file: formsPage, ptr: "/forms/0/fields/0/validators/2/pattern",
		},
		{
			name: "unknown phone region", edit: setValidator(2, 0, map[string]any{"kind": "phone", "region": "QQ"}),
			code: plxerr.FormPhoneRegionUnknown, file: formsPage, ptr: "/forms/0/fields/2/validators/0/region",
		},
		{
			name: "custom rule that is not a predicate", edit: setValidator(7, 0, map[string]any{"kind": "custom", "rule": map[string]any{"$expr": "value"}}),
			code: plxerr.ValueTypeMismatch, file: formsPage, ptr: "/forms/0/fields/7/validators/0/rule",
		},
		{
			name: "custom rule reading page state", edit: setValidator(7, 0, map[string]any{"kind": "custom", "rule": map[string]any{"$expr": "page.message == value"}}),
			code: plxerr.PXLUnknownIdentifier, file: formsPage, ptr: "/forms/0/fields/7/validators/0/rule",
		},
		{name: "async graph with another output", edit: onState("plugins/signup/actions/check-name.graph.json", func(doc map[string]any) {
			doc["output"] = "int?"
			doc["steps"] = doc["steps"].([]any)[:1]
		}), code: plxerr.FormAsyncValidatorInvalid, file: formsPage, ptr: "/forms/0/fields/0/validators/3/$graph"},
		{
			name: "async validator naming no graph", edit: setValidator(0, 3, map[string]any{"kind": "async", "$graph": "01f0c450-6c00-7000-8000-0000000007ff"}),
			code: plxerr.UnresolvedReference, file: formsPage, ptr: "/forms/0/fields/0/validators/3/$graph",
		},
		{
			name: "field without an initial value", edit: onState(formsPage, func(doc map[string]any) { delete(fieldOf(doc, 1), "initial") }),
			code: plxerr.FormFieldInitialMissing, file: formsPage, ptr: "/forms/0/fields/1",
		},
		{name: "form named like a state entry", edit: onState(formsPage, func(doc map[string]any) {
			doc["forms"].([]any)[0].(map[string]any)["name"] = "message"
		}), code: plxerr.FormNameConflict, file: formsPage, ptr: "/forms/0/name"},
		{
			name: "two fields with one name", edit: onState(formsPage, func(doc map[string]any) { fieldOf(doc, 1)["name"] = "name" }),
			code: plxerr.FormNameConflict, file: formsPage, ptr: "/forms/0/fields/1/name",
		},
		{name: "write of a form's errors", edit: onState(formsPage, func(doc map[string]any) {
			formSteps(doc, 6)[0].(map[string]any)["input"] = map[string]any{"path": "page.signup.errors.name", "value": "x"}
		}), code: plxerr.FormWriteInvalid, file: formsPage, ptr: ageButton + "/input/path"},
		{name: "reset of a form with resetState", edit: onState(formsPage, func(doc map[string]any) {
			formSteps(doc, 6)[0] = map[string]any{"action": "resetState", "id": "set", "input": map[string]any{"path": "page.signup"}}
		}), code: plxerr.FormWriteInvalid, file: formsPage, ptr: ageButton + "/input/path"},
		{name: "write of an unknown field", edit: onState(formsPage, func(doc map[string]any) {
			formSteps(doc, 6)[0].(map[string]any)["input"].(map[string]any)["path"] = "page.signup.values.height"
		}), code: plxerr.FormWriteInvalid, file: formsPage, ptr: ageButton + "/input/path"},
		{name: "write of a value of the wrong type", edit: onState(formsPage, func(doc map[string]any) {
			formSteps(doc, 6)[0].(map[string]any)["input"].(map[string]any)["value"] = "old"
		}), code: plxerr.PropTypeMismatch, file: formsPage, ptr: ageButton + "/input/value"},
		{name: "form action naming no form", edit: onState(formsPage, func(doc map[string]any) {
			formSteps(doc, 24)[0].(map[string]any)["input"] = map[string]any{"form": "login"}
		}), code: plxerr.UnresolvedReference, file: formsPage, ptr: "/root/slots/body/children/24/events/onPressed/steps/0/input/form"},
		{name: "page form action in a component", edit: onState(formsComp, func(doc map[string]any) {
			root := doc["root"].(map[string]any)["children"].([]any)[2].(map[string]any)
			steps := root["events"].(map[string]any)["onPressed"].(map[string]any)["steps"].([]any)
			steps[0].(map[string]any)["input"] = map[string]any{"form": "signup"}
		}), code: plxerr.UnresolvedReference, file: formsComp, ptr: "/root/children/2/events/onPressed/steps/0/input/form"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, formsDir)
			c.edit(t, m)
			wantDiag(t, compileFS(m), c.code, c.file, c.ptr)
		})
	}
}
