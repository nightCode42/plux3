// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"pgregory.net/rapid"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// update rewrites the golden bundles.
var update = flag.Bool("update", false, "rewrite the golden bundles in schema/testdata/bundles")

// goldenRoot holds the bundles of the conformance projects, read by the
// Dart tests too (QA-003).
var goldenRoot = filepath.Join("..", "..", "..", "schema", "testdata", "bundles")

// checkGolden compares data with a golden file, rewriting it with -update.
func checkGolden(t *testing.T, path string, data []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path) //nolint:gosec // G304: a path under the repository.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(golden, data) {
		t.Errorf("%s differs from the golden bundle; run the test with -update and review", path)
	}
}

// readAll reads every bundle of a result, failing on any error.
func readAll(t *testing.T, res *Result) []*bundle.Bundle {
	t.Helper()
	var out []*bundle.Bundle
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		if b == nil {
			t.Fatalf("missing bundle; diagnostics:\n%s", list(res.Diagnostics))
		}
		r, err := bundle.Read(b.Data, bundle.ReadOptions{Limits: limits.Defaults(), Supports: func(string) bool { return true }})
		if err != nil {
			t.Fatalf("bundle %s: %v", b.Key, err)
		}
		out = append(out, r)
	}
	return out
}

// Verifies: CMP-001, CMP-003, BND-002, BND-004, QA-003.
func TestCompileConformanceProject(t *testing.T) {
	t.Parallel()
	res := compileFS(fixture(t))
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	if res.App == nil || len(res.Plugins) != 1 || res.Plugins[0].Key != "loans" {
		t.Fatalf("bundles: app %v, plugins %v", res.App, res.Plugins)
	}
	read := readAll(t, res)
	kinds := func(b *bundle.Bundle) []string {
		var ks []string
		for _, s := range b.Sections {
			ks = append(ks, s.Kind.String())
		}
		return ks
	}
	if got := kinds(read[0]); !slices.Equal(got, []string{"meta", "styles", "strings", "l10n", "l10n", "l10n", "schemas", "assets-index"}) {
		t.Errorf("app sections %v", got)
	}
	if got := kinds(read[1]); !slices.Equal(got, []string{"meta", "page", "page", "actions", "pxl", "styles", "strings", "schemas", "assets-index"}) {
		t.Errorf("plugin sections %v", got)
	}
	meta := read[1].Meta
	if string(meta.CompilerVersion()) != "dev" || string(meta.SchemaVersion()) != "1.0.0" || string(meta.MinRuntime()) != "0.1.0" {
		t.Errorf("meta versions %s %s %s", meta.CompilerVersion(), meta.SchemaVersion(), meta.MinRuntime())
	}
	if !slices.Equal(res.Plugins[0].Features, []string{"pxl.v1"}) || meta.RequiredFeaturesLength() != 1 {
		t.Errorf("features %v", res.Plugins[0].Features)
	}
	if meta.PagesLength() != 2 {
		t.Errorf("meta lists %d pages", meta.PagesLength())
	}
}

// Verifies: CMP-002, QA-003.
// The bundles of the conformance project are pinned byte for byte.
func TestGoldenBundles(t *testing.T) {
	t.Parallel()
	res := compileFS(fixture(t))
	readAll(t, res)
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "loan-calculator", b.Key+".pxb"), b.Data)
	}
}

// Verifies: CMP-041.
func TestSourceMaps(t *testing.T) {
	t.Parallel()
	release := compileFS(fixture(t))
	pl := release.Plugins[0]
	if pl.Kind != bundle.KindPlugin || len(pl.SourceMap) == 0 {
		t.Fatalf("release: kind %d, source map %d bytes", pl.Kind, len(pl.SourceMap))
	}
	opts := DefaultOptions()
	opts.Mode = Development
	dev := Compile(fixture(t), opts)
	if len(dev.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(dev.Diagnostics))
	}
	b := readAll(t, dev)[1]
	if b.Kind != bundle.KindDevelopment || b.Flags&bundle.FlagSourceMap == 0 || dev.Plugins[0].SourceMap != nil {
		t.Fatalf("development bundle: kind %d, flags %d", b.Kind, b.Flags)
	}
	s, ok := b.Section(bundle.SectionSourceMap, b.Sections[0].ID)
	if !ok || !bytes.Equal(s.Data, pl.SourceMap) {
		t.Fatal("the development source map differs from the release one")
	}
	sm := fbs.GetRootAsSourceMap(s.Data, 0)
	var loc fbs.Location
	found := false
	for i := range sm.NodesLength() {
		sm.Nodes(&loc, i)
		if string(loc.Pointer()) == "/"+amountNode {
			found = true
		}
	}
	if !found || sm.StepsLength() == 0 {
		t.Errorf("the source map lacks the amount field (%d nodes, %d steps)", sm.NodesLength(), sm.StepsLength())
	}
}

