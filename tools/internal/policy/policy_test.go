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

// Every CI gate fails when its command fails, even when its output is
// piped into the step summary.
// Verifies: CI-001.
func TestProjectWorkflowsRunBashWithPipefail(t *testing.T) {
	t.Parallel()

	if err := CheckWorkflowShells(repoRoot); err != nil {
		t.Fatal(err)
	}
}

// TestCheckWorkflowShellsReportsGaps checks the failures.
func TestCheckWorkflowShellsReportsGaps(t *testing.T) {
	t.Parallel()
	if err := CheckWorkflowShells(t.TempDir()); err == nil {
		t.Error("no workflows passed")
	}
	root := t.TempDir()
	write(t, root, ".github/workflows/ok.yml", "on: push\ndefaults:\n  run:\n    shell: bash\njobs: {}\n")
	if err := CheckWorkflowShells(root); err != nil {
		t.Errorf("a good workflow: %v", err)
	}
	write(t, root, ".github/workflows/bare.yml", "on: push\njobs:\n  a:\n    steps:\n      - run: make check | tee out\n")
	write(t, root, ".github/workflows/sh.yaml", "on: push\ndefaults:\n  run:\n    shell: bash\njobs:\n  a:\n    steps:\n      - run: x\n        shell: sh\n")
	err := CheckWorkflowShells(root)
	if err == nil || !strings.Contains(err.Error(), "bare.yml has no top-level") || !strings.Contains(err.Error(), "sh.yaml sets a shell") {
		t.Errorf("CheckWorkflowShells = %v", err)
	}
}

// Dart packages are published by OIDC, and every release has notes with
// upgrade steps and a changelog entry.
// Verifies: CI-005, DX-006.
func TestProjectDartPackagesPublishByOIDCWithReleaseNotes(t *testing.T) {
	t.Parallel()

	if err := CheckDartPublishing(repoRoot); err != nil {
		t.Fatal(err)
	}
}

// TestCheckDartPublishingReportsGaps checks the failures.
func TestCheckDartPublishingReportsGaps(t *testing.T) {
	t.Parallel()
	good := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		write(t, root, ".github/workflows/release.yml", "on:\n  push:\n    tags:\n      - \"pkg/v*\"\njobs:\n"+
			"  release:\n    steps:\n      - run: |\n          make release-notes > n.md\n          grep -q '^### Upgrading' n.md\n"+
			"  pub:\n    permissions:\n      id-token: write\n    steps:\n      - run: dart pub publish --force\n"+
			"  other:\n    steps:\n      - run: echo ${{ secrets.X }}\n")
		write(t, root, "cliff.toml", "body = \"### Upgrading\"\n")
		write(t, root, "packages/pkg/pubspec.yaml", "name: pkg\nversion: 1.2.0\n")
		write(t, root, "packages/pkg/CHANGELOG.md", "# Changelog\n\n## 1.2.0\n\n- First.\n")
		write(t, root, "packages/pkg/README.md", "# pkg\n")
		write(t, root, "packages/pkg/example/main.dart", "void main() {}\n")
		write(t, root, "packages/private/pubspec.yaml", "name: private\npublish_to: none\n")
		return root
	}
	if err := CheckDartPublishing(good(t)); err != nil {
		t.Fatalf("a good repository: %v", err)
	}
	for _, c := range []struct{ name, file, content, want string }{
		{"stale changelog", "packages/pkg/CHANGELOG.md", "## 1.1.0\n", "no CHANGELOG.md entry for 1.2.0"},
		{"no upgrade notes", "cliff.toml", "body = \"\"\n", "no Upgrading section"},
		{"stored credential", ".github/workflows/release.yml", "on:\n  push:\n    tags:\n      - \"pkg/v*\"\njobs:\n" +
			"  release:\n    steps:\n      - run: make release-notes; grep -q '^### Upgrading' n.md\n" +
			"  pub:\n    permissions:\n      id-token: write\n    steps:\n      - run: echo ${{ secrets.PUB }} > credentials.json; dart pub publish --force\n", "stored credential"},
		{"no tag", ".github/workflows/release.yml", "jobs:\n  release:\n    steps:\n      - run: make release-notes '^### Upgrading'\n" +
			"  pub:\n    permissions:\n      id-token: write\n    steps:\n      - run: dart pub publish --force\n", "no tag pattern for pkg"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := good(t)
			write(t, root, c.file, c.content)
			if err := CheckDartPublishing(root); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("CheckDartPublishing = %v, want %q", err, c.want)
			}
		})
	}
}
