// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// member is a prop, event or slot a node can use, whatever declares it: a
// widget descriptor, a component or a native slot.
type member struct {
	name       string
	id         uint32
	typ        string
	required   bool
	bindable   bool
	list       bool
	revision   uint16
	deprecated *registry.Deprecation
	def        string
	cons       *registry.Constraints
}

// shape is what a node's type declares.
type shape struct {
	what     string
	props    []member
	events   []member
	slots    []member
	children *registry.Children
	params   []string
	widget   *registry.Widget
}

// shapeOf returns the declaration a node is checked against, or nil when
// its type is unknown (already reported).
func (u *unit) shapeOf(n *node) *shape {
	switch {
	case n.widget != nil:
		return widgetShape(n.widget)
	case n.target != nil:
		return componentShape(n.target)
	}
	if ns, ok := u.natives.slots[n.doc.Type]; ok {
		return nativeShape(ns)
	}
	return nil
}

func widgetShape(w *registry.Widget) *shape {
	s := &shape{what: w.Type, children: w.Children, params: w.TypeParameters, widget: w}
	for i := range w.Props {
		p := &w.Props[i]
		s.props = append(s.props, member{
			name: p.Name, id: p.ID, typ: p.Type, required: p.Required, bindable: p.Bindable,
			revision: p.Revision, deprecated: p.Deprecated, def: p.Default, cons: &p.Constraints,
		})
	}
	for _, e := range w.Events {
		s.events = append(s.events, member{name: e.Name, id: e.ID, typ: e.Payload, revision: e.Revision, deprecated: e.Deprecated})
	}
	for _, sl := range w.Slots {
		s.slots = append(s.slots, member{name: sl.Name, id: sl.ID, required: sl.Required, list: sl.List, revision: sl.Revision, deprecated: sl.Deprecated})
	}
	return s
}

// componentShape addresses a component's props and slots by their index,
// props sorted by name (bundle-format.md §2).
func componentShape(c *component) *shape {
	s := &shape{what: "component " + c.doc.Key}
	props := slices.Clone(c.doc.Props)
	slices.SortFunc(props, func(a, b schema.ComponentProp) int { return strings.Compare(a.Name, b.Name) })
	for i, p := range props {
		s.props = append(s.props, member{name: p.Name, id: uint32(i), typ: p.Type, required: p.Required != nil && *p.Required, bindable: true, def: string(p.Default)}) //nolint:gosec // G115: bounded by the document size.
	}
	for i, e := range c.doc.Events {
		s.events = append(s.events, member{name: e.Name, id: uint32(i), typ: e.Payload}) //nolint:gosec // G115: as above.
	}
	for i, sl := range c.doc.Slots {
		s.slots = append(s.slots, member{name: sl.Name, id: uint32(i), required: sl.Required != nil && *sl.Required, list: sl.Multiple != nil && *sl.Multiple}) //nolint:gosec // G115: as above.
	}
	return s
}

// nativeShape addresses a native slot's props and events by index.
func nativeShape(ns *schema.NativeSlot) *shape {
	s := &shape{what: "native slot " + ns.Type}
	for i, p := range ns.Props {
		s.props = append(s.props, member{name: p.Name, id: uint32(i), typ: p.Type, required: p.Required != nil && *p.Required, bindable: true}) //nolint:gosec // G115: bounded by the document size.
	}
	for i, e := range ns.Events {
		s.events = append(s.events, member{name: e.Name, id: uint32(i), typ: e.Payload}) //nolint:gosec // G115: as above.
	}
	return s
}

func findMember(ms []member, name string) (member, bool) {
	for _, m := range ms {
		if m.name == name {
			return m, true
		}
	}
	return member{}, false
}

