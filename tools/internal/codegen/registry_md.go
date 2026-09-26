// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// widgetsMarkdown renders docs/reference/widgets.md.
func widgetsMarkdown(r *registry.Registry) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, RegistrySource))
	b.WriteString("# Widget Reference\n\n")
	b.WriteString("Every widget type Plux documents can use, generated from the descriptors in `schema/widgets/` (`WGT-001`, [ADR-0010](../adr/0010-layered-widget-model.md)). ")
	b.WriteString("Layer 1 mirrors Flutter widgets with their names and semantics, plus structural primitives; Layer 2 holds Plux components. ")
	b.WriteString("How each Flutter parameter is supported or why it is excluded is in the [coverage table](../../schema/widgets/COVERAGE.md) (`WGT-003`). ")
	b.WriteString("Types are written as in the [document model](document-model.md#3-types-and-values); IDs are permanent (`BND-011`).\n\n")

	b.WriteString("## Widgets\n\n| Type | Layer | Category | Phase | Description |\n|---|---|---|---|---|\n")
	for _, w := range r.Widgets {
		fmt.Fprintf(&b, "| [%s](#%s) | %d | %s | %s | %s |\n", w.Type, mdAnchor(w.Type), w.Layer, w.Category, w.Phase, mdCell(w.Description))
	}
	for _, w := range r.Widgets {
		writeWidgetSection(&b, w)
	}

	b.WriteString("\n## Value types\n\nStructured prop values. A literal is an object of the fields below or, where listed, the name of a constant.\n")
	for _, t := range r.Types {
		fmt.Fprintf(&b, "\n### %s\n\n%s\n\nID %d · revision %d", t.Name, t.Description, t.ID, t.Revision)
		if len(t.Flutter) > 0 {
			refs := make([]string, len(t.Flutter))
			for i, c := range t.Flutter {
				refs[i] = flutterRef(c)
			}
			b.WriteString(" · Flutter " + strings.Join(refs, ", "))
		}
		b.WriteString("\n\n| Field | ID | Type | Default | Description |\n|---|---|---|---|---|\n")
		for _, f := range sortedByID(t.Fields, func(f registry.Field) uint32 { return f.ID }) {
			fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %s |\n", f.Name, f.ID, mdTypeCell(f.Type, f.Required), mdDefault(compactJSON(f.Default)), mdCell(f.Description))
		}
		if len(t.Constants) > 0 {
			names := make([]string, len(t.Constants))
			for i, c := range t.Constants {
				names[i] = "`" + c.Name + "`"
			}
			b.WriteString("\nConstants: " + strings.Join(names, ", ") + ".\n")
		}
	}

	b.WriteString("\n## Enums\n")
	for _, e := range r.Enums {
		fmt.Fprintf(&b, "\n### %s\n\n%s\n\nID %d · revision %d", e.Name, e.Description, e.ID, e.Revision)
		if e.Flutter != nil {
			fmt.Fprintf(&b, " · Flutter `%s`", e.Flutter.Enum)
		}
		b.WriteString("\n\n| Value | ID | Description |\n|---|---|---|\n")
		for _, v := range sortedByID(e.Values, func(v registry.EnumValue) uint32 { return v.ID }) {
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", v.Name, v.ID, mdCell(v.Description))
		}
	}
	return b.Bytes()
}

// writeWidgetSection renders one widget's section.
func writeWidgetSection(b *bytes.Buffer, w *registry.Widget) {
	fmt.Fprintf(b, "\n### %s\n\n%s\n\n%s\n", w.Type, w.Description, widgetMeta(w))
	if w.Deprecated != nil {
		fmt.Fprintf(b, "\n**Deprecated** in revision %d: %s\n", w.Deprecated.Revision, w.Deprecated.Message)
	}
	writePropsTable(b, w.Props)
	if len(w.Events) > 0 {
		b.WriteString("\n| Event | ID | Payload | Description |\n|---|---|---|---|\n")
		for _, e := range sortedByID(w.Events, func(e registry.Event) uint32 { return e.ID }) {
			payload := "—"
			if e.Payload != "" {
				payload = "`" + e.Payload + "`"
			}
			fmt.Fprintf(b, "| `%s` | %d | %s | %s |\n", e.Name, e.ID, payload, mdCell(e.Description))
		}
	}
	if len(w.Slots) > 0 {
		b.WriteString("\n| Slot | ID | Holds | Description |\n|---|---|---|---|\n")
		for _, s := range sortedByID(w.Slots, func(s registry.Slot) uint32 { return s.ID }) {
			fmt.Fprintf(b, "| `%s` | %d | %s | %s |\n", s.Name, s.ID, slotHolds(s), mdCell(s.Description))
		}
	}
	if c := w.Children; c != nil {
		b.WriteString("\nTakes a list of `children`" + mdChildren(c) + ".\n")
	}
}

