// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// registryGo renders backend/internal/schema/registry/registry_gen.go.
func registryGo(r *registry.Registry) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangGo, RegistrySource))
	b.WriteString("package registry\n\n")
	b.WriteString("// widgets holds every widget descriptor, sorted by type name. It is read-only.\n")
	b.WriteString("var widgets = [...]Widget{\n")
	for _, w := range r.Widgets {
		writeGoWidget(&b, w)
	}
	b.WriteString("}\n\n// valueTypes holds every value type, sorted by name. It is read-only.\n")
	b.WriteString("var valueTypes = [...]ValueType{\n")
	for _, t := range r.Types {
		writeGoValueType(&b, t)
	}
	b.WriteString("}\n\n// enums holds every enum, sorted by name. It is read-only.\n")
	b.WriteString("var enums = [...]Enum{\n")
	for _, e := range r.Enums {
		fmt.Fprintf(&b, "{Name: %q, ID: %d, Revision: %d, Runtimes: %s, Description: %q, Values: []EnumValue{\n",
			e.Name, e.ID, e.Revision, goStrings(runtimes(e.Revisions)), e.Description)
		for _, v := range sortedByID(e.Values, func(v registry.EnumValue) uint32 { return v.ID }) {
			fmt.Fprintf(&b, "{Name: %q, ID: %d, Revision: %d, Deprecated: %s, Description: %q},\n",
				v.Name, v.ID, revisionOr1(v.Revision), goDeprecation(v.Deprecated), v.Description)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n\n// actions holds every action, sorted by name. It is read-only.\n")
	b.WriteString("var actions = [...]Action{\n")
	for _, a := range r.Actions {
		writeGoAction(&b, a)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// writeGoWidget renders one Widget literal.
func writeGoWidget(b *bytes.Buffer, w *registry.Widget) {
	fmt.Fprintf(b, "{\nType: %q, ID: %d, Layer: %d, Phase: %q, Revision: %d, Runtimes: %s,\n",
		w.Type, w.ID, w.Layer, w.Phase, w.Revision, goStrings(runtimes(w.Revisions)))
	fmt.Fprintf(b, "Category: %q, Icon: %q, Description: %q,\n", w.Category, w.Icon, w.Description)
	plats := make([]string, len(w.Platforms))
	for i, p := range w.Platforms {
		plats[i] = "Platform" + GoName(p)
	}
	fmt.Fprintf(b, "Platforms: %s, Cost: %d, Role: Role%s, Interactive: %t,\n",
		strings.Join(plats, " | "), w.Cost, strings.ToUpper(w.Accessibility.Role[:1])+w.Accessibility.Role[1:], w.Accessibility.Interactive)
	if len(w.TypeParameters) > 0 {
		fmt.Fprintf(b, "TypeParameters: %s,\n", goStrings(w.TypeParameters))
	}
	if len(w.Props) > 0 {
		b.WriteString("Props: []Prop{\n")
		for _, p := range sortedByID(w.Props, func(p registry.Prop) uint32 { return p.ID }) {
			fmt.Fprintf(b, "{Name: %q, ID: %d, Type: %q, Required: %t, Default: %q, Constraints: %s, Revision: %d, Deprecated: %s, Bindable: %t, Description: %q},\n",
				p.Name, p.ID, p.Type, p.Required, compactJSON(p.Default), goConstraints(p.Constraints), revisionOr1(p.Revision), goDeprecation(p.Deprecated), p.IsBindable(), p.Description)
		}
		b.WriteString("},\n")
	}
	if len(w.Events) > 0 {
		b.WriteString("Events: []Event{\n")
		for _, e := range sortedByID(w.Events, func(e registry.Event) uint32 { return e.ID }) {
			fmt.Fprintf(b, "{Name: %q, ID: %d, Payload: %q, Revision: %d, Deprecated: %s, Description: %q},\n",
				e.Name, e.ID, e.Payload, revisionOr1(e.Revision), goDeprecation(e.Deprecated), e.Description)
		}
		b.WriteString("},\n")
	}
	if len(w.Slots) > 0 {
		b.WriteString("Slots: []Slot{\n")
		for _, s := range sortedByID(w.Slots, func(s registry.Slot) uint32 { return s.ID }) {
			fmt.Fprintf(b, "{Name: %q, ID: %d, List: %t, Required: %t, Template: %t, Revision: %d, Deprecated: %s, Description: %q},\n",
				s.Name, s.ID, s.List, s.Required, s.Template, revisionOr1(s.Revision), goDeprecation(s.Deprecated), s.Description)
		}
		b.WriteString("},\n")
	}
	if c := w.Children; c != nil {
		minimum, maximum := 0, 0
		if c.Min != nil {
			minimum = *c.Min
		}
		if c.Max != nil {
			maximum = *c.Max
		}
		fmt.Fprintf(b, "Children: &Children{Min: %d, Max: %d},\n", minimum, maximum)
	}
	if w.Deprecated != nil {
		fmt.Fprintf(b, "Deprecated: %s,\n", goDeprecation(w.Deprecated))
	}
	b.WriteString("},\n")
}

// writeGoValueType renders one ValueType literal.
func writeGoValueType(b *bytes.Buffer, t *registry.ValueType) {
	fmt.Fprintf(b, "{\nName: %q, ID: %d, Revision: %d, Runtimes: %s, Description: %q,\nFields: []Field{\n",
		t.Name, t.ID, t.Revision, goStrings(runtimes(t.Revisions)), t.Description)
	for _, f := range sortedByID(t.Fields, func(f registry.Field) uint32 { return f.ID }) {
		fmt.Fprintf(b, "{Name: %q, ID: %d, Type: %q, Required: %t, Default: %q, Revision: %d, Deprecated: %s, Description: %q},\n",
			f.Name, f.ID, f.Type, f.Required, compactJSON(f.Default), revisionOr1(f.Revision), goDeprecation(f.Deprecated), f.Description)
	}
	b.WriteString("},\n")
	if len(t.Constants) > 0 {
		b.WriteString("Constants: []Constant{\n")
		for _, c := range t.Constants {
			fmt.Fprintf(b, "{Name: %q, Value: %q},\n", c.Name, compactJSON(c.Value))
		}
		b.WriteString("},\n")
	}
	b.WriteString("},\n")
}

// writeGoAction renders one Action literal.
func writeGoAction(b *bytes.Buffer, a *registry.Action) {
	params := make([]string, len(a.TypeParams))
	for i, tp := range a.TypeParams {
		params[i] = tp.Name
	}
	fmt.Fprintf(b, "{\nName: %q, ID: %d, Phase: %q, Category: %q, Description: %q,\n", a.Name, a.ID, a.Phase, a.Category, a.Description)
	if len(params) > 0 {
		fmt.Fprintf(b, "TypeParameters: %s,\n", goStrings(params))
	}
	if len(a.Inputs) > 0 {
		b.WriteString("Inputs: []Input{\n")
		for _, in := range sortedByID(a.Inputs, func(in registry.Input) uint32 { return in.ID }) {
			fmt.Fprintf(b, "{Name: %q, ID: %d, Type: %q, Required: %t, Default: %q, Ref: %q, Description: %q},\n",
				in.Name, in.ID, in.Type, in.Required, compactJSON(in.Default), in.Ref, in.Description)
		}
		b.WriteString("},\n")
	}
	if a.Output != "" {
		fmt.Fprintf(b, "Output: %q,\n", a.Output)
	}
	if len(a.Branches) > 0 {
		fmt.Fprintf(b, "Branches: %s,\n", goStrings(a.Branches))
	}
	if a.BranchesFrom != "" {
		fmt.Fprintf(b, "BranchesFrom: %q,\n", a.BranchesFrom)
	}
	if len(a.Effects) > 0 {
		fmt.Fprintf(b, "Effects: %s,\n", goStrings(a.Effects))
	}
	b.WriteString("},\n")
}

// goConstraints renders a Constraints literal.
func goConstraints(c *registry.Constraints) string {
	if c == nil {
		return "Constraints{}"
	}
	var parts []string
	bound := func(name string, set bool, v float64) {
		if set {
			parts = append(parts, fmt.Sprintf("%s: Bound{Value: %v, Set: true}", name, v))
		}
	}
	bound("Min", c.Min != nil, deref(c.Min))
	bound("Max", c.Max != nil, deref(c.Max))
	bound("MinLength", c.MinLength != nil, float64(deref(c.MinLength)))
	bound("MaxLength", c.MaxLength != nil, float64(deref(c.MaxLength)))
	if c.Pattern != "" {
		parts = append(parts, fmt.Sprintf("Pattern: %q", c.Pattern))
	}
	return "Constraints{" + strings.Join(parts, ", ") + "}"
}

// deref returns *p, or the zero value for nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// goStrings renders a []string literal.
func goStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// sortedByID returns members sorted by their ID.
func sortedByID[T any](members []T, id func(T) uint32) []T {
	out := slices.Clone(members)
	slices.SortStableFunc(out, func(a, b T) int { return cmp.Compare(id(a), id(b)) })
	return out
}
