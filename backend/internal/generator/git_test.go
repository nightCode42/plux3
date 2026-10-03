// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

import "testing"

// TestCheckGitTarget checks that a remote or branch git would read as an
// option, or that is no valid name, is refused before git runs.
// Verifies: GEN-003.
func TestCheckGitTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		remote, branch string
		ok             bool
	}{
		{"https://github.com/acme/shop.git", "main", true},
		{"git@github.com:acme/shop.git", "release/1.0", true},
		{"../shop.git", "feature/app-v2", true},
		{"--receive-pack=touch /tmp/x", "main", false},
		{"-uhttps://example.com", "main", false},
		{"https://example.com/a b.git", "main", false},
		{"", "main", false},
		{"https://example.com/shop.git", "--force", false},
		{"https://example.com/shop.git", "", false},
		{"https://example.com/shop.git", "a..b", false},
		{"https://example.com/shop.git", "a b", false},
		{"https://example.com/shop.git", "a:b", false},
		{"https://example.com/shop.git", "a/.hidden", false},
		{"https://example.com/shop.git", "main.lock", false},
		{"https://example.com/shop.git", "a/", false},
		{"https://example.com/shop.git", "@", false},
		{"https://example.com/shop.git", "a@{1}", false},
	} {
		if err := CheckGitTarget(tc.remote, tc.branch); (err == nil) != tc.ok {
			t.Errorf("CheckGitTarget(%q, %q) = %v, want ok %v", tc.remote, tc.branch, err, tc.ok)
		}
	}
}