// widgetMeta renders the line of facts under a widget's heading.
func widgetMeta(w *registry.Widget) string {
	meta := []string{
		fmt.Sprintf("ID %d", w.ID), fmt.Sprintf("Layer %d", w.Layer), w.Phase,
		fmt.Sprintf("revision %d (runtime %s)", w.Revision, w.Revisions[len(w.Revisions)-1].Runtime),
	}
	if w.Flutter != nil {
		meta = append(meta, "Flutter "+flutterRef(*w.Flutter))
	}
	meta = append(meta, strings.Join(w.Platforms, ", "), fmt.Sprintf("cost %d µs", w.Cost), "role "+w.Accessibility.Role)
	if w.Accessibility.Interactive {
		meta = append(meta, "interactive")
	}
	if len(w.TypeParameters) > 0 {
		meta = append(meta, "type parameters "+strings.Join(w.TypeParameters, ", "))
	}
	return strings.Join(meta, " · ")
}

// writePropsTable renders the props of a widget.
func writePropsTable(b *bytes.Buffer, props []registry.Prop) {
	if len(props) == 0 {
		return
	}
	b.WriteString("\n| Prop | ID | Type | Default | Description |\n|---|---|---|---|---|\n")
	for _, p := range sortedByID(props, func(p registry.Prop) uint32 { return p.ID }) {
		desc := p.Description
		if c := mdConstraints(p.Constraints); c != "" {
			desc = strings.TrimSpace(desc + " " + c)
		}
		if !p.IsBindable() {
			desc = strings.TrimSpace(desc + " Literal only.")
		}
		fmt.Fprintf(b, "| `%s` | %d | %s | %s | %s |\n", p.Name, p.ID, mdTypeCell(p.Type, p.Required), mdDefault(compactJSON(p.Default)), mdCell(desc))
	}
}

// slotHolds describes what a slot holds.
func slotHolds(s registry.Slot) string {
	holds := "one node"
	switch {
	case s.List:
		holds = "a list of nodes"
	case s.Template:
		holds = "an item template"
	}
	if s.Required {
		holds += ", required"
	}
	return holds
}

// mdTypeCell renders a type, marking required values.
func mdTypeCell(t string, required bool) string {
	s := "`" + t + "`"
	if required {
		s += ", required"
	}
	return s
}

// mdDefault renders a default literal.
func mdDefault(def string) string {
	if def == "" {
		return "—"
	}
	return "`" + mdCell(def) + "`"
}

// mdConstraints describes constraints as a sentence.
func mdConstraints(c *registry.Constraints) string {
	if c == nil {
		return ""
	}
	var parts []string
	if c.Min != nil {
		parts = append(parts, fmt.Sprintf("at least %v", *c.Min))
	}
	if c.Max != nil {
		parts = append(parts, fmt.Sprintf("at most %v", *c.Max))
	}
	if c.MinLength != nil {
		parts = append(parts, fmt.Sprintf("length at least %d", *c.MinLength))
	}
	if c.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("length at most %d", *c.MaxLength))
	}
	if c.Pattern != "" {
		parts = append(parts, "matches `"+c.Pattern+"`")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Must be " + strings.Join(parts, " and ") + "."
}

// mdChildren describes the bounds of a children list.
func mdChildren(c *registry.Children) string {
	switch {
	case c.Min != nil && c.Max != nil:
		return fmt.Sprintf(" (%d to %d)", *c.Min, *c.Max)
	case c.Min != nil:
		return fmt.Sprintf(" (at least %d)", *c.Min)
	case c.Max != nil:
		return fmt.Sprintf(" (at most %d)", *c.Max)
	default:
		return ""
	}
}

