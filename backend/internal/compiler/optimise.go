// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// optimise rewrites the checked trees without changing what they render
// (CMP-020, CMP-022, CMP-024): expressions folded to constants become
// literals, subtrees whose visibility is constantly false are removed,
// nested Padding with literal insets is flattened, props equal to their
// descriptor default are omitted, and rendering hints are added.
func optimise(u *unit) {
	for _, c := range u.shared {
		u.optimiseTree(c.root, false)
	}
	for _, pl := range u.plugins {
		for _, c := range pl.components {
			u.optimiseTree(c.root, false)
		}
		for _, pg := range pl.pages {
			u.optimiseTree(pg.root, true)
		}
	}
}

// optimiseTree optimises a tree; page roots get a repaint boundary.
func (u *unit) optimiseTree(root *node, pageRoot bool) {
	if root == nil {
		return
	}
	u.optimiseNode(root)
	if pageRoot {
		root.hints |= fbs.NodeHintsRepaintBoundary
	}
}

// optimiseNode optimises a node and its subtree, bottom-up.
func (u *unit) optimiseNode(n *node) {
	for _, p := range n.props {
		p.value = foldValue(p.value)
	}
	for _, o := range n.overrides {
		for _, p := range o.props {
			p.value = foldValue(p.value)
		}
	}
	n.visible = foldValue(n.visible)
	if n.visible != nil && n.visible.kind == fbs.ValueKindBool && n.visible.i == 1 {
		n.visible = nil // visible is the default
	}
	n.children = u.keep(n.children)
	for _, sf := range n.slots {
		sf.nodes = u.keep(sf.nodes)
		for _, c := range sf.nodes {
			if n.widget != nil && isTemplateSlot(n.widget, sf.name) {
				c.hints |= fbs.NodeHintsRepaintBoundary // list items (CMP-024)
			}
		}
	}
	flattenPadding(n)
	n.props = u.withoutDefaults(n)
	if static(n) {
		n.hints |= fbs.NodeHintsStatic
	}
}

// keep optimises nodes and drops those whose visibility is constantly
// false, with their subtrees (CMP-022).
func (u *unit) keep(nodes []*node) []*node {
	out := nodes[:0]
	for _, c := range nodes {
		u.optimiseNode(c)
		if c.visible != nil && c.visible.kind == fbs.ValueKindBool && c.visible.i == 0 {
			markRemoved(c)
			continue
		}
		out = append(out, c)
	}
	return out
}

// markRemoved marks a subtree removed, so encoding skips it.
func markRemoved(n *node) {
	n.removed = true
	for _, c := range n.children {
		markRemoved(c)
	}
	for _, sf := range n.slots {
		for _, c := range sf.nodes {
			markRemoved(c)
		}
	}
}

// isTemplateSlot reports whether a widget's slot is built per item.
func isTemplateSlot(w *registry.Widget, name string) bool {
	s, ok := w.Slot(name)
	return ok && s.Template
}

// foldValue turns an expression that folded to a scalar constant into a
// literal, so the runtime reads it without evaluating (CMP-022).
func foldValue(v *value) *value {
	if v == nil {
		return nil
	}
	if v.kind == fbs.ValueKindExpr && v.prog != nil {
		if c, ok := constantOf(v.prog); ok {
			return c
		}
		return v
	}
	for i, it := range v.items {
		v.items[i] = foldValue(it)
	}
	for i := range v.entries {
		v.entries[i].value = foldValue(v.entries[i].value)
	}
	return v
}

// constantOf returns the literal of a program that only pushes a
// constant of a primitive type.
func constantOf(p *pxl.Program) (*value, bool) {
	if len(p.Reads) > 0 || !primitives[p.Result] || p.Result == "route" || p.Result == "asset" {
		return nil, false
	}
	switch {
	case len(p.Code) == 1 && pxl.Opcode(p.Code[0]) == pxl.OpPushTrue:
		return primitiveValue(true), true
	case len(p.Code) == 1 && pxl.Opcode(p.Code[0]) == pxl.OpPushFalse:
		return primitiveValue(false), true
	case len(p.Code) == 3 && pxl.Opcode(p.Code[0]) == pxl.OpConst:
		i := int(p.Code[1]) | int(p.Code[2])<<8
		if i < len(p.Constants) {
			return primitiveValue(p.Constants[i]), true
		}
	}
	return nil, false
}

