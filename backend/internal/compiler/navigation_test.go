// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"testing"
	"testing/fstest"

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
			name: "result of the wrong type", edit: both(onPage(func(t *testing.T, doc map[string]any) { doc["result"] = "int" }),
				withStep(`{"id": "extra", "action": "pop", "input": {"result": "seven"}}`)),
			code: plxerr.PropTypeMismatch, file: calculateGraph, ptr: "/steps/3/input/result",
		},
	}
}

// TestInvalidNavigation checks the diagnostics of the app's navigation,
// host events and typed results.
// Verifies: NAV-003, NAV-005, NAV-008, NAV-011, HST-013.
func TestInvalidNavigation(t *testing.T) {
	t.Parallel()
	for _, tc := range navigationCases() {
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
