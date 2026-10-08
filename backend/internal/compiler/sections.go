// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/icons"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// lower assigns every action graph to the bundle that carries it: a
// plugin's document graphs and the inline graphs of its pages and
// private components, and the inline graphs of shared components in the
// app bundle. Graphs are ordered by ID, so the actions section depends on
// content only.
func lower(u *unit) {
	byID := func(a, b *graph) int { return bytes.Compare(a.id[:], b.id[:]) }
	slices.SortFunc(u.appGraphs, byID)
	for _, pl := range u.plugins {
		pl.lowered = slices.SortedFunc(slices.Values(slices.Concat(pl.graphs, pl.inline)), byID)
	}
}

// encode writes the sections of every bundle (BND-002, BND-004).
func encode(u *unit) {
	kind := func(k bundle.Kind) bundle.Kind {
		if u.opts.Mode == Development {
			return bundle.KindDevelopment
		}
		return k
	}
	app := u.project.App.Doc
	appOut := newOut(kind(bundle.KindApp), nil, uuidBytes(app.ID), app.Key)
	for f := range u.features[nil] {
		appOut.features[f] = true
	}
	for _, c := range u.shared {
		appOut.add(bundle.SectionComponent, uuidBytes(c.doc.ID), u.componentSection(appOut, c))
	}
	u.actionsSection(appOut, u.appGraphs)
	u.localeSections(appOut)
	u.schemasSection(appOut, u.types.app, u.appState, u.appSources, app.Collections, app.DroppedCollections, app.Variables, app.UserContext)
	u.appOut = appOut
	for _, pl := range u.plugins {
		o := newOut(kind(bundle.KindPlugin), pl, uuidBytes(pl.doc.ID), pl.key)
		for f := range u.features[pl] {
			o.features[f] = true
		}
		if icon := pl.doc.Icon.Asset; icon != "" {
			o.assets[icon] = true
		}
		for _, pg := range pl.pages {
			o.add(bundle.SectionPage, uuidBytes(pg.doc.ID), u.pageSection(o, pg))
		}
		for _, c := range pl.components {
			o.add(bundle.SectionComponent, uuidBytes(c.doc.ID), u.componentSection(o, c))
		}
		u.timelinesSection(o, pl.pages)
		u.actionsSection(o, pl.lowered)
		u.schemasSection(o, u.types.plugin[pl], pl.state, pl.sources, pl.doc.Collections, pl.doc.DroppedCollections, nil, nil)
		u.pluginOuts = append(u.pluginOuts, o)
	}
}

// nodes writes a flat node array (BND-015): the kept nodes in pre-order,
// children and slot fills as indices, and records their locations.
func encodeNodes(e *valueEnc, sectionID [16]byte, all []*node) flatbuffers.UOffsetT {
	var kept []*node
	for _, n := range all {
		if !n.removed {
			n.index = uint32(len(kept)) //nolint:gosec // G115: bounded by page.nodes.
			kept = append(kept, n)
		}
	}
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(kept))
	for i, n := range kept {
		offs[i] = encodeNode(e, n)
		e.o.srcmap.node(sectionID, n.index, n.owner.file(), n.ptr)
	}
	return offsetVector(b, offs)
}

// node writes one node.
func encodeNode(e *valueEnc, n *node) flatbuffers.UOffsetT {
	b := e.b
	props := propVector(e, n.props)
	hs := handlers(b, n.handlers)
	children := u32Vector(b, indices(n.children))
	slots := slotVector(b, n.slots)
	visible := e.value(n.visible)
	sem := semanticsTable(e, n.semantics)
	overrides := overrides(e, n.overrides)
	ta := typeArguments(e, n)
	anim := nodeAnimationTable(e, n)
	testID := e.strs.of(n.doc.TestID)
	var native uint32
	if n.widget == nil && n.target == nil {
		native = e.strs.of(n.doc.Type)
	}
	fbs.NodeStart(b)
	hi, lo := uuidHalves(uuidBytes(n.doc.ID))
	fbs.NodeAddId(b, fbs.CreateUuid(b, hi, lo))
	switch {
	case n.widget != nil:
		fbs.NodeAddWidget(b, n.widget.ID)
	case n.target != nil:
		chi, clo := uuidHalves(uuidBytes(n.target.doc.ID))
		fbs.NodeAddComponent(b, fbs.CreateUuid(b, chi, clo))
		fbs.NodeAddComponentVersion(b, uint32(n.target.doc.Version)) //nolint:gosec // G115: versions are small.
	default:
		fbs.NodeAddNativeSlot(b, native)
	}
	fbs.NodeAddProps(b, props)
	fbs.NodeAddHandlers(b, hs)
	fbs.NodeAddChildren(b, children)
	fbs.NodeAddSlots(b, slots)
	if visible != 0 {
		fbs.NodeAddVisible(b, visible)
	}
	if sem != 0 {
		fbs.NodeAddSemantics(b, sem)
	}
	if testID != 0 {
		fbs.NodeAddTestId(b, testID)
	}
	fbs.NodeAddOverrides(b, overrides)
	if n.hints != 0 {
		fbs.NodeAddHints(b, byte(n.hints))
	}
	fbs.NodeAddTypeArguments(b, ta)
	if anim != 0 {
		fbs.NodeAddAnimation(b, anim)
	}
	return fbs.NodeEnd(b)
}

// slotVector writes the filled slots of a node.
func slotVector(b *flatbuffers.Builder, fills []*slotFill) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, 0, len(fills))
	for _, sf := range fills {
		if len(sf.nodes) == 0 {
			continue
		}
		v := u32Vector(b, indices(sf.nodes))
		fbs.SlotFillStart(b)
		fbs.SlotFillAddId(b, sf.id)
		fbs.SlotFillAddNodes(b, v)
		offs = append(offs, fbs.SlotFillEnd(b))
	}
	return offsetVector(b, offs)
}

// typeArguments writes the bound type parameters of a widget node, in
// the descriptor's order.
func typeArguments(e *valueEnc, n *node) flatbuffers.UOffsetT {
	var args []uint32
	if n.widget != nil {
		for _, p := range n.widget.TypeParameters {
			if t, ok := n.typeArgs[p]; ok {
				args = append(args, e.strs.of(t.String()))
			}
		}
	}
	return u32Vector(e.b, args)
}

// indices returns the array indices of kept nodes.
func indices(ns []*node) []uint32 {
	out := make([]uint32, 0, len(ns))
	for _, n := range ns {
		if !n.removed {
			out = append(out, n.index)
		}
	}
	return out
}

// propVector writes props keyed by ID.
func propVector(e *valueEnc, ps []*prop) flatbuffers.UOffsetT {
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(ps))
	for i, p := range ps {
		v := e.value(p.value)
		fbs.PropStart(b)
		fbs.PropAddId(b, p.id)
		fbs.PropAddValue(b, v)
		offs[i] = fbs.PropEnd(b)
	}
	return offsetVector(b, offs)
}

