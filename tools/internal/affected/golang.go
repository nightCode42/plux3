// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"bufio"
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// goModule is one module of the Go workspace.
type goModule struct {
	path string // the module path of its go.mod
	dir  string // slash-separated, relative to the repository root
}

// goPackage is one directory of Go files in the repository.
type goPackage struct {
	dir         string   // slash-separated, relative to the repository root
	module      goModule // the module the directory belongs to
	imports     []string // import paths of the non-test files
	testImports []string // import paths of the _test.go files
	embeds      []string // //go:embed patterns of the non-test files
	testEmbeds  []string // //go:embed patterns of the _test.go files
}

// goGraph is the import graph of the repository's Go packages.
type goGraph struct {
	modules  []goModule
	packages map[string]*goPackage // by directory
}

// loadGoGraph reads every module that go.work uses under root.
func loadGoGraph(root string) (*goGraph, error) {
	work, err := os.ReadFile(filepath.Join(root, "go.work")) //nolint:gosec // G304: the repository's go.work.
	if err != nil {
		return nil, fmt.Errorf("affected: %w", err)
	}
	g := &goGraph{packages: map[string]*goPackage{}}
	for _, dir := range workUses(string(work)) {
		mod, err := readModule(root, dir)
		if err != nil {
			return nil, err
		}
		g.modules = append(g.modules, mod)
	}
	for _, mod := range g.modules {
		if err := g.walk(root, mod); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// workUses returns the directories of a go.work's use directives.
func workUses(work string) []string {
	var dirs []string
	inBlock := false
	for line := range strings.Lines(work) {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		switch {
		case line == "use (":
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			dirs = append(dirs, path.Clean(line))
		case strings.HasPrefix(line, "use ") && !strings.HasSuffix(line, "("):
			dirs = append(dirs, path.Clean(strings.TrimSpace(strings.TrimPrefix(line, "use "))))
		}
	}
	return dirs
}

func readModule(root, dir string) (goModule, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "go.mod")) //nolint:gosec // G304: a go.mod go.work names.
	if err != nil {
		return goModule{}, fmt.Errorf("affected: %w", err)
	}
	for line := range strings.Lines(string(data)) {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return goModule{path: strings.Trim(strings.TrimSpace(p), `"`), dir: dir}, nil
		}
	}
	return goModule{}, fmt.Errorf("affected: %s/go.mod has no module line", dir)
}

// walk records every directory of Go files in mod, skipping what the go
// command skips: testdata, vendor, names starting with "." or "_", and
// nested modules.
func (g *goGraph) walk(root string, mod goModule) error {
	base := filepath.Join(root, filepath.FromSlash(mod.dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != base && skipDir(p, d.Name()):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), ".go"):
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return fmt.Errorf("affected: %w", err)
		}
		return g.addFile(p, filepath.ToSlash(rel), mod)
	})
	if err != nil {
		return fmt.Errorf("affected: %w", err)
	}
	return nil
}

// skipDir reports whether the go command ignores the directory at p.
func skipDir(p, name string) bool {
	if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, err := os.Stat(filepath.Join(p, "go.mod"))
	return err == nil
}

// addFile records the imports and embed patterns of one Go file.
func (g *goGraph) addFile(file, dir string, mod goModule) error {
	src, err := os.ReadFile(file) //nolint:gosec // G304: a Go file of the repository.
	if err != nil {
		return fmt.Errorf("affected: %w", err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("affected: %w", err)
	}
	pkg := g.packages[dir]
	if pkg == nil {
		pkg = &goPackage{dir: dir, module: mod}
		g.packages[dir] = pkg
	}
	var imports []string
	for _, spec := range f.Imports {
		ip, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return fmt.Errorf("affected: %s: %w", file, err)
		}
		imports = append(imports, ip)
	}
	embeds := embedPatterns(src)
	if strings.HasSuffix(file, "_test.go") {
		pkg.testImports = append(pkg.testImports, imports...)
		pkg.testEmbeds = append(pkg.testEmbeds, embeds...)
	} else {
		pkg.imports = append(pkg.imports, imports...)
		pkg.embeds = append(pkg.embeds, embeds...)
	}
	return nil
}

// embedPatterns returns the patterns of a file's //go:embed directives,
// without an "all:" prefix.
func embedPatterns(src []byte) []string {
	var patterns []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<24)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "//go:embed ")
		if !ok {
			continue
		}
		for _, field := range embedFields(rest) {
			patterns = append(patterns, strings.TrimPrefix(field, "all:"))
		}
	}
	return patterns
}

