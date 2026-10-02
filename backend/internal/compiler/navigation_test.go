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
)

// onApp edits app.json.
func onApp(f func(t *testing.T, doc map[string]any)) func(*testing.T, fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) {
		edit(t, m, "app.json", func(doc map[string]any) { f(t, doc) })
	}
}

// withNavigation sets the app's navigation.
func withNavigation(nav string) func(*testing.T, fstest.MapFS) {
	return onApp(func(t *testing.T, doc map[string]any) { doc["navigation"] = raw(t, nav) })
}

// tab is a valid shell tab opening route.
func tab(key, route string) string {
	return `{"key": "` + key + `", "label": "` + key + `", "icon": {"name": "home"}, "initialRoute": "` + route + `"}`
}

// withStep adds a step to the calculate graph after its last one.
func withStep(step string) func(*testing.T, fstest.MapFS) {
	return graphDoc(func(t *testing.T, doc map[string]any) {
		steps := doc["steps"].([]any)
		steps[len(steps)-1].(map[string]any)["next"] = "extra"
		doc["steps"] = append(steps, raw(t, step))
	})
}

const submitted = `[{"name": "loanSubmitted", "fields": [{"name": "amount", "type": "decimal"}, {"name": "note", "type": "string?"}]}]`

// navigationCases break the app's navigation, host events and typed
// results (ADR-0040).
func navigationCases() []invalidCase {
	events := onApp(func(t *testing.T, doc map[string]any) { doc["hostEvents"] = raw(t, submitted) })
	both := func(a, b func(*testing.T, fstest.MapFS)) func(*testing.T, fstest.MapFS) {
		return func(t *testing.T, m fstest.MapFS) { a(t, m); b(t, m) }
	}
	return []invalidCase{
		{
			name: "unknown not-found route", edit: withNavigation(`{"notFound": "nowhere"}`),
			code: plxerr.UnknownRoute, file: "app.json", ptr: "/navigation/notFound",
		},
		{
			name: "deep link to an unknown route", edit: withNavigation(`{"deepLinks": {"routes": [{"path": "/loans", "route": "nowhere"}]}}`),
			code: plxerr.UnknownRoute, file: "app.json", ptr: "/navigation/deepLinks/routes/0/route",
		},
		{
			name: "deep link to a native route", edit: withNavigation(`{"deepLinks": {"routes": [{"path": "/accounts", "route": "account-overview"}]}}`),
			code: plxerr.UnknownRoute, file: "app.json", ptr: "/navigation/deepLinks/routes/0/route",
		},
		{
			name: "deep link with an unknown parameter", edit: withNavigation(`{"deepLinks": {"routes": [{"path": "/loans/{product}", "route": "loan-calculator"}]}}`),
			code: plxerr.UnknownRouteParameter, file: "app.json", ptr: "/navigation/deepLinks/routes/0/path",
		},
		{
			name: "deep link reading an object parameter", edit: both(onPage(func(t *testing.T, doc map[string]any) {
				doc["params"] = raw(t, `[{"id": "01a0c450-6c00-7015-8000-00000002899b", "name": "productId", "required": true, "type": "list<string>", "mock": []}]`)
			}), withNavigation(`{"deepLinks": {"routes": [{"path": "/loans/{productId}", "route": "loan-calculator"}]}}`)),
			code: plxerr.RouteParameterTypeInvalid, file: "app.json", ptr: "/navigation/deepLinks/routes/0/path",
		},
		{
			name: "deep link naming a parameter twice", edit: withNavigation(`{"deepLinks": {"routes": [{"path": "/loans/{productId}/{productId}", "route": "loan-calculator"}]}}`),
			code: plxerr.DuplicateKey, file: "app.json", ptr: "/navigation/deepLinks/routes/0/path",
		},
		{
			name: "deep link under the reserved prefix", edit: withNavigation(`{"deepLinks": {"routes": [{"path": "/p/loans", "route": "loan-calculator"}]}}`),
			code: plxerr.ConstraintViolation, file: "app.json", ptr: "/navigation/deepLinks/routes/0/path",
		},
		{
			name: "deep links of the same shape", edit: withNavigation(`{"deepLinks": {"routes": [
				{"path": "/loans/{productId}", "route": "loan-calculator"}, {"path": "/loans/{x}", "route": "loan-calculator"}]}}`),
			code: plxerr.DuplicateKey, file: "app.json", ptr: "/navigation/deepLinks/routes/1/path",
		},
		{
			name: "duplicate tab", edit: withNavigation(`{"shells": [{"key": "main", "tabs": [` + tab("home", "loan-calculator") + `, ` + tab("home", "account-overview") + `]}]}`),
			code: plxerr.DuplicateKey, file: "app.json", ptr: "/navigation/shells/0/tabs/1/key",
		},
		{
			name: "duplicate shell", edit: withNavigation(`{"shells": [
				{"key": "main", "tabs": [` + tab("a", "loan-calculator") + `, ` + tab("b", "loan-calculator") + `]},
				{"key": "main", "tabs": [` + tab("c", "loan-calculator") + `, ` + tab("d", "loan-calculator") + `]}]}`),
			code: plxerr.DuplicateKey, file: "app.json", ptr: "/navigation/shells/1/key",
		},
		{
			name: "tab opening an unknown route", edit: withNavigation(`{"shells": [{"key": "main", "tabs": [` + tab("a", "loan-calculator") + `, ` + tab("b", "nowhere") + `]}]}`),
			code: plxerr.UnknownRoute, file: "app.json", ptr: "/navigation/shells/0/tabs/1/initialRoute",
		},
		{
			name: "tab label of the wrong type", edit: withNavigation(`{"shells": [{"key": "main", "tabs": [
				{"key": "a", "label": 3, "icon": {"name": "home"}, "initialRoute": "loan-calculator"}, ` + tab("b", "loan-calculator") + `]}]}`),
			code: plxerr.PropTypeMismatch, file: "app.json", ptr: "/navigation/shells/0/tabs/0/label",
		},
		{
			name: "tab label bound to an expression of the wrong type", edit: withNavigation(`{"shells": [{"key": "main", "tabs": [
				{"key": "a", "label": {"$expr": "1 + 1"}, "icon": {"name": "home"}, "initialRoute": "loan-calculator"}, ` + tab("b", "loan-calculator") + `]}]}`),
			code: plxerr.PropTypeMismatch, file: "app.json", ptr: "/navigation/shells/0/tabs/0/label",
		},
		{
			name: "switch to an unknown tab", edit: withStep(`{"id": "extra", "action": "switchTab", "input": {"tab": "settings"}}`),
			code: plxerr.UnresolvedReference, file: calculateGraph, ptr: "/steps/3/input/tab",
		},
		{name: "duplicate host event", edit: onApp(func(t *testing.T, doc map[string]any) {
			doc["hostEvents"] = raw(t, `[{"name": "opened"}, {"name": "opened"}]`)
		}), code: plxerr.DuplicateKey, file: "app.json", ptr: "/hostEvents/1/name"},
		{name: "host event field of an unknown type", edit: onApp(func(t *testing.T, doc map[string]any) {
			doc["hostEvents"] = raw(t, `[{"name": "opened", "fields": [{"name": "loan", "type": "Loan"}]}]`)
		}), code: plxerr.UnknownType, file: "app.json", ptr: "/hostEvents/0/fields/0/type"},
		{
			name: "unknown host event", edit: withStep(`{"id": "extra", "action": "emitHostEvent", "input": {"event": "loanApproved"}}`),
			code: plxerr.UnresolvedReference, file: calculateGraph, ptr: "/steps/3/input/event",
		},
		{
			name: "host event payload missing a field", edit: both(events, withStep(`{"id": "extra", "action": "emitHostEvent",
				"input": {"event": "loanSubmitted", "payload": {"note": "x"}}}`)),
			code: plxerr.MissingRequiredProp, file: calculateGraph, ptr: "/steps/3/input/payload",
		},
		{
			name: "host event payload with an unknown field", edit: both(events, withStep(`{"id": "extra", "action": "emitHostEvent",
				"input": {"event": "loanSubmitted", "payload": {"amount": "1", "term": 12}}}`)),
			code: plxerr.UnknownProp, file: calculateGraph, ptr: "/steps/3/input/payload/term",
		},
		{
			name: "host event payload of the wrong type", edit: both(events, withStep(`{"id": "extra", "action": "emitHostEvent",
				"input": {"event": "loanSubmitted", "payload": {"amount": true}}}`)),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/payload/amount",
		},
		{name: "user context named like the built-in", edit: onApp(func(t *testing.T, doc map[string]any) {
			doc["userContext"] = append(doc["userContext"].([]any), raw(t, `{"name": "authenticated", "type": "bool"}`))
		}), code: plxerr.DuplicateKey, file: "app.json", ptr: "/userContext/2/name"},
		{
			name: "result from a page that declares none", edit: withStep(`{"id": "extra", "action": "pop", "input": {"result": 1}}`),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/result",
		},
		{
			name: "dialog result from a page that declares none", edit: withStep(`{"id": "extra", "action": "openDialog",
				"input": {"route": "loan-calculator", "params": {"productId": "p"}, "dismissible": {"$expr": "steps.extra.output == null"}}}`),
			code: plxerr.PXLUnknownField, file: calculateGraph, ptr: "/steps/3/input/dismissible",
		},
		{
			name: "stop result from a graph that declares no output", edit: withStep(`{"id": "extra", "action": "stop", "input": {"result": 1}}`),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/result",
		},
		{
			name: "stop result of the wrong type", edit: both(graphDoc(func(t *testing.T, doc map[string]any) { doc["output"] = "int" }),
				withStep(`{"id": "extra", "action": "stop", "input": {"result": "seven"}}`)),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/result",
		},
		{
			name: "result of the wrong type", edit: both(onPage(func(t *testing.T, doc map[string]any) { doc["result"] = "int" }),
				withStep(`{"id": "extra", "action": "pop", "input": {"result": "seven"}}`)),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/result",
		},
	}
}

