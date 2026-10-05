// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// stateDir is the conformance project of the state engine, whose bundles
// the runtime's state tests run.
var stateDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "state")

const (
	stateApp   = "app.json"
	statePage  = "plugins/notes/pages/home.page.json"
	stateGraph = "plugins/notes/actions/bump.graph.json"
	statePl    = "plugins/notes/plugin.json"
)

// TestStateGoldenBundles pins the bundles of the state project byte for
// byte, and checks that they require the state features and record their
// stored entries.
// Verifies: STA-001, STA-003, STA-004, CMP-002, QA-003.
func TestStateGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(stateDir), DefaultOptions())
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	readAll(t, res)
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "state", b.Key+".pxb"), b.Data)
	}
	app := res.StoredState[""]
	if len(app) != 4 {
		t.Fatalf("app stored state %+v", app)
	}
	for _, e := range app {
		if len(e.Fingerprint) != 16 || e.File != stateApp {
			t.Errorf("entry %+v", e)
		}
	}
	notes := res.StoredState["notes"]
	if len(notes) != 1 || notes[0].Name != "draft" || notes[0].Type != "Draft" {
		t.Errorf("plugin stored state %+v", notes)
	}
	fromBundle, err := StoredState(res.Plugins[0].Data)
	if err != nil || len(fromBundle) != 1 || fromBundle[0].ID != notes[0].ID || fromBundle[0].Fingerprint != notes[0].Fingerprint {
		t.Errorf("stored state read back %+v, %v", fromBundle, err)
	}
	fromApp, err := StoredState(res.App.Data)
	if err != nil || len(fromApp) != 4 || !slices.ContainsFunc(fromApp, func(e StoredEntry) bool { return e.Name == "level" && e.MigrationFrom != "" }) {
		t.Errorf("app stored state read back %+v, %v", fromApp, err)
	}
}

// TestStateFeaturesAreRaised checks that a bundle writing, storing or
// computing state needs runtime 0.3.0: rejected under an older
// minRuntimeVersion, or raised as required features (BND-008).
// Verifies: STA-002, STA-003, STA-004, BND-008.
func TestStateFeaturesAreRaised(t *testing.T) {
	t.Parallel()
	m := project(t, stateDir)
	edit(t, m, stateApp, func(doc map[string]any) { doc["minRuntimeVersion"] = "0.2.0" })
	res := compileFS(m)
	wantDiag(t, res, plxerr.RuntimeTooOld, statePage, "/root/slots/body/children/6/events/onPressed/steps/0/action")
	wantDiag(t, res, plxerr.RuntimeTooOld, stateApp, "/state/0/persistence")
	wantDiag(t, res, plxerr.RuntimeTooOld, stateApp, "/state/3/computed")

	m = project(t, stateDir)
	edit(t, m, stateApp, func(doc map[string]any) {
		doc["minRuntimeVersion"] = "0.2.0"
		doc["requiredFeatures"] = "raise"
	})
	res = compileFS(m)
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	for _, want := range []string{"state.computed.v1", "state.persistence.v1"} {
		if !slices.Contains(res.App.Features, want) {
			t.Errorf("app features %v lack %s", res.App.Features, want)
		}
	}
	for _, want := range []string{"state.computed.v1", "state.persistence.v1", "state.write.v1"} {
		if !slices.Contains(res.Plugins[0].Features, want) {
			t.Errorf("plugin features %v lack %s", res.Plugins[0].Features, want)
		}
	}
}

// stateCase edits the state project and expects one diagnostic.
type stateCase struct {
	name      string
	edit      func(t *testing.T, m fstest.MapFS)
	code      plxerr.Code
	file, ptr string
}

func onState(file string, f func(doc map[string]any)) func(t *testing.T, m fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) { t.Helper(); edit(t, m, file, f) }
}

func stateEntryOf(doc map[string]any, i int) map[string]any {
	return doc["state"].([]any)[i].(map[string]any)
}

func pageStep(doc map[string]any, child int) map[string]any {
	body := doc["root"].(map[string]any)["slots"].(map[string]any)["body"].(map[string]any)
	n := body["children"].([]any)[child].(map[string]any)
	return n["events"].(map[string]any)["onPressed"].(map[string]any)["steps"].([]any)[0].(map[string]any)
}