// embedFields splits a //go:embed argument list: space-separated
// patterns, each optionally a Go string literal.
func embedFields(s string) []string {
	var fields []string
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if s[0] == '"' || s[0] == '`' {
			end := strings.IndexByte(s[1:], s[0])
			if end < 0 {
				return append(fields, s)
			}
			lit := s[:end+2]
			if v, err := strconv.Unquote(lit); err == nil {
				fields = append(fields, v)
			}
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t")
		if end < 0 {
			return append(fields, s)
		}
		fields = append(fields, s[:end])
		s = s[end:]
	}
	return fields
}

// resolve maps an import path to the directory of an in-repository
// package, or "" for any other import.
func (g *goGraph) resolve(importPath string) string {
	for _, mod := range g.modules {
		rest, ok := strings.CutPrefix(importPath, mod.path)
		if !ok || (rest != "" && rest[0] != '/') {
			continue
		}
		dir := path.Join(mod.dir, strings.TrimPrefix(rest, "/"))
		if _, ok := g.packages[dir]; ok {
			return dir
		}
	}
	return ""
}

// match returns the package directories a pattern names: a directory,
// or "dir/..." for it and every package below it.
func (g *goGraph) match(pattern string) ([]string, error) {
	var dirs []string
	if base, ok := strings.CutSuffix(pattern, "/..."); ok {
		for dir := range g.packages {
			if dir == base || strings.HasPrefix(dir, base+"/") {
				dirs = append(dirs, dir)
			}
		}
	} else if _, ok := g.packages[pattern]; ok {
		dirs = append(dirs, pattern)
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("affected: Go pattern %q names no package", pattern)
	}
	slices.Sort(dirs)
	return dirs, nil
}

// sources returns what a change must touch to affect the packages the
// patterns name: with tests, their tests are built too, as by
// `go list -deps -test`.
func (g *goGraph) sources(patterns []string, tests bool) ([]source, error) {
	targets := map[string]bool{}
	for _, p := range patterns {
		dirs, err := g.match(p)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			targets[d] = true
		}
	}
	modules := map[string]bool{}
	var srcs []source
	for _, dir := range g.closure(targets, tests) {
		pkg := g.packages[dir]
		modules[pkg.module.dir] = true
		s := goDirSource{dir: dir, tests: tests && targets[dir], embeds: pkg.embeds}
		if s.tests {
			s.embeds = append(slices.Clone(pkg.embeds), pkg.testEmbeds...)
		}
		srcs = append(srcs, s)
	}
	files := []string{"go.work", "go.work.sum"}
	for _, mod := range slices.Sorted(maps.Keys(modules)) {
		files = append(files, path.Join(mod, "go.mod"), path.Join(mod, "go.sum"))
	}
	return append(srcs, fileSource(files)), nil
}

// closure returns, sorted, the targets and every in-repository package
// they import, directly or not; with tests, the targets' tests' imports
// count too.
func (g *goGraph) closure(targets map[string]bool, tests bool) []string {
	seen := map[string]bool{}
	var queue []string
	visit := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			queue = append(queue, dir)
		}
	}
	for dir := range targets {
		visit(dir)
		if tests {
			for _, ip := range g.packages[dir].testImports {
				visit(g.resolve(ip))
			}
		}
	}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, ip := range g.packages[dir].imports {
			visit(g.resolve(ip))
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// goDirSource is a package directory a job depends on.
type goDirSource struct {
	dir    string
	tests  bool     // the job builds this package's tests
	embeds []string // the //go:embed patterns of what the job builds
}

func (s goDirSource) contains(p string) bool {
	rest, ok := strings.CutPrefix(p, s.dir+"/")
	if !ok {
		return false
	}
	if !strings.Contains(rest, "/") && (s.tests || !strings.HasSuffix(rest, "_test.go")) {
		return true
	}
	if s.tests && strings.HasPrefix(rest, "testdata/") {
		return true
	}
	for _, pattern := range s.embeds {
		if embedMatch(pattern, rest) {
			return true
		}
	}
	return false
}

func (s goDirSource) String() string {
	if s.tests {
		return "the Go package " + s.dir + " and its tests"
	}
	return "the Go package " + s.dir
}

// embedMatch reports whether rest, relative to the package directory, is
// a file a //go:embed pattern names: the pattern's segments match its
// leading segments (a matched directory embeds everything in it).
func embedMatch(pattern, rest string) bool {
	ps := strings.Split(pattern, "/")
	rs := strings.Split(rest, "/")
	if len(rs) < len(ps) {
		return false
	}
	for i, p := range ps {
		if ok, err := path.Match(p, rs[i]); err != nil || !ok {
			return false
		}
	}
	return true
}