// guardID names the guard flows the guard cases add, by page.
var guardID = map[string]string{
	calculatorPage: "01a0c450-6c00-7099-8000-0000000000a1",
	resultPage:     "01a0c450-6c00-7099-8000-0000000000a2",
}

// withGuard adds a guard flow returning a GuardResult through steps to
// the loans plugin, and guards the page with it.
func withGuard(page, steps string) func(*testing.T, fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) {
		id := guardID[page]
		m["plugins/loans/actions/guard-"+id[len(id)-2:]+".graph.json"] = &fstest.MapFile{Data: []byte(`{"id": "` + id +
			`", "key": "guard-` + id[len(id)-2:] + `", "kind": "actionGraph", "output": "GuardResult", "schemaVersion": "1.0.0", "steps": ` + steps + `}`)}
		edit(t, m, page, func(doc map[string]any) { doc["routeOptions"] = raw(t, `{"guards": [{"$graph": "`+id+`"}]}`) })
	}
}

// redirectTo is a guard's steps that always redirect with this result.
func redirectTo(result string) string {
	return `[{"id": "go", "action": "stop", "input": {"result": ` + result + `}}]`
}

// guardFile is the document withGuard adds for the page.
func guardFile(page string) string {
	id := guardID[page]
	return "plugins/loans/actions/guard-" + id[len(id)-2:] + ".graph.json"
}