// checkNode checks a node against its declaration (SCH-023, WGT-001):
// props, events, children, slots, visibility, semantics and responsive
// overrides.
func (u *unit) checkNode(n *node) {
	sh := u.shapeOf(n)
	if sh == nil {
		return
	}
	file := n.owner.file()
	c := vctx{file: file, ptr: n.ptr, scope: n.scope, pl: n.owner.plugin(), code: plxerr.PropTypeMismatch, from: ownerID(n.owner)}
	if sh.widget != nil {
		u.useRevision("widget."+sh.widget.Type, sh.widget.Runtimes, 1, c.at("type"))
		u.deprecation(sh.widget.Deprecated, sh.widget.Type, vctx{file: file, ptr: n.ptr + "/type"})
	}
	n.props = u.checkProps(n, sh, n.doc.Props, c, "props", true)
	u.checkEvents(n, sh, c)
	u.checkChildren(n, sh, file)
	if len(n.doc.Visible) > 0 {
		n.visible = u.checkRaw(vctx{file: file, ptr: n.ptr + "/visible", scope: n.scope, pl: c.pl, code: plxerr.PropTypeMismatch}, n.doc.Visible, &texpr{name: "bool"})
	}
	if sem := n.doc.Semantics; sem != nil {
		n.semantics = u.checkSemantics(n, sem, c)
	}
	if r := n.doc.Responsive; r != nil {
		for _, layer := range []struct {
			key   string
			props map[string]json.RawMessage
		}{{"medium", r.Medium}, {"expanded", r.Expanded}} {
			if len(layer.props) == 0 {
				continue
			}
			props := u.checkProps(n, sh, layer.props, c, "responsive/"+layer.key, false)
			n.overrides = append(n.overrides, &override{kind: fbs.OverrideKindSizeClass, key: layer.key, props: props})
		}
	}
}

// checkProps checks a set of props against the shape; base reports
// missing required props, which overrides need not repeat.
func (u *unit) checkProps(n *node, sh *shape, raw map[string]json.RawMessage, c vctx, at string, base bool) []*prop {
	var out []*prop
	prefix := n.ptr + "/" + at
	for _, name := range sortedKeys(raw) {
		ptr := prefix + plxerr.Pointer(name)
		m, ok := findMember(sh.props, name)
		if !ok {
			u.report(plxerr.UnknownProp, c.file, ptr, "%s has no prop %q", sh.what, name)
			continue
		}
		pc := c
		pc.ptr, pc.constraints = ptr, m.cons
		if sh.widget != nil && m.revision > 1 {
			u.useRevision("widget."+sh.widget.Type, sh.widget.Runtimes, m.revision, pc)
		}
		u.deprecation(m.deprecated, sh.what+"."+name, pc)
		if !m.bindable && isBindingRaw(raw[name]) {
			u.report(plxerr.PropTypeMismatch, c.file, ptr, "prop %q of %s accepts literals only", name, sh.what)
			continue
		}
		te := u.memberType(n, m, c.file, ptr)
		if te == nil {
			continue
		}
		if v := u.checkRaw(pc, raw[name], te); v != nil {
			out = append(out, &prop{name: name, id: m.id, ptr: ptr, value: v})
		}
	}
	if base {
		for _, m := range sh.props {
			if _, set := raw[m.name]; m.required && !set {
				u.report(plxerr.MissingRequiredProp, c.file, n.ptr, "%s needs prop %q", sh.what, m.name)
			}
		}
	}
	slices.SortFunc(out, func(a, b *prop) int { return int(a.id) - int(b.id) })
	return out
}

// memberType is a member's type with the node's type arguments bound.
func (u *unit) memberType(n *node, m member, file, ptr string) *texpr {
	te, err := parseTypeExpr(m.typ)
	if err != nil {
		u.report(plxerr.InvalidTypeExpression, file, ptr, "%q: %v", m.typ, err)
		return nil
	}
	if len(n.typeArgs) == 0 {
		return te
	}
	bind := map[string]*texpr{}
	for name, t := range n.typeArgs {
		if b, err := parseTypeExpr(t.String()); err == nil {
			bind[name] = b
		}
	}
	return te.subst(bind)
}

// isBindingRaw reports whether a raw value is a binding.
func isBindingRaw(raw json.RawMessage) bool {
	v, ok := decodeJSON(raw)
	obj, isObj := v.(map[string]any)
	return ok && isObj && isBinding(obj)
}