// TestInvalidState checks the compile-time state diagnostics: writes to
// computed entries, patches of non-objects and unknown fields, typed
// writes, persistence where an entry cannot have one, and migrations that
// can never run.
// Verifies: STA-001, STA-002, STA-003, STA-004, STA-040.
func TestInvalidState(t *testing.T) {
	t.Parallel()
	const inc = "/root/slots/body/children/6/events/onPressed/steps/0"
	const patch = "/root/slots/body/children/8/events/onPressed/steps/0"
	cases := []stateCase{
		{name: "write to a computed entry", edit: onState(statePage, func(doc map[string]any) {
			pageStep(doc, 6)["input"].(map[string]any)["path"] = "page.total"
		}), code: plxerr.StateEntryReadOnly, file: statePage, ptr: inc + "/input/path"},
		{name: "reset of a computed app entry", edit: onState(statePage, func(doc map[string]any) {
			pageStep(doc, 9)["input"].(map[string]any)["path"] = "app.doubled"
		}), code: plxerr.StateEntryReadOnly, file: statePage, ptr: "/root/slots/body/children/9/events/onPressed/steps/0/input/path"},
		{name: "patch of an int", edit: onState(statePage, func(doc map[string]any) {
			s := pageStep(doc, 8)
			s["input"] = map[string]any{"path": "page.count", "patch": 1}
		}), code: plxerr.StatePatchNotObject, file: statePage, ptr: patch + "/input/path"},
		{name: "patch of an unknown field", edit: onState(statePage, func(doc map[string]any) {
			pageStep(doc, 8)["input"].(map[string]any)["patch"] = map[string]any{"colour": "red"}
		}), code: plxerr.PropTypeMismatch, file: statePage, ptr: patch + "/input/patch/colour"},
		{name: "patch of a field of the wrong type", edit: onState(statePage, func(doc map[string]any) {
			pageStep(doc, 8)["input"].(map[string]any)["patch"] = map[string]any{"done": "yes"}
		}), code: plxerr.PropTypeMismatch, file: statePage, ptr: patch + "/input/patch/done"},
		{name: "write of the wrong type", edit: onState(statePage, func(doc map[string]any) {
			pageStep(doc, 10)["input"].(map[string]any)["value"] = 3
		}), code: plxerr.PropTypeMismatch, file: statePage, ptr: "/root/slots/body/children/10/events/onPressed/steps/0/input/value"},
		{name: "write to an undeclared run variable", edit: onState(stateGraph, func(doc map[string]any) {
			doc["steps"].([]any)[0].(map[string]any)["input"].(map[string]any)["path"] = "run.missing"
		}), code: plxerr.UnresolvedReference, file: stateGraph, ptr: "/steps/0/input/path"},
		{name: "persisted run variable", edit: onState(stateGraph, func(doc map[string]any) {
			stateEntryOf(doc, 0)["persistence"] = "persisted"
		}), code: plxerr.StatePersistenceNotAllowed, file: stateGraph, ptr: "/state/0/persistence"},
		{name: "persisted computed entry", edit: onState(stateApp, func(doc map[string]any) {
			stateEntryOf(doc, 3)["persistence"] = "session"
		}), code: plxerr.StatePersistenceNotAllowed, file: stateApp, ptr: "/state/3/persistence"},
		{name: "migration of a memory entry", edit: onState(statePl, func(doc map[string]any) {
			stateEntryOf(doc, 0)["migration"] = map[string]any{"reset": true}
		}), code: plxerr.StateMigrationInvalid, file: statePl, ptr: "/state/0/migration"},
		{name: "migration with reset false", edit: onState(statePl, func(doc map[string]any) {
			stateEntryOf(doc, 1)["migration"] = map[string]any{"reset": false}
		}), code: plxerr.StateMigrationInvalid, file: statePl, ptr: "/state/1/migration/reset"},
		{name: "migration from the current type", edit: onState(stateApp, func(doc map[string]any) {
			stateEntryOf(doc, 0)["migration"] = map[string]any{"from": "int", "value": map[string]any{"$expr": "previous"}}
		}), code: plxerr.StateMigrationInvalid, file: stateApp, ptr: "/state/0/migration/from"},
		{name: "migration of the wrong type", edit: onState(stateApp, func(doc map[string]any) {
			stateEntryOf(doc, 0)["migration"] = map[string]any{"from": "string", "value": map[string]any{"$expr": "previous"}}
		}), code: plxerr.ValueTypeMismatch, file: stateApp, ptr: "/state/0/migration/value/$expr"},
		{name: "migration from an unknown type", edit: onState(stateApp, func(doc map[string]any) {
			stateEntryOf(doc, 0)["migration"] = map[string]any{"from": "Old", "value": map[string]any{"$expr": "0"}}
		}), code: plxerr.UnknownType, file: stateApp, ptr: "/state/0/migration/from"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, stateDir)
			c.edit(t, m)
			wantDiag(t, compileFS(m), c.code, c.file, c.ptr)
		})
	}
}