// semanticsTable writes a node's semantics.
func semanticsTable(e *valueEnc, s *semantics) flatbuffers.UOffsetT {
	if s == nil {
		return 0
	}
	b := e.b
	label, hint, val := e.value(s.label), e.value(s.hint), e.value(s.value)
	fbs.SemanticsStart(b)
	if label != 0 {
		fbs.SemanticsAddLabel(b, label)
	}
	if hint != 0 {
		fbs.SemanticsAddHint(b, hint)
	}
	if val != 0 {
		fbs.SemanticsAddValue(b, val)
	}
	fbs.SemanticsAddHeader(b, s.header)
	fbs.SemanticsAddButton(b, s.button)
	fbs.SemanticsAddLiveRegion(b, s.liveRegion)
	fbs.SemanticsAddExclude(b, s.excluded)
	return fbs.SemanticsEnd(b)
}

// overrides writes override layers (BND-016).
func overrides(e *valueEnc, os []*override) flatbuffers.UOffsetT {
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(os))
	for i, o := range os {
		props := propVector(e, o.props)
		key := e.strs.of(o.key)
		fbs.OverrideStart(b)
		fbs.OverrideAddKind(b, o.kind)
		fbs.OverrideAddKey(b, key)
		fbs.OverrideAddProps(b, props)
		offs[i] = fbs.OverrideEnd(b)
	}
	return offsetVector(b, offs)
}

// pageKinds maps a document page kind to the bundle's.
var pageKinds = map[schema.PageKind]fbs.PageKind{
	schema.PageKindScreen: fbs.PageKindScreen, schema.PageKindDialog: fbs.PageKindDialog,
	schema.PageKindBottomSheet: fbs.PageKindBottomSheet, schema.PageKindFullscreenDialog: fbs.PageKindFullscreenDialog,
}

// pageOptions are a page's route and security options as the page
// section stores them: string-table indices and the assurance level.
type pageOptions struct {
	transition, timeline, result, assurance uint32
	secure                                  bool
}

// pageOptionsOf reads a page's transition, result type and security.
func pageOptionsOf(e *valueEnc, pg *page) pageOptions {
	var o pageOptions
	if ro := pg.doc.RouteOptions; ro != nil {
		o.transition = e.strs.of(string(ro.Transition))
		o.timeline = e.strs.of(ro.Timeline)
	}
	o.result = e.strs.of(pg.doc.Result)
	if sec := pg.doc.Security; sec != nil {
		o.secure = sec.Secure != nil && *sec.Secure
		if lvl := string(sec.RequiresAssurance); len(lvl) == 3 {
			o.assurance = uint32(lvl[2] - '0')
		}
	}
	return o
}

// pageSection encodes a page with its own string table (CMP-020).
func (u *unit) pageSection(o *out, pg *page) []byte {
	b := flatbuffers.NewBuilder(4096)
	e := &valueEnc{u: u, o: o, b: b, strs: newInterner()}
	id := uuidBytes(pg.doc.ID)
	nodes := encodeNodes(e, id, pg.nodes)
	title := e.value(pg.title)
	params := e.params(pg.params)
	state := e.state(pg.state)
	sources := e.sources(pg.sources)
	lifecycle := handlers(b, pg.lifecycle)
	triggers := triggerTables(b, pg.triggers)
	forms := e.forms(pg.forms)
	var guards [][16]byte
	for _, g := range pg.guards {
		guards = append(guards, g.id)
	}
	gv := uuidVector(b, guards)
	key, route := e.strs.of(pg.doc.Key), e.strs.of(pg.route)
	opts := pageOptionsOf(e, pg)
	strs := e.strs.vector(b)
	fbs.PageStart(b)
	hi, lo := uuidHalves(id)
	fbs.PageAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.PageAddKey(b, key)
	fbs.PageAddRoute(b, route)
	fbs.PageAddKind(b, pageKinds[pg.doc.PageKind])
	if title != 0 {
		fbs.PageAddTitle(b, title)
	}
	fbs.PageAddParams(b, params)
	fbs.PageAddState(b, state)
	fbs.PageAddDataSources(b, sources)
	fbs.PageAddLifecycle(b, lifecycle)
	addOptional(b, triggers, fbs.PageAddTriggers)
	if opts.transition != 0 {
		fbs.PageAddTransition(b, opts.transition)
	}
	if opts.timeline != 0 {
		fbs.PageAddTransitionTimeline(b, opts.timeline)
	}
	fbs.PageAddGuards(b, gv)
	fbs.PageAddSecure(b, opts.secure)
	if opts.assurance != 0 {
		fbs.PageAddRequiresAssurance(b, opts.assurance)
	}
	fbs.PageAddNodes(b, nodes)
	fbs.PageAddStrings(b, strs)
	if opts.result != 0 {
		fbs.PageAddResult(b, opts.result)
	}
	addOptional(b, forms, fbs.PageAddForms)
	data := finish(b, fbs.PageEnd(b), bundle.SectionPage)
	if max := u.opts.Limits.Get(limits.BundlePageSectionSize); int64(len(data)) > max {
		u.report(plxerr.LimitExceeded, pg.file, "", "the page section has %d bytes, above bundle.pageSectionSize = %d", len(data), max)
	}
	return data
}

// componentSection encodes a component definition (CMP-021).
func (u *unit) componentSection(o *out, c *component) []byte {
	b := flatbuffers.NewBuilder(2048)
	e := &valueEnc{u: u, o: o, b: b, strs: newInterner()}
	id := uuidBytes(c.doc.ID)
	nodes := encodeNodes(e, id, c.nodes)
	props := e.params(c.props)
	state := e.state(c.state)
	slotOffs := make([]flatbuffers.UOffsetT, len(c.doc.Slots))
	for i, s := range c.doc.Slots {
		name := e.strs.of(s.Name)
		fbs.ComponentSlotStart(b)
		fbs.ComponentSlotAddName(b, name)
		fbs.ComponentSlotAddRequired(b, s.Required != nil && *s.Required)
		fbs.ComponentSlotAddMultiple(b, s.Multiple != nil && *s.Multiple)
		slotOffs[i] = fbs.ComponentSlotEnd(b)
	}
	slots := offsetVector(b, slotOffs)
	eventOffs := make([]flatbuffers.UOffsetT, len(c.doc.Events))
	for i, ev := range c.doc.Events {
		name, payload := e.strs.of(ev.Name), e.strs.of(ev.Payload)
		fbs.ComponentEventStart(b)
		fbs.ComponentEventAddName(b, name)
		fbs.ComponentEventAddPayload(b, payload)
		eventOffs[i] = fbs.ComponentEventEnd(b)
	}
	events := offsetVector(b, eventOffs)
	forms := e.forms(c.forms)
	key := e.strs.of(c.doc.Key)
	strs := e.strs.vector(b)
	fbs.ComponentStart(b)
	hi, lo := uuidHalves(id)
	fbs.ComponentAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.ComponentAddKey(b, key)
	fbs.ComponentAddVersion(b, uint32(c.doc.Version)) //nolint:gosec // G115: versions are small.
	fbs.ComponentAddProps(b, props)
	fbs.ComponentAddSlots(b, slots)
	fbs.ComponentAddEvents(b, events)
	fbs.ComponentAddState(b, state)
	fbs.ComponentAddNodes(b, nodes)
	fbs.ComponentAddStrings(b, strs)
	addOptional(b, forms, fbs.ComponentAddForms)
	return finish(b, fbs.ComponentEnd(b), bundle.SectionComponent)
}

