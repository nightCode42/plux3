// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// dartPackage is one package of the pub workspace.
type dartPackage struct {
	name    string
	dir     string   // slash-separated, relative to the repository root
	deps    []string // names under dependencies
	devDeps []string // names under dev_dependencies
}

// dartGraph is the dependency graph of the pub workspace's packages.
type dartGraph struct {
	root    string
	members map[string]*dartPackage // by name
	byDir   map[string]*dartPackage
}

// loadDartGraph reads the workspace the root pubspec.yaml declares.
func loadDartGraph(root string) (*dartGraph, error) {
	data, err := os.ReadFile(filepath.Join(root, "pubspec.yaml")) //nolint:gosec // G304: the workspace's pubspec.
	if err != nil {
		return nil, fmt.Errorf("affected: %w", err)
	}
	g := &dartGraph{root: root, members: map[string]*dartPackage{}, byDir: map[string]*dartPackage{}}
	for _, dir := range yamlList(string(data), "workspace") {
		pkg, err := readPubspec(root, path.Clean(dir))
		if err != nil {
			return nil, err
		}
		g.members[pkg.name] = pkg
		g.byDir[pkg.dir] = pkg
	}
	return g, nil
}

func readPubspec(root, dir string) (*dartPackage, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "pubspec.yaml")) //nolint:gosec // G304: a pubspec of the repository.
	if err != nil {
		return nil, fmt.Errorf("affected: %w", err)
	}
	text := string(data)
	pkg := &dartPackage{dir: dir, deps: yamlKeys(text, "dependencies"), devDeps: yamlKeys(text, "dev_dependencies")}
	for line := range strings.Lines(text) {
		if name, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "name:"); ok {
			pkg.name = strings.Trim(strings.TrimSpace(name), `"'`)
		}
	}
	if pkg.name == "" {
		return nil, fmt.Errorf("affected: %s/pubspec.yaml has no name", dir)
	}
	return pkg, nil
}

// yamlSection returns the lines of a top-level YAML key's block: the
// lines after "key:" up to the next line that starts in column 0.
func yamlSection(text, key string) []string {
	var lines []string
	in := false
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" && line[0] != ' ' && line[0] != '#' {
			in = strings.TrimSpace(strings.SplitN(line, "#", 2)[0]) == key+":"
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	return lines
}

// yamlList returns the items of a top-level block sequence.
func yamlList(text, key string) []string {
	var items []string
	for _, line := range yamlSection(text, key) {
		if item, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok {
			items = append(items, strings.Trim(strings.TrimSpace(item), `"'`))
		}
	}
	return items
}

// yamlKeys returns the keys indented by two spaces in a top-level block
// mapping.
func yamlKeys(text, key string) []string {
	var keys []string
	for _, line := range yamlSection(text, key) {
		if len(line) < 3 || line[:2] != "  " || line[2] == ' ' || line[2] == '#' {
			continue
		}
		if k, _, ok := strings.Cut(line[2:], ":"); ok {
			keys = append(keys, strings.TrimSpace(k))
		}
	}
	return keys
}

// sources returns what a change must touch to affect the package in dir:
// all of the package, the workspace packages it depends on (directly,
// through its dev dependencies, or transitively through their
// dependencies) without their tests, examples and Markdown, and the
// workspace's pubspec and lockfile. A package outside the workspace
// stands alone.
func (g *dartGraph) sources(dir string) ([]source, error) {
	pkg := g.byDir[dir]
	if pkg == nil {
		alone, err := readPubspec(g.root, dir)
		if err != nil {
			return nil, err
		}
		return []source{dartSource{dir: alone.dir, root: true}}, nil
	}
	seen := map[string]bool{pkg.name: true}
	queue := slices.Concat(pkg.deps, pkg.devDeps)
	var deps []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		dep := g.members[name]
		if dep == nil || seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dep.dir)
		queue = append(queue, dep.deps...)
	}
	slices.Sort(deps)
	srcs := []source{dartSource{dir: pkg.dir, root: true}}
	for _, d := range deps {
		srcs = append(srcs, dartSource{dir: d})
	}
	return append(srcs, fileSource{"pubspec.yaml", "pubspec.lock"}), nil
}

// dartSource is a Dart package a job builds: all of it when it is the
// job's own package, otherwise what its dependants compile.
type dartSource struct {
	dir  string
	root bool
}

func (s dartSource) contains(p string) bool {
	rest, ok := strings.CutPrefix(p, s.dir+"/")
	if !ok {
		return false
	}
	if s.root {
		return true
	}
	for _, skip := range []string{"test/", "example/", "coverage/", "integration_test/"} {
		if strings.HasPrefix(rest, skip) {
			return false
		}
	}
	return !strings.HasSuffix(rest, ".md")
}

func (s dartSource) String() string {
	if s.root {
		return "the Dart package " + s.dir
	}
	return "the Dart package " + s.dir + ", a dependency"
}