// flattenPadding merges a Padding whose only content is another Padding,
// when both insets are literals without directional fields: the insets
// add up and the result renders the same (CMP-022).
func flattenPadding(n *node) {
	if n.widget == nil || n.widget.Type != "Padding" || len(n.slots) != 1 || len(n.slots[0].nodes) != 1 {
		return
	}
	inner := n.slots[0].nodes[0]
	if inner.widget == nil || inner.widget.Type != "Padding" || len(inner.handlers) > 0 || inner.visible != nil ||
		inner.semantics != nil || inner.doc.TestID != "" || inner.anim != nil || inner.animated || n.anim != nil || n.animated || len(inner.overrides) > 0 || len(n.overrides) > 0 || len(inner.props) != 1 || len(n.props) != 1 {
		return
	}
	a, okA := insets(n.props[0].value)
	b, okB := insets(inner.props[0].value)
	if !okA || !okB {
		return
	}
	vt, _ := registry.LookupValueType("EdgeInsets")
	sum := &value{kind: fbs.ValueKindObject, valueType: vt.ID}
	for i, side := range []string{"left", "top", "right", "bottom"} {
		f, _ := vt.Field(side)
		if total := a[i] + b[i]; total != 0 {
			sum.entries = append(sum.entries, entry{id: f.ID, value: &value{kind: fbs.ValueKindDouble, d: total}})
		}
	}
	slices.SortFunc(sum.entries, func(x, y entry) int { return int(x.id) - int(y.id) })
	n.props[0].value = sum
	inner.removed = true
	n.slots[0].nodes = inner.slots[0].nodes
	for _, c := range n.slots[0].nodes {
		c.parent = n
	}
}

// insets reads a literal EdgeInsets as left, top, right, bottom; it fails
// on bindings and on the directional fields start and end.
func insets(v *value) ([4]float64, bool) {
	var out [4]float64
	vt, _ := registry.LookupValueType("EdgeInsets")
	if v == nil || v.kind != fbs.ValueKindObject || v.valueType != vt.ID {
		return out, false
	}
	for _, e := range v.entries {
		if e.value.kind != fbs.ValueKindDouble {
			return out, false
		}
		d := e.value.d
		switch name := fieldName(vt, e.id); name {
		case "all":
			for i := range out {
				out[i] += d
			}
		case "horizontal":
			out[0] += d
			out[2] += d
		case "vertical":
			out[1] += d
			out[3] += d
		case "left":
			out[0] += d
		case "top":
			out[1] += d
		case "right":
			out[2] += d
		case "bottom":
			out[3] += d
		default:
			return out, false
		}
	}
	return out, true
}

// fieldName returns the name of a value type's field.
func fieldName(vt registry.ValueType, id uint32) string {
	for _, f := range vt.Fields {
		if f.ID == id {
			return f.Name
		}
	}
	return ""
}

// withoutDefaults drops literal props equal to their descriptor default
// (CMP-020). Overrides keep them: they reset a value the base changed.
func (u *unit) withoutDefaults(n *node) []*prop {
	if n.widget == nil {
		return n.props
	}
	out := n.props[:0]
	for _, p := range n.props {
		d, ok := n.widget.Prop(p.name)
		if ok && d.Default != "" && u.equalsDefault(n, d, p.value) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// equalsDefault compares a value with a prop's default literal.
func (u *unit) equalsDefault(n *node, d registry.Prop, v *value) bool {
	te, err := parseTypeExpr(d.Type)
	if err != nil || unbound(te, n.widget.TypeParameters) {
		return false
	}
	mark := len(u.diags)
	def := u.checkRaw(vctx{code: plxerr.ValueTypeMismatch}, json.RawMessage(d.Default), te)
	u.diags = u.diags[:mark] // the registry's defaults are checked by schemagen
	return def != nil && bytes.Equal(canonical(def), canonical(v))
}

// static reports whether a subtree reads no state, tokens or
// translations and handles no events, so the runtime can build it once
// (CMP-024).
func static(n *node) bool {
	if len(n.handlers) > 0 || n.visible != nil || len(n.overrides) > 0 || n.anim != nil || n.animated {
		return false
	}
	for _, p := range n.props {
		if dynamic(p.value) {
			return false
		}
	}
	if n.semantics != nil && (dynamic(n.semantics.label) || dynamic(n.semantics.hint) || dynamic(n.semantics.value)) {
		return false
	}
	for _, c := range n.children {
		if c.hints&fbs.NodeHintsStatic == 0 {
			return false
		}
	}
	for _, sf := range n.slots {
		for _, c := range sf.nodes {
			if c.hints&fbs.NodeHintsStatic == 0 {
				return false
			}
		}
	}
	return n.target == nil
}

// dynamic reports whether a value depends on state, theme or locale.
func dynamic(v *value) bool {
	if v == nil {
		return false
	}
	switch v.kind {
	case fbs.ValueKindExpr, fbs.ValueKindToken, fbs.ValueKindTranslation:
		return true
	}
	for _, it := range v.items {
		if dynamic(it) {
			return true
		}
	}
	for _, e := range v.entries {
		if dynamic(e.value) {
			return true
		}
	}
	return false
}
