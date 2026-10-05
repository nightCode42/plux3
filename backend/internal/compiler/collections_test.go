// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	taskID  = "01a0c450-6c00-7014-8000-0000000600a1"
	notesID = "01a0c450-6c00-7014-8000-0000000600a2"
)

// collection builds a collection document.
func collection(id, key string, version int, fields [][2]string, pk []string, extra map[string]any) map[string]any {
	fs := make([]any, len(fields))
	for i, f := range fields {
		fs[i] = map[string]any{"name": f[0], "type": f[1]}
	}
	c := map[string]any{"id": id, "key": key, "fields": fs, "primaryKey": pk, "version": version}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

// taskFields are the fields of the tasks collection at version 1.
var taskFields = [][2]string{{"id", "string"}, {"title", "string"}, {"done", "bool"}}

// compileCollections compiles the state project with the app's collections
// and dropped collections replaced.
func compileCollections(t *testing.T, cols []any, dropped ...string) *Result {
	t.Helper()
	m := project(t, stateDir)
	edit(t, m, stateApp, func(doc map[string]any) {
		doc["collections"] = cols
		if len(dropped) > 0 {
			doc["droppedCollections"] = dropped
		}
	})
	return compileFS(m)
}

func collectionSet(t *testing.T, cols []any, dropped ...string) CollectionSet {
	t.Helper()
	res := compileCollections(t, cols, dropped...)
	if res.App == nil {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	return res.Collections[""]
}

func codes(ds plxerr.Diagnostics) []plxerr.Code {
	var out []plxerr.Code
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

// TestCollectionsRoundTripThroughTheBundle checks that the compiled
// collections, with versions, plans and dropped IDs, are read back from the
// bundle as the compiler saw them, and that the bundle needs db.v1.
// Verifies: DB-004, DB-005, BND-008.
func TestCollectionsRoundTripThroughTheBundle(t *testing.T) {
	t.Parallel()
	plan := map[string]any{"migrations": []any{map[string]any{
		"from": 1, "rename": map[string]any{"title": "name"}, "drop": []any{"legacy"}, "reset": []any{"done"},
	}}, "indexes": []any{[]any{"done"}}}
	cols := []any{collection(taskID, "tasks", 2, taskFields, []string{"id"}, plan)}
	res := compileCollections(t, cols, notesID)
	if res.App == nil {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	got, err := StoredCollections(res.App.Data)
	if err != nil {
		t.Fatal(err)
	}
	want := res.Collections[""]
	if len(got.Collections) != 1 || len(want.Collections) != 1 {
		t.Fatalf("collections %+v, want %+v", got, want)
	}
	g, w := got.Collections[0], want.Collections[0]
	if g.ID != taskID || g.Key != "tasks" || g.Version != 2 || !slices.Equal(g.PrimaryKey, []string{"id"}) ||
		len(g.Fields) != 3 || !sameSchema(g, w) || len(g.Indexes) != 1 {
		t.Errorf("read back %+v", g)
	}
	if len(g.Migrations) != 1 || g.Migrations[0].From != 1 || g.Migrations[0].Rename["title"] != "name" ||
		!slices.Equal(g.Migrations[0].Drop, []string{"legacy"}) || !slices.Equal(g.Migrations[0].Reset, []string{"done"}) {
		t.Errorf("migrations %+v", g.Migrations)
	}
	if !slices.Equal(got.Dropped, []string{notesID}) {
		t.Errorf("dropped %v", got.Dropped)
	}

	m := project(t, stateDir)
	edit(t, m, stateApp, func(doc map[string]any) {
		doc["collections"] = cols
		doc["minRuntimeVersion"] = "0.2.0"
		doc["requiredFeatures"] = "raise"
	})
	raised := compileFS(m)
	if raised.App == nil || !slices.Contains(raised.App.Features, "db.v1") {
		t.Errorf("features %v, diagnostics:\n%s", raised.App, list(raised.Diagnostics))
	}
}

// TestCollectionMigrationChecks compares collections with the previous
// release: additive changes pass, a changed schema raises the version, and
// a change that loses data needs a plan and warns.
// Verifies: DB-005.
func TestCollectionMigrationChecks(t *testing.T) {
	t.Parallel()
	v1 := collectionSet(t, []any{collection(taskID, "tasks", 1, taskFields, []string{"id"}, nil)})
	withFields := func(version int, fields [][2]string, extra map[string]any) CollectionSet {
		return collectionSet(t, []any{collection(taskID, "tasks", version, fields, []string{"id"}, extra)})
	}
	plan := func(m map[string]any) map[string]any {
		m["from"] = 1
		return map[string]any{"migrations": []any{m}}
	}
	added := append(slices.Clone(taskFields), [2]string{"note", "string?"})
	cases := []struct {
		name string
		cur  CollectionSet
		want []plxerr.Code
	}{
		{"unchanged", withFields(1, taskFields, nil), nil},
		{"added nullable field", withFields(2, added, nil), nil},
		{"widened to nullable", withFields(2, [][2]string{{"id", "string"}, {"title", "string?"}, {"done", "bool"}}, nil), nil},
		{"renamed with a plan", withFields(2, [][2]string{{"id", "string"}, {"name", "string"}, {"done", "bool"}},
			plan(map[string]any{"rename": map[string]any{"name": "title"}})), nil},
		{"changed without a new version", withFields(1, added, nil), []plxerr.Code{plxerr.CollectionVersionInvalid}},
		{"dropped field without a plan", withFields(2, taskFields[:2], nil), []plxerr.Code{plxerr.CollectionPlanRequired}},
		{"dropped field with a plan", withFields(2, taskFields[:2], plan(map[string]any{"drop": []any{"done"}})), []plxerr.Code{plxerr.CollectionDestructive}},
		{"added field that is not nullable", withFields(2, append(slices.Clone(taskFields), [2]string{"count", "int"}), nil), []plxerr.Code{plxerr.CollectionPlanRequired}},
		{"added field reset", withFields(2, append(slices.Clone(taskFields), [2]string{"count", "int"}), plan(map[string]any{"reset": []any{"count"}})), []plxerr.Code{plxerr.CollectionDestructive}},
		{"narrowed type without a plan", withFields(2, [][2]string{{"id", "string"}, {"title", "int"}, {"done", "bool"}}, nil), []plxerr.Code{plxerr.CollectionPlanRequired}},
		{"narrowed type reset", withFields(2, [][2]string{{"id", "string"}, {"title", "int"}, {"done", "bool"}}, plan(map[string]any{"reset": []any{"title"}})), []plxerr.Code{plxerr.CollectionDestructive}},
		{"primary key changed", collectionSet(t, []any{collection(taskID, "tasks", 2, taskFields, []string{"title"}, nil)}), []plxerr.Code{plxerr.CollectionKeyChanged}},
		{"collection removed", collectionSet(t, []any{}), []plxerr.Code{plxerr.CollectionPlanRequired}},
		{"collection dropped", collectionSet(t, []any{}, taskID), []plxerr.Code{plxerr.CollectionDestructive}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := codes(CheckCollections(v1, c.cur)); !slices.Equal(got, c.want) {
				t.Errorf("codes %v, want %v", got, c.want)
			}
		})
	}

	v2 := withFields(2, added, nil)
	if got := codes(CheckCollections(v2, v1)); !slices.Equal(got, []plxerr.Code{plxerr.CollectionVersionInvalid}) {
		t.Errorf("a lowered version: %v", got)
	}
}

// TestCollectionMigrationChain checks that a plan chain covers a device
// that skipped versions: a field added at version 2 and dropped at 3 needs
// nothing from a device at version 1 beyond the plans.
// Verifies: DB-005.
func TestCollectionMigrationChain(t *testing.T) {
	t.Parallel()
	v1 := collectionSet(t, []any{collection(taskID, "tasks", 1, taskFields, []string{"id"}, nil)})
	cur := collectionSet(t, []any{collection(taskID, "tasks", 3, taskFields[:2], []string{"id"}, map[string]any{
		"migrations": []any{
			map[string]any{"from": 2, "drop": []any{"extra"}},
			map[string]any{"from": 1, "drop": []any{"done"}, "reset": []any{}},
		},
	})})
	got := CheckCollections(v1, cur)
	if len(got) != 1 || got[0].Code != plxerr.CollectionDestructive {
		t.Errorf("diagnostics: %v", got)
	}
}

// TestCollectionDeclarationChecks checks plans and keys on their own.
// Verifies: DB-004, DB-005.
func TestCollectionDeclarationChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		col  map[string]any
		code plxerr.Code
		ptr  string
	}{
		{"plan from the current version", collection(taskID, "tasks", 2, taskFields, []string{"id"}, map[string]any{
			"migrations": []any{map[string]any{"from": 2}},
		}), plxerr.CollectionPlanInvalid, "/collections/0/migrations/0/from"},
		{"two plans from one version", collection(taskID, "tasks", 3, taskFields, []string{"id"}, map[string]any{
			"migrations": []any{map[string]any{"from": 1}, map[string]any{"from": 1}},
		}), plxerr.CollectionPlanInvalid, "/collections/0/migrations/1/from"},
		{"drop of a declared field", collection(taskID, "tasks", 2, taskFields, []string{"id"}, map[string]any{
			"migrations": []any{map[string]any{"from": 1, "drop": []any{"title"}}},
		}), plxerr.CollectionPlanInvalid, "/collections/0/migrations/0/drop/0"},
		{"reset of an unknown field", collection(taskID, "tasks", 2, taskFields, []string{"id"}, map[string]any{
			"migrations": []any{map[string]any{"from": 1, "reset": []any{"nope"}}},
		}), plxerr.CollectionPlanInvalid, "/collections/0/migrations/0/reset/0"},
		{"reset of a field with no empty value", collection(taskID, "tasks", 2, [][2]string{{"id", "string"}, {"at", "date"}}, []string{"id"}, map[string]any{
			"migrations": []any{map[string]any{"from": 1, "reset": []any{"at"}}},
		}), plxerr.CollectionPlanInvalid, "/collections/0/migrations/0/reset/0"},
		{"key of a double", collection(taskID, "tasks", 1, [][2]string{{"id", "double"}}, []string{"id"}, nil), plxerr.CollectionKeyTypeInvalid, "/collections/0/primaryKey/0"},
		{"nullable key", collection(taskID, "tasks", 1, [][2]string{{"id", "string?"}}, []string{"id"}, nil), plxerr.CollectionKeyTypeInvalid, "/collections/0/primaryKey/0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wantDiag(t, compileCollections(t, []any{c.col}), c.code, stateApp, c.ptr)
		})
	}
	both := compileCollections(t, []any{collection(taskID, "tasks", 1, taskFields, []string{"id"}, nil)}, taskID)
	wantDiag(t, both, plxerr.CollectionPlanInvalid, stateApp, "/droppedCollections/0")
}

