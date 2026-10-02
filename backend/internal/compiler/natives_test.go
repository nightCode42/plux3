// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"reflect"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// TestNativeDeclarationsReachTheAppBundle checks that the app bundle
// carries the native catalogue's routes, slots and custom actions with
// their declared types, in the catalogue's order, and that plugin bundles
// do not (ADR-0041, P4 plan A24).
// Verifies: NAV-002, WGT-033, ACT-060.
func TestNativeDeclarationsReachTheAppBundle(t *testing.T) {
	t.Parallel()
	res := compileFS(project(t, featuresDir))
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	read := readAll(t, res)
	schemasOf := func(b *bundle.Bundle) (*fbs.Schemas, []string) {
		var schemas *fbs.Schemas
		var strs []string
		for _, s := range b.Sections {
			switch s.Kind {
			case bundle.SectionSchemas:
				schemas = fbs.GetRootAsSchemas(s.Data, 0)
			case bundle.SectionStrings:
				st := fbs.GetRootAsStrings(s.Data, 0)
				for i := range st.StringsLength() {
					strs = append(strs, string(st.Strings(i)))
				}
			}
		}
		return schemas, strs
	}
	schemas, strs := schemasOf(read[0])
	if schemas == nil || schemas.NativeRoutesLength() != 1 || schemas.NativeSlotsLength() != 1 || schemas.NativeActionsLength() != 1 {
		t.Fatal("the app bundle lacks the native declarations")
	}
	var route fbs.NativeRouteDecl
	schemas.NativeRoutes(&route, 0)
	if strs[route.Name()] != "settings" {
		t.Errorf("route %q", strs[route.Name()])
	}
	var slot fbs.NativeSlotDecl
	schemas.NativeSlots(&slot, 0)
	var prop fbs.Param
	var event fbs.ComponentEvent
	slot.Props(&prop, 0)
	slot.Events(&event, 0)
	if strs[slot.Type()] != "MapView" || slot.PropsLength() != 1 || strs[prop.Name()] != "zoom" ||
		slot.EventsLength() != 1 || strs[event.Name()] != "onPan" {
		t.Errorf("slot %q %q %q", strs[slot.Type()], strs[prop.Name()], strs[event.Name()])
	}
	var action fbs.NativeActionDecl
	schemas.NativeActions(&action, 0)
	var input fbs.Param
	action.Inputs(&input, 0)
	if strs[action.Name()] != "shareNote" || strs[input.Name()] != "text" || strs[input.Type()] != "string" || strs[action.Output()] != "bool" {
		t.Errorf("action %q(%q %q) %q", strs[action.Name()], strs[input.Name()], strs[input.Type()], strs[action.Output()])
	}
	if plugin, _ := schemasOf(read[1]); plugin != nil && (plugin.NativeSlotsLength() != 0 || plugin.NativeActionsLength() != 0) {
		t.Error("a plugin bundle carries native declarations")
	}
}

// TestCustomActionOutputsAreTyped checks that a custom action's step, named
// after the action or as callNative, types steps.<id>.output by the
// catalogue's declared output, nullable (ACT-060).
// Verifies: ACT-060.
func TestCustomActionOutputsAreTyped(t *testing.T) {
	t.Parallel()
	const listFile = "plugins/tasks/pages/list.page.json"
	const steps = "root/slots/body/children/8/events/onPressed/steps"
	const message = steps + "/6/input/input/message"
	callNative := func(t *testing.T, doc map[string]any) {
		step := at(t, doc, steps+"/5")
		step["action"] = "callNative"
		step["input"] = raw(t, `{"action": "shareNote", "input": {"text": {"$expr": "env.apiBaseUrl"}}}`)
	}
	for _, tc := range []struct {
		name string
		edit func(*testing.T, map[string]any)
		expr string
		code plxerr.Code
	}{
		{"named, read as bool?", nil, `string(steps.share.output ?? false)`, 0},
		{"named, misused", nil, `steps.share.output + 1`, plxerr.PXLTypeMismatch},
		{"callNative, read as bool?", callNative, `string(steps.share.output ?? false)`, 0},
		{"callNative, misused", callNative, `steps.share.output + 1`, plxerr.PXLTypeMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, featuresDir)
			edit(t, m, listFile, func(doc map[string]any) {
				if tc.edit != nil {
					tc.edit(t, doc)
				}
				parent := at(t, doc, steps+"/6/input/input")
				parent["message"] = map[string]any{"$expr": tc.expr}
			})
			res := compileFS(m)
			if tc.code == 0 {
				if res.Diagnostics.HasErrors() {
					t.Fatalf("diagnostics:\n%v", res.Diagnostics)
				}
				return
			}
			wantDiag(t, res, tc.code, listFile, "/"+message+"/$expr")
		})
	}
}

// TestHostBuildIncompatibilities checks every native entry the routing
// project uses against host builds: one with the project's catalogue, one
// lacking the slot, and one declaring the route's result with another
// type (ADR-0041).
// Verifies: WGT-032, REL-080.
func TestHostBuildIncompatibilities(t *testing.T) {
	t.Parallel()
	res := compileFS(project(t, routingDir))
	if res.Diagnostics.HasErrors() || res.Natives == nil {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	same := *res.Natives
	if got := HostBuildIncompatibilities(res, nil, "1.0.0+1", &same); len(got) != 0 {
		t.Fatalf("the project's own catalogue: %v", got)
	}
	lacking := *res.Natives
	lacking.Slots = nil
	got := HostBuildIncompatibilities(res, nil, "1.0.0+2", &lacking)
	if len(got) != 1 || got[0].Code != plxerr.HostBuildIncompatible || got[0].Path != "/root/slots/body/slots/child/type" ||
		got[0].Message != `host build 1.0.0+2 registers no native slot "Counter"` {
		t.Fatalf("lacking the slot: %+v", got)
	}
	other := *res.Natives
	other.Routes = append([]schema.NativeRoute(nil), other.Routes...)
	other.Routes[0].Result = "string"
	got = HostBuildIncompatibilities(res, nil, "1.0.0+3", &other)
	if len(got) != 2 {
		t.Fatalf("another result type: want both uses of the route, got %+v", got)
	}
	for _, d := range got {
		if d.Message != `host build 1.0.0+3 declares the native route "profile" with other types` {
			t.Errorf("message %q", d.Message)
		}
	}
	if got := HostBuildIncompatibilities(res, nil, "0.9.0+1", nil); len(got) != 4 {
		t.Errorf("no catalogue at all: want the route twice, the slot and the action, got %d: %+v", len(got), got)
	}
	uses := NativeUses(res, nil)
	want := []NativeUse{
		{Kind: NativeActionUse, Name: "scan", Params: []string{"prompt:string"}, Result: "string"},
		{Kind: NativeRouteUse, Name: "profile", Params: []string{"userId:string!"}, Result: "bool"},
		{Kind: NativeSlotUse, Name: "Counter", Params: []string{"label:string!"}, Events: []string{"onTap:int"}},
	}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses %+v", uses)
	}
	if !Compatible(uses, &same) || Compatible(uses, &lacking) || Compatible(uses, &other) || Compatible(uses, nil) || !Compatible(nil, nil) {
		t.Error("Compatible disagrees with the diagnostics")
	}
	if got := NativeUses(res, func(f string) bool { return f == "plugins/nav/pages/slots.page.json" }); len(got) != 1 || got[0].Name != "Counter" {
		t.Errorf("uses of one file %+v", got)
	}
}
