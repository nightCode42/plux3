// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: CMP-052, QA-004.
// Whatever the content of a page or a graph, the compiler reports
// diagnostics and never fails internally.
func FuzzCompile(f *testing.F) {
	base := fixture(f)
	for _, file := range []string{calculatorPage, resultPage, calculateGraph} {
		f.Add(file == calculateGraph, base[file].Data)
	}
	f.Add(false, []byte(`{"kind":"page","schemaVersion":"1.0.0"}`))
	f.Add(true, []byte(`{"kind":"action-graph","steps":[{"id":"a","action":"stop"}]}`))
	f.Fuzz(func(t *testing.T, graph bool, data []byte) {
		m := fstest.MapFS{}
		for k, v := range base {
			m[k] = v
		}
		file := calculatorPage
		if graph {
			file = calculateGraph
		}
		m[file] = &fstest.MapFile{Data: data}
		res := compileFS(m)
		for _, d := range res.Diagnostics {
			if d.Code == plxerr.InternalCompilerError {
				t.Fatalf("internal error: %s", d)
			}
		}
		if res.Diagnostics.HasErrors() && res.App != nil {
			t.Fatal("bundles despite errors")
		}
	})
}

// Verifies: CMP-052, SCH-042.
func FuzzValidatePage(f *testing.F) {
	base := fixture(f)
	v, _ := NewValidator(base, DefaultOptions())
	f.Add(base[calculatorPage].Data)
	f.Add(base[resultPage].Data)
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, d := range v.ValidatePage(calculatorPage, data) {
			if d.Code == plxerr.InternalCompilerError {
				t.Fatalf("internal error: %s", d)
			}
		}
	})
}
