// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// dataDir is the conformance project of the data layer (ADR-0048), whose
// bundles the runtime's data tests run.
var dataDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "data")

const shopFile = "plugins/shop/plugin.json"

// onlyRaised fails on any diagnostic but the raised data.v1 feature.
func onlyRaised(t *testing.T, res *Result) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Code != plxerr.RequiredFeaturesRaised {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	}
}

// TestDataGoldenBundles pins the bundles of the data project byte for
// byte and checks that a bundle declaring a REST or GraphQL source needs
// data.v1, first in runtime 0.3.0, so an older runtime never shows a page
// whose sources nothing loads.
// Verifies: CMP-002, QA-003, DAT-001, BND-008.
func TestDataGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(dataDir), DefaultOptions())
	onlyRaised(t, res)
	readAll(t, res)
	if !slices.Contains(res.Plugins[0].Features, "data.v1") {
		t.Errorf("plugin features %v lack data.v1", res.Plugins[0].Features)
	}
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "data", b.Key+".pxb"), b.Data)
	}
}

// sourceConfig returns the encoded configuration of the plugin's source
// named name, as a map of its literal entries' keys.
func sourceConfig(t *testing.T, b *bundle.Bundle, name string) (*fbs.Value, func(uint32) string) {
	t.Helper()
	var s *bundle.Section
	for i := range b.Sections {
		if b.Sections[i].Kind == bundle.SectionSchemas {
			s = &b.Sections[i]
		}
	}
	if s == nil {
		t.Fatal("no schemas section")
	}
	sch := fbs.GetRootAsSchemas(s.Data, 0)
	strs := stringsOf(t, b)
	var d fbs.DataSource
	for i := range sch.DataSourcesLength() {
		sch.DataSources(&d, i)
		if strs(d.Name()) == name {
			return d.Config(nil), strs
		}
	}
	t.Fatalf("no source %s", name)
	return nil, nil
}

// stringsOf returns the bundle's string table.
func stringsOf(t *testing.T, b *bundle.Bundle) func(uint32) string {
	t.Helper()
	for _, sec := range b.Sections {
		if sec.Kind == bundle.SectionStrings {
			st := fbs.GetRootAsStrings(sec.Data, 0)
			return func(i uint32) string { return string(st.Strings(int(i))) }
		}
	}
	t.Fatal("no strings section")
	return nil
}

// valueEntry returns a map value's entry.
func valueEntry(v *fbs.Value, strs func(uint32) string, key string) *fbs.Value {
	var e fbs.Entry
	for i := range v.EntriesLength() {
		v.Entries(&e, i)
		if strs(e.Key()) == key {
			return e.Value(nil)
		}
	}
	return nil
}

// TestDataConfigIsEncoded checks that the bundle carries each source's
// base URL per environment, resolved from the app's variables (DAT-003),
// its success mock beside the other mock states (DAT-080) and its
// transform as a compiled expression (DAT-004).
// Verifies: DAT-003, DAT-004, DAT-080.
func TestDataConfigIsEncoded(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(dataDir), DefaultOptions())
	onlyRaised(t, res)
	shop := readAll(t, res)[1]
	cfg, strs := sourceConfig(t, shop, "tasks")
	urls := valueEntry(cfg, strs, "baseUrls")
	if urls == nil || urls.EntriesLength() != 2 {
		t.Fatal("baseUrls not encoded for both environments")
	}
	if prod := valueEntry(urls, strs, "production"); prod == nil || strs(prod.S()) != "https://api.example.com/v1" {
		t.Error("production base URL")
	}
	mocks := valueEntry(cfg, strs, "mocks")
	for _, state := range []string{"success", "empty", "error"} {
		if mocks == nil || valueEntry(mocks, strs, state) == nil {
			t.Errorf("mock state %s not encoded", state)
		}
	}
	count, strs := sourceConfig(t, shop, "count")
	if tr := valueEntry(count, strs, "transform"); tr == nil || tr.Kind() != fbs.ValueKindExpr {
		t.Error("transform not compiled")
	}
}

// dataCase breaks the data project.
type dataCase struct {
	name string
	edit func(t *testing.T, m fstest.MapFS)
	opts func(t *testing.T, o *Options)
	code plxerr.Code
	file string
	ptr  string
}

// onSource edits the configuration of the plugin's source i.
func onSource(i string, f func(t *testing.T, src, cfg map[string]any)) func(*testing.T, fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) {
		edit(t, m, shopFile, func(doc map[string]any) {
			src := at(t, doc, "dataSources/"+i)
			cfg, _ := src["config"].(map[string]any)
			f(t, src, cfg)
		})
	}
}

