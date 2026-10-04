// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"cmp"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// Native entry kinds of a NativeUse.
const (
	NativeRouteUse  = "route"
	NativeSlotUse   = "slot"
	NativeActionUse = "action"
)

// NativeUse is a native route, slot or custom action a project uses, with
// the types the project's catalogue declares for it (ADR-0041): what the
// host build that runs the release must declare the same way. The server
// stores a release's uses, so it can judge host builds whose catalogue
// arrives later (REL-080).
type NativeUse struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Params are a route's parameters, a slot's props or an action's
	// inputs, each "name:type", with "!" when required.
	Params []string `json:"params,omitempty"`
	// Events are a slot's events, each "name:payload".
	Events []string `json:"events,omitempty"`
	// Result is a route's result or an action's output.
	Result string `json:"result,omitempty"`
}

// In reports whether the catalogue declares the entry, and whether with
// the same types.
func (u NativeUse) In(c *schema.NativeCatalogueDocument) (declared, same bool) {
	if c == nil {
		return false, false
	}
	got, ok := nativeUse(c, u.Kind, u.Name)
	if !ok {
		return false, false
	}
	return true, slices.Equal(u.Params, got.Params) && slices.Equal(u.Events, got.Events) && u.Result == got.Result
}

// Compatible reports whether a host build with catalogue c can run a
// release that uses uses: it declares every one with the same types.
func Compatible(uses []NativeUse, c *schema.NativeCatalogueDocument) bool {
	for _, u := range uses {
		if _, same := u.In(c); !same {
			return false
		}
	}
	return true
}

// NativeUses lists the native entries the compiled project uses in the
// files keep accepts (all when keep is nil), sorted and unique.
func NativeUses(res *Result, keep func(file string) bool) []NativeUse {
	var out []NativeUse
	for _, e := range nativeEdges(res, keep) {
		if u, ok := nativeUse(res.Natives, kindOf(e.Kind), e.To); ok {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b NativeUse) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name)) })
	return slices.CompactFunc(out, func(a, b NativeUse) bool { return a.Kind == b.Kind && a.Name == b.Name })
}

// HostBuildIncompatibilities checks the native entries a compiled project
// uses, in the files keep accepts (all when keep is nil), against the
// native catalogue of one host build (WGT-032, ADR-0041): each use of an
// entry the build lacks, or declares with other types than the project's
// catalogue, is a warning naming the entry and the build, at the use's
// JSON path. None means the build can run what was compiled (REL-080).
func HostBuildIncompatibilities(res *Result, keep func(file string) bool, build string, catalogue *schema.NativeCatalogueDocument) plxerr.Diagnostics {
	var out plxerr.Diagnostics
	for _, e := range nativeEdges(res, keep) {
		use, ok := nativeUse(res.Natives, kindOf(e.Kind), e.To)
		if !ok {
			continue
		}
		loc := plxerr.Location{File: e.File, Path: e.Path}
		switch declared, same := use.In(catalogue); {
		case !declared:
			out = append(out, plxerr.NewDiagnostic(plxerr.HostBuildIncompatible, loc, "host build %s registers no %s %q", build, kindName[use.Kind], e.To))
		case !same:
			out = append(out, plxerr.NewDiagnostic(plxerr.HostBuildIncompatible, loc, "host build %s declares the %s %q with other types", build, kindName[use.Kind], e.To))
		}
	}
	out.Sort()
	return out
}

var kindName = map[string]string{NativeRouteUse: "native route", NativeSlotUse: "native slot", NativeActionUse: "custom action"}

// nativeEdges are the reference graph's uses of native entries, one per
// location.
func nativeEdges(res *Result, keep func(string) bool) []Edge {
	if res == nil || res.Graph == nil {
		return nil
	}
	var out []Edge
	for _, e := range res.Graph.Edges {
		if kindOf(e.Kind) != "" && (keep == nil || keep(e.File)) {
			out = append(out, e)
		}
	}
	return slices.CompactFunc(out, func(a, b Edge) bool { return a.File == b.File && a.Path == b.Path })
}

func kindOf(k EdgeKind) string {
	switch k {
	case EdgeNavigatesNative:
		return NativeRouteUse
	case EdgeUsesSlot:
		return NativeSlotUse
	case EdgeUsesAction:
		return NativeActionUse
	}
	return ""
}

// nativeUse is the entry kind/name of catalogue c with its types.
func nativeUse(c *schema.NativeCatalogueDocument, kind, name string) (NativeUse, bool) {
	if c == nil {
		return NativeUse{}, false
	}
	switch kind {
	case NativeRouteUse:
		if i := slices.IndexFunc(c.Routes, func(r schema.NativeRoute) bool { return r.Name == name }); i >= 0 {
			return NativeUse{Kind: kind, Name: name, Params: paramTypes(c.Routes[i].Params), Result: c.Routes[i].Result}, true
		}
	case NativeSlotUse:
		if i := slices.IndexFunc(c.Slots, func(s schema.NativeSlot) bool { return s.Type == name }); i >= 0 {
			return slotUse(c.Slots[i]), true
		}
	case NativeActionUse:
		if i := slices.IndexFunc(c.Actions, func(a schema.NativeAction) bool { return a.Name == name }); i >= 0 {
			return NativeUse{Kind: kind, Name: name, Params: paramTypes(c.Actions[i].Inputs), Result: c.Actions[i].Output}, true
		}
	}
	return NativeUse{}, false
}

func slotUse(s schema.NativeSlot) NativeUse {
	u := NativeUse{Kind: NativeSlotUse, Name: s.Type, Params: paramTypes(s.Props)}
	for _, ev := range s.Events {
		u.Events = append(u.Events, ev.Name+":"+ev.Payload)
	}
	return u
}

func paramTypes(ps []schema.Param) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name + ":" + p.Type
		if p.Required != nil && *p.Required && len(p.Default) == 0 {
			out[i] += "!"
		}
	}
	return out
}
