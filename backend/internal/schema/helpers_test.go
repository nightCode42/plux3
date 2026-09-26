// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// exampleDir is the conformance project shared with the compiler and the
// runtime (QA-003).
var exampleDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")

// sharedValidator is compiled once for all tests.
var sharedValidator = sync.OnceValues(NewValidator)

// newLoader returns a loader with default limits.
func newLoader(t *testing.T) *Loader {
	t.Helper()
	v, err := sharedValidator()
	if err != nil {
		t.Fatal(err)
	}
	return NewLoader(v, DefaultMigrator(), limits.Defaults())
}

// exampleFS returns an in-memory copy of the conformance project.
func exampleFS(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	err := filepath.WalkDir(exampleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(exampleDir, path)
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// edit decodes a JSON file of fsys, lets f change it, and writes it back.
func edit(t *testing.T, fsys fstest.MapFS, file string, f func(doc map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(fsys[file].Data, &doc); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	f(doc)
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	fsys[file] = &fstest.MapFile{Data: data}
}

// at navigates a decoded document by a slash-separated path of keys and
// indices, e.g. "root/slots/body".
func at(t *testing.T, doc any, path string) map[string]any {
	t.Helper()
	cur := doc
	for _, key := range strings.Split(path, "/") {
		switch c := cur.(type) {
		case map[string]any:
			cur = c[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil {
				t.Fatalf("index %q: %v", key, err)
			}
			cur = c[i]
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, not an object", path, cur)
	}
	return m
}

// wantDiagnostic fails unless diags contain exactly one diagnostic with the
// code at file#path, and returns it.
func wantDiagnostic(t *testing.T, diags plxerr.Diagnostics, code plxerr.Code, file, path string) plxerr.Diagnostic {
	t.Helper()
	var found []plxerr.Diagnostic
	for _, d := range diags {
		if d.Code == code && d.File == file && d.Path == path {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s at %s#%s, got %d; all diagnostics:\n%s", code, file, path, len(found), list(diags))
	}
	return found[0]
}

// list formats diagnostics one per line.
func list(diags plxerr.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString("  " + d.String() + "\n")
	}
	return b.String()
}
