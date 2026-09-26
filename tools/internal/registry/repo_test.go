// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import "testing"

// TestRepositoryRegistry loads the committed registry: every descriptor,
// value type, enum and action is consistent and every Flutter parameter of
// the pinned SDK is covered (WGT-001, WGT-003, BND-011).
func TestRepositoryRegistry(t *testing.T) {
	r, err := Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Widgets) == 0 || len(r.Coverage) == 0 {
		t.Fatalf("empty registry: %d widgets, %d covered classes", len(r.Widgets), len(r.Coverage))
	}
}