// guardCases break route guards (NAV-009, ADR-0040).
func guardCases() []invalidCase {
	return []invalidCase{
		{
			name: "guard without a GuardResult", edit: onPage(func(t *testing.T, doc map[string]any) {
				doc["routeOptions"] = raw(t, `{"guards": [{"$graph": "01a0c450-6c00-7021-8000-00000003fccf"}]}`)
			}),
			code: plxerr.InvalidActionGraph, file: calculatorPage, ptr: "/routeOptions/guards/0/$graph",
		},
		{
			name: "guard flow with inputs", edit: func(t *testing.T, m fstest.MapFS) {
				withGuard(calculatorPage, redirectTo(`"allow"`))(t, m)
				edit(t, m, guardFile(calculatorPage), func(doc map[string]any) {
					doc["inputs"] = raw(t, `[{"name": "who", "type": "string"}]`)
				})
			},
			code: plxerr.InvalidActionGraph, file: calculatorPage, ptr: "/routeOptions/guards/0/$graph",
		},
		{
			name: "guard redirect to an unknown route", edit: withGuard(calculatorPage, redirectTo(`{"decision": "redirect", "route": "nowhere"}`)),
			code: plxerr.UnknownRoute, file: guardFile(calculatorPage), ptr: "/steps/0/input/result/route",
		},
		{
			name: "guard redirect with an unknown parameter", edit: withGuard(resultPage,
				redirectTo(`{"decision": "redirect", "route": "loan-calculator", "params": {"productId": "p", "product": "p"}}`)),
			code: plxerr.UnknownRouteParameter, file: guardFile(resultPage), ptr: "/steps/0/input/result/params/product",
		},
		{
			name: "guard redirect to a parameter no text carries", edit: withGuard(calculatorPage,
				redirectTo(`{"decision": "redirect", "route": "result", "params": {"schedule": "s"}}`)),
			code: plxerr.RouteParameterTypeInvalid, file: guardFile(calculatorPage), ptr: "/steps/0/input/result/params/schedule",
		},
		{
			name: "guard redirect without a required parameter", edit: withGuard(resultPage,
				redirectTo(`{"decision": "redirect", "route": "loan-calculator"}`)),
			code: plxerr.RouteParameterMissing, file: guardFile(resultPage), ptr: "/steps/0/input/result/params",
		},
		{
			name: "guard redirect loop", edit: func(t *testing.T, m fstest.MapFS) {
				withGuard(calculatorPage, redirectTo(`{"decision": "redirect", "route": "result"}`))(t, m)
				withGuard(resultPage, redirectTo(`{"decision": "redirect", "route": "loan-calculator", "params": {"productId": "p"}}`))(t, m)
			},
			code: plxerr.RedirectLoop, file: guardFile(calculatorPage), ptr: "/steps/0/input/result",
		},
		{
			name: "guarded page on an older runtime", edit: onApp(func(t *testing.T, doc map[string]any) { doc["minRuntimeVersion"] = "0.1.0" }),
			code: plxerr.RuntimeTooOld, file: calculatorPage, ptr: "/security/requiresAssurance",
		},
	}
}

