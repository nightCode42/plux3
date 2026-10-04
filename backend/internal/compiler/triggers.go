// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"strconv"
	"strings"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// actionsRuntimes lists the runtime each revision of the P5 engine
// features first shipped in (ADR-0040's pattern for navigation.guards):
// a bundle that needs one is refused by older runtimes (BND-008).
var actionsRuntimes = []string{"0.3.0"}

// The engine features of P5's milestone R1, each raised as <name>.v1.
const (
	// triggersFeature: page lifecycle handlers, or any trigger of a page,
	// plugin or the app (ACT-002, ACT-020).
	triggersFeature = "actions.triggers"
	// concurrencyFeature: a handler that declares a policy other than drop,
	// or a detached run (ACT-003, ACT-004). It also marks the encoding of
	// an absent policy as the trigger's default, so a runtime honours a
	// declared parallel as parallel.
	concurrencyFeature = "actions.concurrency"
	// retryFeature: a step with a retry policy (ACT-006).
	retryFeature = "actions.retry"
	// flowsFeature: a callFlow step (ACT-061).
	flowsFeature = "actions.flows"
	// controlFeature: the control, analytics, sync and component-event
	// actions R1 delivers.
	controlFeature = "actions.control"
)

// actionFeatures maps the actions P5 delivers to the feature a bundle using
// them requires, so a runtime that cannot run them refuses the bundle
// instead of failing their steps with PLX-4010.
var actionFeatures = map[string]string{
	"callFlow":   flowsFeature,
	"switch":     controlFeature,
	"forEach":    controlFeature,
	"parallel":   controlFeature,
	"delay":      controlFeature,
	"trackEvent": controlFeature,
	"sync":       controlFeature,
	"emitEvent":  controlFeature,
}

// requireFeature records that a bundle needs an engine feature of runtime
// 0.3.0 (useRevision). With always, the feature is listed even when the
// app's minimum runtime has it, because the runtime reads it as a marker.
func (u *unit) requireFeature(name string, c vctx, always bool) {
	u.useRevision(name, actionsRuntimes, 1, c)
	if always && !semverLess(u.project.App.Doc.MinRuntimeVersion, actionsRuntimes[0]) {
		set := u.features[c.pl]
		if set == nil {
			set = map[string]bool{}
			u.features[c.pl] = set
		}
		set[name+".v1"] = true
	}
}

// trigger is a trigger of a page, a plugin or the app (ACT-002), or its
// error handler (ACT-020).
type trigger struct {
	kind     fbs.TriggerKind
	name     string
	interval uint32
	repeat   bool
	eh       *schema.EventHandler
	graph    *graph
	file     string
	ptr      string
	handler  *handler
}

// defaultPolicy is the concurrency policy of a trigger that declares none
// (ACT-003): widget events drop, so a double tap never submits twice;
// lifecycle, host, push, data-source and error events queue, so none is
// lost; a timer's tick is dropped while its run is in progress; a watcher
// restarts on the newest value.
func defaultPolicy(kind fbs.TriggerKind) fbs.Concurrency {
	switch kind {
	case fbs.TriggerKindTimer:
		return fbs.ConcurrencyDrop
	case fbs.TriggerKindStateChange:
		return fbs.ConcurrencyRestart
	}
	return fbs.ConcurrencyQueue
}

// resolveTriggers builds the triggers of an owner: a page (pg), a plugin
// (pl) or the app (both nil), resolving their handlers' graphs.
func (u *unit) resolveTriggers(ts *schema.Triggers, pl *plugin, pg *page, anchor, file string) []*trigger {
	if ts == nil {
		return nil
	}
	var out []*trigger
	add := func(kind fbs.TriggerKind, name string, eh *schema.EventHandler, event, ptr string) *trigger {
		if eh == nil {
			return nil
		}
		g := u.triggerGraph(pl, pg, eh, anchor, "trigger:"+event, file, ptr)
		if g == nil {
			return nil
		}
		t := &trigger{kind: kind, name: name, eh: eh, graph: g, file: file, ptr: ptr}
		out = append(out, t)
		return t
	}
	for i := range ts.Timers {
		tm := &ts.Timers[i]
		ptr := plxerr.Pointer("triggers", "timers", strconv.Itoa(i), "handler")
		if t := add(fbs.TriggerKindTimer, tm.Name, &tm.Handler, "timer:"+tm.Name, ptr); t != nil {
			t.interval = clampU32(tm.IntervalMs)
			t.repeat = tm.Repeat == nil || *tm.Repeat
		}
	}
	for i := range ts.Watch {
		w := &ts.Watch[i]
		add(fbs.TriggerKindStateChange, w.Path, &w.Handler, "watch:"+strconv.Itoa(i), plxerr.Pointer("triggers", "watch", strconv.Itoa(i), "handler"))
	}
	add(fbs.TriggerKindAppResume, "", ts.OnAppResume, "onAppResume", "/triggers/onAppResume")
	add(fbs.TriggerKindAppPause, "", ts.OnAppPause, "onAppPause", "/triggers/onAppPause")
	add(fbs.TriggerKindPushOpened, "", ts.OnPushOpened, "onPushOpened", "/triggers/onPushOpened")
	for _, name := range sortedKeys(ts.HostEvents) {
		eh := ts.HostEvents[name]
		add(fbs.TriggerKindHostEvent, name, &eh, "hostEvent:"+name, plxerr.Pointer("triggers", "hostEvents", name))
	}
	for _, name := range sortedKeys(ts.DataSources) {
		ds := ts.DataSources[name]
		add(fbs.TriggerKindDataLoaded, name, ds.OnLoaded, "data:"+name+":onLoaded", plxerr.Pointer("triggers", "dataSources", name, "onLoaded"))
		add(fbs.TriggerKindDataFailed, name, ds.OnFailed, "data:"+name+":onFailed", plxerr.Pointer("triggers", "dataSources", name, "onFailed"))
	}
	add(fbs.TriggerKindError, "", ts.OnError, "onError", "/triggers/onError")
	return out
}

