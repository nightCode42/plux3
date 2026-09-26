// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CheckSecurityPolicy verifies that SECURITY.md routes reports to private
// vulnerability reporting and states response targets (SEC-192).
func CheckSecurityPolicy(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "SECURITY.md"))
	if err != nil {
		return fmt.Errorf("policy.CheckSecurityPolicy: %w", err)
	}
	text := string(data)
	var missing []string
	for _, want := range []string{"security/advisories/new", "## Response targets", "Acknowledge", "disclosure"} {
		if !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("policy.CheckSecurityPolicy: SECURITY.md lacks %q", missing)
	}
	return nil
}

// Update is one Dependabot update entry.
type Update struct {
	// Ecosystem is the package-ecosystem value, e.g. "gomod".
	Ecosystem string
	// Directories are the manifest directories, e.g. "/backend".
	Directories []string
	// Grouped, Cooled and Prefixed record whether the entry sets groups,
	// a cooldown and a commit-message prefix.
	Grouped, Cooled, Prefixed bool
}

var (
	dirPattern             = regexp.MustCompile(`"(/[^"]*)"`)
	workspaceMemberPattern = regexp.MustCompile(`(?m)^resolution:\s*workspace\s*$`)
)

// ParseDependabot reads the update entries of a dependabot.yml. It
// understands the subset of YAML the repository uses: one entry per
// "- package-ecosystem:" line, with quoted directory values.
func ParseDependabot(text string) []Update {
	var updates []Update
	var cur *Update
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if eco, ok := strings.CutPrefix(line, "- package-ecosystem:"); ok {
			updates = append(updates, Update{Ecosystem: strings.Trim(strings.TrimSpace(eco), `"`)})
			cur = &updates[len(updates)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "directory:"), strings.HasPrefix(line, "directories:"), isDirectoryItem(line):
			for _, m := range dirPattern.FindAllStringSubmatch(line, -1) {
				cur.Directories = append(cur.Directories, m[1])
			}
		case strings.HasPrefix(line, "groups:"):
			cur.Grouped = true
		case strings.HasPrefix(line, "cooldown:"):
			cur.Cooled = true
		case strings.HasPrefix(line, "prefix:"):
			cur.Prefixed = true
		}
	}
	return updates
}

// isDirectoryItem reports whether line is an item of a block-style
// directories list.
func isDirectoryItem(line string) bool {
	return strings.HasPrefix(line, `- "/`)
}

// Manifest is a directory that needs dependency updates.
type Manifest struct {
	// Ecosystem is the Dependabot ecosystem that covers it.
	Ecosystem string
	// Directory is the repository-relative directory with a leading slash.
	Directory string
}

// skipDirs are never searched for manifests.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".dart_tool": true, "build": true, "testdata": true}

// FindManifests lists every directory in root that Dependabot must cover:
// Go modules, the Dart workspace root, the Bun workspace root, and GitHub
// Actions workflows.
func FindManifests(root string) ([]Manifest, error) {
	var out []Manifest
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return fmt.Errorf("policy.FindManifests: %w", err)
		}
		dir := path.Clean("/" + filepath.ToSlash(rel))
		eco, err := manifestEcosystem(p, d.Name())
		if eco != "" {
			out = append(out, Manifest{Ecosystem: eco, Directory: dir})
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("policy.FindManifests: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "workflows")); err == nil {
		out = append(out, Manifest{Ecosystem: "github-actions", Directory: "/"})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Directory != out[j].Directory {
			return out[i].Directory < out[j].Directory
		}
		return out[i].Ecosystem < out[j].Ecosystem
	})
	return out, nil
}

// manifestEcosystem classifies a file as the root manifest of an ecosystem.
// Workspace members are covered by their workspace root and return "".
func manifestEcosystem(p, name string) (string, error) {
	switch name {
	case "go.mod":
		return "gomod", nil
	case "pubspec.yaml", "package.json":
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("policy.manifestEcosystem: %w", err)
		}
		if name == "pubspec.yaml" && !workspaceMemberPattern.Match(data) {
			return "pub", nil
		}
		if name == "package.json" {
			var pkg struct {
				Workspaces []string `json:"workspaces"`
			}
			if err := json.Unmarshal(data, &pkg); err != nil {
				return "", fmt.Errorf("policy.manifestEcosystem %s: %w", p, err)
			}
			if pkg.Workspaces != nil {
				return "bun", nil
			}
		}
	}
	return "", nil
}

// CheckDependabotCoverage verifies that every manifest in root has a grouped
// Dependabot entry with a cooldown and a Conventional Commits prefix (CI-007).
func CheckDependabotCoverage(root string) error {
	data, err := os.ReadFile(filepath.Join(root, ".github", "dependabot.yml"))
	if err != nil {
		return fmt.Errorf("policy.CheckDependabotCoverage: %w", err)
	}
	updates := ParseDependabot(string(data))
	manifests, err := FindManifests(root)
	if err != nil {
		return err
	}
	var problems []error
	for _, m := range manifests {
		u, ok := findUpdate(updates, m)
		switch {
		case !ok:
			problems = append(problems, fmt.Errorf("%s in %s has no Dependabot entry", m.Ecosystem, m.Directory))
		case !u.Grouped || !u.Cooled || !u.Prefixed:
			problems = append(problems, fmt.Errorf("%s in %s must set groups, cooldown and commit-message prefix", m.Ecosystem, m.Directory))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("policy.CheckDependabotCoverage: %w", err)
	}
	return nil
}

// findUpdate returns the entry covering a manifest.
func findUpdate(updates []Update, m Manifest) (Update, bool) {
	for _, u := range updates {
		if u.Ecosystem != m.Ecosystem {
			continue
		}
		for _, d := range u.Directories {
			if d == m.Directory {
				return u, true
			}
		}
	}
	return Update{}, false
}
