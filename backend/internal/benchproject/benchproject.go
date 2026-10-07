// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package benchproject builds the project the P3 performance benchmarks
// run (QA-007, docs/benchmarks/p3-runtime.md): an app whose first plugin
// has a page of exactly [CatalogNodes] nodes (NFR-002, NFR-003), whose
// second has a 500-item list bound through PXL for frame times, whose
// third has the 1,000-item list NFR-004 scrolls, and whose others are
// small pages, so the device holds as many plugins as a benchmark asks
// for (NFR-008). A revision changes a text in the first three plugins
// only: the typical update of NFR-007.
//
// The documents are built in code rather than committed, because a
// 300-node page and fifty plugins are data no one reviews by reading; the
// compiled, signed baseline the runtime benchmark embeds is committed and
// pinned by this package's test.
package benchproject

import (
	"encoding/json"
	"fmt"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/icons/fonts"
)

// AppID is the benchmark app's ID.
const AppID = "01f0c450-6c00-7000-8000-000000000001"

// CatalogNodes is the node count of the catalog page, the page size
// NFR-002 and NFR-003 name.
const CatalogNodes = 300

// FeedItems is the length of the feed page's list.
const FeedItems = 500

// ListItems is the length of the list page's list, the list size NFR-004
// names.
const ListItems = 1000

// Changed is how many plugins a new revision changes (NFR-007).
const Changed = 3

// catalogItems is the catalog's repeated rows; with the page's frame
// they make CatalogNodes nodes (see catalogPage).
const catalogItems = 29

// id is a UUID unique in the project: group 0 is the app's documents,
// group n the n-th plugin's.
func id(group, n int) string {
	return fmt.Sprintf("01f0c450-6c00-7000-8%03x-%012x", group, n)
}

// node is one widget node of a page document.
type node map[string]any

// ids hands out a plugin's node IDs.
type ids struct {
	group, next int
}

func (s *ids) id() string {
	s.next++
	return id(s.group, s.next)
}

// w is a node of type t with props, slots and children (each optional).
func (s *ids) w(t string, props map[string]any, slots map[string]node, children ...node) node {
	n := node{"id": s.id(), "type": t}
	if len(props) > 0 {
		n["props"] = props
	}
	if len(slots) > 0 {
		n["slots"] = slots
	}
	if len(children) > 0 {
		n["children"] = children
	}
	return n
}

func (s *ids) text(data any, props map[string]any) node {
	p := map[string]any{"data": data}
	for k, v := range props {
		p[k] = v
	}
	return s.w("Text", p, nil)
}

// revisionText is the text a revision changes.
func revisionText(revision int) string {
	return fmt.Sprintf("Revision %d", revision)
}

// scaffold is a page frame: Scaffold, AppBar and title (3 nodes) around
// body.
func (s *ids) scaffold(title string, body node) node {
	return s.w("Scaffold", nil, map[string]node{
		"appBar": s.w("AppBar", nil, map[string]node{"title": s.text(title, nil)}),
		"body":   body,
	})
}

// catalogPage has CatalogNodes nodes: the frame (3), a scroll view and
// its column (2), 29 rows of 10 and a footer of 5.
func catalogPage(s *ids, revision int) node {
	var rows []node
	for i := range catalogItems {
		rows = append(rows, s.w("Padding", map[string]any{"padding": map[string]any{"all": 8.0}}, map[string]node{
			"child": s.w("Row", nil, nil,
				s.w("Icon", map[string]any{"icon": map[string]any{"name": "shopping_bag"}, "size": 32.0}, nil),
				s.w("SizedBox", map[string]any{"width": 12.0}, nil),
				s.w("Expanded", nil, map[string]node{"child": s.w("Column",
					map[string]any{"crossAxisAlignment": "start"}, nil,
					s.text(fmt.Sprintf("Product %d", i+1), map[string]any{"style": map[string]any{"fontWeight": "w600"}}),
					s.text("A short description of the product, long enough to wrap onto a second line.", nil),
					s.text(fmt.Sprintf("SKU-%04d", 1000+i), nil),
				)}),
				s.text(fmt.Sprintf("€%d.99", 9+i), nil),
			),
		}))
	}
	footer := s.w("Padding", map[string]any{"padding": map[string]any{"all": 16.0}}, map[string]node{
		"child": s.w("Column", nil, nil,
			s.text(revisionText(revision), nil),
			s.w("Divider", nil, nil),
			s.text("bench-end", nil),
		),
	})
	return s.scaffold("Catalog", s.w("SingleChildScrollView", nil, map[string]node{
		"child": s.w("Column", nil, nil, append(rows, footer)...),
	}))
}