// TestInvalidDataSources checks each compile-time check of data sources.
// Verifies: DAT-001, DAT-003, DAT-004, DAT-010, DAT-011, DAT-030, SEC-080.
func TestInvalidDataSources(t *testing.T) {
	t.Parallel()
	const tasks = "/dataSources/0/config"
	cases := []dataCase{
		{
			"no config", onSource("0", func(_ *testing.T, src, _ map[string]any) { delete(src, "config") }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks,
		},
		{
			"unknown property", onSource("0", func(_ *testing.T, _, c map[string]any) { c["retries"] = 3 }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks,
		},
		{
			"no base URL", onSource("0", func(_ *testing.T, _, c map[string]any) { delete(c, "baseUrl") }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks,
		},
		{
			"base URL not a variable", onSource("0", func(_ *testing.T, _, c map[string]any) { c["baseUrl"] = "nope" }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/baseUrl",
		},
		{"undeclared domain", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, shopFile, func(doc map[string]any) {
				at(t, doc, "capabilities")["networkDomains"] = []any{"*.example.org"}
			})
		}, nil, plxerr.DataSourceDomainUndeclared, shopFile, tasks + "/baseUrl"},
		{"cleartext base URL", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) {
				at(t, doc, "environments/0/values")["apiBaseUrl"] = "http://api.example.com"
			})
		}, nil, plxerr.DataSourceDomainUndeclared, shopFile, tasks + "/baseUrl"},
		{"environment without a value", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) {
				delete(at(t, doc, "environments/1/values"), "apiBaseUrl")
			})
		}, nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/baseUrl"},
		{
			"bad method", onSource("0", func(_ *testing.T, _, c map[string]any) { c["method"] = "DELETE" }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/method",
		},
		{
			"relative path", onSource("0", func(_ *testing.T, _, c map[string]any) { c["path"] = "tasks?x=1" }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/path",
		},
		{
			"unbound placeholder", onSource("0", func(_ *testing.T, _, c map[string]any) { c["path"] = "/tasks/{id}" }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/path",
		},
		{"credential header", onSource("0", func(_ *testing.T, _, c map[string]any) {
			c["headers"] = map[string]any{"X-Api-Key": "k"}
		}), nil, plxerr.DataSourceSecretHeader, shopFile, tasks + "/headers"},
		{"authorization header", onSource("0", func(_ *testing.T, _, c map[string]any) {
			c["headers"] = map[string]any{"Authorization": "Bearer x"}
		}), nil, plxerr.DataSourceSecretHeader, shopFile, tasks + "/headers"},
		{
			"bad selector", onSource("0", func(_ *testing.T, _, c map[string]any) { c["select"] = "items..x" }),
			nil, plxerr.DataMappingInvalid, shopFile, tasks + "/select",
		},
		{
			"transform without response type", onSource("1", func(_ *testing.T, _, c map[string]any) { delete(c, "responseType") }),
			nil, plxerr.DataMappingInvalid, shopFile, "/dataSources/1/config/transform",
		},
		{
			"response type without transform", onSource("1", func(_ *testing.T, _, c map[string]any) { delete(c, "transform") }),
			nil, plxerr.DataMappingInvalid, shopFile, "/dataSources/1/config/responseType",
		},
		{"transform of the wrong type", onSource("1", func(_ *testing.T, _, c map[string]any) {
			c["transform"] = map[string]any{"$expr": `"x"`}
		}), nil, plxerr.DataMappingInvalid, shopFile, "/dataSources/1/config/transform"},
		{"unknown cache policy", onSource("0", func(_ *testing.T, _, c map[string]any) {
			c["cache"] = map[string]any{"policy": "forever"}
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/cache/policy"},
		{"cache without TTL", onSource("0", func(_ *testing.T, _, c map[string]any) {
			c["cache"] = map[string]any{"policy": "cacheFirst"}
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/cache/ttlSeconds"},
		{"pagination of a non-list", onSource("1", func(_ *testing.T, _, c map[string]any) {
			c["pagination"] = map[string]any{"style": "page", "pageSize": 10, "pageParam": "p"}
		}), nil, plxerr.DataPaginationInvalid, shopFile, "/dataSources/1/config/pagination"},
		{"unknown pagination style", onSource("0", func(_ *testing.T, _, c map[string]any) {
			at(t, c, "pagination")["style"] = "keyset"
		}), nil, plxerr.DataPaginationInvalid, shopFile, tasks + "/pagination/style"},
		{"cursor without next cursor", onSource("0", func(_ *testing.T, _, c map[string]any) {
			delete(at(t, c, "pagination"), "nextCursor")
		}), nil, plxerr.DataPaginationInvalid, shopFile, tasks + "/pagination"},
		{
			"page size over the limit", nil, tighten(limits.DataPageSize, 1),
			plxerr.DataPaginationInvalid, shopFile, tasks + "/pagination/pageSize",
		},
		{"empty mock of the wrong type", onSource("0", func(_ *testing.T, _, c map[string]any) {
			at(t, c, "mocks")["empty"] = "none"
		}), nil, plxerr.ValueTypeMismatch, shopFile, tasks + "/mocks/empty"},
		{"unknown error kind", onSource("0", func(_ *testing.T, _, c map[string]any) {
			at(t, c, "mocks/error")["kind"] = "boom"
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/mocks/error/kind"},
		{"operation without method", onSource("0", func(_ *testing.T, _, c map[string]any) {
			delete(at(t, c, "operations/createTask"), "method")
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/operations/createTask/method"},
		{"operation input not an object", onSource("0", func(_ *testing.T, _, c map[string]any) {
			at(t, c, "operations/createTask")["input"] = "string"
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, tasks + "/operations/createTask/input"},
		{
			"GraphQL source without query", onSource("2", func(_ *testing.T, _, c map[string]any) { delete(c, "query") }),
			nil, plxerr.DataSourceConfigInvalid, shopFile, "/dataSources/2/config/query",
		},
		{"GraphQL operation that is not a document", onSource("2", func(_ *testing.T, _, c map[string]any) {
			at(t, c, "operations/rename")["query"] = "rename"
		}), nil, plxerr.DataSourceConfigInvalid, shopFile, "/dataSources/2/config/operations/rename/query"},
		{
			"too many sources", nil, tighten(limits.DataSourcesPerPlugin, 2),
			plxerr.LimitExceeded, shopFile, "/dataSources",
		},
		{"unknown operation", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "plugins/shop/pages/tasks.page.json", func(doc map[string]any) {
				at(t, doc, "root/slots/body/children/2/events/onPressed/steps/0/input")["operation"] = "tasks.nope"
			})
		}, nil, plxerr.UnresolvedReference, "plugins/shop/pages/tasks.page.json", "/root/slots/body/children/2/events/onPressed/steps/0/input/operation"},
		{"operation input of the wrong type", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "plugins/shop/pages/tasks.page.json", func(doc map[string]any) {
				at(t, doc, "root/slots/body/children/2/events/onPressed/steps/0/input")["input"] = map[string]any{"title": 1}
			})
		}, nil, plxerr.PropTypeMismatch, "plugins/shop/pages/tasks.page.json", "/root/slots/body/children/2/events/onPressed/steps/0/input/input"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, dataDir)
			if c.edit != nil {
				c.edit(t, m)
			}
			opts := DefaultOptions()
			if c.opts != nil {
				c.opts(t, &opts)
			}
			wantDiag(t, Compile(m, opts), c.code, c.file, c.ptr)
		})
	}
}

// TestApiCallOutputIsTyped checks that a step's output is the operation's
// declared output, so later steps read its fields (DAT-001, DAT-004).
// Verifies: DAT-004.
func TestApiCallOutputIsTyped(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	edit(t, m, "plugins/shop/pages/tasks.page.json", func(doc map[string]any) {
		steps := at(t, doc, "root/slots/body/children/2/events/onPressed")
		steps["steps"] = raw(t, `[
			{"action": "apiCall", "id": "create", "input": {"operation": "tasks.createTask", "input": {"title": "New"}}, "next": "check"},
			{"action": "condition", "id": "check", "input": {"when": {"$expr": "steps.create.output?.done ?? false"}}}]`)
	})
	onlyRaised(t, compileFS(m))
	edit(t, m, "plugins/shop/pages/tasks.page.json", func(doc map[string]any) {
		at(t, doc, "root/slots/body/children/2/events/onPressed/steps/1/input")["when"] = raw(t, `{"$expr": "steps.create.output?.missing ?? false"}`)
	})
	if !compileFS(m).Diagnostics.HasErrors() {
		t.Error("a field the output type lacks was accepted")
	}
}

// TestDataSourcesInOlderRuntimesAreRejected checks that a project whose
// app does not allow raising required features cannot use the data layer
// below runtime 0.3.0 (WGT-004, BND-008).
// Verifies: BND-008.
func TestDataSourcesInOlderRuntimesAreRejected(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	edit(t, m, "app.json", func(doc map[string]any) { doc["requiredFeatures"] = "reject" })
	wantDiag(t, compileFS(m), plxerr.RuntimeTooOld, shopFile, "/dataSources/0")
}
