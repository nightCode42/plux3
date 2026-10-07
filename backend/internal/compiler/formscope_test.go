// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"strconv"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// scopedText is a FormScope around a Text that reads the scoped form.
func scopedText(form, expr string) map[string]any {
	return map[string]any{
		"id": "01f0c450-6c00-7000-8000-000000000792", "type": "FormScope",
		"props": map[string]any{"form": form},
		"slots": map[string]any{"child": map[string]any{
			"id": "01f0c450-6c00-7000-8000-000000000793", "type": "Text",
			"props": map[string]any{"data": map[string]any{"$expr": expr}},
		}},
	}
}

// appendScoped appends node to the page's body and returns its index.
func appendScoped(doc map[string]any, node map[string]any) int {
	body := doc["root"].(map[string]any)["slots"].(map[string]any)["body"].(map[string]any)
	children := append(body["children"].([]any), node)
	body["children"] = children
	return len(children) - 1
}

// TestFormScopeRootIsTheScopedForm checks that below a FormScope the
// `form` root is the named form's state, typed as that form, that a
// FormScope naming no form is reported, and that the widget raises its
// feature for an older minRuntimeVersion.
// Verifies: STA-020, BND-008.
func TestFormScopeRootIsTheScopedForm(t *testing.T) {
	t.Parallel()
	m := project(t, formsDir)
	edit(t, m, formsPage, func(doc map[string]any) {
		appendScoped(doc, scopedText("signup", "form.values.name + ': ' + (form.errors.name ?? 'ok')"))
	})
	if res := compileFS(m); res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}

	m = project(t, formsDir)
	edit(t, m, formsPage, func(doc map[string]any) { appendScoped(doc, scopedText("signup", "form.values.nope")) })
	if res := compileFS(m); !res.Diagnostics.HasErrors() {
		t.Error("a field the form does not declare compiled")
	}

	for _, name := range []string{"missing", ""} {
		m = project(t, formsDir)
		at := 0
		edit(t, m, formsPage, func(doc map[string]any) { at = appendScoped(doc, scopedText(name, "'x'")) })
		wantDiag(t, compileFS(m), plxerr.FormScopeInvalid, formsPage, "/root/slots/body/children/"+strconv.Itoa(at)+"/props/form")
	}

	m = project(t, formsDir)
	edit(t, m, formsPage, func(doc map[string]any) { appendScoped(doc, scopedText("signup", "form.values.name")) })
	edit(t, m, formsApp, func(doc map[string]any) {
		doc["minRuntimeVersion"] = "0.2.0"
		doc["requiredFeatures"] = "raise"
	})
	res := compileFS(m)
	if !slices.Contains(res.Plugins[0].Features, "widget.FormScope.v1") {
		t.Errorf("plugin features %v lack widget.FormScope.v1", res.Plugins[0].Features)
	}
}