// checkEvents checks the handled events and builds the handlers.
func (u *unit) checkEvents(n *node, sh *shape, c vctx) {
	for _, name := range sortedKeys(n.doc.Events) {
		ptr := n.ptr + plxerr.Pointer("events", name)
		m, ok := findMember(sh.events, name)
		if !ok {
			u.report(plxerr.UnknownEvent, c.file, ptr, "%s has no event %q", sh.what, name)
			continue
		}
		if sh.widget != nil && m.revision > 1 {
			u.useRevision("widget."+sh.widget.Type, sh.widget.Runtimes, m.revision, vctx{file: c.file, ptr: ptr, pl: c.pl})
		}
		eh := n.doc.Events[name]
		g := n.graphs[name]
		if g == nil {
			continue
		}
		h := &handler{event: m.id, graph: g}
		u.concurrency(h, eh.Concurrency, c.file, ptr)
		h.detached = eh.Detached != nil && *eh.Detached
		n.handlers = append(n.handlers, h)
	}
}

// concurrency parses a concurrency policy (ACT-*).
func (u *unit) concurrency(h *handler, policy, file, ptr string) {
	kind, arg, _ := strings.Cut(policy, ":")
	switch kind {
	case "", "parallel":
		h.concurrency = fbs.ConcurrencyParallel
	case "drop":
		h.concurrency = fbs.ConcurrencyDrop
	case "restart":
		h.concurrency = fbs.ConcurrencyRestart
	case "queue":
		h.concurrency = fbs.ConcurrencyQueue
	case "debounce", "throttle":
		h.concurrency = fbs.ConcurrencyDebounce
		if kind == "throttle" {
			h.concurrency = fbs.ConcurrencyThrottle
		}
		ms, err := strconv.ParseUint(arg, 10, 32)
		if err != nil {
			u.report(plxerr.InvalidFormat, file, ptr+"/concurrency", "invalid interval in %q", policy)
			return
		}
		h.interval = uint32(ms)
	default:
		u.report(plxerr.InvalidFormat, file, ptr+"/concurrency", "unknown concurrency policy %q", policy)
	}
}

// checkChildren checks children and slots against the declaration.
func (u *unit) checkChildren(n *node, sh *shape, file string) {
	switch {
	case len(n.children) > 0 && sh.children == nil:
		u.report(plxerr.InvalidChildren, file, n.ptr+"/children", "%s takes no children", sh.what)
	case sh.children != nil && len(n.children) < sh.children.Min:
		u.report(plxerr.InvalidChildren, file, n.ptr, "%s needs at least %d children", sh.what, sh.children.Min)
	case sh.children != nil && sh.children.Max > 0 && len(n.children) > sh.children.Max:
		u.report(plxerr.InvalidChildren, file, n.ptr+"/children", "%s takes at most %d children", sh.what, sh.children.Max)
	}
	filled := map[string]bool{}
	for _, sf := range n.slots {
		m, ok := findMember(sh.slots, sf.name)
		if !ok {
			u.report(plxerr.UnknownSlot, file, sf.ptr, "%s has no slot %q", sh.what, sf.name)
			continue
		}
		filled[sf.name] = true
		sf.id = m.id
		if sh.widget != nil && m.revision > 1 {
			u.useRevision("widget."+sh.widget.Type, sh.widget.Runtimes, m.revision, vctx{file: file, ptr: sf.ptr, pl: n.owner.plugin()})
		}
		if fill := n.doc.Slots[sf.name]; !m.list && fill.Many != nil {
			u.report(plxerr.InvalidChildren, file, sf.ptr, "slot %q of %s holds one node, not a list", sf.name, sh.what)
		}
	}
	for _, m := range sh.slots {
		if m.required && !filled[m.name] {
			u.report(plxerr.MissingRequiredSlot, file, n.ptr, "%s needs slot %q", sh.what, m.name)
		}
	}
}

// checkSemantics checks a node's semantics.
func (u *unit) checkSemantics(n *node, sem *schema.Semantics, c vctx) *semantics {
	out := &semantics{
		header: sem.Header != nil && *sem.Header, button: sem.Button != nil && *sem.Button,
		liveRegion: sem.LiveRegion != nil && *sem.LiveRegion, excluded: sem.ExcludeSemantics != nil && *sem.ExcludeSemantics,
	}
	str := &texpr{name: "string"}
	for _, f := range []struct {
		name string
		raw  json.RawMessage
		dst  **value
	}{{"label", sem.Label, &out.label}, {"hint", sem.Hint, &out.hint}, {"value", sem.Value, &out.value}} {
		if len(f.raw) > 0 {
			fc := c
			fc.ptr = n.ptr + "/semantics/" + f.name
			*f.dst = u.checkRaw(fc, f.raw, str)
		}
	}
	return out
}
