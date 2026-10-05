// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// dbDir is the conformance project of the local database, whose bundles
// the runtime's database tests run.
var dbDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "db")

const dbPlugin = "plugins/todo/plugin.json"

// TestDBGoldenBundles pins the bundles of the database project byte for
// byte and checks that they need db.v1, first in runtime 0.3.0, and carry
// the collections with their versions and migrations.
// Verifies: CMP-002, QA-003, DB-004, DB-005, DB-006, BND-008.
func TestDBGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(dbDir), DefaultOptions())
	onlyRaised(t, res)
	readAll(t, res)
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "db", b.Key+".pxb"), b.Data)
		if !slices.Contains(b.Features, "db.v1") {
			t.Errorf("bundle %s features %v lack db.v1", b.Key, b.Features)
		}
	}
	todo, err := StoredCollections(res.Plugins[0].Data)
	if err != nil || len(todo.Collections) != 1 {
		t.Fatalf("plugin collections %+v, %v", todo, err)
	}
	c := todo.Collections[0]
	if c.Key != "tasks" || c.Version != 2 || len(c.Fields) != 5 || len(c.Indexes) != 2 ||
		len(c.Migrations) != 1 || c.Migrations[0].Rename["title"] != "name" {
		t.Errorf("tasks %+v", c)
	}
	shared, err := StoredCollections(res.App.Data)
	if err != nil || len(shared.Collections) != 1 || shared.Collections[0].Key != "labels" || shared.Collections[0].Version != 1 {
		t.Errorf("app collections %+v, %v", shared, err)
	}
}

// TestDatabaseSourceChecks checks the configuration of watched queries.
// Verifies: DB-006.
func TestDatabaseSourceChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		config map[string]any
		ptr    string
	}{
		{"unknown collection", map[string]any{"collection": "nope"}, "/dataSources/0/config/collection"},
		{"unknown field in a filter", map[string]any{"collection": "tasks", "filter": map[string]any{"field": "nope", "op": "eq", "value": 1}}, "/dataSources/0/config/filter/field"},
		{"operator that does not apply", map[string]any{"collection": "tasks", "filter": map[string]any{"field": "done", "op": "contains", "value": "x"}}, "/dataSources/0/config/filter/op"},
		{"value of the wrong type", map[string]any{"collection": "tasks", "filter": map[string]any{"field": "rank", "op": "gt", "value": "high"}}, "/dataSources/0/config/filter/value"},
		{"in without a list", map[string]any{"collection": "tasks", "filter": map[string]any{"field": "rank", "op": "in", "value": 3}}, "/dataSources/0/config/filter/value"},
		{"nested value of the wrong type", map[string]any{"collection": "tasks", "filter": map[string]any{"and": []any{map[string]any{"field": "done", "op": "eq", "value": 1}}}}, "/dataSources/0/config/filter/and/0/value"},
		{"sort by an unknown field", map[string]any{"collection": "tasks", "orderBy": "nope"}, "/dataSources/0/config/orderBy"},
		{"descending without a sort", map[string]any{"collection": "tasks", "descending": true}, "/dataSources/0/config/descending"},
		{"limit over db.queryRows", map[string]any{"collection": "tasks", "limit": 100000000}, "/dataSources/0/config/limit"},
		{"negative offset", map[string]any{"collection": "tasks", "offset": -1}, "/dataSources/0/config/offset"},
		{"unknown property", map[string]any{"collection": "tasks", "extra": true}, "/dataSources/0/config"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, dbDir)
			edit(t, m, dbPlugin, func(doc map[string]any) {
				doc["dataSources"].([]any)[0].(map[string]any)["config"] = c.config
			})
			wantDiag(t, compileFS(m), plxerr.DatabaseSourceInvalid, dbPlugin, c.ptr)
		})
	}
	t.Run("a source that is not a list", func(t *testing.T) {
		t.Parallel()
		m := project(t, dbDir)
		edit(t, m, dbPlugin, func(doc map[string]any) {
			doc["dataSources"].([]any)[0].(map[string]any)["type"] = "Task"
			doc["dataSources"].([]any)[0].(map[string]any)["mock"] = map[string]any{"id": "a", "title": "t", "done": false, "rank": 1, "note": nil}
		})
		wantDiag(t, compileFS(m), plxerr.DatabaseSourceInvalid, dbPlugin, "/dataSources/0/type")
	})
}