// triggerGraph resolves a trigger's handler: through a page's handlers,
// or for a plugin and the app a referenced flow of the plugin or an
// inline graph anchored at the owner.
func (u *unit) triggerGraph(pl *plugin, pg *page, eh *schema.EventHandler, anchor, event, file, ptr string) *graph {
	if pg != nil {
		return u.handlerGraph(owner{page: pg}, eh, anchor, event, file, ptr)
	}
	if eh.Graph != "" {
		if pl == nil {
			u.report(plxerr.UnresolvedReference, file, ptr+"/$graph", "the app's triggers can only use inline action graphs")
			return nil
		}
		return u.graphRef(pl, nil, eh.Graph, anchor, file, ptr+"/$graph")
	}
	g := &graph{id: derivedID("graph", anchor, event), key: event, file: file, ptr: ptr, steps: eh.Steps, plugin: pl, inline: true, used: true}
	for i := range g.steps {
		for _, name := range sortedKeys(g.steps[i].Input) {
			u.scanRefs(anchor, file, ptr+plxerr.Pointer("steps", strconv.Itoa(i), "input", name), g.steps[i].Input[name])
		}
	}
	if pl != nil {
		pl.inline = append(pl.inline, g)
	} else {
		u.appGraphs = append(u.appGraphs, g)
	}
	return g
}

// triggers type-checks what each trigger names in scope s and sets the
// type of its graph's `event` (ACT-002): a timer's tick count, a watched
// entry's new value, a host event's payload, a data source's value or
// error, and the error an error handler handles.
func (t *typer) triggers(ts []*trigger, s *scope, roots ...string) {
	u := t.u
	timers := u.newKeys("timer")
	for _, tr := range ts {
		if tr.kind == fbs.TriggerKindTimer {
			timers.claim(tr.name, tr.file, strings.TrimSuffix(tr.ptr, "/handler")+"/name")
		}
		if payload, ok := t.triggerPayload(tr, s, roots); ok {
			t.useGraph(tr.graph, payload, tr.file, tr.ptr)
		}
	}
}

// triggerPayload returns the type of a trigger's `event`, or false after
// reporting what the trigger names that is not visible.
func (t *typer) triggerPayload(tr *trigger, s *scope, roots []string) (string, bool) {
	u := t.u
	payload := ""
	switch tr.kind {
	case fbs.TriggerKindTimer:
		payload = "int"
	case fbs.TriggerKindStateChange:
		root, _, _ := strings.Cut(tr.name, ".")
		typ := pathType(s, tr.name)
		if typ == nil || !slices.Contains(roots, root) {
			u.report(plxerr.InvalidTrigger, tr.file, strings.TrimSuffix(tr.ptr, "/handler")+"/path", "no state entry %q is visible here", tr.name)
			return "", false
		}
		payload = typ.String()
	case fbs.TriggerKindHostEvent:
		ev := u.hostEvents[tr.name]
		if ev == nil {
			u.report(plxerr.InvalidTrigger, tr.file, tr.ptr, "the app document declares no host event %q", tr.name)
			return "", false
		}
		payload = "PluxHostEvent" + upperFirst(ev.Name)
		var fields [][2]string
		for _, f := range ev.Fields {
			if te, err := parseTypeExpr(f.Type); err == nil {
				fields = append(fields, [2]string{f.Name, te.String()})
			}
		}
		tr.graph.eventTypes = map[string]pxl.TypeSpec{payload: objectType(fields)}
	case fbs.TriggerKindDataLoaded, fbs.TriggerKindDataFailed:
		src := sourceNamed(s.sources, tr.name)
		if src == nil {
			u.report(plxerr.InvalidTrigger, tr.file, tr.ptr, "no data source %q is visible here", tr.name)
			return "", false
		}
		payload = src.typ
		if tr.kind == fbs.TriggerKindDataFailed {
			payload = "PluxActionError"
		}
	case fbs.TriggerKindError:
		payload = "PluxActionError"
	}
	return payload, true
}

