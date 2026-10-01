// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"fmt"
	"path"
	"strings"
)

// Glob is a path pattern anchored at the repository root. Segments are
// separated by "/"; a "**" segment matches zero or more segments; any
// other segment is a path.Match pattern of one segment.
type Glob struct {
	pattern  string
	segments []string
}

// ParseGlob checks pattern and returns it as a Glob.
func ParseGlob(pattern string) (Glob, error) {
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") {
		return Glob{}, fmt.Errorf("affected: glob %q: must be a non-empty relative path", pattern)
	}
	segments := strings.Split(pattern, "/")
	for _, s := range segments {
		if s == "" || s == "." || s == ".." {
			return Glob{}, fmt.Errorf("affected: glob %q: empty, . or .. segment", pattern)
		}
		if s != "**" && strings.Contains(s, "**") {
			return Glob{}, fmt.Errorf("affected: glob %q: ** must be a whole segment", pattern)
		}
		if _, err := path.Match(s, ""); err != nil {
			return Glob{}, fmt.Errorf("affected: glob %q: %w", pattern, err)
		}
	}
	return Glob{pattern: pattern, segments: segments}, nil
}

// String returns the pattern.
func (g Glob) String() string { return g.pattern }

// Match reports whether the slash-separated relative path p matches.
func (g Glob) Match(p string) bool {
	return matchSegments(g.segments, strings.Split(p, "/"))
}

func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			rest := pattern[1:]
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		// The pattern was validated by ParseGlob, so Match cannot fail.
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// PathSet is an ordered list of including and excluding globs: a path is
// in the set when the last glob that matches it includes it.
type PathSet struct {
	globs   []Glob
	exclude []bool
}

// ParsePathSet parses globs; one starting with "!" excludes.
func ParsePathSet(globs []string) (PathSet, error) {
	var s PathSet
	for _, raw := range globs {
		pattern, exclude := strings.CutPrefix(raw, "!")
		g, err := ParseGlob(pattern)
		if err != nil {
			return PathSet{}, err
		}
		if exclude && len(s.globs) == 0 {
			return PathSet{}, fmt.Errorf("affected: glob %q excludes before anything is included", raw)
		}
		s.globs = append(s.globs, g)
		s.exclude = append(s.exclude, exclude)
	}
	return s, nil
}

// Contains reports whether p is in the set.
func (s PathSet) Contains(p string) bool {
	in := false
	for i, g := range s.globs {
		if g.Match(p) {
			in = !s.exclude[i]
		}
	}
	return in
}

// Empty reports whether the set has no globs.
func (s PathSet) Empty() bool { return len(s.globs) == 0 }
