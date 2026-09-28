// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repoRoot is the repository root relative to this package.
var repoRoot = filepath.Join("..", "..", "..")

// write creates a file under root, creating parent directories.
func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestProjectSecurityPolicyDefinesDisclosureProcess checks the real
// SECURITY.md.
// Verifies: SEC-192.
func TestProjectSecurityPolicyDefinesDisclosureProcess_SEC_192(t *testing.T) {
	t.Parallel()

	if err := CheckSecurityPolicy(repoRoot); err != nil {
		t.Fatal(err)
	}
}

// TestCheckSecurityPolicyRejectsIncompletePolicy checks that a policy without
// response targets fails.
func TestCheckSecurityPolicyRejectsIncompletePolicy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "SECURITY.md", "Report at security/advisories/new.\n")

	err := CheckSecurityPolicy(root)

	if err == nil || !strings.Contains(err.Error(), "## Response targets") {
		t.Fatalf("CheckSecurityPolicy() = %v, want missing response targets", err)
	}
}

// TestProjectDependabotCoversEveryManifest checks that every Go module, the
// Dart and Bun workspaces and the workflows receive grouped, cooled-down
// updates with Conventional Commits messages.
// Verifies: CI-007.
func TestProjectDependabotCoversEveryManifest_CI_007(t *testing.T) {
	t.Parallel()

	if err := CheckDependabotCoverage(repoRoot); err != nil {
		t.Fatal(err)
	}
}

// TestFindManifestsSkipsWorkspaceMembers checks manifest discovery.
func TestFindManifestsSkipsWorkspaceMembers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "backend/go.mod", "module x\n")
	write(t, root, "pubspec.yaml", "# Members declare resolution: workspace.\nname: ws\nworkspace:\n  - packages/p\n")
	write(t, root, "packages/p/pubspec.yaml", "name: p\nresolution: workspace\n")
	write(t, root, "studio/package.json", `{"workspaces": ["packages/*"]}`)
	write(t, root, "studio/packages/b/package.json", `{"name": "b"}`)
	write(t, root, "studio/node_modules/x/package.json", `{"workspaces": []}`)
	write(t, root, ".github/workflows/ci.yml", "on: push\n")
	write(t, root, "backend/Dockerfile", "FROM scratch\n")
	write(t, root, "deploy/compose/compose.yaml", "services: {}\n")
	write(t, root, "deploy/compose/compose.dev.yaml", "services: {}\n")

	got, err := FindManifests(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []Manifest{
		{Ecosystem: "github-actions", Directory: "/"},
		{Ecosystem: "pub", Directory: "/"},
		{Ecosystem: "docker", Directory: "/backend"},
		{Ecosystem: "gomod", Directory: "/backend"},
		{Ecosystem: "docker-compose", Directory: "/deploy/compose"},
		{Ecosystem: "bun", Directory: "/studio"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindManifests() = %+v, want %+v", got, want)
	}
}

// TestCheckDependabotCoverageReportsGaps checks that a missing entry and an
// entry without grouping are both reported.
func TestCheckDependabotCoverageReportsGaps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "backend/go.mod", "module x\n")
	write(t, root, "tools/go.mod", "module y\n")
	write(t, root, ".github/dependabot.yml", strings.Join([]string{
		"version: 2",
		"updates:",
		"  - package-ecosystem: gomod",
		"    directories:",
		`      - "/backend"`,
		"    commit-message:",
		"      prefix: chore",
	}, "\n"))

	err := CheckDependabotCoverage(root)

	if err == nil {
		t.Fatal("CheckDependabotCoverage() = nil, want errors")
	}
	for _, want := range []string{"gomod in /backend must set groups", "gomod in /tools has no Dependabot entry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}
