// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// bundleLimits lists the limit entries of a meta section by key.
func bundleLimits(m *fbs.Meta) map[string]int64 {
	out := map[string]int64{}
	var l fbs.Limit
	for i := range m.LimitsLength() {
		m.Limits(&l, i)
		out[string(l.Key())] = l.Value()
	}
	return out
}

// Verifies: LIM-001.
func TestBundleLimitsCarryOnlyOverrides_LIM_001(t *testing.T) {
	t.Parallel()
	res := compileFS(fixture(t))
	clean(t, res)
	for _, b := range readAll(t, res) {
		if got := bundleLimits(metaOf(t, b)); len(got) != 0 {
			t.Errorf("a bundle without overrides carries limits: %v", got)
		}
	}

	opts := DefaultOptions()
	set, err := opts.Limits.Tighten(limits.ActionForEachItems, limits.ScopeApp, 50)
	if err != nil {
		t.Fatal(err)
	}
	opts.Limits = set
	res = Compile(fixture(t), opts)
	clean(t, res)
	for _, b := range readAll(t, res) {
		got := bundleLimits(metaOf(t, b))
		if len(got) != 1 || got[string(limits.ActionForEachItems)] != 50 {
			t.Errorf("limits = %v, want only the override", got)
		}
	}
}