// errorKinds maps retry error kinds to the bundle's.
var errorKinds = map[schema.ErrorKind]fbs.ErrorKind{
	schema.ErrorKindNetwork: fbs.ErrorKindNetwork, schema.ErrorKindHTTP: fbs.ErrorKindHttp, schema.ErrorKindTimeout: fbs.ErrorKindTimeout,
	schema.ErrorKindValidation: fbs.ErrorKindValidation, schema.ErrorKindFunction: fbs.ErrorKindFunction,
	schema.ErrorKindPermission: fbs.ErrorKindPermission, schema.ErrorKindCancelled: fbs.ErrorKindCancelled, schema.ErrorKindCustom: fbs.ErrorKindCustom,
}

// actionsSection encodes the graphs of a bundle; strings go to the
// bundle's strings section.
func (u *unit) actionsSection(o *out, graphs []*graph) {
	if len(graphs) == 0 {
		return
	}
	b := flatbuffers.NewBuilder(4096)
	e := &valueEnc{u: u, o: o, b: b, strs: o.shared}
	offs := make([]flatbuffers.UOffsetT, len(graphs))
	for i, g := range graphs {
		offs[i] = graphTable(e, g)
	}
	gv := offsetVector(b, offs)
	fbs.ActionsStart(b)
	fbs.ActionsAddGraphs(b, gv)
	o.add(bundle.SectionActions, o.id, finish(b, fbs.ActionsEnd(b), bundle.SectionActions))
}

// graphTable writes one graph.
func graphTable(e *valueEnc, g *graph) flatbuffers.UOffsetT {
	b := e.b
	steps := make([]flatbuffers.UOffsetT, len(g.lowered))
	for i, s := range g.lowered {
		steps[i] = stepTable(e, s)
		e.o.srcmap.step(g.id, uint32(i), g.file, s.ptr) //nolint:gosec // G115: bounded by the document size.
	}
	sv := offsetVector(b, steps)
	var inputs flatbuffers.UOffsetT
	exported := false
	if g.doc != nil {
		var ps []*param
		for _, p := range g.doc.Inputs {
			ps = append(ps, &param{id: uuidBytes(p.ID), name: p.Name, typ: p.Type, required: p.Required != nil && *p.Required})
		}
		inputs = e.params(ps)
		exported = g.doc.Exported != nil && *g.doc.Exported
	}
	var state flatbuffers.UOffsetT
	if len(g.state) > 0 {
		state = e.state(g.state)
	}
	key, output := e.strs.of(g.key), e.strs.of(g.output)
	fbs.GraphStart(b)
	hi, lo := uuidHalves(g.id)
	fbs.GraphAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.GraphAddKey(b, key)
	if g.page != nil {
		phi, plo := uuidHalves(uuidBytes(g.page.doc.ID))
		fbs.GraphAddPage(b, fbs.CreateUuid(b, phi, plo))
	}
	fbs.GraphAddExported(b, exported)
	if inputs != 0 {
		fbs.GraphAddInputs(b, inputs)
	}
	if output != 0 {
		fbs.GraphAddOutput(b, output)
	}
	fbs.GraphAddSteps(b, sv)
	if state != 0 {
		fbs.GraphAddState(b, state)
	}
	return fbs.GraphEnd(b)
}

// stepTable writes one lowered step.
func stepTable(e *valueEnc, s *step) flatbuffers.UOffsetT {
	b := e.b
	inputs := propVector(e, s.inputs)
	brOffs := make([]flatbuffers.UOffsetT, len(s.branches))
	for i, br := range s.branches {
		name := e.strs.of(br.name)
		fbs.BranchStart(b)
		fbs.BranchAddName(b, name)
		fbs.BranchAddStep(b, br.step)
		brOffs[i] = fbs.BranchEnd(b)
	}
	branches := offsetVector(b, brOffs)
	var retry flatbuffers.UOffsetT
	if r := s.retry; r != nil {
		kinds := make([]byte, len(r.On))
		for i, k := range r.On {
			kinds[i] = byte(errorKinds[k])
		}
		on := b.CreateByteVector(kinds)
		fbs.RetryStart(b)
		fbs.RetryAddCount(b, clampU32(r.Count))
		if r.BackoffMs != nil {
			fbs.RetryAddBackoffMs(b, clampU32(*r.BackoffMs))
		}
		if r.MaxBackoffMs != nil {
			fbs.RetryAddMaxBackoffMs(b, clampU32(*r.MaxBackoffMs))
		}
		fbs.RetryAddJitter(b, r.Jitter != nil && *r.Jitter)
		fbs.RetryAddOn(b, on)
		retry = fbs.RetryEnd(b)
	}
	redact := redactVector(b, s.redact)
	id := e.strs.of(s.id)
	fbs.StepStart(b)
	fbs.StepAddId(b, id)
	fbs.StepAddAction(b, s.action)
	fbs.StepAddInput(b, inputs)
	fbs.StepAddNext(b, s.next)
	fbs.StepAddOnSuccess(b, s.onSuccess)
	fbs.StepAddOnError(b, s.onError)
	fbs.StepAddBranches(b, branches)
	if retry != 0 {
		fbs.StepAddRetry(b, retry)
	}
	if s.timeoutMs != 0 {
		fbs.StepAddTimeoutMs(b, s.timeoutMs)
	}
	addOptional(b, redact, fbs.StepAddRedact)
	return fbs.StepEnd(b)
}

// clampU32 clamps a document integer to uint32.
func clampU32(v int64) uint32 { return uint32(max(0, min(v, 1<<32-1))) } //nolint:gosec // G115: clamped.

// localeSections encodes one l10n section per locale.
func (u *unit) localeSections(o *out) {
	for _, t := range u.project.Translations {
		if t.Doc == nil {
			continue
		}
		id, err := bundle.LocaleID(t.Doc.Locale)
		if err != nil {
			u.report(plxerr.OutOfRange, t.Source.File, "/locale", "%v", err)
			continue
		}
		b := flatbuffers.NewBuilder(1024)
		keys := sortedKeys(t.Doc.Messages)
		slices.SortFunc(keys, func(a, c string) int {
			x, y := uuidBytes(a), uuidBytes(c)
			return bytes.Compare(x[:], y[:])
		})
		offs := make([]flatbuffers.UOffsetT, len(keys))
		for i, k := range keys {
			text := b.CreateString(t.Doc.Messages[k])
			fbs.MessageStart(b)
			hi, lo := uuidHalves(uuidBytes(k))
			fbs.MessageAddKey(b, fbs.CreateUuid(b, hi, lo))
			fbs.MessageAddText(b, text)
			offs[i] = fbs.MessageEnd(b)
		}
		mv := offsetVector(b, offs)
		tag := b.CreateString(t.Doc.Locale)
		fbs.LocaleStart(b)
		fbs.LocaleAddTag(b, tag)
		fbs.LocaleAddMessages(b, mv)
		o.add(bundle.SectionL10n, id, finish(b, fbs.LocaleEnd(b), bundle.SectionL10n))
	}
}