// boundList is a page of a lazily built list of n ListTile rows whose
// texts are bound through PXL; the items are "<label> 1" to "<label> n".
func boundList(s *ids, title, label string, n, revision int) (node, []any) {
	items := make([]any, n)
	for i := range items {
		items[i] = fmt.Sprintf("%s %d", label, i+1)
	}
	list := s.w("ListView", map[string]any{"items": map[string]any{"$expr": "page.items"}}, map[string]node{
		"item": s.w("ListTile", nil, map[string]node{
			"leading":  s.w("Icon", map[string]any{"icon": map[string]any{"name": "article"}}, nil),
			"title":    s.text(map[string]any{"$expr": "item"}, nil),
			"subtitle": s.text(map[string]any{"$expr": `"#" + string(index)`}, nil),
		}),
	})
	body := s.w("Column", nil, nil, s.text(revisionText(revision), nil), s.w("Expanded", nil, map[string]node{"child": list}))
	return s.scaffold(title, body), items
}

// feedPage is a list of FeedItems rows bound through PXL.
func feedPage(s *ids, revision int) (node, []any) {
	return boundList(s, "Feed", "Story", FeedItems, revision)
}

// listPage is the feed's twin with ListItems rows, the list NFR-004
// scrolls.
func listPage(s *ids, revision int) (node, []any) {
	return boundList(s, "List", "Item", ListItems, revision)
}

// smallPage is the page of every other plugin.
func smallPage(s *ids, key string, revision int) node {
	return s.scaffold(key, s.w("Center", nil, map[string]node{
		"child": s.w("Column", map[string]any{"mainAxisSize": "min"}, nil,
			s.text("A small plugin page.", nil),
			s.text(revisionText(revision), nil),
		),
	}))
}

// PluginKey is the key of the i-th plugin (from 0): catalog, feed, list,
// then extra01, extra02 and so on.
func PluginKey(i int) string {
	switch i {
	case 0:
		return "catalog"
	case 1:
		return "feed"
	case 2:
		return "list"
	default:
		return fmt.Sprintf("extra%02d", i-2)
	}
}

// Project returns the documents of the benchmark project with the given
// number of plugins (at least Changed) at a revision: the first Changed
// plugins show the revision, the others always show revision 1.
func Project(plugins, revision int) fstest.MapFS {
	if plugins < Changed {
		plugins = Changed
	}
	fs := fstest.MapFS{}
	put := func(path string, v any) {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			panic(err) // maps of strings, numbers and lists always marshal
		}
		fs[path] = &fstest.MapFile{Data: append(data, '\n'), Mode: 0o644}
	}
	keys := make([]string, plugins)
	for i := range plugins {
		keys[i] = PluginKey(i)
	}
	put("app.json", map[string]any{
		"schemaVersion": "1.0.0", "kind": "app", "id": AppID, "key": "bench", "name": "Plux benchmarks",
		"defaultLocale": "en", "supportedLocales": []string{"en"}, "entryRoute": "catalog",
		"environments":      []map[string]any{{"key": "production", "name": "Production"}, {"key": "staging", "name": "Staging"}},
		"icon":              map[string]any{"monogram": map[string]any{"background": "#1F6FEB", "text": "PB"}},
		"minRuntimeVersion": "0.1.0", "plugins": keys, "securityProfile": "standard",
		"sync":  map[string]any{"activation": "atSafePoint", "startup": map[string]any{"mode": "useCacheThenSync"}},
		"theme": id(0, 2),
	})
	put("theme.json", map[string]any{
		"schemaVersion": "1.0.0", "kind": "theme", "id": id(0, 2), "key": "bench", "name": "Bench",
		"tokens": map[string]any{"color": map[string]any{"$type": "color", "primary": map[string]any{"$value": "#1F6FEB"}}},
	})
	for i, key := range keys {
		group := i + 1
		s := &ids{group: group, next: 16}
		rev := 1
		if i < Changed {
			rev = revision
		}
		page := map[string]any{
			"schemaVersion": "1.0.0", "kind": "page", "id": id(group, 2), "key": key, "pageKind": "screen", "route": key, "title": key,
		}
		switch i {
		case 0:
			page["root"] = catalogPage(s, rev)
		case 1, 2:
			build := feedPage
			if i == 2 {
				build = listPage
			}
			root, items := build(s, rev)
			page["root"] = root
			page["state"] = []map[string]any{{"id": s.id(), "name": "items", "type": "list<string>", "default": items}}
		default:
			page["root"] = smallPage(s, key, rev)
		}
		put("plugins/"+key+"/plugin.json", map[string]any{
			"schemaVersion": "1.0.0", "kind": "plugin", "id": id(group, 1), "key": key, "name": key, "team": "bench",
			"entryPage": id(group, 2), "pages": []string{id(group, 2)},
			"icon": map[string]any{"monogram": map[string]any{"background": "#1F6FEB", "text": "B"}},
		})
		put("plugins/"+key+"/pages/"+key+".page.json", page)
	}
	return fs
}

// Options are the compiler options the server publishes with: icon fonts
// subset from the embedded sets (ADR-0032 § Icons).
func Options() compiler.Options {
	opts := compiler.DefaultOptions()
	opts.IconFont = fonts.Build
	return opts
}
