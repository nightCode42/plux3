// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

const (
	listFile   = "plugins/tasks/pages/list.page.json"
	tasksFile  = "plugins/tasks/plugin.json"
	badgeFile  = "components/badge.component.json"
	notifyFile = "plugins/tasks/actions/notify.graph.json"
	pressSteps = "root/slots/body/children/8/events/onPressed/steps"
)

// features compiles the features project after the edits.
func features(t *testing.T, edits ...func(m fstest.MapFS)) *Result {
	t.Helper()
	m := project(t, featuresDir)
	for _, e := range edits {
		e(m)
	}
	return compileFS(m)
}

// older lets the app run on runtimes before 0.3.0 and raise required
// features, so the features a bundle needs are listed (BND-008).
func older(t *testing.T) func(m fstest.MapFS) {
	return func(m fstest.MapFS) {
		edit(t, m, "app.json", func(doc map[string]any) {
			doc["minRuntimeVersion"] = "0.2.0"
			doc["requiredFeatures"] = "raise"
		})
	}
}

// noErrors fails the test on any error; warnings such as PLX-1120 pass.
func noErrors(t *testing.T, res *Result) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Severity == plxerr.SeverityError {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	}
}

// clean fails the test on any diagnostic.
func clean(t *testing.T, res *Result) {
	t.Helper()
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
}

// metaOf returns the meta section of a read bundle.
func metaOf(t *testing.T, b *bundle.Bundle) *fbs.Meta {
	t.Helper()
	if b.Meta == nil {
		t.Fatal("no meta")
	}
	return b.Meta
}

// triggersOf lists the kind, name and policy of each trigger.
type triggerRow struct {
	kind   fbs.TriggerKind
	name   string
	policy fbs.Concurrency
	every  uint32
	repeat bool
}

func pageTriggers(p *fbs.Page) []triggerRow {
	var out []triggerRow
	var tr fbs.Trigger
	var h fbs.Handler
	for i := range p.TriggersLength() {
		p.Triggers(&tr, i)
		tr.Handler(&h)
		out = append(out, triggerRow{tr.Kind(), string(tr.Name()), h.Concurrency(), tr.IntervalMs(), tr.Repeat()})
	}
	return out
}

func metaTriggers(m *fbs.Meta) []triggerRow {
	var out []triggerRow
	var tr fbs.Trigger
	var h fbs.Handler
	for i := range m.TriggersLength() {
		m.Triggers(&tr, i)
		tr.Handler(&h)
		out = append(out, triggerRow{tr.Kind(), string(tr.Name()), h.Concurrency(), tr.IntervalMs(), tr.Repeat()})
	}
	return out
}

