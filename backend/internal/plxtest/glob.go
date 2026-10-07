// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// DefaultTests is where scenarios live when plux.yaml does not say.
const DefaultTests = "tests/**/*.scenario.{yaml,json}"

// Match reports whether a slash-separated path matches a glob: `*` and
// `?` match within one path segment, `**` matches any number of whole
// segments (also none), and `{a,b}` matches either alternative.
func Match(pattern, name string) (bool, error) {
	alts, err := expandBraces(pattern)
	if err != nil {
		return false, err
	}
	segs := strings.Split(name, "/")
	for _, alt := range alts {
		ok, err := matchSegments(strings.Split(alt, "/"), segs)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func matchSegments(pat, name []string) (bool, error) {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				ok, err := matchSegments(pat[1:], name[i:])
				if err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(name) == 0 {
			return false, nil
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil {
			return false, fmt.Errorf("pattern segment %q: %w", pat[0], err)
		}
		if !ok {
			return false, nil
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0, nil
}

// expandBraces expands the first `{a,b}` group recursively, so a pattern
// holds no braces afterwards. Groups do not nest.
func expandBraces(pattern string) ([]string, error) {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		return []string{pattern}, nil
	}
	end := strings.IndexByte(pattern[open:], '}')
	if end < 0 {
		return nil, fmt.Errorf("pattern %q: unclosed {", pattern)
	}
	end += open
	var out []string
	for _, alt := range strings.Split(pattern[open+1:end], ",") {
		rest, err := expandBraces(pattern[:open] + alt + pattern[end+1:])
		if err != nil {
			return nil, err
		}
		out = append(out, rest...)
	}
	return out, nil
}

// Find lists the files of fsys that match the glob, sorted by path;
// directories whose name starts with a dot are not entered.
func Find(fsys fs.FS, pattern string) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		ok, err := Match(pattern, p)
		if err != nil {
			return err
		}
		if ok {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find scenario files: %w", err)
	}
	slices.Sort(out)
	return out, nil
}