// TestInvalidNavigation checks the diagnostics of the app's navigation,
// host events and typed results.
// Verifies: NAV-003, NAV-005, NAV-008, NAV-011, HST-013.
func TestInvalidNavigation(t *testing.T) {
	t.Parallel()
	for _, tc := range append(navigationCases(), guardCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := fixture(t)
			tc.edit(t, m)
			res := compileFS(m)
			wantDiag(t, res, tc.code, tc.file, tc.ptr)
		})
	}
}

// TestNavigationCompiles checks that a project using every addition —
// the not-found route, deep links, a shell, push, host events, a typed
// result presented and returned, the built-in user.authenticated —
// compiles without diagnostics.
// Verifies: NAV-005, NAV-008, NAV-011, HST-013.
func TestNavigationCompiles(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	onApp(func(t *testing.T, doc map[string]any) {
		doc["navigation"] = raw(t, `{
			"notFound": "loan-calculator",
			"deepLinks": {"hosts": ["loans.example.com"], "schemes": ["acme"],
				"routes": [{"path": "/loans/{productId}", "route": "loan-calculator"}, {"path": "/loans", "route": "loan-calculator"}]},
			"shells": [{"key": "main", "tabs": [`+tab("loans", "loan-calculator")+`,
				{"key": "account", "label": {"$expr": "\"Acc\" + \"ount\""}, "icon": {"name": "home"}, "initialRoute": "account-overview"}]}]}`)
		doc["push"] = raw(t, `{"enabled": true, "payloadKey": "data.plux"}`)
		doc["hostEvents"] = raw(t, submitted)
	})(t, m)
	onPage(func(t *testing.T, doc map[string]any) { doc["result"] = "decimal" })(t, m)
	graphDoc(func(t *testing.T, doc map[string]any) {
		steps := doc["steps"].([]any)
		steps[len(steps)-1].(map[string]any)["next"] = "ask"
		doc["steps"] = append(steps,
			raw(t, `{"id": "ask", "action": "openDialog", "input": {"route": "loan-calculator", "params": {"productId": "p"}}, "next": "emit"}`),
			raw(t, `{"id": "emit", "action": "emitHostEvent", "input": {"event": "loanSubmitted", "payload": {"amount": {"$expr": "steps.ask.output ?? 0d"}}}, "next": "tab"}`),
			raw(t, `{"id": "tab", "action": "switchTab", "input": {"tab": "account"}, "next": "check"}`),
			raw(t, `{"id": "check", "action": "condition", "input": {"when": {"$expr": "user.authenticated"}}, "branches": {"then": "done"}}`),
			raw(t, `{"id": "done", "action": "pop", "input": {"result": "1200.50"}}`))
	})(t, m)
	res := compileFS(m)
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	if res.App == nil {
		t.Fatal("no app bundle")
	}
}