// TestTriggersCompile_ACT_002 checks that pages, plugins and the app
// declare every kind of trigger, typed `event` included, and that each is
// encoded with its handler's default policy.
// Verifies: ACT-002, ACT-003, ACT-020.
func TestTriggersCompile_ACT_002(t *testing.T) {
	t.Parallel()
	res := features(t, func(m fstest.MapFS) {
		edit(t, m, listFile, func(doc map[string]any) {
			doc["triggers"] = raw(t, `{
				"timers": [{"name": "poll", "intervalMs": 5000, "handler": {"steps": [
					{"id": "t", "action": "condition", "input": {"when": {"$expr": "event > 2"}}}]}}],
				"watch": [{"path": "page.priority", "handler": {"concurrency": "debounce:300", "steps": [
					{"id": "w", "action": "condition", "input": {"when": {"$expr": "event == 'high'"}}}]}}],
				"onAppResume": {"steps": [{"id": "r", "action": "sync"}]},
				"hostEvents": {"taskCompleted": {"steps": [
					{"id": "h", "action": "trackEvent", "input": {"name": "done", "props": {"title": {"$expr": "event.title"}}}}]}},
				"dataSources": {"summary": {"onLoaded": {"steps": [
					{"id": "d", "action": "condition", "input": {"when": {"$expr": "event > 0"}}}]},
					"onFailed": {"steps": [{"id": "f", "action": "condition", "input": {"when": {"$expr": "event.kind == 'network'"}}}]}}},
				"onError": {"steps": [{"id": "e", "action": "condition", "input": {"when": {"$expr": "event.status == 404 || event.code == 'x'"}}}]}
			}`)
		})
		edit(t, m, tasksFile, func(doc map[string]any) {
			doc["triggers"] = raw(t, `{
				"watch": [{"path": "plugin.filter", "handler": {"steps": [{"id": "w", "action": "condition", "input": {"when": {"$expr": "event == 'open'"}}}]}}],
				"onError": {"steps": [{"id": "e", "action": "condition", "input": {"when": {"$expr": "event.kind == 'custom'"}}}]}
			}`)
		})
		edit(t, m, "app.json", func(doc map[string]any) {
			doc["triggers"] = raw(t, `{
				"timers": [{"name": "once", "intervalMs": 100, "repeat": false, "handler": {"steps": [{"id": "s", "action": "sync"}]}}],
				"watch": [{"path": "app.visits", "handler": {"steps": [{"id": "w", "action": "condition", "input": {"when": {"$expr": "event > 1"}}}]}}],
				"onPushOpened": {"steps": [{"id": "p", "action": "sync"}]},
				"onAppPause": {"steps": [{"id": "q", "action": "sync"}]}
			}`)
		})
	})
	clean(t, res)
	read := readAll(t, res)
	pg := fbs.GetRootAsPage(mustSection(t, read[1], bundle.SectionPage, listPageID).Data, 0)
	want := []triggerRow{
		{fbs.TriggerKindTimer, "poll", fbs.ConcurrencyDrop, 5000, true},
		{fbs.TriggerKindStateChange, "page.priority", fbs.ConcurrencyDebounce, 0, false},
		{fbs.TriggerKindAppResume, "", fbs.ConcurrencyQueue, 0, false},
		{fbs.TriggerKindHostEvent, "taskCompleted", fbs.ConcurrencyQueue, 0, false},
		{fbs.TriggerKindDataLoaded, "summary", fbs.ConcurrencyQueue, 0, false},
		{fbs.TriggerKindDataFailed, "summary", fbs.ConcurrencyQueue, 0, false},
		{fbs.TriggerKindError, "", fbs.ConcurrencyQueue, 0, false},
	}
	if got := pageTriggers(pg); !slices.Equal(got, want) {
		t.Errorf("page triggers\n got %v\nwant %v", got, want)
	}
	if got := metaTriggers(metaOf(t, read[1])); !slices.Equal(got, []triggerRow{
		{fbs.TriggerKindStateChange, "plugin.filter", fbs.ConcurrencyRestart, 0, false},
		{fbs.TriggerKindError, "", fbs.ConcurrencyQueue, 0, false},
	}) {
		t.Errorf("plugin triggers %v", got)
	}
	if got := metaTriggers(metaOf(t, read[0])); !slices.Equal(got, []triggerRow{
		{fbs.TriggerKindTimer, "once", fbs.ConcurrencyDrop, 100, false},
		{fbs.TriggerKindStateChange, "app.visits", fbs.ConcurrencyRestart, 0, false},
		{fbs.TriggerKindAppPause, "", fbs.ConcurrencyQueue, 0, false},
		{fbs.TriggerKindPushOpened, "", fbs.ConcurrencyQueue, 0, false},
	}) {
		t.Errorf("app triggers %v", got)
	}
	// The app's inline trigger graphs travel in the app bundle.
	if !slices.ContainsFunc(read[0].Sections, func(s bundle.Section) bool { return s.Kind == bundle.SectionActions }) {
		t.Error("the app bundle has no actions section")
	}
}

