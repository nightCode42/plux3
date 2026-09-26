// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileCoverage is the measured coverage of one source file.
type FileCoverage struct {
	// Path is the repository-relative path with forward slashes.
	Path string
	// Total is the number of measurable units: statements for Go, lines for LCOV.
	Total int
	// Covered is the number of units executed at least once.
	Covered int
}

// modulePattern extracts the module path from a go.mod file.
var modulePattern = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

// ReadGoProfile reads a Go cover profile. The module is found in the go.mod
// next to the profile, and file paths are rewritten relative to repoRoot.
func ReadGoProfile(profilePath, repoRoot string) ([]FileCoverage, error) {
	moduleDir := filepath.Dir(profilePath)
	gomod, err := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("coverage.ReadGoProfile: %w", err)
	}
	m := modulePattern.FindSubmatch(gomod)
	if m == nil {
		return nil, fmt.Errorf("coverage.ReadGoProfile: no module line in %s", filepath.Join(moduleDir, "go.mod"))
	}
	prefix, err := relSlash(repoRoot, moduleDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("coverage.ReadGoProfile: %w", err)
	}
	return parseGoProfile(bytes.NewReader(data), string(m[1]), prefix)
}

// parseGoProfile parses profile lines of the form
// "module/pkg/file.go:10.2,12.3 2 1". A block seen several times (one per
// test binary) counts once and is covered if any run executed it.
func parseGoProfile(r io.Reader, module, prefix string) ([]FileCoverage, error) {
	type block struct{ stmts, count int }
	blocks := map[string]map[string]block{}
	var order []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") || strings.TrimSpace(line) == "" {
			continue
		}
		file, rng, stmts, count, err := splitGoLine(line)
		if err != nil {
			return nil, err
		}
		rel := path.Join(prefix, strings.TrimPrefix(strings.TrimPrefix(file, module), "/"))
		if blocks[rel] == nil {
			blocks[rel] = map[string]block{}
			order = append(order, rel)
		}
		b := blocks[rel][rng]
		b.stmts = stmts
		b.count += count
		blocks[rel][rng] = b
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage.parseGoProfile: %w", err)
	}
	out := make([]FileCoverage, 0, len(order))
	for _, rel := range order {
		fc := FileCoverage{Path: rel}
		for _, b := range blocks[rel] {
			fc.Total += b.stmts
			if b.count > 0 {
				fc.Covered += b.stmts
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

// splitGoLine splits one profile line into file, block range, statement
// count and execution count.
func splitGoLine(line string) (file, rng string, stmts, count int, err error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", "", 0, 0, fmt.Errorf("coverage: malformed profile line %q", line)
	}
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return "", "", 0, 0, fmt.Errorf("coverage: malformed profile line %q", line)
	}
	stmts, err1 := strconv.Atoi(fields[1])
	count, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil {
		return "", "", 0, 0, fmt.Errorf("coverage: malformed counts in %q", line)
	}
	return fields[0][:colon], fields[0][colon+1:], stmts, count, nil
}

// ReadLCOV reads an LCOV file. Relative source paths are resolved against the
// directory that contains the "coverage" directory holding the file, which is
// where `flutter test` and `bun test` run; paths are then made relative to
// repoRoot.
func ReadLCOV(lcovPath, repoRoot string) ([]FileCoverage, error) {
	base := filepath.Dir(filepath.Dir(lcovPath))
	data, err := os.ReadFile(lcovPath)
	if err != nil {
		return nil, fmt.Errorf("coverage.ReadLCOV: %w", err)
	}
	var out []FileCoverage
	var cur *FileCoverage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		key, value, _ := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		switch key {
		case "SF":
			src := value
			if !filepath.IsAbs(src) {
				src = filepath.Join(base, src)
			}
			rel, err := relSlash(repoRoot, src)
			if err != nil {
				return nil, err
			}
			out = append(out, FileCoverage{Path: rel})
			cur = &out[len(out)-1]
		case "LF":
			if cur != nil {
				cur.Total, _ = strconv.Atoi(value)
			}
		case "LH":
			if cur != nil {
				cur.Covered, _ = strconv.Atoi(value)
			}
		case "end_of_record":
			cur = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage.ReadLCOV: %w", err)
	}
	return out, nil
}

// relSlash returns target relative to root with forward slashes.
func relSlash(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("coverage: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("coverage: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", fmt.Errorf("coverage: %w", err)
	}
	return filepath.ToSlash(rel), nil
}