// schemasSection encodes declared types, state, data sources, collections,
// variables and user context (BND-004, BND-017).
func (u *unit) schemasSection(o *out, types map[string]pxl.TypeSpec, state []*stateEntry, sources []*dataSource,
	cols []schema.Collection, dropped []string, vars, userContext []schema.Field,
) {
	b := flatbuffers.NewBuilder(2048)
	e := &valueEnc{u: u, o: o, b: b, strs: o.shared}
	typeOffs := make([]flatbuffers.UOffsetT, 0, len(types))
	for _, name := range sortedKeys(types) {
		spec := types[name]
		var fields []*param
		for _, f := range sortedKeys(spec.Fields) {
			fields = append(fields, &param{name: f, typ: spec.Fields[f]})
		}
		fv := e.params(fields)
		members := make([]uint32, len(spec.Enum))
		for i, m := range spec.Enum {
			members[i] = e.strs.of(m)
		}
		mv := u32Vector(b, members)
		n := e.strs.of(name)
		fbs.TypeDeclStart(b)
		fbs.TypeDeclAddName(b, n)
		fbs.TypeDeclAddFields(b, fv)
		fbs.TypeDeclAddMembers(b, mv)
		typeOffs = append(typeOffs, fbs.TypeDeclEnd(b))
	}
	tv := offsetVector(b, typeOffs)
	sv := e.state(state)
	dv := e.sources(sources)
	colOffs := make([]flatbuffers.UOffsetT, len(cols))
	for i, c := range cols {
		colOffs[i] = collectionTable(e, c)
	}
	cv := offsetVector(b, colOffs)
	dropV := droppedVector(b, dropped)
	vv := e.params(fieldParams(vars))
	uv := e.params(fieldParams(userContext))
	var natives nativeDecls
	if o.pl == nil {
		natives = u.nativeDeclarations(e)
	}
	fbs.SchemasStart(b)
	fbs.SchemasAddTypes(b, tv)
	fbs.SchemasAddState(b, sv)
	fbs.SchemasAddDataSources(b, dv)
	fbs.SchemasAddCollections(b, cv)
	fbs.SchemasAddVariables(b, vv)
	fbs.SchemasAddUserContext(b, uv)
	addOptional(b, dropV, fbs.SchemasAddDroppedCollections)
	addOptional(b, natives.routes, fbs.SchemasAddNativeRoutes)
	addOptional(b, natives.slots, fbs.SchemasAddNativeSlots)
	addOptional(b, natives.actions, fbs.SchemasAddNativeActions)
	o.add(bundle.SectionSchemas, o.id, finish(b, fbs.SchemasEnd(b), bundle.SectionSchemas))
}

// fieldParams converts declared fields.
func fieldParams(fs []schema.Field) []*param {
	out := make([]*param, len(fs))
	for i, f := range fs {
		out[i] = &param{name: f.Name, typ: f.Type, sensitive: f.Sensitive != nil && *f.Sensitive}
	}
	return out
}

// collectionTable writes a local collection.
func collectionTable(e *valueEnc, c schema.Collection) flatbuffers.UOffsetT {
	b := e.b
	fv := e.params(fieldParams(c.Fields))
	pk := make([]uint32, len(c.PrimaryKey))
	for i, k := range c.PrimaryKey {
		pk[i] = e.strs.of(k)
	}
	pkv := u32Vector(b, pk)
	idxOffs := make([]flatbuffers.UOffsetT, len(c.Indexes))
	for i, idx := range c.Indexes {
		fields := make([]uint32, len(idx))
		for j, f := range idx {
			fields[j] = e.strs.of(f)
		}
		v := u32Vector(b, fields)
		fbs.IndexStart(b)
		fbs.IndexAddFields(b, v)
		idxOffs[i] = fbs.IndexEnd(b)
	}
	iv := offsetVector(b, idxOffs)
	mv := collectionMigrations(e, c.Migrations)
	key := e.strs.of(c.Key)
	fbs.CollectionStart(b)
	hi, lo := uuidHalves(uuidBytes(c.ID))
	fbs.CollectionAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.CollectionAddKey(b, key)
	fbs.CollectionAddFields(b, fv)
	fbs.CollectionAddPrimaryKey(b, pkv)
	fbs.CollectionAddIndexes(b, iv)
	if v := collectionVersion(c); v > 1 {
		fbs.CollectionAddVersion(b, uint32(min(v, 1<<32-1))) //nolint:gosec // G115: clamped.
	}
	addOptional(b, mv, fbs.CollectionAddMigrations)
	return fbs.CollectionEnd(b)
}

// collectionMigrations writes a collection's migration plans, oldest
// first; 0 when it has none (DB-005).
func collectionMigrations(e *valueEnc, plans []schema.CollectionMigration) flatbuffers.UOffsetT {
	if len(plans) == 0 {
		return 0
	}
	b := e.b
	sorted := slices.Clone(plans)
	slices.SortFunc(sorted, func(x, y schema.CollectionMigration) int { return int(x.From - y.From) })
	offs := make([]flatbuffers.UOffsetT, len(sorted))
	for i, m := range sorted {
		strs := func(names []string) flatbuffers.UOffsetT {
			ids := make([]uint32, len(names))
			for j, n := range names {
				ids[j] = e.strs.of(n)
			}
			return u32Vector(b, ids)
		}
		dv, rv := strs(m.Drop), strs(m.Reset)
		to := sortedKeys(m.Rename)
		renames := make([]flatbuffers.UOffsetT, len(to))
		for j, n := range to {
			nn, old := e.strs.of(n), e.strs.of(m.Rename[n])
			fbs.FieldRenameStart(b)
			fbs.FieldRenameAddTo(b, nn)
			fbs.FieldRenameAddFrom(b, old)
			renames[j] = fbs.FieldRenameEnd(b)
		}
		rnv := offsetVector(b, renames)
		fbs.CollectionMigrationStart(b)
		fbs.CollectionMigrationAddFrom(b, uint32(max(0, min(m.From, 1<<32-1)))) //nolint:gosec // G115: clamped.
		fbs.CollectionMigrationAddRename(b, rnv)
		fbs.CollectionMigrationAddDrop(b, dv)
		fbs.CollectionMigrationAddReset(b, rv)
		offs[i] = fbs.CollectionMigrationEnd(b)
	}
	return offsetVector(b, offs)
}

