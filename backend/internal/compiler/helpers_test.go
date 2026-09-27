// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// fixtureDir is the conformance project (Appendix A).
var fixtureDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")

// Paths of the fixture's documents.
const (
	calculatorPage = "plugins/loans/pages/calculator.page.json"
	resultPage     = "plugins/loans/pages/result.page.json"
	calculateGraph = "plugins/loans/actions/calculate.graph.json"
	pluginFile     = "plugins/loans/plugin.json"
)

// fixture returns the conformance project in memory.
func fixture(t testing.TB) fstest.MapFS { return project(t, fixtureDir) }

// project reads a project directory into memory.
func project(t testing.TB, dir string) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// edit changes a JSON document of fs.
func edit(t testing.TB, m fstest.MapFS, file string, f func(doc map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(m[file].Data, &doc); err != nil {
		t.Fatal(err)
	}
	f(doc)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	m[file] = &fstest.MapFile{Data: data}
}

// at walks a JSON path of object keys and list indices.
func at(t testing.TB, doc any, path string) map[string]any {
	t.Helper()
	cur := doc
	for _, tok := range strings.Split(path, "/") {
		switch x := cur.(type) {
		case map[string]any:
			cur = x[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i >= len(x) {
				t.Fatalf("path %s: bad index %s", path, tok)
			}
			cur = x[i]
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("path %s is not an object", path)
	}
	return m
}

// raw decodes a JSON literal for use in edits.
func raw(t testing.TB, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// compileFS compiles a project with default options.
func compileFS(fsys fs.FS) *Result { return Compile(fsys, DefaultOptions()) }

// Paths in the calculator page.
const (
	column     = "root/slots/body/slots/child"
	amountNode = column + "/children/0"
	sliderNode = column + "/children/2"
	buttonNode = column + "/children/4"
)

// wantDiag fails unless a diagnostic with the code is reported in file at
// a pointer starting with ptr.
func wantDiag(t *testing.T, res *Result, code plxerr.Code, file, ptr string) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Code == code && d.File == file && strings.HasPrefix(d.Path, ptr) {
			return
		}
	}
	t.Errorf("no %s at %s#%s; got:\n%s", code, file, ptr, list(res.Diagnostics))
}

// list formats diagnostics, one per line.
func list(ds plxerr.Diagnostics) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString("  " + d.String() + "\n")
	}
	return b.String()
}

// Lifecycles that redirect the two pages of the fixture to each other.
const (
	calculatorRedirect = `{"onEnter": {"steps": [{"id": "go", "action": "navigate", "input": {"route": "result",
		"params": {"schedule": {"monthlyPayment": "1", "totalInterest": "0", "months": 1}}}}]}}`
	resultRedirect = `{"onEnter": {"steps": [{"id": "back", "action": "navigate", "input": {"route": "loan-calculator",
		"params": {"productId": "p"}}}]}}`
)

// redirect sets the lifecycle of a page to a redirect.
func redirect(t testing.TB, m fstest.MapFS, file, lifecycle string) {
	t.Helper()
	edit(t, m, file, func(doc map[string]any) { doc["lifecycle"] = raw(t, lifecycle) })
}