// TestTriggerReferencesChecked_ACT_002 checks what a trigger names.
// Verifies: ACT-002.
func TestTriggerReferencesChecked_ACT_002(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		file, triggers, ptr string
	}{
		"unknown host event":   {listFile, `{"hostEvents": {"nope": {"steps": [{"id": "s", "action": "sync"}]}}}`, "/triggers/hostEvents/nope"},
		"unknown data source":  {listFile, `{"dataSources": {"nope": {"onLoaded": {"steps": [{"id": "s", "action": "sync"}]}}}}`, "/triggers/dataSources/nope/onLoaded"},
		"unknown state entry":  {listFile, `{"watch": [{"path": "page.nope", "handler": {"steps": [{"id": "s", "action": "sync"}]}}]}`, "/triggers/watch/0/path"},
		"page entry of plugin": {tasksFile, `{"watch": [{"path": "page.priority", "handler": {"steps": [{"id": "s", "action": "sync"}]}}]}`, "/triggers/watch/0/path"},
		"plugin entry of app":  {"app.json", `{"watch": [{"path": "plugin.filter", "handler": {"steps": [{"id": "s", "action": "sync"}]}}]}`, "/triggers/watch/0/path"},
		"duplicate timer": {listFile, `{"timers": [
			{"name": "a", "intervalMs": 10, "handler": {"steps": [{"id": "s", "action": "sync"}]}},
			{"name": "a", "intervalMs": 20, "handler": {"steps": [{"id": "s", "action": "sync"}]}}]}`, "/triggers/timers/1/name"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := features(t, func(m fstest.MapFS) {
				edit(t, m, c.file, func(doc map[string]any) { doc["triggers"] = raw(t, c.triggers) })
			})
			code := plxerr.InvalidTrigger
			if name == "duplicate timer" {
				code = plxerr.DuplicateKey
			}
			wantDiag(t, res, code, c.file, c.ptr)
		})
	}
	res := features(t, func(m fstest.MapFS) {
		edit(t, m, "app.json", func(doc map[string]any) {
			doc["triggers"] = raw(t, `{"onError": {"$graph": "01b0c450-6c00-7000-8000-00000000000d"}}`)
		})
	})
	wantDiag(t, res, plxerr.UnresolvedReference, "app.json", "/triggers/onError/$graph")
}

// TestConcurrencyEncoding_ACT_003 checks the encoding of policies: an
// absent policy is the trigger's default, drop for widget events, and a
// declared policy older runtimes cannot run requires actions.concurrency.v1,
// which also marks a declared parallel (ADR-0039's open point).
// Verifies: ACT-003, ACT-004, BND-008.
func TestConcurrencyEncoding_ACT_003(t *testing.T) {
	t.Parallel()
	press := func(policy string, detached bool) func(m fstest.MapFS) {
		return func(m fstest.MapFS) {
			edit(t, m, listFile, func(doc map[string]any) {
				h := at(t, doc, "root/slots/body/children/8/events/onPressed")
				delete(h, "concurrency")
				if policy != "" {
					h["concurrency"] = policy
				}
				if detached {
					h["detached"] = true
				}
			})
		}
	}
	handler := func(t *testing.T, res *Result) *fbs.Handler {
		t.Helper()
		nodes, _ := pageNodes(t, readAll(t, res)[1], listPageID)
		for _, n := range nodes {
			var h fbs.Handler
			for i := range n.HandlersLength() {
				n.Handlers(&h, i)
				if n.Widget() != 0 {
					if w, ok := registry.LookupWidget("FilledButton"); ok && n.Widget() == w.ID {
						return &h
					}
				}
			}
		}
		t.Fatal("no FilledButton handler")
		return nil
	}
	for _, c := range []struct {
		policy   string
		detached bool
		want     fbs.Concurrency
		feature  bool
	}{
		{"", false, fbs.ConcurrencyDrop, false},
		{"drop", false, fbs.ConcurrencyDrop, false},
		{"parallel", false, fbs.ConcurrencyParallel, true},
		{"queue", false, fbs.ConcurrencyQueue, true},
		{"throttle:250", false, fbs.ConcurrencyThrottle, true},
		{"", true, fbs.ConcurrencyDrop, true},
	} {
		res := features(t, press(c.policy, c.detached), older(t))
		noErrors(t, res)
		h := handler(t, res)
		if h.Concurrency() != c.want || h.Detached() != c.detached {
			t.Errorf("%q: %v detached %v", c.policy, h.Concurrency(), h.Detached())
		}
		has := false
		for _, d := range res.Diagnostics {
			if d.Code == plxerr.RequiredFeaturesRaised && d.Path == "/root/slots/body/children/8/events/onPressed" && strings.Contains(d.Message, "actions.concurrency.v1") {
				has = true
			}
		}
		if has != c.feature {
			t.Errorf("%q detached %v: features %v", c.policy, c.detached, res.Plugins[0].Features)
		}
	}
	// A declared parallel is listed even when the minimum runtime runs it:
	// the runtime reads the key as the marker of the new encoding.
	res := features(t, press("parallel", false), func(m fstest.MapFS) {
		for _, f := range []string{listFile} {
			edit(t, m, f, func(doc map[string]any) {
				delete(at(t, doc, "lifecycle/onResume"), "concurrency")
			})
		}
	})
	clean(t, res)
	if !slices.Contains(res.Plugins[0].Features, "actions.concurrency.v1") {
		t.Errorf("parallel: features %v", res.Plugins[0].Features)
	}
	// Under an older minimum runtime, the app's policy decides.
	res = features(t, press("restart", false), func(m fstest.MapFS) {
		edit(t, m, "app.json", func(doc map[string]any) {
			doc["minRuntimeVersion"] = "0.2.0"
			doc["requiredFeatures"] = "reject"
		})
	})
	wantDiag(t, res, plxerr.RuntimeTooOld, listFile, "/root/slots/body/children/8/events/onPressed")
}