// typeOwnGraphs type-checks the inline graphs of a plugin's or the app's
// triggers in the owner's scope.
func (t *typer) typeOwnGraphs(ts []*trigger, s *scope) {
	for _, tr := range ts {
		if tr.graph.inline && tr.graph.scope == nil && t.u.graphInFocus(tr.graph) {
			t.graph(tr.graph, s)
		}
	}
}

// pathType resolves "<root>.<name>" to the type of the entry in scope s.
func pathType(s *scope, path string) *texpr {
	root, name, ok := strings.Cut(path, ".")
	if !ok {
		return nil
	}
	typ, isRoot := s.roots[root]
	if !isRoot {
		return nil
	}
	spec, ok := s.synth[typ]
	if !ok {
		return nil
	}
	f, ok := spec.Fields[name]
	if !ok {
		return nil
	}
	te, _ := parseTypeExpr(f)
	return te
}

// sourceNamed finds a data source by name.
func sourceNamed(sources []sourceField, name string) *sourceField {
	for i := range sources {
		if sources[i].name == name {
			return &sources[i]
		}
	}
	return nil
}

// checkTriggers builds each trigger's handler with its policy, and raises
// the triggers feature (ACT-002, ACT-003).
func (u *unit) checkTriggers(ts []*trigger, pl *plugin) {
	for _, tr := range ts {
		h := &handler{graph: tr.graph}
		u.policy(h, tr.eh, defaultPolicy(tr.kind), vctx{file: tr.file, ptr: tr.ptr, pl: pl})
		tr.handler = h
		u.requireFeature(triggersFeature, vctx{file: tr.file, ptr: tr.ptr, pl: pl}, false)
	}
}

// policy sets a handler's concurrency policy and detached flag: the
// declared policy, or the trigger's default when none is declared, and
// raises the concurrency feature for a policy older runtimes cannot run.
func (u *unit) policy(h *handler, eh *schema.EventHandler, def fbs.Concurrency, c vctx) {
	h.detached = eh.Detached != nil && *eh.Detached
	if eh.Concurrency == "" {
		h.concurrency = def
	} else {
		u.concurrency(h, eh.Concurrency, c.file, c.ptr)
	}
	declared := eh.Concurrency != "" && h.concurrency != fbs.ConcurrencyDrop
	if declared || h.detached {
		u.requireFeature(concurrencyFeature, c, h.concurrency == fbs.ConcurrencyParallel)
	}
}

// ownTriggers returns the triggers a bundle's meta carries: the plugin's or
// the app's.
func (u *unit) ownTriggers(o *out) []*trigger {
	if o.pl != nil {
		return o.pl.triggers
	}
	return u.appTriggers
}

// triggerTables writes a vector of triggers; 0 when there are none.
func triggerTables(b *flatbuffers.Builder, ts []*trigger) flatbuffers.UOffsetT {
	var hs []*handler
	for _, tr := range ts {
		if tr.handler != nil {
			hs = append(hs, tr.handler)
		}
	}
	if len(hs) == 0 {
		return 0
	}
	handlerOffs := make([]flatbuffers.UOffsetT, len(hs))
	for i, h := range hs {
		handlerOffs[i] = handlerTable(b, h)
	}
	offs := make([]flatbuffers.UOffsetT, 0, len(hs))
	i := 0
	for _, tr := range ts {
		if tr.handler == nil {
			continue
		}
		var name flatbuffers.UOffsetT
		if tr.name != "" {
			name = b.CreateString(tr.name)
		}
		fbs.TriggerStart(b)
		fbs.TriggerAddKind(b, tr.kind)
		if name != 0 {
			fbs.TriggerAddName(b, name)
		}
		if tr.interval != 0 {
			fbs.TriggerAddIntervalMs(b, tr.interval)
		}
		fbs.TriggerAddRepeat(b, tr.repeat)
		fbs.TriggerAddHandler(b, handlerOffs[i])
		offs = append(offs, fbs.TriggerEnd(b))
		i++
	}
	return offsetVector(b, offs)
}
