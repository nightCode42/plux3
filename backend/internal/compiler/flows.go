// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// flowRef resolves a callFlow's flow (ACT-061): "<flow>", a flow of the
// calling plugin, or "<plugin>/<flow>", an exported flow of any plugin.
// private reports a flow that exists but is not exported.
func (u *unit) flowRef(from *plugin, name string) (f *graph, private bool) {
	pluginKey, key, qualified := strings.Cut(name, "/")
	if !qualified {
		return flowByKey(from, name), false
	}
	for _, pl := range u.plugins {
		if pl.key != pluginKey {
			continue
		}
		f = flowByKey(pl, key)
		if f == nil {
			return nil, false
		}
		if pl != from && (f.doc.Exported == nil || !*f.doc.Exported) {
			return nil, true
		}
		return f, false
	}
	return nil, false
}

// flowInput checks a callFlow's input against the flow's declared inputs.
func (u *unit) flowInput(g *graph, raw json.RawMessage, c vctx) *value {
	st := g.steps[stepIndex(c.ptr)]
	if f, _ := u.flowRef(g.plugin, literalString(st.Input["flow"])); f != nil {
		return u.namedParams(raw, f.doc.Inputs, "flow "+f.key, c)
	}
	return nil
}

// flowCall is a callFlow step's literal target.
type flowCall struct {
	to   *graph
	file string
	ptr  string
}

// checkFlowCycles refuses graphs that call each other through callFlow in
// a cycle, so no run recurses (ACT-001, ACT-061, PLX-1125).
func (u *unit) checkFlowCycles() {
	graphs := u.allGraphs()
	calls := u.flowCalls(graphs)
	state := map[*graph]int8{} // 0 new, 1 on the path, 2 done
	var visit func(g *graph)
	visit = func(g *graph) {
		state[g] = 1
		for _, c := range calls[g] {
			switch state[c.to] {
			case 1:
				if u.focus == "" || u.graphInFocus(g) {
					u.report(plxerr.FlowCallCycle, c.file, c.ptr, "flow %q calls back into a graph that calls it", c.to.key)
				}
			case 0:
				visit(c.to)
			}
		}
		state[g] = 2
	}
	for _, g := range graphs {
		if state[g] == 0 {
			visit(g)
		}
	}
}

// flowCalls lists each graph's callFlow steps with a literal target.
func (u *unit) flowCalls(graphs []*graph) map[*graph][]flowCall {
	calls := map[*graph][]flowCall{}
	for _, g := range graphs {
		for i, st := range g.steps {
			if st.Action != "callFlow" {
				continue
			}
			if f, _ := u.flowRef(g.plugin, literalString(st.Input["flow"])); f != nil {
				calls[g] = append(calls[g], flowCall{to: f, file: g.file, ptr: g.ptr + plxerr.Pointer("steps", strconv.Itoa(i), "input", "flow")})
			}
		}
	}
	return calls
}

// componentEventRef resolves emitEvent's event: one its component
// declares (D11, SCH-030).
func (u *unit) componentEventRef(g *graph, name string, c vctx) *value {
	if g.component == nil {
		u.report(plxerr.EmitEventOutsideComponent, c.file, c.ptr, "emitEvent runs only in a component's handlers")
		return nil
	}
	if componentEvent(g.component, name) == nil {
		u.report(plxerr.UndeclaredComponentEvent, c.file, c.ptr, "component %q declares no event %q", g.component.doc.Key, name)
		return nil
	}
	return &value{kind: fbs.ValueKindString, s: name}
}

// componentEventPayload checks emitEvent's payload against the payload
// type its event declares.
func (u *unit) componentEventPayload(g *graph, raw json.RawMessage, c vctx) *value {
	if g.component == nil {
		return nil // the event input reports it
	}
	st := g.steps[stepIndex(c.ptr)]
	ev := componentEvent(g.component, literalString(st.Input["event"]))
	if ev == nil {
		return nil
	}
	if ev.Payload == "" {
		u.report(plxerr.UndeclaredComponentEvent, c.file, c.ptr, "event %q of component %q declares no payload", ev.Name, g.component.doc.Key)
		return nil
	}
	te, err := parseTypeExpr(ev.Payload)
	if err != nil {
		return nil // reported where the event is declared
	}
	return u.checkRaw(c, raw, te)
}