// util is a second plugin with an exported and a private flow.
func util(t *testing.T, m fstest.MapFS) {
	t.Helper()
	m["plugins/util/plugin.json"] = &fstest.MapFile{Data: []byte(`{
		"schemaVersion": "1.0.0", "kind": "plugin", "id": "01b0c450-6c00-7000-8000-0000000a0001",
		"key": "util", "name": "Util", "team": "tour", "icon": {"monogram": {"text": "U", "background": "#336699"}}, "entryPage": "01b0c450-6c00-7000-8000-0000000a0002",
		"pages": ["01b0c450-6c00-7000-8000-0000000a0002"]}`)}
	m["plugins/util/pages/home.page.json"] = &fstest.MapFile{Data: []byte(`{
		"schemaVersion": "1.0.0", "kind": "page", "id": "01b0c450-6c00-7000-8000-0000000a0002",
		"key": "home", "route": "util-home", "pageKind": "screen", "title": "Util",
		"root": {"id": "01b0c450-6c00-7000-8000-0000000a0003", "type": "Text", "props": {"data": "Util"}}}`)}
	m["plugins/util/actions/shout.graph.json"] = &fstest.MapFile{Data: []byte(`{
		"schemaVersion": "1.0.0", "kind": "actionGraph", "id": "01b0c450-6c00-7000-8000-0000000a0004",
		"key": "shout", "exported": true,
		"inputs": [{"id": "01b0c450-6c00-7000-8000-0000000a0005", "name": "text", "type": "string", "required": true}],
		"output": "string",
		"steps": [{"id": "done", "action": "stop", "input": {"result": {"$expr": "params.text + '!'"}}}]}`)}
	m["plugins/util/actions/hidden.graph.json"] = &fstest.MapFile{Data: []byte(`{
		"schemaVersion": "1.0.0", "kind": "actionGraph", "id": "01b0c450-6c00-7000-8000-0000000a0006",
		"key": "hidden", "output": "bool",
		"steps": [{"id": "done", "action": "stop", "input": {"result": true}}]}`)}
	edit(t, m, "app.json", func(doc map[string]any) { doc["plugins"] = []any{"tasks", "util"} })
}

// callStep makes the list page's button call a flow first.
func callStep(t *testing.T, m fstest.MapFS, flow string) {
	t.Helper()
	edit(t, m, listFile, func(doc map[string]any) {
		h := at(t, doc, "root/slots/body/children/8/events/onPressed")
		h["steps"] = raw(t, `[{"id": "call", "action": "callFlow", "input": {"flow": "`+flow+`", "input": {"text": "hi"}}, "next": "use"},
			{"id": "use", "action": "condition", "input": {"when": {"$expr": "steps.call.output == 'hi!'"}}}]`)
	})
}

