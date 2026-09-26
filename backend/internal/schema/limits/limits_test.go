// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package limits

import (
	"slices"
	"testing"
)

// TestRegistryHoldsTheSpecifiedDefaults_BND_010 pins the defaults the
// specification states (SCH-005, BND-010, PXL-001, ACT-005, §30.3).
// Verifies: BND-010, SCH-005, PXL-001, CMP-040.
func TestRegistryHoldsTheSpecifiedDefaults_BND_010(t *testing.T) {
	t.Parallel()
	const MiB, KiB = 1 << 20, 1 << 10
	want := map[Key][2]int64{ // default, warning
		ReleaseAppSize:         {100 * MiB, 0},
		BundlePluginSize:       {20 * MiB, 0},
		BundlePageSectionSize:  {1 * MiB, 0},
		AppPlugins:             {200, 0},
		PluginPages:            {500, 0},
		PageNodes:              {5000, 1000},
		PageDepth:              {64, 32},
		PageBuildCost:          {16000, 8000},
		PageImageBytes:         {5 * MiB, 1 * MiB},
		PageAnimations:         {30, 10},
		DocumentStringPropSize: {64 * KiB, 0},
		PXLOperationBudget:     {10000, 0},
		ActionStepsPerRun:      {10000, 0},
		ActionForEachItems:     {1000, 0},
		ActionStepTimeout:      {30000, 0},
		ActionRunTimeout:       {120000, 0},
	}
	for k, w := range want {
		def, ok := Lookup(k)
		if !ok {
			t.Errorf("%s not registered", k)
			continue
		}
		if def.Default != w[0] || def.Warning != w[1] {
			t.Errorf("%s = default %d warning %d, want %d %d", k, def.Default, def.Warning, w[0], w[1])
		}
	}
}

// TestRegistryIsConsistent checks ordering, bounds and flags of every entry.
func TestRegistryIsConsistent(t *testing.T) {
	t.Parallel()
	defs := Definitions()
	if !slices.IsSortedFunc(defs, func(a, b Definition) int { return compare(a.Key, b.Key) }) {
		t.Error("registry is not sorted by key")
	}
	for _, d := range defs {
		if d.Default < 1 || d.Default > d.Max || d.Warning >= d.Default || d.Scopes&ScopeInstallation == 0 ||
			d.EnforcedBy == 0 || d.Unit < UnitBytes || d.Unit > UnitCodepoints || d.Description == "" {
			t.Errorf("inconsistent definition %+v", d)
		}
	}
	if _, ok := Lookup("no.suchKey"); ok {
		t.Error("unknown key found")
	}
}

// compare orders keys.
func compare(a, b Key) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TestSetTightensButNeverRaises_LIM_002 checks resolution rules.
// Verifies: LIM-002, LIM-003.
func TestSetTightensButNeverRaises_LIM_002(t *testing.T) {
	t.Parallel()
	base := Defaults()
	if base.Get(PageNodes) != 5000 || base.Warning(PageNodes) != 1000 || base.Warning(PluginPages) != 400 {
		t.Fatalf("defaults: nodes %d warn %d, pages warn %d", base.Get(PageNodes), base.Warning(PageNodes), base.Warning(PluginPages))
	}

	app, err := base.Tighten(PageNodes, ScopeApp, 800)
	if err != nil {
		t.Fatal(err)
	}
	if app.Get(PageNodes) != 800 || base.Get(PageNodes) != 5000 || app.Warning(PageNodes) != 800 {
		t.Errorf("tighten: app %d (warn %d), base %d", app.Get(PageNodes), app.Warning(PageNodes), base.Get(PageNodes))
	}
	for name, try := range map[string]func() error{
		"raise":       func() error { _, err := app.Tighten(PageNodes, ScopePlugin, 900); return err },
		"above max":   func() error { _, err := base.Tighten(PageNodes, ScopeApp, 60000); return err },
		"zero":        func() error { _, err := base.Tighten(PageNodes, ScopeApp, 0); return err },
		"wrong scope": func() error { _, err := base.Tighten(ReleaseAppSize, ScopePlugin, 1); return err },
		"unknown key": func() error { _, err := base.Tighten("x.y", ScopeApp, 1); return err },
	} {
		if try() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TestGetPanicsOnUnregisteredKeys checks that typos cannot pass silently.
func TestGetPanicsOnUnregisteredKeys(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	Defaults().Get("no.suchKey")
}