// actionsMarkdown renders docs/reference/actions.md.
func actionsMarkdown(r *registry.Registry) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, RegistrySource))
	b.WriteString("# Action Reference\n\n")
	b.WriteString("The built-in actions of the action catalogue (spec Appendix D), generated from the descriptors in `schema/actions/`. ")
	b.WriteString("A step of an action graph names an action, gives its inputs as prop values and continues through `next`, `onSuccess`, `onError` or the action's named branches ([document model](document-model.md#5-action-graphs)). ")
	b.WriteString("Type parameters are bound by the compiler from the step's context. Each action is implemented in the phase shown; the compiler rejects actions whose phase has not been delivered. IDs are permanent (`BND-011`).\n\n")
	b.WriteString("| Action | Category | Phase | Description |\n|---|---|---|---|\n")
	for _, a := range r.Actions {
		fmt.Fprintf(&b, "| [%s](#%s) | %s | %s | %s |\n", a.Name, mdAnchor(a.Name), a.Category, a.Phase, mdCell(a.Description))
	}
	for _, a := range r.Actions {
		writeActionSection(&b, a)
	}
	return b.Bytes()
}

// writeActionSection renders one action's section.
func writeActionSection(b *bytes.Buffer, a *registry.Action) {
	fmt.Fprintf(b, "\n### %s\n\n%s\n\nID %d · %s · %s", a.Name, a.Description, a.ID, a.Category, a.Phase)
	if len(a.Effects) > 0 {
		b.WriteString(" · effects: " + strings.Join(a.Effects, ", "))
	}
	b.WriteString("\n")
	if len(a.TypeParams) > 0 {
		b.WriteString("\nType parameters:\n\n")
		for _, tp := range a.TypeParams {
			fmt.Fprintf(b, "- `%s` — %s\n", tp.Name, tp.Description)
		}
	}
	if len(a.Inputs) > 0 {
		b.WriteString("\n| Input | ID | Type | Default | Description |\n|---|---|---|---|---|\n")
		for _, in := range sortedByID(a.Inputs, func(in registry.Input) uint32 { return in.ID }) {
			desc := in.Description
			if in.Ref != "" {
				desc = strings.TrimSpace(desc + " Names a " + in.Ref + ".")
			}
			fmt.Fprintf(b, "| `%s` | %d | %s | %s | %s |\n", in.Name, in.ID, mdTypeCell(in.Type, in.Required), mdDefault(compactJSON(in.Default)), mdCell(desc))
		}
	}
	if tail := actionTail(a); tail != "" {
		b.WriteString("\n" + tail + "\n")
	}
}

// actionTail describes an action's output and branches.
func actionTail(a *registry.Action) string {
	var tail []string
	if a.Output != "" {
		tail = append(tail, "Output: `"+a.Output+"`.")
	}
	var names []string
	for _, br := range a.Branches {
		names = append(names, "`"+br+"`")
	}
	if a.BranchesFrom != "" {
		names = append(names, "one per value of `"+a.BranchesFrom+"`")
	}
	if len(names) > 0 {
		tail = append(tail, "Branches: "+strings.Join(names, ", ")+".")
	}
	return strings.Join(tail, " ")
}

// coverageMarkdown renders schema/widgets/COVERAGE.md, the coverage table
// of WGT-003.
func coverageMarkdown(r *registry.Registry) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, RegistrySource+" and "+registry.APIFile))
	b.WriteString("# Flutter Coverage Table\n\n")
	fmt.Fprintf(&b, "How the widget descriptors and value types cover the constructor parameters of their Flutter counterparts in the pinned **Flutter %s**, and the mirrored enums their values (`WGT-003`, [ADR-0010](../../docs/adr/0010-layered-widget-model.md)). ", r.API.Flutter)
	b.WriteString("Every parameter is supported by a prop, event, slot, the children list or a value-type field, or excluded with a reason. ")
	b.WriteString("`make gen` fails when a parameter is neither, and the Dart CI job fails when the snapshot `flutter-api.json` does not match the pinned SDK, so a Flutter upgrade that adds a parameter cannot pass unnoticed. ")
	b.WriteString("The members are documented in the [widget reference](../../docs/reference/widgets.md).\n\n")
	writeCoverageSummary(&b, r)
	for _, owner := range []string{"widget", "type"} {
		fmt.Fprintf(&b, "\n## %s\n", map[string]string{"widget": "Widgets", "type": "Value types"}[owner])
		for _, c := range r.Coverage {
			if c.Owner != owner {
				continue
			}
			fmt.Fprintf(&b, "\n### %s · %s\n\n", c.Name, flutterRef(c.Class))
			b.WriteString("| Parameter | Flutter type | Plux | Note |\n|---|---|---|---|\n")
			for _, p := range c.Params {
				b.WriteString(coverageRow(c, p))
			}
		}
	}
	b.WriteString("\n## Enums\n\n| Enum | Flutter | Values | Excluded |\n|---|---|---|---|\n")
	for _, e := range r.EnumCoverage {
		var excluded []string
		for _, v := range e.Values {
			if v.Exclusion != nil {
				excluded = append(excluded, fmt.Sprintf("`%s` (%s)", v.Name, v.Exclusion.Reason))
			}
		}
		cell := "—"
		if len(excluded) > 0 {
			cell = strings.Join(excluded, ", ")
		}
		fmt.Fprintf(&b, "| %s | `%s` | %d | %s |\n", e.Name, e.Flutter.Enum, len(e.Values), cell)
	}
	return b.Bytes()
}