// droppedVector writes the IDs of dropped collections, sorted; 0 when
// there are none (DB-005).
func droppedVector(b *flatbuffers.Builder, ids []string) flatbuffers.UOffsetT {
	if len(ids) == 0 {
		return 0
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	fbs.SchemasStartDroppedCollectionsVector(b, len(sorted))
	for i := len(sorted) - 1; i >= 0; i-- {
		hi, lo := uuidHalves(uuidBytes(sorted[i]))
		fbs.CreateUuid(b, hi, lo)
	}
	return b.EndVector(len(sorted))
}

// assets adds the assets-index sections — every asset to the app bundle,
// the assets it uses to each plugin bundle — and then the sections every
// bundle shares: programs, styles, strings, meta and the source map.
// Asset files are not in the bundles: each is its own content-addressed
// object, and the index lists it with its transcoded variants (CMP-030).
func assets(u *unit) {
	for _, o := range u.outs() {
		u.assetsSection(o)
		u.pluginAssetBytes(o)
		u.metaSection(o)
		programsSection(o)
		u.stylesSection(o)
		stringsSection(o)
		sourceMapSection(o)
	}
}

// outs lists every bundle being encoded: the app's, then the plugins'.
func (u *unit) outs() []*out { return append([]*out{u.appOut}, u.pluginOuts...) }

// indexedAsset is an asset a bundle indexes: an uploaded one, or an icon
// font the compilation made.
type indexedAsset struct {
	id         [16]byte
	key, media string
	data       []byte
}

// iconFontKey is the key of an icon set's font in an assets index; asset
// keys of documents are slugs, so it can never be one of theirs.
func iconFontKey(set icons.Set) string { return "@icons/" + string(set) }

// assetsSection encodes the assets a bundle uses, with its icon fonts.
func (u *unit) assetsSection(o *out) {
	var entries []indexedAsset
	if idx := u.project.Assets; idx != nil && idx.Doc != nil {
		for i := range idx.Doc.Assets {
			a := &idx.Doc.Assets[i]
			if o.pl == nil || o.assets[a.ID] {
				entries = append(entries, indexedAsset{uuidBytes(a.ID), a.Key, string(a.MediaType), u.project.AssetFiles[a.File]})
			}
		}
	}
	o.iconFonts = u.iconFonts(o)
	entries = append(entries, o.iconFonts...)
	if len(entries) == 0 {
		return
	}
	slices.SortFunc(entries, func(a, c indexedAsset) int { return bytes.Compare(a.id[:], c.id[:]) })
	b := flatbuffers.NewBuilder(1024)
	offs := make([]flatbuffers.UOffsetT, len(entries))
	for i, a := range entries {
		sum := sha256.Sum256(a.data)
		hash := b.CreateByteVector(sum[:])
		key, media := b.CreateString(a.key), b.CreateString(a.media)
		variants := u.variantsOf(b, sum)
		fbs.AssetStart(b)
		hi, lo := uuidHalves(a.id)
		fbs.AssetAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.AssetAddKey(b, key)
		fbs.AssetAddMediaType(b, media)
		fbs.AssetAddHash(b, hash)
		fbs.AssetAddSize(b, uint64(len(a.data)))
		if variants != 0 {
			fbs.AssetAddVariants(b, variants)
		}
		offs[i] = fbs.AssetEnd(b)
	}
	av := offsetVector(b, offs)
	fbs.AssetIndexStart(b)
	fbs.AssetIndexAddAssets(b, av)
	o.add(bundle.SectionAssetsIndex, o.id, finish(b, fbs.AssetIndexEnd(b), bundle.SectionAssetsIndex))
}

// iconFonts builds the icon fonts of a bundle: one per set its documents
// use, subset to their icons (THM-005), kept as files of the compilation.
func (u *unit) iconFonts(o *out) []indexedAsset {
	used := u.icons[o.pl]
	if u.opts.IconFont == nil || len(used) == 0 {
		return nil
	}
	var out []indexedAsset
	for _, set := range icons.Sets() {
		if len(used[set]) == 0 {
			continue
		}
		data, err := u.opts.IconFont(set, slices.Sorted(maps.Keys(used[set])))
		if err != nil {
			u.internalError("the %s icon font of %s: %v", set, o.key, err)
			return nil
		}
		u.files[sha256.Sum256(data)] = data
		out = append(out, indexedAsset{derivedID("icons", o.key, string(set)), iconFontKey(set), "font/ttf", data})
	}
	return out
}

// variantsOf encodes an asset file's variants, sorted by media type and
// density; 0 when it has none.
func (u *unit) variantsOf(b *flatbuffers.Builder, sum [sha256.Size]byte) flatbuffers.UOffsetT {
	if u.opts.AssetVariants == nil {
		return 0
	}
	vs := slices.Clone(u.opts.AssetVariants(sum))
	if len(vs) == 0 {
		return 0
	}
	slices.SortFunc(vs, func(a, c AssetVariant) int {
		if n := strings.Compare(a.MediaType, c.MediaType); n != 0 {
			return n
		}
		return a.Density - c.Density
	})
	offs := make([]flatbuffers.UOffsetT, len(vs))
	for i, v := range vs {
		media, hash := b.CreateString(v.MediaType), b.CreateByteVector(v.Hash[:])
		fbs.AssetVariantStart(b)
		fbs.AssetVariantAddMediaType(b, media)
		fbs.AssetVariantAddDensity(b, uint32(v.Density)) //nolint:gosec // 1 to 3
		fbs.AssetVariantAddWidth(b, uint32(v.Width))     //nolint:gosec // bounded by asset.imagePixels
		fbs.AssetVariantAddHeight(b, uint32(v.Height))   //nolint:gosec // bounded by asset.imagePixels
		fbs.AssetVariantAddHash(b, hash)
		fbs.AssetVariantAddSize(b, uint64(v.Size)) //nolint:gosec // a file size
		offs[i] = fbs.AssetVariantEnd(b)
	}
	return offsetVector(b, offs)
}

// pluginAssetBytes checks the asset files a plugin uses against
// plugin.assetBytes at publish (AST-003).
func (u *unit) pluginAssetBytes(o *out) {
	if o.pl == nil {
		return
	}
	var total int64
	idx := u.project.Assets
	if idx == nil || idx.Doc == nil {
		idx = &schema.Loaded[schema.AssetIndexDocument]{Doc: &schema.AssetIndexDocument{}}
	}
	for _, a := range idx.Doc.Assets {
		if o.assets[a.ID] {
			total += int64(len(u.project.AssetFiles[a.File]))
		}
	}
	for _, f := range o.iconFonts {
		total += int64(len(f.data))
	}
	if limit := u.opts.Limits.Get(limits.PluginAssetBytes); total > limit {
		u.report(plxerr.LimitExceeded, o.pl.file, "", "plugin %s uses %d bytes of assets, above plugin.assetBytes = %d", o.key, total, limit)
	} else {
		u.approaching(limits.PluginAssetBytes, o.pl.file, total, "plugin "+o.key+"'s assets")
	}
}

// approaching warns when a value has passed a limit's warning threshold,
// 80% unless the registry sets another, without exceeding the limit
// (LIM-003).
func (u *unit) approaching(key limits.Key, file string, v int64, what string) {
	limit, warn := u.opts.Limits.Get(key), u.opts.Limits.Warning(key)
	if v > warn && v <= limit {
		u.report(plxerr.LimitApproaching, file, "", "%s has %d of the %d bytes %s allows", what, v, limit, key)
	}
}

// programsSection encodes the programs of a bundle, sorted by ID.
func programsSection(o *out) {
	if len(o.programs) == 0 {
		return
	}
	ids := make([]uint64, 0, len(o.programs))
	for id := range o.programs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	b := flatbuffers.NewBuilder(4096)
	offs := make([]flatbuffers.UOffsetT, len(ids))
	for i, id := range ids {
		code := b.CreateByteVector(o.programs[id].Encode())
		fbs.ProgramStart(b)
		fbs.ProgramAddId(b, id)
		fbs.ProgramAddCode(b, code)
		offs[i] = fbs.ProgramEnd(b)
	}
	pv := offsetVector(b, offs)
	fbs.ProgramsStart(b)
	fbs.ProgramsAddPrograms(b, pv)
	o.add(bundle.SectionPXL, o.id, finish(b, fbs.ProgramsEnd(b), bundle.SectionPXL))
}

// stylesSection encodes the styles of a bundle and, in the app bundle,
// the design tokens (THM-001: values as JSON-shaped values with $type).
func (u *unit) stylesSection(o *out) {
	var tokens []*token
	if o.pl == nil {
		for _, path := range sortedKeys(u.tokens) {
			tokens = append(tokens, u.tokens[path])
		}
	}
	if len(o.styles) == 0 && len(tokens) == 0 {
		return
	}
	b := flatbuffers.NewBuilder(4096)
	e := &valueEnc{u: u, o: o, b: b, strs: o.shared, inStyle: true}
	ids := make([]uint64, 0, len(o.styles))
	for id := range o.styles {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	offs := make([]flatbuffers.UOffsetT, len(ids))
	for i, id := range ids {
		st := o.styles[id]
		v := e.value(st.value)
		fbs.StyleStart(b)
		fbs.StyleAddId(b, id)
		fbs.StyleAddType(b, st.typ)
		fbs.StyleAddValue(b, v)
		offs[i] = fbs.StyleEnd(b)
	}
	sv := offsetVector(b, offs)
	tokOffs := make([]flatbuffers.UOffsetT, len(tokens))
	for i, t := range tokens {
		light, dark := e.value(jsonValue(t.light)), e.value(jsonValue(t.dark))
		path, typ := b.CreateString(t.path), b.CreateString(t.typ)
		fbs.TokenStart(b)
		fbs.TokenAddPath(b, path)
		if light != 0 {
			fbs.TokenAddLight(b, light)
		}
		if dark != 0 {
			fbs.TokenAddDark(b, dark)
		}
		fbs.TokenAddType(b, typ)
		tokOffs[i] = fbs.TokenEnd(b)
	}
	tv := offsetVector(b, tokOffs)
	fbs.StylesStart(b)
	fbs.StylesAddStyles(b, sv)
	fbs.StylesAddTokens(b, tv)
	o.add(bundle.SectionStyles, o.id, finish(b, fbs.StylesEnd(b), bundle.SectionStyles))
}

// jsonValue converts a raw JSON value into a value of its JSON shape.
func jsonValue(raw json.RawMessage) *value {
	if len(raw) == 0 {
		return nil
	}
	v, ok := decodeJSON(raw)
	if !ok {
		return nil
	}
	return shapeValue(v)
}

func shapeValue(v any) *value {
	switch x := v.(type) {
	case map[string]any:
		out := &value{kind: fbs.ValueKindMap}
		for _, k := range sortedKeys(x) {
			out.entries = append(out.entries, entry{key: k, value: shapeValue(x[k])})
		}
		return out
	case []any:
		out := &value{kind: fbs.ValueKindList}
		for _, it := range x {
			out.items = append(out.items, shapeValue(it))
		}
		return out
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return &value{kind: fbs.ValueKindInt, i: i}
		}
		f, _ := x.Float64()
		return &value{kind: fbs.ValueKindDouble, d: f}
	case string:
		return &value{kind: fbs.ValueKindString, s: x}
	case bool:
		return primitiveValue(x)
	}
	return &value{kind: fbs.ValueKindNull}
}

// stringsSection encodes the shared string table, written last because
// every shared section adds to it.
func stringsSection(o *out) {
	if len(o.shared.list) == 1 {
		return
	}
	b := flatbuffers.NewBuilder(4096)
	v := o.shared.vector(b)
	fbs.StringsStart(b)
	fbs.StringsAddStrings(b, v)
	o.add(bundle.SectionStrings, o.id, finish(b, fbs.StringsEnd(b), bundle.SectionStrings))
}

// sourceMap collects locations of nodes and steps (CMP-041).
type sourceMap struct {
	nodes []location
	steps []location
}

// location is a node or step and where it is written.
type location struct {
	section [16]byte
	index   uint32
	file    string
	ptr     string
}

func (m *sourceMap) node(section [16]byte, index uint32, file, ptr string) {
	m.nodes = append(m.nodes, location{section, index, file, ptr})
}

func (m *sourceMap) step(graph [16]byte, index uint32, file, ptr string) {
	m.steps = append(m.steps, location{graph, index, file, ptr})
}

// sourceMapSection encodes the source map: embedded in development
// bundles, returned separately for release bundles (CMP-041).
func sourceMapSection(o *out) {
	b := flatbuffers.NewBuilder(4096)
	files := newInterner()
	write := func(locs []location) flatbuffers.UOffsetT {
		slices.SortFunc(locs, func(x, y location) int {
			return cmp.Or(bytes.Compare(x.section[:], y.section[:]), cmp.Compare(x.index, y.index))
		})
		offs := make([]flatbuffers.UOffsetT, len(locs))
		for i, l := range locs {
			ptr := b.CreateString(l.ptr)
			file := files.of(l.file)
			fbs.LocationStart(b)
			hi, lo := uuidHalves(l.section)
			fbs.LocationAddSection(b, fbs.CreateUuid(b, hi, lo))
			fbs.LocationAddIndex(b, l.index)
			fbs.LocationAddFile(b, file)
			fbs.LocationAddPointer(b, ptr)
			offs[i] = fbs.LocationEnd(b)
		}
		return offsetVector(b, offs)
	}
	nv, sv := write(o.srcmap.nodes), write(o.srcmap.steps)
	fv := files.vector(b)
	fbs.SourceMapStart(b)
	fbs.SourceMapAddFiles(b, fv)
	fbs.SourceMapAddNodes(b, nv)
	fbs.SourceMapAddSteps(b, sv)
	data := finish(b, fbs.SourceMapEnd(b), bundle.SectionSourceMap)
	if o.kind == bundle.KindDevelopment {
		o.add(bundle.SectionSourceMap, o.id, data)
		return
	}
	o.srcmapData = data
}

// hash lays out each bundle's container, which hashes every section and
// the directory (BND-005), checks it by reading it back, and enforces the
// bundle and release size limits (BND-010).
func hash(u *unit) {
	var total int64
	for _, o := range u.outs() {
		data, err := bundle.Encode(o.kind, o.sections)
		if err != nil {
			u.internalError("bundle %s: %v", o.key, err)
			return
		}
		b, err := bundle.Read(data, bundle.ReadOptions{Limits: u.opts.Limits, Supports: func(string) bool { return true }})
		if err != nil {
			if code, ok := plxerr.CodeOf(err); ok && code == plxerr.LimitExceeded {
				u.report(plxerr.LimitExceeded, "", "", "bundle %s: %v", o.key, err)
				continue
			}
			u.internalError("bundle %s does not read back: %v", o.key, err)
			return
		}
		total += int64(len(data))
		if o.pl != nil {
			u.approaching(limits.BundlePluginSize, o.pl.file, int64(len(data)), "bundle "+o.key)
		}
		out := &Bundle{Kind: o.kind, ID: o.id, Key: o.key, Data: data, Hash: b.Hash, Features: featureList(o.features), SourceMap: o.srcmapData}
		if o.pl == nil {
			u.app = out
		} else {
			u.outputs = append(u.outputs, out)
		}
	}
	if max := u.opts.Limits.Get(limits.ReleaseAppSize); total > max {
		u.report(plxerr.LimitExceeded, "app.json", "", "the release has %d bytes, above release.appSize = %d", total, max)
	} else {
		u.approaching(limits.ReleaseAppSize, "app.json", total, "the release")
	}
	slices.SortFunc(u.outputs, func(a, b *Bundle) int { return strings.Compare(a.Key, b.Key) })
}

// featureList returns features sorted.
func featureList(set map[string]bool) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for f := range set {
			if !yield(f) {
				return
			}
		}
	})
}