// TestGuardGraphCompiles checks that a graph declaring GuardResult as its
// output returns one with stop, and that the type needs runtime 0.2.0.
// Verifies: NAV-009.
func TestGuardGraphCompiles(t *testing.T) {
	t.Parallel()
	guard := func(t *testing.T, m fstest.MapFS) {
		graphDoc(func(t *testing.T, doc map[string]any) {
			doc["output"] = "GuardResult"
			steps := doc["steps"].([]any)
			steps[len(steps)-1].(map[string]any)["next"] = "redirect"
			doc["steps"] = append(steps, raw(t, `{"id": "redirect", "action": "stop",
				"input": {"result": {"decision": "redirect", "route": "loan-calculator", "params": {"productId": "p"}}}}`))
		})(t, m)
	}
	m := fixture(t)
	guard(t, m)
	onApp(func(t *testing.T, doc map[string]any) { doc["minRuntimeVersion"] = "0.1.0" })(t, m)
	wantDiag(t, compileFS(m), plxerr.RuntimeTooOld, calculateGraph, "/steps/3/input/result")

	m = fixture(t)
	guard(t, m)
	onApp(func(t *testing.T, doc map[string]any) { doc["minRuntimeVersion"] = "0.2.0" })(t, m)
	if res := compileFS(m); len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
}

