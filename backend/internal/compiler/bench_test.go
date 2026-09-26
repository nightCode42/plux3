// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// Patterns rewritten in page copies.
var (
	uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
	keyPattern  = regexp.MustCompile(`"key": "([a-z-]+)"`)
)

// calculatorID is the calculator page's ID.
const calculatorID = "01a0c450-6c00-7012-8000-000000022cce"

// manyPages returns the conformance project with n copies of the
// calculator page and its action graphs added to the loans plugin: each
// copy gets its own IDs, keys and route, and keeps its references to the
// plugin's state, translations and assets.
func manyPages(t testing.TB, n int) fstest.MapFS {
	t.Helper()
	m := fixture(t)
	docs := map[string]string{calculatorPage: string(m[calculatorPage].Data)}
	own := map[string]bool{}
	for file, f := range m {
		var doc map[string]any
		if err := json.Unmarshal(f.Data, &doc); err != nil {
			continue
		}
		if file == calculatorPage || strings.HasSuffix(file, ".graph.json") && doc["page"] == calculatorID {
			docs[file] = string(f.Data)
			collectIDs(doc, own)
		}
	}
	var pages []any
	for i := range n {
		ids := map[string]string{}
		key := fmt.Sprintf("copy%d", i)
		for _, file := range sortedKeys(docs) {
			copied := uuidPattern.ReplaceAllStringFunc(docs[file], func(id string) string {
				if !own[id] {
					return id
				}
				if _, ok := ids[id]; !ok {
					ids[id] = fmt.Sprintf("01c0%04x-6c00-7000-8000-%012x", i, len(ids))
				}
				return ids[id]
			})
			copied = keyPattern.ReplaceAllString(copied, `"key": "${1}-`+key+`"`)
			copied = strings.Replace(copied, `"route": "loan-calculator"`, `"route": "`+key+`"`, 1)
			m[strings.Replace(file, ".", "-"+key+".", 1)] = &fstest.MapFile{Data: []byte(copied)}
		}
		pages = append(pages, ids[calculatorID])
	}
	edit(t, m, pluginFile, func(doc map[string]any) { doc["pages"] = append(doc["pages"].([]any), pages...) })
	return m
}

// collectIDs adds the values of every "id" key that holds a UUID.
func collectIDs(v any, ids map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, it := range x {
			if s, ok := it.(string); ok && k == "id" && uuidPattern.MatchString(s) {
				ids[s] = true
			}
			collectIDs(it, ids)
		}
	case []any:
		for _, it := range x {
			collectIDs(it, ids)
		}
	}
}

// TestManyPagesCompile checks the benchmark's input: it must compile
// cleanly, or the benchmark would measure an early exit.
func TestManyPagesCompile(t *testing.T) {
	t.Parallel()
	res := compileFS(manyPages(t, 48))
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	if pages := readAll(t, res)[1].Meta.PagesLength(); pages != 50 {
		t.Fatalf("%d pages", pages)
	}
}

// Verifies: CMP-050.
// Compiling a 50-page plugin without asset processing; the budget is
// 1 s on the CI reference runner.
func BenchmarkCompile50Pages(b *testing.B) {
	m := manyPages(b, 48)
	opts := DefaultOptions()
	for b.Loop() {
		if res := Compile(m, opts); res.App == nil {
			b.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	}
}

// Verifies: SCH-042, NFR-033.
// Validating one edited page of a 50-page plugin; the budget is 50 ms
// at the 95th percentile.
func BenchmarkValidatePage(b *testing.B) {
	m := manyPages(b, 48)
	v := newValidator(b, m)
	data := m[calculatorPage].Data
	for b.Loop() {
		if diags := v.ValidatePage(calculatorPage, data); len(diags) > 0 {
			b.Fatalf("diagnostics:\n%s", list(diags))
		}
	}
}