// createUUID writes the UUID in its canonical text form as a bundle UUID
// struct.
func createUUID(b *flatbuffers.Builder, id string) flatbuffers.UOffsetT {
	hi, lo := uuidHalves(uuidBytes(id))
	return fbs.CreateUuid(b, hi, lo)
}

// metaSection encodes the identity, versions, required features and
// limits of a bundle (CMP-005, BND-008). Values in it use the bundle's
// strings section.
func (u *unit) metaSection(o *out) {
	app := u.project.App.Doc
	b := flatbuffers.NewBuilder(2048)
	e := &valueEnc{u: u, o: o, b: b, strs: o.shared}
	features := stringVector(b, featureList(o.features))
	lv := u.runtimeLimits(b)
	var pages, exported, plugins, locales, flags, sampling, hostEvents flatbuffers.UOffsetT
	name, key := app.Name, app.Key
	if o.pl != nil {
		name, key = o.pl.doc.Name, o.pl.key
		pages = pageEntries(b, o.pl)
		exported = componentEntries(b, o.pl)
	} else {
		plugins = u.pluginIDs(b)
		locales = stringVector(b, app.SupportedLocales)
		flags = u.flagTables(e, app.Flags)
		sampling = samplingTables(b, app.Telemetry)
		hostEvents = hostEventTables(b, app.HostEvents)
	}
	capabilities := u.capabilitiesOf(b, o)
	nameOff, keyOff := b.CreateString(name), b.CreateString(key)
	compiler, schemaVersion, minRuntime := b.CreateString(u.opts.Version), b.CreateString(schema.CurrentVersion), b.CreateString(app.MinRuntimeVersion)
	tv := triggerTables(b, u.ownTriggers(o))
	var defLocale, entryRoute, profile, notFound, shells, links, push flatbuffers.UOffsetT
	if o.pl == nil {
		defLocale, entryRoute, profile = b.CreateString(app.DefaultLocale), b.CreateString(app.EntryRoute), b.CreateString(string(app.SecurityProfile))
		if nav := app.Navigation; nav != nil && nav.NotFound != "" {
			notFound = b.CreateString(nav.NotFound)
		}
		shells = u.shellTables(e)
		links, push = u.deepLinksTable(b), u.pushTable(b)
	}
	fbs.MetaStart(b)
	fbs.MetaAddKind(b, fbs.BundleKind(o.kind)) //nolint:gosec // G115: bundle kinds are 1–3.
	hi, lo := uuidHalves(o.id)
	fbs.MetaAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.MetaAddKey(b, keyOff)
	fbs.MetaAddName(b, nameOff)
	fbs.MetaAddCompilerVersion(b, compiler)
	fbs.MetaAddSchemaVersion(b, schemaVersion)
	fbs.MetaAddRequiredFeatures(b, features)
	fbs.MetaAddMinRuntime(b, minRuntime)
	fbs.MetaAddLimits(b, lv)
	addOptional(b, tv, fbs.MetaAddTriggers)
	if o.pl != nil {
		fbs.MetaAddCapabilities(b, capabilities)
		fbs.MetaAddPages(b, pages)
		fbs.MetaAddEntryPage(b, createUUID(b, o.pl.doc.EntryPage))
		if o.pl.doc.FallbackPage != "" {
			fbs.MetaAddFallbackPage(b, createUUID(b, o.pl.doc.FallbackPage))
		}
		addOptional(b, exported, fbs.MetaAddComponents)
	} else {
		fbs.MetaAddPlugins(b, plugins)
		fbs.MetaAddDefaultLocale(b, defLocale)
		fbs.MetaAddSupportedLocales(b, locales)
		fbs.MetaAddEntryRoute(b, entryRoute)
		fbs.MetaAddFlags(b, flags)
		addOptional(b, capabilities, fbs.MetaAddCapabilities)
		if app.NativeCatalogue != "" {
			fbs.MetaAddNativeCatalogue(b, createUUID(b, app.NativeCatalogue))
		}
		fbs.MetaAddSecurityProfile(b, profile)
		addOptional(b, sampling, fbs.MetaAddTelemetrySampling)
		addOptional(b, hostEvents, fbs.MetaAddHostEvents)
		addOptional(b, notFound, fbs.MetaAddNotFoundRoute)
		addOptional(b, shells, fbs.MetaAddShells)
		addOptional(b, links, fbs.MetaAddDeepLinks)
		addOptional(b, push, fbs.MetaAddPush)
	}
	o.add(bundle.SectionMeta, o.id, finish(b, fbs.MetaEnd(b), bundle.SectionMeta))
}