// TestNavigationIsEncoded checks that the app bundle carries the
// not-found route and the shells with their tabs, and that a page section
// carries its result type (ADR-0040).
// Verifies: NAV-003, NAV-005, NAV-011.
func TestNavigationIsEncoded(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	onApp(func(t *testing.T, doc map[string]any) {
		doc["navigation"] = raw(t, `{"notFound": "loan-calculator", "shells": [{"key": "main", "tabs": [`+
			tab("loans", "loan-calculator")+`, `+tab("account", "account-overview")+`]}]}`)
	})(t, m)
	onPage(func(t *testing.T, doc map[string]any) { doc["result"] = "decimal" })(t, m)
	res := compileFS(m)
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	read := readAll(t, res)

	meta := read[0].Meta
	if got := string(meta.NotFoundRoute()); got != "loan-calculator" {
		t.Errorf("not-found route %q", got)
	}
	var shell fbs.Shell
	if meta.ShellsLength() != 1 || !meta.Shells(&shell, 0) || string(shell.Key()) != "main" || shell.TabsLength() != 2 {
		t.Fatalf("shells: %d", meta.ShellsLength())
	}
	var tab fbs.ShellTab
	shell.Tabs(&tab, 1)
	if string(tab.Key()) != "account" || string(tab.InitialRoute()) != "account-overview" || tab.Label(nil) == nil || tab.Icon(nil) == nil {
		t.Errorf("tab %s → %s", tab.Key(), tab.InitialRoute())
	}

	results := map[string]string{}
	for _, s := range read[1].Sections {
		if s.Kind != bundle.SectionPage {
			continue
		}
		p := fbs.GetRootAsPage(s.Data, 0)
		route := string(p.Strings(int(p.Route())))
		results[route] = ""
		if r := p.Result(); r != 0 {
			results[route] = string(p.Strings(int(r)))
		}
	}
	if results["loan-calculator"] != "decimal" || len(results) != 2 {
		t.Errorf("page results %v", results)
	}
	for route, r := range results {
		if route != "loan-calculator" && r != "" {
			t.Errorf("page %s has result %q, declares none", route, r)
		}
	}
}

// routingDir is the conformance project of the action engine and
// navigation (ADR-0039, ADR-0040), whose bundles the runtime's tests run.
var routingDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "routing")

// TestRoutingGoldenBundles pins the bundles of the routing project byte
// for byte; the runtime's tests run its graphs and pages.
// Verifies: CMP-002, QA-003.
func TestRoutingGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(routingDir), DefaultOptions())
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	readAll(t, res)
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "routing", b.Key+".pxb"), b.Data)
	}
}

// TestGuardsAndLinksAreEncoded checks that a plugin with a guarded page
// requires navigation.guards.v1 when its app allows older runtimes, so
// they never open the page unguarded, and that the app bundle carries the
// deep links and the push payload key, `plux` by default (ADR-0040).
// Verifies: NAV-008, NAV-009.
func TestGuardsAndLinksAreEncoded(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	onApp(func(t *testing.T, doc map[string]any) {
		doc["minRuntimeVersion"] = "0.1.0"
		doc["requiredFeatures"] = "raise"
		doc["navigation"] = raw(t, `{"deepLinks": {"hosts": ["loans.example.com"], "schemes": ["acme"],
			"routes": [{"path": "/loans/{productId}", "route": "loan-calculator"}]}}`)
		doc["push"] = raw(t, `{"enabled": true}`)
	})(t, m)
	res := compileFS(m)
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	if !slices.Contains(res.Plugins[0].Features, "navigation.guards.v1") {
		t.Errorf("plugin features %v lack navigation.guards.v1", res.Plugins[0].Features)
	}
	meta := readAll(t, res)[0].Meta
	links := meta.DeepLinks(nil)
	if links == nil || links.HostsLength() != 1 || string(links.Hosts(0)) != "loans.example.com" ||
		links.SchemesLength() != 1 || string(links.Schemes(0)) != "acme" || links.RoutesLength() != 1 {
		t.Fatalf("deep links not encoded")
	}
	var r fbs.DeepLinkRoute
	links.Routes(&r, 0)
	if string(r.Path()) != "/loans/{productId}" || string(r.Route()) != "loan-calculator" {
		t.Errorf("deep link %s → %s", r.Path(), r.Route())
	}
	push := meta.Push(nil)
	if push == nil || !push.Enabled() || string(push.PayloadKey()) != "plux" {
		t.Errorf("push not encoded with the default key")
	}

	// Without them, neither is written.
	res = compileFS(fixture(t))
	if meta := readAll(t, res)[0].Meta; meta.DeepLinks(nil) != nil || meta.Push(nil) != nil {
		t.Errorf("deep links or push written for an app that declares neither")
	}
}