// TestDBQueryConditionSeesTheRecord checks that a dbQuery condition is
// typed over the collection's record, and that a field the collection does
// not have is an error.
// Verifies: DB-006.
func TestDBQueryConditionSeesTheRecord(t *testing.T) {
	t.Parallel()
	compile := func(where string) *Result {
		m := project(t, stateDir)
		edit(t, m, statePl, func(doc map[string]any) {
			doc["collections"] = []any{collection(taskID, "tasks", 1, taskFields, []string{"id"}, nil)}
		})
		edit(t, m, stateGraph, func(doc map[string]any) {
			steps := doc["steps"].([]any)
			steps[len(steps)-1].(map[string]any)["next"] = "find"
			doc["steps"] = append(steps, map[string]any{
				"id": "find", "action": "dbQuery",
				"input": map[string]any{"collection": "tasks", "where": map[string]any{"$expr": where}},
			})
		})
		return compileFS(m)
	}
	if res := compile("record.done == false && record.title != \"\""); res.Diagnostics.HasErrors() {
		t.Errorf("a condition over the record's fields:\n%s", list(res.Diagnostics))
	}
	if res := compile("record.missing == false"); !res.Diagnostics.HasErrors() {
		t.Errorf("a condition over a field the collection does not have compiled")
	}
}
