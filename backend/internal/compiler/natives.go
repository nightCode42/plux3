// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// nativeDecls is the native catalogue's declarations in an app bundle.
type nativeDecls struct {
	routes, slots, actions flatbuffers.UOffsetT
}

// nativeDeclarations writes the declarations of the native catalogue the
// app is compiled against, in the catalogue's order, which slot nodes
// address by index (ADR-0041, P4 plan A24): the runtime maps slot props
// and events by them and checks every value crossing into host code.
// Defaults stay in the host: a plugin passes every value it uses.
func (u *unit) nativeDeclarations(e *valueEnc) nativeDecls {
	nc := u.project.NativeCatalogue
	if nc == nil || nc.Doc == nil {
		return nativeDecls{}
	}
	b := e.b
	doc := nc.Doc
	routes := make([]flatbuffers.UOffsetT, len(doc.Routes))
	for i, r := range doc.Routes {
		pv := e.params(nativeParams(r.Params))
		name, result := e.strs.of(r.Name), e.strs.of(r.Result)
		fbs.NativeRouteDeclStart(b)
		fbs.NativeRouteDeclAddName(b, name)
		fbs.NativeRouteDeclAddParams(b, pv)
		fbs.NativeRouteDeclAddResult(b, result)
		routes[i] = fbs.NativeRouteDeclEnd(b)
	}
	slots := make([]flatbuffers.UOffsetT, len(doc.Slots))
	for i, s := range doc.Slots {
		pv := e.params(nativeParams(s.Props))
		events := make([]flatbuffers.UOffsetT, len(s.Events))
		for j, ev := range s.Events {
			name, payload := e.strs.of(ev.Name), e.strs.of(ev.Payload)
			fbs.ComponentEventStart(b)
			fbs.ComponentEventAddName(b, name)
			fbs.ComponentEventAddPayload(b, payload)
			events[j] = fbs.ComponentEventEnd(b)
		}
		ev := offsetVector(b, events)
		typ := e.strs.of(s.Type)
		fbs.NativeSlotDeclStart(b)
		fbs.NativeSlotDeclAddType(b, typ)
		fbs.NativeSlotDeclAddProps(b, pv)
		fbs.NativeSlotDeclAddEvents(b, ev)
		slots[i] = fbs.NativeSlotDeclEnd(b)
	}
	actions := make([]flatbuffers.UOffsetT, len(doc.Actions))
	for i, a := range doc.Actions {
		iv := e.params(nativeParams(a.Inputs))
		name, output := e.strs.of(a.Name), e.strs.of(a.Output)
		fbs.NativeActionDeclStart(b)
		fbs.NativeActionDeclAddName(b, name)
		fbs.NativeActionDeclAddInputs(b, iv)
		fbs.NativeActionDeclAddOutput(b, output)
		actions[i] = fbs.NativeActionDeclEnd(b)
	}
	return nativeDecls{routes: offsetVector(b, routes), slots: offsetVector(b, slots), actions: offsetVector(b, actions)}
}

// nativeParams converts a catalogue entry's parameters, props or inputs.
func nativeParams(ps []schema.Param) []*param {
	out := make([]*param, len(ps))
	for i, p := range ps {
		out[i] = &param{name: p.Name, typ: p.Type, required: p.Required != nil && *p.Required, sensitive: p.Sensitive != nil && *p.Sensitive}
	}
	return out
}