// Verifies: SCH-041.
func TestReferenceGraph(t *testing.T) {
	t.Parallel()
	res := compileFS(fixture(t))
	has := func(kind EdgeKind, to, file string) bool {
		return slices.ContainsFunc(res.Graph.Edges, func(e Edge) bool { return e.Kind == kind && e.To == to && e.File == file })
	}
	for _, want := range []struct {
		kind     EdgeKind
		to, file string
	}{
		{EdgeNavigates, "01a0c450-6c00-7013-8000-000000024bbd", calculateGraph},
		{EdgeUsesGraph, "01a0c450-6c00-7021-8000-00000003fccf", calculatorPage},
		{EdgeUsesState, "01a0c450-6c00-7014-8000-000000026aac", calculateGraph},
		{EdgeUsesState, "01a0c450-6c00-7017-8000-00000002c779", calculatorPage},
		{EdgeUsesTranslation, "01a0c450-6c00-7006-8000-00000000b99a", resultPage},
		{EdgeUsesToken, "space.md", calculatorPage},
		{EdgeUsesAsset, "01a0c450-6c00-700d-8000-000000019223", pluginFile},
	} {
		if !has(want.kind, want.to, want.file) {
			t.Errorf("no %s edge to %s from %s", want.kind, want.to, want.file)
		}
	}
	if uses := res.Graph.UsesOf("space.md"); len(uses) != 2 {
		t.Errorf("space.md is used %d times", len(uses))
	}
	if !slices.IsSortedFunc(res.Graph.Edges, func(a, b Edge) int { return strings.Compare(a.From+string(a.Kind)+a.To, b.From+string(b.Kind)+b.To) }) {
		t.Error("edges are not sorted")
	}
}

// writeShuffled writes a JSON value with keys in a random order and
// random whitespace.
func writeShuffled(b *bytes.Buffer, v any, r *rand.Rand) {
	space := func() {
		b.WriteString([]string{"", " ", "\n", "\t  "}[r.IntN(4)])
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			space()
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteString(":")
			space()
			writeShuffled(b, x[k], r)
		}
		space()
		b.WriteString("}")
	case []any:
		b.WriteString("[")
		for i, it := range x {
			if i > 0 {
				b.WriteString(",")
			}
			space()
			writeShuffled(b, it, r)
		}
		b.WriteString("]")
	default:
		data, _ := json.Marshal(x)
		b.Write(data)
	}
}

// Verifies: CMP-002, QA-002.
// Key order and whitespace of the documents never change the bundles.
func TestDeterminismProperty(t *testing.T) {
	t.Parallel()
	base := compileFS(fixture(t))
	want := append([]*Bundle{base.App}, base.Plugins...)
	rapid.Check(t, func(rt *rapid.T) {
		seed := rapid.Uint64().Draw(rt, "seed")
		r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: reproducible shuffles.
		m := fixture(t)
		for name, f := range m {
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			dec := json.NewDecoder(bytes.NewReader(f.Data))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				rt.Fatal(err)
			}
			var buf bytes.Buffer
			writeShuffled(&buf, v, r)
			m[name] = &fstest.MapFile{Data: buf.Bytes()}
		}
		got := compileFS(m)
		if len(got.Diagnostics) > 0 {
			rt.Fatalf("diagnostics:\n%s", list(got.Diagnostics))
		}
		for i, b := range append([]*Bundle{got.App}, got.Plugins...) {
			if !bytes.Equal(b.Data, want[i].Data) {
				rt.Fatalf("bundle %s differs after shuffling", b.Key)
			}
		}
	})
}

// Verifies: CMP-002.
func TestRepeatedCompilationsAreIdentical(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	first := compileFS(m)
	for range 3 {
		again := compileFS(m)
		if !bytes.Equal(again.Plugins[0].Data, first.Plugins[0].Data) || !bytes.Equal(again.App.Data, first.App.Data) {
			t.Fatal("two compilations differ")
		}
	}
}

// Verifies: CMP-052.
func TestCompileNeverPanics(t *testing.T) {
	t.Parallel()
	for name, fsys := range map[string]fstest.MapFS{
		"empty":      {},
		"no plugins": {"app.json": fixture(t)["app.json"]},
		"broken app": {"app.json": &fstest.MapFile{Data: []byte("{")}},
		"garbage page": func() fstest.MapFS {
			m := fixture(t)
			m[calculatorPage] = &fstest.MapFile{Data: []byte(`{"kind":"page"}`)}
			return m
		}(),
		"limits unset": fixture(t),
	} {
		opts := DefaultOptions()
		if name == "limits unset" {
			opts.Limits = limits.Set{}
		}
		res := Compile(fsys, opts)
		if res.App != nil && name != "no plugins" {
			t.Errorf("%s: a bundle was produced", name)
		}
		if name == "limits unset" && !slices.ContainsFunc(res.Diagnostics, func(d plxerr.Diagnostic) bool { return d.Code == plxerr.InternalCompilerError }) {
			t.Errorf("%s: no internal error: %v", name, res.Diagnostics)
		}
	}
}

// ExampleCompile shows the library use of the CLI and the server.
func ExampleCompile() {
	res := Compile(os.DirFS(fixtureDir), DefaultOptions())
	fmt.Println(len(res.Diagnostics), res.App.Key, res.Plugins[0].Key, res.Plugins[0].Features)
	// Output: 0 demo loans [pxl.v1]
}
