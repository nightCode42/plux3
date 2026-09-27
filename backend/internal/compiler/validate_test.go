// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// newValidator returns a validator of the conformance project.
func newValidator(t testing.TB, m fstest.MapFS) *Validator {
	t.Helper()
	v, diags := NewValidator(m, DefaultOptions())
	if v == nil || len(diags) > 0 {
		t.Fatalf("validator: %v", diags)
	}
	return v
}

// pageEdit returns the calculator page with an edit applied.
func pageEdit(t testing.TB, f func(doc map[string]any)) []byte {
	t.Helper()
	m := fixture(t)
	edit(t, m, calculatorPage, f)
	return m[calculatorPage].Data
}

// Verifies: SCH-042.
func TestValidatePage(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	redirect(t, m, calculatorPage, calculatorRedirect)
	v := newValidator(t, m)
	for _, tc := range []struct {
		name string
		file string
		data []byte
		code plxerr.Code
		ptr  string
	}{
		{name: "unchanged", file: calculatorPage, data: m[calculatorPage].Data},
		{name: "type error", file: calculatorPage, data: pageEdit(t, func(doc map[string]any) {
			at(t, doc, sliderNode)["props"].(map[string]any)["value"] = map[string]any{"$expr": "page.nothing"}
		}), code: plxerr.PXLUnknownField, ptr: "/" + sliderNode + "/props/value/$expr"},
		{name: "structural error", file: calculatorPage, data: []byte(`{"kind": "page"}`), code: plxerr.MissingProperty, ptr: ""},
		{name: "not JSON", file: calculatorPage, data: []byte(`{`), code: plxerr.InvalidJSON},
		{name: "new page", file: "plugins/loans/pages/extra.page.json", data: pageEdit(t, func(doc map[string]any) {
			doc["id"] = "01a0c450-6c00-7fff-8000-000000000010"
			doc["key"] = "extra"
			doc["route"] = "extra"
			at(t, doc, "root")["id"] = "01a0c450-6c00-7fff-8000-000000000011"
			at(t, doc, "root")["type"] = "Knob"
		}), code: plxerr.UnknownWidgetType, ptr: "/root/type"},
		{name: "state a graph needs", file: calculatorPage, data: pageEdit(t, func(doc map[string]any) {
			at(t, doc, "state/0")["name"] = "principal"
		}), code: plxerr.PXLUnknownField, ptr: "/steps/0/input/value/totalInterest/$expr"},
		{name: "redirect loop", file: resultPage, data: func() []byte {
			r := fixture(t)
			redirect(t, r, resultPage, resultRedirect)
			return r[resultPage].Data
		}(), code: plxerr.RedirectLoop, ptr: "/lifecycle/onEnter"},
		// The calculator page is listed first, so it claims the ID first.
		{name: "ID of another page", file: calculatorPage, data: pageEdit(t, func(doc map[string]any) {
			at(t, doc, amountNode)["id"] = "01a0c450-6c00-702c-8000-000000055114" // the result page's root
		}), code: plxerr.DuplicateID, ptr: "/" + amountNode + "/id"},
		{name: "route of another page", file: calculatorPage, data: pageEdit(t, func(doc map[string]any) {
			doc["route"] = "result"
		}), code: plxerr.DuplicateRouteName, ptr: "/route"},
		{name: "outside a plugin", file: "pages/extra.page.json", data: m[calculatorPage].Data, code: plxerr.InvalidProjectLayout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags := v.ValidatePage(tc.file, tc.data)
			if tc.code == 0 {
				if len(diags) > 0 {
					t.Fatalf("diagnostics:\n%s", list(diags))
				}
				return
			}
			for _, d := range diags {
				if d.File != tc.file && !strings.Contains(d.File, "/actions/") {
					t.Errorf("diagnostic outside the page and its graphs: %s", d)
				}
			}
			found := false
			for _, d := range diags {
				found = found || d.Code == tc.code && strings.HasPrefix(d.Path, tc.ptr)
			}
			if !found {
				t.Errorf("no %s at %s; got:\n%s", tc.code, tc.ptr, list(diags))
			}
		})
	}
}

// Verifies: SCH-042.
// Validating an edit leaves the loaded project as it was: a broken edit
// followed by the original page reports nothing.
func TestValidatePageKeepsTheProject(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	v := newValidator(t, m)
	broken := pageEdit(t, func(doc map[string]any) { at(t, doc, sliderNode)["type"] = "Knob" })
	if diags := v.ValidatePage(calculatorPage, broken); !diags.HasErrors() {
		t.Fatal("the broken page was accepted")
	}
	if diags := v.ValidatePage(calculatorPage, m[calculatorPage].Data); len(diags) > 0 {
		t.Fatalf("the original page reports:\n%s", list(diags))
	}
	if res := compileFS(m); len(res.Diagnostics) > 0 {
		t.Fatalf("the project no longer compiles:\n%s", list(res.Diagnostics))
	}
}

// Verifies: CMP-052.
func TestNewValidatorReportsTheProject(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	m["app.json"] = &fstest.MapFile{Data: []byte("{")}
	v, diags := NewValidator(m, DefaultOptions())
	if v == nil || !diags.HasErrors() {
		t.Fatalf("validator %v, diagnostics %v", v, diags)
	}
	// A page of a project without an app still validates without panicking.
	for _, d := range v.ValidatePage(calculatorPage, m[calculatorPage].Data) {
		if d.Code == plxerr.InternalCompilerError {
			t.Errorf("internal error: %s", d)
		}
	}
}