// TestFlowsAcrossPlugins_ACT_061 checks callFlow: exported flows of other
// plugins by <plugin>/<flow>, typed inputs and output, private flows
// refused, and call cycles refused at compile time.
// Verifies: ACT-061, ACT-001.
func TestFlowsAcrossPlugins_ACT_061(t *testing.T) {
	t.Parallel()
	res := features(t, func(m fstest.MapFS) { util(t, m); callStep(t, m, "util/shout") })
	clean(t, res)
	res = features(t, func(m fstest.MapFS) { util(t, m); callStep(t, m, "util/shout") }, older(t))
	noErrors(t, res)
	if !slices.Contains(res.Plugins[0].Features, "actions.flows.v1") {
		t.Errorf("features %v", res.Plugins[0].Features)
	}

	res = features(t, func(m fstest.MapFS) { util(t, m); callStep(t, m, "util/hidden") })
	wantDiag(t, res, plxerr.FlowNotExported, listFile, "/"+pressSteps+"/0/input/flow")

	res = features(t, func(m fstest.MapFS) {
		util(t, m)
		callStep(t, m, "util/shout")
		edit(t, m, listFile, func(doc map[string]any) {
			at(t, doc, pressSteps+"/0")["input"].(map[string]any)["input"] = map[string]any{"text": 3}
		})
	})
	wantDiag(t, res, plxerr.PropTypeMismatch, listFile, "/"+pressSteps+"/0/input/input/text")

	// shout calls tasks/notify, which calls util/shout back.
	res = features(t, func(m fstest.MapFS) {
		util(t, m)
		edit(t, m, notifyFile, func(doc map[string]any) {
			doc["exported"] = true
			doc["steps"] = raw(t, `[{"id": "say", "action": "callFlow", "input": {"flow": "util/shout", "input": {"text": "x"}}}]`)
		})
		edit(t, m, "plugins/util/actions/shout.graph.json", func(doc map[string]any) {
			doc["steps"] = raw(t, `[{"id": "back", "action": "callFlow", "input": {"flow": "tasks/notify", "input": {"message": "x"}}, "next": "done"},
				{"id": "done", "action": "stop", "input": {"result": "x"}}]`)
		})
	})
	if !hasCode(res, plxerr.FlowCallCycle) {
		t.Errorf("no PLX-1125:\n%s", list(res.Diagnostics))
	}
}