// writeCoverageSummary renders the totals of the coverage table.
func writeCoverageSummary(b *bytes.Buffer, r *registry.Registry) {
	counts := map[string]int{}
	var params int
	for _, c := range r.Coverage {
		for _, p := range c.Params {
			params++
			if p.Exclusion != nil {
				counts["excluded: "+p.Exclusion.Reason]++
				continue
			}
			kind, _, _ := strings.Cut(p.Members[0], " ")
			counts[kind]++
		}
	}
	b.WriteString("## Summary\n\n| | Count |\n|---|---|\n")
	fmt.Fprintf(b, "| Flutter classes | %d |\n| Constructor parameters | %d |\n", len(r.Coverage), params)
	kinds := []struct{ kind, label string }{
		{"prop", "props"}, {"event", "events"}, {"slot", "slots"}, {"children", "the children list"}, {"field", "value-type fields"},
	}
	for _, k := range kinds {
		fmt.Fprintf(b, "| Supported as %s | %d |\n", k.label, counts[k.kind])
	}
	for _, reason := range registry.Reasons() {
		fmt.Fprintf(b, "| Excluded: %s | %d |\n", reason, counts["excluded: "+reason])
	}
	var values, excludedValues int
	for _, e := range r.EnumCoverage {
		for _, v := range e.Values {
			values++
			if v.Exclusion != nil {
				excludedValues++
			}
		}
	}
	fmt.Fprintf(b, "| Mirrored enums | %d |\n| Enum values | %d |\n| Enum values excluded | %d |\n", len(r.EnumCoverage), values, excludedValues)
	b.WriteString("\nCallbacks are supported as events and builders as slots or item templates. A parameter is excluded as **callback→event** or **builder→template** when an event or template elsewhere covers its purpose, ")
	b.WriteString("**controller→state** when the runtime owns the controller and binds it to state, **non-serialisable** when the value cannot be expressed as data, ")
	b.WriteString("**deprecated** when Flutter deprecates it, and **deferred** when it is expressible but planned for a later revision.\n")
}

// coverageRow renders the row of one parameter.
func coverageRow(c registry.ClassCoverage, p registry.ParamCoverage) string {
	param := "`" + p.Name + "`"
	if p.Deprecated {
		param += " (deprecated)"
	}
	if len(p.Constructors) < len(c.Class.Constructors) {
		param += " — " + ctorList(c.Class.Class, p.Constructors)
	}
	if p.Exclusion != nil {
		return fmt.Sprintf("| %s | `%s` | excluded: %s | %s |\n", param, mdCell(p.Type), p.Exclusion.Reason, mdCell(p.Exclusion.Note))
	}
	members := make([]string, len(p.Members))
	for i, m := range p.Members {
		kind, name, _ := strings.Cut(m, " ")
		members[i] = kind + " `" + name + "`"
		if name == "" {
			members[i] = "`" + kind + "`"
		}
	}
	return fmt.Sprintf("| %s | `%s` | %s |  |\n", param, mdCell(p.Type), strings.Join(members, ", "))
}

// ctorList names constructors of a class for a table cell.
func ctorList(class string, ctors []string) string {
	names := slices.Clone(ctors)
	for i, c := range names {
		if c == "" {
			names[i] = class
		} else {
			names[i] = class + "." + c
		}
	}
	return strings.Join(names, ", ")
}