// capabilitiesOf writes the capabilities table of a bundle's meta: a
// plugin's declaration (SEC-080), or for the app the device APIs and
// domains it approves, which its own triggers may use (SEC-080), and the
// pins of its customer domains (SEC-042). 0 when an app declares none.
func (u *unit) capabilitiesOf(b *flatbuffers.Builder, o *out) flatbuffers.UOffsetT {
	if o.pl != nil {
		return capabilitiesTable(b, o.pl.doc.Capabilities, nil)
	}
	c := u.project.App.Doc.Capabilities
	if c == nil {
		return 0
	}
	return capabilitiesTable(b, &schema.Capabilities{DeviceApis: c.DeviceApis, NetworkDomains: c.NetworkDomains}, c.NetworkPins)
}

// pluginIDs writes the ids of the plugins the app lists, in the app's
// order.
func (u *unit) pluginIDs(b *flatbuffers.Builder) flatbuffers.UOffsetT {
	var ids [][16]byte
	for _, k := range u.project.App.Doc.Plugins {
		for _, pl := range u.plugins {
			if pl.key == k {
				ids = append(ids, uuidBytes(pl.doc.ID))
			}
		}
	}
	return uuidVector(b, ids)
}

// addOptional adds an optional field to the table being built, unless its
// offset is 0 (absent).
func addOptional(b *flatbuffers.Builder, off flatbuffers.UOffsetT, add func(*flatbuffers.Builder, flatbuffers.UOffsetT)) {
	if off != 0 {
		add(b, off)
	}
}

// samplingTables writes the app's telemetry sampling rates in
// thousandths, sorted by event (ANL-003, ADR-0034); 0 when it sets none.
func samplingTables(b *flatbuffers.Builder, t *schema.TelemetryPolicy) flatbuffers.UOffsetT {
	if t == nil || len(t.Sampling) == 0 {
		return 0
	}
	events := slices.Sorted(maps.Keys(t.Sampling))
	offs := make([]flatbuffers.UOffsetT, len(events))
	for i, ev := range events {
		name := b.CreateString(ev)
		fbs.SamplingStart(b)
		fbs.SamplingAddEvent(b, name)
		fbs.SamplingAddRate(b, uint32(math.Round(min(max(t.Sampling[ev], 0), 1)*1000)))
		offs[i] = fbs.SamplingEnd(b)
	}
	return offsetVector(b, offs)
}