func hasCode(res *Result, code plxerr.Code) bool {
	for _, d := range res.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

// TestEmitEvent_D11 checks emitEvent: only in a component's handlers, only
// its declared events, with the declared payload type.
// Verifies: ACT-001, SCH-030.
func TestEmitEvent_D11(t *testing.T) {
	t.Parallel()
	emit := func(input, payload string) func(m fstest.MapFS) {
		return func(m fstest.MapFS) {
			edit(t, m, badgeFile, func(doc map[string]any) {
				doc["events"] = raw(t, `[{"name": "onTap"}, {"name": "onPicked", "payload": "`+payload+`"}]`)
				at(t, doc, "root/events")["onTap"] = raw(t, `{"steps": [{"id": "emit", "action": "emitEvent", "input": `+input+`}]}`)
			})
		}
	}
	res := features(t, emit(`{"event": "onPicked", "payload": "x"}`, "string"))
	clean(t, res)
	const ptr = "/root/events/onTap/steps/0/input"
	res = features(t, emit(`{"event": "onNope"}`, "string"))
	wantDiag(t, res, plxerr.UndeclaredComponentEvent, badgeFile, ptr+"/event")
	res = features(t, emit(`{"event": "onTap", "payload": 1}`, "string"))
	wantDiag(t, res, plxerr.UndeclaredComponentEvent, badgeFile, ptr+"/payload")
	res = features(t, emit(`{"event": "onPicked", "payload": 1}`, "string"))
	wantDiag(t, res, plxerr.PropTypeMismatch, badgeFile, ptr+"/payload")
	res = features(t, func(m fstest.MapFS) {
		edit(t, m, listFile, func(doc map[string]any) {
			at(t, doc, "root/slots/body/children/8/events/onPressed")["steps"] = raw(t, `[{"id": "emit", "action": "emitEvent", "input": {"event": "onTap"}}]`)
		})
	})
	wantDiag(t, res, plxerr.EmitEventOutsideComponent, listFile, "/"+pressSteps+"/0/input/event")
}

// TestStepPolicies_ACT_006 checks retry policies and the forEach bound.
// Verifies: ACT-006, ACT-005, LIM-001.
func TestStepPolicies_ACT_006(t *testing.T) {
	t.Parallel()
	step := func(s string) func(m fstest.MapFS) {
		return func(m fstest.MapFS) {
			edit(t, m, listFile, func(doc map[string]any) {
				at(t, doc, "root/slots/body/children/8/events/onPressed")["steps"] = raw(t, "["+s+"]")
			})
		}
	}
	res := features(t, step(`{"id": "s", "action": "sync", "retry": {"count": 3, "backoffMs": 100, "maxBackoffMs": 1000, "jitter": true, "on": ["network"]}}`), older(t))
	noErrors(t, res)
	if !slices.Contains(res.Plugins[0].Features, "actions.retry.v1") {
		t.Errorf("features %v", res.Plugins[0].Features)
	}
	res = features(t, step(`{"id": "s", "action": "sync", "retry": {"count": 3, "backoffMs": 100, "maxBackoffMs": 10}}`))
	wantDiag(t, res, plxerr.InvalidRetryPolicy, listFile, "/"+pressSteps+"/0/retry/maxBackoffMs")
	res = features(t, step(`{"id": "s", "action": "sync", "retry": {"count": 3, "on": ["cancelled"]}}`))
	wantDiag(t, res, plxerr.InvalidRetryPolicy, listFile, "/"+pressSteps+"/0/retry/on")

	m := project(t, featuresDir)
	step(`{"id": "loop", "action": "forEach", "input": {"items": [1, 2, 3]}}`)(m)
	opts := DefaultOptions()
	lim, err := opts.Limits.Tighten(limits.ActionForEachItems, limits.ScopeApp, 2)
	if err != nil {
		t.Fatal(err)
	}
	opts.Limits = lim
	wantDiag(t, Compile(m, opts), plxerr.LimitExceeded, listFile, "/"+pressSteps+"/0/input/items")
}

// TestRedactedInputs_ACT_031 checks that the inputs reading a sensitive
// value, and the steps reading such a step, are marked for traces.
// Verifies: ACT-031, SCH-012.
func TestRedactedInputs_ACT_031(t *testing.T) {
	t.Parallel()
	res := features(t, func(m fstest.MapFS) {
		edit(t, m, listFile, func(doc map[string]any) {
			at(t, doc, "root/slots/body/children/8/events/onPressed")["steps"] = raw(t, `[
				{"id": "secret", "action": "condition", "input": {"when": {"$expr": "page.draft == ''"}}, "branches": {"then": "after", "else": "plain"}},
				{"id": "after", "action": "condition", "input": {"when": {"$expr": "steps.secret.error == null"}}},
				{"id": "plain", "action": "condition", "input": {"when": {"$expr": "page.priority == 'high'"}}}]`)
		})
	})
	clean(t, res)
	cond, _ := registry.LookupAction("condition")
	in, _ := cond.Input("when")
	when := in.ID
	redacted := map[string][]uint32{}
	for _, sec := range readAll(t, res)[1].Sections {
		if sec.Kind != bundle.SectionActions {
			continue
		}
		actions := fbs.GetRootAsActions(sec.Data, 0)
		var g fbs.Graph
		var st fbs.Step
		for i := range actions.GraphsLength() {
			actions.Graphs(&g, i)
			if g.StepsLength() != 3 {
				continue
			}
			for j := range g.StepsLength() {
				g.Steps(&st, j)
				var ids []uint32
				for k := range st.RedactLength() {
					ids = append(ids, st.Redact(k))
				}
				redacted[[]string{"secret", "after", "plain"}[j]] = ids
			}
		}
	}
	if !slices.Equal(redacted["secret"], []uint32{when}) || !slices.Equal(redacted["after"], []uint32{when}) || len(redacted["plain"]) != 0 {
		t.Errorf("redacted %v", redacted)
	}
}