// componentEvent finds a component's declared event.
func componentEvent(c *component, name string) *schema.ComponentEvent {
	for i := range c.doc.Events {
		if c.doc.Events[i].Name == name {
			return &c.doc.Events[i]
		}
	}
	return nil
}

// stepFeatures raises the features a step needs (BND-008), and checks its
// retry policy (ACT-006) and a literal forEach list against its bound
// (ACT-005).
func (u *unit) stepFeatures(g *graph, a *registry.Action, st schema.Step, ptr string) {
	c := vctx{file: g.file, ptr: ptr, pl: g.plugin}
	if f, ok := actionFeatures[a.Name]; ok {
		u.requireFeature(f, c, false)
	}
	if r := st.Retry; r != nil {
		rc := c
		rc.ptr = ptr + "/retry"
		u.requireFeature(retryFeature, rc, false)
		if r.BackoffMs != nil && r.MaxBackoffMs != nil && *r.MaxBackoffMs < *r.BackoffMs {
			u.report(plxerr.InvalidRetryPolicy, g.file, ptr+"/retry/maxBackoffMs", "maxBackoffMs %d is below backoffMs %d", *r.MaxBackoffMs, *r.BackoffMs)
		}
		if slices.Contains(r.On, schema.ErrorKindCancelled) {
			u.report(plxerr.InvalidRetryPolicy, g.file, ptr+"/retry/on", "a cancelled step is never retried")
		}
	}
	if a.Name == "forEach" {
		var items []json.RawMessage
		if json.Unmarshal(st.Input["items"], &items) == nil {
			if limit := u.opts.Limits.Get(limits.ActionForEachItems); int64(len(items)) > limit {
				u.report(plxerr.LimitExceeded, g.file, ptr+"/input/items", "forEach has %d items, above action.forEachItems = %d", len(items), limit)
			}
		}
	}
}

// redactions marks the inputs of each step that read sensitive values, and
// the steps whose outputs derive from them, so traces never record them
// (ACT-031, SCH-012).
func (u *unit) redactions(g *graph) {
	sensitive := u.sensitivePaths(g)
	if g.doc != nil {
		for _, p := range g.doc.Inputs {
			if p.Sensitive != nil && *p.Sensitive {
				sensitive = append(sensitive, "params."+p.Name)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range g.lowered {
			if len(s.redact) > 0 {
				continue
			}
			if s.redact = redactedInputs(s, sensitive); len(s.redact) > 0 {
				sensitive = append(sensitive, "steps."+s.id)
				changed = true
			}
		}
	}
}

// redactedInputs lists the IDs of a step's inputs that read sensitive
// paths.
func redactedInputs(s *step, sensitive []string) []uint32 {
	var out []uint32
	for _, in := range s.inputs {
		if readsSensitive(in.value, sensitive) {
			out = append(out, in.id)
		}
	}
	return out
}

// readsSensitive reports whether a value's expressions read a sensitive
// path.
func readsSensitive(v *value, sensitive []string) bool {
	if v == nil {
		return false
	}
	if v.prog != nil {
		for _, r := range v.prog.Reads {
			if sensitiveRead(r, sensitive) != "" {
				return true
			}
		}
	}
	for _, e := range v.entries {
		if readsSensitive(e.value, sensitive) {
			return true
		}
	}
	for _, it := range v.items {
		if readsSensitive(it, sensitive) {
			return true
		}
	}
	return false
}

// redactVector writes a step's redacted input IDs; 0 when there are none.
func redactVector(b *flatbuffers.Builder, ids []uint32) flatbuffers.UOffsetT {
	if len(ids) == 0 {
		return 0
	}
	fbs.StepStartRedactVector(b, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		b.PrependUint32(ids[i])
	}
	return b.EndVector(len(ids))
}