// hostEventTables writes the app's host events with their fields' types,
// sorted by name, for the runtime to check `Plux.sendEvent` payloads
// against (HST-013); 0 when the app declares none.
func hostEventTables(b *flatbuffers.Builder, events []schema.HostEventDecl) flatbuffers.UOffsetT {
	if len(events) == 0 {
		return 0
	}
	sorted := slices.Clone(events)
	slices.SortStableFunc(sorted, func(x, y schema.HostEventDecl) int { return strings.Compare(x.Name, y.Name) })
	offs := make([]flatbuffers.UOffsetT, len(sorted))
	for i, ev := range sorted {
		fields := make([]flatbuffers.UOffsetT, len(ev.Fields))
		for j, f := range ev.Fields {
			typ := f.Type
			if te, err := parseTypeExpr(typ); err == nil {
				typ = te.String()
			}
			name, tv := b.CreateString(f.Name), b.CreateString(typ)
			fbs.HostEventFieldStart(b)
			fbs.HostEventFieldAddName(b, name)
			fbs.HostEventFieldAddType(b, tv)
			fields[j] = fbs.HostEventFieldEnd(b)
		}
		name, fv := b.CreateString(ev.Name), offsetVector(b, fields)
		fbs.HostEventStart(b)
		fbs.HostEventAddName(b, name)
		fbs.HostEventAddDirection(b, hostEventDirection(ev.Direction))
		addOptional(b, fv, fbs.HostEventAddFields)
		offs[i] = fbs.HostEventEnd(b)
	}
	return offsetVector(b, offs)
}

// hostEventDirection maps the document's direction; absent means toHost.
func hostEventDirection(d schema.HostEventDirection) fbs.HostEventDirection {
	switch d {
	case schema.HostEventDirectionToPlux:
		return fbs.HostEventDirectionToPlux
	case schema.HostEventDirectionBoth:
		return fbs.HostEventDirectionBoth
	}
	return fbs.HostEventDirectionToHost
}

// runtimeLimits writes the runtime-enforced limits whose effective value
// differs from the registry default, sorted by key (LIM-001). A limit the
// bundle does not carry is the registry default: the runtime reads it from
// its generated registry, so a bundle without an override states nothing.
func (u *unit) runtimeLimits(b *flatbuffers.Builder) flatbuffers.UOffsetT {
	var offs []flatbuffers.UOffsetT
	defs := limits.Definitions()
	slices.SortFunc(defs, func(x, y limits.Definition) int { return strings.Compare(string(x.Key), string(y.Key)) })
	for _, d := range defs {
		v := u.opts.Limits.Get(d.Key)
		if d.EnforcedBy&limits.EnforcerRuntime == 0 || v == d.Default {
			continue
		}
		key := b.CreateString(string(d.Key))
		fbs.LimitStart(b)
		fbs.LimitAddKey(b, key)
		fbs.LimitAddValue(b, v)
		offs = append(offs, fbs.LimitEnd(b))
	}
	return offsetVector(b, offs)
}

// pageEntries lists a plugin's pages for cross-plugin navigation.
func pageEntries(b *flatbuffers.Builder, pl *plugin) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(pl.pages))
	for i, pg := range pl.pages {
		key, route := b.CreateString(pg.doc.Key), b.CreateString(pg.route)
		fbs.PageEntryStart(b)
		hi, lo := uuidHalves(uuidBytes(pg.doc.ID))
		fbs.PageEntryAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.PageEntryAddKey(b, key)
		fbs.PageEntryAddRoute(b, route)
		offs[i] = fbs.PageEntryEnd(b)
	}
	return offsetVector(b, offs)
}

// componentEntries lists a plugin's exported components, sorted by key,
// for PluxView (NAV-004, ADR-0023); 0 when it exports none.
func componentEntries(b *flatbuffers.Builder, pl *plugin) flatbuffers.UOffsetT {
	var exported []*component
	for _, c := range pl.components {
		if c.doc.Exported != nil && *c.doc.Exported {
			exported = append(exported, c)
		}
	}
	if len(exported) == 0 {
		return 0
	}
	slices.SortFunc(exported, func(x, y *component) int { return strings.Compare(x.doc.Key, y.doc.Key) })
	offs := make([]flatbuffers.UOffsetT, len(exported))
	for i, c := range exported {
		key := b.CreateString(c.doc.Key)
		fbs.ComponentEntryStart(b)
		hi, lo := uuidHalves(uuidBytes(c.doc.ID))
		fbs.ComponentEntryAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.ComponentEntryAddKey(b, key)
		offs[i] = fbs.ComponentEntryEnd(b)
	}
	return offsetVector(b, offs)
}

// capabilitiesTable writes what a plugin may use (SEC-080, SEC-102), and
// for the app also the pins of its customer API domains (SEC-042), by
// domain and sorted, so equal documents give equal bytes (CMP-002).
func capabilitiesTable(b *flatbuffers.Builder, c *schema.Capabilities, pins map[string][]string) flatbuffers.UOffsetT {
	if c == nil {
		c = &schema.Capabilities{}
	}
	domains := stringVector(b, c.NetworkDomains)
	fnOffs := make([]flatbuffers.UOffsetT, len(c.Functions))
	for i, f := range c.Functions {
		fn, alias := b.CreateString(f.Function), b.CreateString(f.Alias)
		fbs.FunctionGrantStart(b)
		hi, lo := uuidHalves(uuidBytes(f.ID))
		fbs.FunctionGrantAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.FunctionGrantAddFunction(b, fn)
		fbs.FunctionGrantAddAlias(b, alias)
		fnOffs[i] = fbs.FunctionGrantEnd(b)
	}
	fns := offsetVector(b, fnOffs)
	apis := make([]string, len(c.DeviceApis))
	for i, a := range c.DeviceApis {
		apis[i] = string(a)
	}
	devices := stringVector(b, apis)
	routes := stringVector(b, c.NativeRoutes)
	hosts := make([]string, 0, len(pins))
	for h := range pins {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	pinOffs := make([]flatbuffers.UOffsetT, len(hosts))
	for i, h := range hosts {
		list := stringVector(b, slices.Sorted(slices.Values(pins[h])))
		host := b.CreateString(h)
		fbs.DomainPinsStart(b)
		fbs.DomainPinsAddHost(b, host)
		fbs.DomainPinsAddPins(b, list)
		pinOffs[i] = fbs.DomainPinsEnd(b)
	}
	var networkPins flatbuffers.UOffsetT
	if len(pinOffs) > 0 {
		networkPins = offsetVector(b, pinOffs)
	}
	fbs.CapabilitiesStart(b)
	fbs.CapabilitiesAddNetworkDomains(b, domains)
	fbs.CapabilitiesAddFunctions(b, fns)
	fbs.CapabilitiesAddDeviceApis(b, devices)
	fbs.CapabilitiesAddNativeRoutes(b, routes)
	addOptional(b, networkPins, fbs.CapabilitiesAddNetworkPins)
	return fbs.CapabilitiesEnd(b)
}

// flagTables writes the app's flags sorted by name.
func (u *unit) flagTables(e *valueEnc, flags []schema.FlagDecl) flatbuffers.UOffsetT {
	b := e.b
	sorted := slices.Clone(flags)
	slices.SortFunc(sorted, func(x, y schema.FlagDecl) int { return strings.Compare(x.Name, y.Name) })
	offs := make([]flatbuffers.UOffsetT, len(sorted))
	for i, f := range sorted {
		def := e.value(u.checkRaw(vctx{code: plxerr.ValueTypeMismatch}, f.Default, &texpr{name: string(f.Type)}))
		name, typ := b.CreateString(f.Name), b.CreateString(string(f.Type))
		fbs.FlagStart(b)
		fbs.FlagAddName(b, name)
		fbs.FlagAddType(b, typ)
		if def != 0 {
			fbs.FlagAddDefault(b, def)
		}
		offs[i] = fbs.FlagEnd(b)
	}
	return offsetVector(b, offs)
}