// TestStateMigrationCompiles checks that a migration from the previous
// type compiles, is encoded, and satisfies the comparison with the
// previous release, while a missing or mismatched one does not.
// Verifies: STA-040.
func TestStateMigrationCompiles(t *testing.T) {
	t.Parallel()
	prev := Compile(os.DirFS(stateDir), DefaultOptions())
	if prev.App == nil {
		t.Fatalf("diagnostics:\n%s", list(prev.Diagnostics))
	}
	before, err := StoredState(prev.App.Data)
	if err != nil {
		t.Fatal(err)
	}

	retype := func(migration map[string]any) *Result {
		m := project(t, stateDir)
		edit(t, m, stateApp, func(doc map[string]any) {
			e := stateEntryOf(doc, 1)
			e["type"], e["default"] = "int", 0
			if migration != nil {
				e["migration"] = migration
			}
		})
		return compileFS(m)
	}

	none := retype(nil)
	if none.App == nil {
		t.Fatalf("diagnostics:\n%s", list(none.Diagnostics))
	}
	diags := CheckStoredState(before, none.StoredState[""])
	if len(diags) != 1 || diags[0].Code != plxerr.StateMigrationRequired || diags[0].Path != "/state/1" {
		t.Errorf("without a migration: %v", diags)
	}

	good := retype(map[string]any{"from": "string", "value": map[string]any{"$expr": "len(previous)"}})
	if good.App == nil {
		t.Fatalf("diagnostics:\n%s", list(good.Diagnostics))
	}
	if diags := CheckStoredState(before, good.StoredState[""]); len(diags) != 0 {
		t.Errorf("with a migration: %v", diags)
	}
	back, err := StoredState(good.App.Data)
	if err != nil || !slices.ContainsFunc(back, func(e StoredEntry) bool { return e.Name == "theme" && e.MigrationFrom != "" }) {
		t.Errorf("migration not encoded: %+v %v", back, err)
	}

	wrong := retype(map[string]any{"from": "double", "value": map[string]any{"$expr": "int(previous)"}})
	diags = CheckStoredState(before, wrong.StoredState[""])
	if len(diags) != 1 || diags[0].Code != plxerr.StateMigrationMismatch || diags[0].Path != "/state/1/migration/from" {
		t.Errorf("with a migration from another type: %v", diags)
	}

	reset := retype(map[string]any{"reset": true})
	if diags := CheckStoredState(before, reset.StoredState[""]); len(diags) != 0 {
		t.Errorf("with a reset: %v", diags)
	}
	if _, err := StoredState([]byte("not a bundle")); err == nil {
		t.Error("a malformed bundle was read")
	}
}

// TestTypeFingerprints checks that a fingerprint follows a type's
// structure: field order and type names do not change it; a field, a
// field type, an enum member or nullability does.
// Verifies: STA-040.
func TestTypeFingerprints(t *testing.T) {
	t.Parallel()
	fp := func(f func(doc map[string]any)) string {
		m := project(t, stateDir)
		edit(t, m, statePl, f)
		res := compileFS(m)
		if res.Plugins == nil {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
		return res.StoredState["notes"][0].Fingerprint
	}
	types := func(doc map[string]any) map[string]any { return doc["types"].([]any)[0].(map[string]any) }
	base := fp(func(map[string]any) {})
	reordered := fp(func(doc map[string]any) {
		fs := types(doc)["fields"].([]any)
		fs[0], fs[1] = fs[1], fs[0]
	})
	if reordered != base {
		t.Error("reordering fields changed the fingerprint")
	}
	note := func(typ string, def any) string {
		return fp(func(doc map[string]any) {
			types(doc)["fields"] = append(types(doc)["fields"].([]any), map[string]any{"name": "note", "type": typ})
			if def != nil {
				stateEntryOf(doc, 1)["default"].(map[string]any)["note"] = def
			}
		})
	}
	added, retyped, nonNull := note("string?", nil), note("int?", nil), note("string", "")
	if added == base || retyped == added || nonNull == added {
		t.Errorf("fingerprints %s %s %s %s", base, added, retyped, nonNull)
	}
}
