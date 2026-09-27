// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// testChain renames page "pageType" (0.8.0) to "kind"-era "pageKind" in two
// steps, to exercise ordering.
func testChain() []Migration {
	return []Migration{
		{From: "0.8.0", To: "0.9.0", Apply: func(doc map[string]any) error {
			if v, ok := doc["pageType"]; ok {
				doc["pageStyle"] = v
				delete(doc, "pageType")
			}
			return nil
		}},
		{From: "0.9.0", To: "1.0.0", Apply: func(doc map[string]any) error {
			if v, ok := doc["pageStyle"]; ok {
				doc["pageKind"] = v
				delete(doc, "pageStyle")
			}
			return nil
		}},
	}
}

// TestMigratorAppliesStepsInOrder_SCH_043 checks forward-only, ordered
// migration from any released version and the resulting version stamp.
// Verifies: SCH-043, SCH-000.
func TestMigratorAppliesStepsInOrder_SCH_043(t *testing.T) {
	t.Parallel()
	m, err := NewMigrator("1.0.0", testChain())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Released(); len(got) != 3 || got[0] != "0.8.0" || got[2] != "1.0.0" {
		t.Fatalf("Released() = %v", got)
	}
	for _, start := range []string{"0.8.0", "0.9.0"} {
		doc := map[string]any{"schemaVersion": start, "pageType": "screen", "pageStyle": "screen"}
		if start == "0.9.0" {
			delete(doc, "pageType")
		}
		if d := m.Migrate(doc, "p.json"); d != nil {
			t.Fatalf("%s: %v", start, d)
		}
		if doc["schemaVersion"] != "1.0.0" || doc["pageKind"] != "screen" || doc["pageStyle"] != nil || doc["pageType"] != nil {
			t.Errorf("%s migrated to %v", start, doc)
		}
	}
	current := map[string]any{"schemaVersion": "1.0.0", "pageKind": "dialog"}
	if d := m.Migrate(current, "p.json"); d != nil || current["pageKind"] != "dialog" {
		t.Errorf("current version changed: %v %v", current, d)
	}
}

// TestMigratorRejectsUnknownVersionsAndFailures_SCH_000 checks that nothing
// is silently reinterpreted.
// Verifies: SCH-000.
func TestMigratorRejectsUnknownVersionsAndFailures_SCH_000(t *testing.T) {
	t.Parallel()
	failing := []Migration{{From: "0.9.0", To: "1.0.0", Apply: func(map[string]any) error { return errors.New("boom") }}}
	m, err := NewMigrator("1.0.0", failing)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		doc  map[string]any
		code plxerr.Code
	}{
		{map[string]any{"schemaVersion": "0.9.0"}, plxerr.MigrationFailed},
		{map[string]any{"schemaVersion": "2.0.0"}, plxerr.UnsupportedSchemaVersion},
		{map[string]any{"schemaVersion": 1}, plxerr.MissingProperty},
		{map[string]any{}, plxerr.MissingProperty},
	}
	for _, tt := range tests {
		d := m.Migrate(tt.doc, "p.json")
		if d == nil || d.Code != tt.code {
			t.Errorf("Migrate(%v) = %v, want %s", tt.doc, d, tt.code)
		}
	}
}

// TestNewMigratorRejectsBrokenChains checks the chain's integrity checks.
func TestNewMigratorRejectsBrokenChains(t *testing.T) {
	t.Parallel()
	noop := func(map[string]any) error { return nil }
	for name, steps := range map[string][]Migration{
		"gap":          {{From: "0.8.0", To: "0.9.0", Apply: noop}},
		"cycle":        {{From: "1.0.0", To: "1.0.0", Apply: noop}},
		"no apply":     {{From: "0.9.0", To: "1.0.0"}},
		"out of order": {{From: "0.9.0", To: "1.0.0", Apply: noop}, {From: "0.8.0", To: "0.9.0", Apply: noop}},
	} {
		if _, err := NewMigrator("1.0.0", steps); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestMigrationGoldensForEveryReleasedVersion_SCH_043 migrates the golden
// inputs of every released version and compares the canonical result with
// the expected current-version document. For 1.0.0, the only released
// version, the conformance project must come through unchanged.
// Verifies: SCH-043.
func TestMigrationGoldensForEveryReleasedVersion_SCH_043(t *testing.T) {
	t.Parallel()
	m := DefaultMigrator()
	if got := m.Released(); len(got) != 1 || got[0] != CurrentVersion {
		t.Fatalf("released versions %v: add golden inputs for the new version", got)
	}
	v, err := sharedValidator()
	if err != nil {
		t.Fatal(err)
	}
	l := NewLoader(v, m, limits.Defaults())
	err = filepath.WalkDir(exampleDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src, diags := l.ParseDocument(path, data, "")
		if len(diags) != 0 {
			t.Errorf("%s:\n%s", path, list(diags))
			return nil
		}
		want, err := jcs.Canonicalize(data, 512)
		if err != nil {
			return err
		}
		if string(src.Canonical) != string(want) {
			t.Errorf("%s changed under the identity migration", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestLoaderUsesTheMigratorBeforeValidation checks the pipeline order: a
// document of an older released version validates after migration.
func TestLoaderUsesTheMigratorBeforeValidation(t *testing.T) {
	t.Parallel()
	fsys := exampleFS(t)
	edit(t, fsys, "plugins/loans/pages/result.page.json", func(d map[string]any) {
		d["schemaVersion"] = "0.8.0"
		d["pageType"] = d["pageKind"]
		delete(d, "pageKind")
	})
	m, err := NewMigrator("1.0.0", testChain())
	if err != nil {
		t.Fatal(err)
	}
	v, err := sharedValidator()
	if err != nil {
		t.Fatal(err)
	}

	p, diags := NewLoader(v, m, limits.Defaults()).Load(fsys)

	if len(diags) != 0 {
		t.Fatalf("migrated document rejected:\n%s", list(diags))
	}
	if doc := p.Plugins[0].Pages[1].Doc; doc == nil || doc.PageKind != PageKindScreen {
		t.Errorf("migrated page decoded as %+v", doc)
	}
}
