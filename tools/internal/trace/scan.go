// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind classifies where evidence was found.
type Kind string

// Evidence kinds.
const (
	// KindTest is an automated test.
	KindTest Kind = "test"
	// KindCI is a CI job or Makefile target that enforces the requirement.
	KindCI Kind = "ci"
)

// Evidence is one place in the repository that verifies a requirement.
type Evidence struct {
	// ID is the requirement identifier, e.g. "SYN-005".
	ID string
	// Path is the file path relative to the scanned root, with forward slashes.
	Path string
	// Line is the 1-based line number.
	Line int
	// Kind says whether the evidence is a test or a CI check.
	Kind Kind
}

var (
	// Evidence must be a real comment or declaration at the start of a line,
	// so identifiers inside string literals (such as test fixtures) never count.
	verifiesPattern = regexp.MustCompile(`^\s*(?://+|#)\s*Verifies:\s*([A-Z][A-Z0-9]*-\d{3}(?:\s*,\s*[A-Z][A-Z0-9]*-\d{3})*)`)
	idPattern       = regexp.MustCompile(`[A-Z][A-Z0-9]*-\d{3}`)
	goNamePattern   = regexp.MustCompile(`^func (?:Test|Fuzz|Benchmark)\w*?_([A-Z][A-Z0-9]*)_(\d{3})\(`)
	bracketPattern  = regexp.MustCompile(`\[([A-Z][A-Z0-9]*-\d{3})\]`)
)

// skipDirs are directory names never scanned: VCS data, dependencies, build
// output and test fixtures. Coverage output is not skipped by name, because
// source packages may be called "coverage"; it holds no test files anyway.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".dart_tool": true, "build": true,
	"vendor": true, "testdata": true, ".idea": true,
}

// Scan walks root and returns all evidence, ordered by path and line.
func Scan(root string) ([]Evidence, error) {
	var all []Evidence
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("trace.Scan: %w", err)
		}
		rel = filepath.ToSlash(rel)
		kind, ok := classify(rel)
		if !ok {
			return nil
		}
		found, err := scanFile(path, rel, kind)
		all = append(all, found...)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("trace.Scan: %w", err)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	return all, nil
}

// classify reports whether a file can hold evidence, and of which kind.
func classify(rel string) (Kind, bool) {
	base := filepath.Base(rel)
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.dart"),
		strings.HasSuffix(base, ".test.ts"), strings.HasSuffix(base, ".test.tsx"):
		return KindTest, true
	case strings.HasPrefix(rel, ".github/workflows/") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")),
		rel == "Makefile":
		return KindCI, true
	}
	return "", false
}

// scanFile extracts the evidence in one file.
func scanFile(path, rel string, kind Kind) ([]Evidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("trace.scanFile: %w", err)
	}
	defer func() { _ = f.Close() }()

	var found []Evidence
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		for _, id := range idsInLine(scanner.Text(), rel, kind) {
			found = append(found, Evidence{ID: id, Path: rel, Line: line, Kind: kind})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("trace.scanFile %s: %w", rel, err)
	}
	return found, nil
}

// idsInLine returns the distinct identifiers a line declares as evidence.
func idsInLine(text, rel string, kind Kind) []string {
	var ids []string
	if m := verifiesPattern.FindStringSubmatch(text); m != nil {
		ids = append(ids, idPattern.FindAllString(m[1], -1)...)
	}
	if kind == KindTest {
		if m := goNamePattern.FindStringSubmatch(text); m != nil && strings.HasSuffix(rel, ".go") {
			ids = append(ids, m[1]+"-"+m[2])
		}
		if !strings.HasSuffix(rel, ".go") {
			for _, m := range bracketPattern.FindAllStringSubmatch(text, -1) {
				ids = append(ids, m[1])
			}
		}
	}
	return dedupe(ids)
}

// dedupe removes repeated identifiers while keeping their first order.
func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
