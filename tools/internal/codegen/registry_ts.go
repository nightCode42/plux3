// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// TypeScript shapes of the registry, encoded as JSON object literals.
type (
	tsWidget struct {
		Type           string         `json:"type"`
		ID             uint32         `json:"id"`
		Layer          int            `json:"layer"`
		Phase          string         `json:"phase"`
		Revision       int            `json:"revision"`
		Runtimes       []string       `json:"runtimes"`
		Category       string         `json:"category"`
		Icon           string         `json:"icon"`
		Description    string         `json:"description"`
		Platforms      []string       `json:"platforms"`
		Cost           int            `json:"cost"`
		Accessibility  any            `json:"accessibility"`
		TypeParameters []string       `json:"typeParameters"`
		Props          []tsProp       `json:"props"`
		Events         []tsEvent      `json:"events"`
		Slots          []tsSlot       `json:"slots"`
		Children       *tsChildren    `json:"children,omitempty"`
		Deprecated     *tsDeprecation `json:"deprecated,omitempty"`
	}
	tsProp struct {
		Name        string          `json:"name"`
		ID          uint32          `json:"id"`
		Type        string          `json:"type"`
		Required    bool            `json:"required"`
		Default     json.RawMessage `json:"default,omitempty"`
		Constraints *tsConstraints  `json:"constraints,omitempty"`
		Revision    int             `json:"revision"`
		Deprecated  *tsDeprecation  `json:"deprecated,omitempty"`
		Bindable    bool            `json:"bindable"`
		Description string          `json:"description,omitempty"`
	}
	tsConstraints struct {
		Min       *float64 `json:"min,omitempty"`
		Max       *float64 `json:"max,omitempty"`
		MinLength *int     `json:"minLength,omitempty"`
		MaxLength *int     `json:"maxLength,omitempty"`
		Pattern   string   `json:"pattern,omitempty"`
	}
	tsEvent struct {
		Name        string         `json:"name"`
		ID          uint32         `json:"id"`
		Payload     string         `json:"payload,omitempty"`
		Revision    int            `json:"revision"`
		Deprecated  *tsDeprecation `json:"deprecated,omitempty"`
		Description string         `json:"description,omitempty"`
	}
	tsSlot struct {
		Name        string         `json:"name"`
		ID          uint32         `json:"id"`
		List        bool           `json:"list"`
		Required    bool           `json:"required"`
		Template    bool           `json:"template"`
		Revision    int            `json:"revision"`
		Deprecated  *tsDeprecation `json:"deprecated,omitempty"`
		Description string         `json:"description,omitempty"`
	}
	tsChildren struct {
		Min *int `json:"min,omitempty"`
		Max *int `json:"max,omitempty"`
	}
	tsDeprecation struct {
		Revision int    `json:"revision"`
		Message  string `json:"message"`
	}
	tsValueType struct {
		Name        string       `json:"name"`
		ID          uint32       `json:"id"`
		Revision    int          `json:"revision"`
		Runtimes    []string     `json:"runtimes"`
		Description string       `json:"description"`
		Fields      []tsField    `json:"fields"`
		Constants   []tsConstant `json:"constants"`
	}
	tsField struct {
		Name        string          `json:"name"`
		ID          uint32          `json:"id"`
		Type        string          `json:"type"`
		Required    bool            `json:"required"`
		Default     json.RawMessage `json:"default,omitempty"`
		Revision    int             `json:"revision"`
		Deprecated  *tsDeprecation  `json:"deprecated,omitempty"`
		Description string          `json:"description,omitempty"`
	}
	tsConstant struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	tsEnum struct {
		Name        string        `json:"name"`
		ID          uint32        `json:"id"`
		Revision    int           `json:"revision"`
		Runtimes    []string      `json:"runtimes"`
		Description string        `json:"description"`
		Values      []tsEnumValue `json:"values"`
	}
	tsEnumValue struct {
		Name        string         `json:"name"`
		ID          uint32         `json:"id"`
		Revision    int            `json:"revision"`
		Deprecated  *tsDeprecation `json:"deprecated,omitempty"`
		Description string         `json:"description,omitempty"`
	}
	tsAction struct {
		Name           string               `json:"name"`
		ID             uint32               `json:"id"`
		Phase          string               `json:"phase"`
		Category       string               `json:"category"`
		Description    string               `json:"description"`
		TypeParameters []registry.TypeParam `json:"typeParameters"`
		Inputs         []tsInput            `json:"inputs"`
		Output         string               `json:"output,omitempty"`
		Branches       []string             `json:"branches"`
		BranchesFrom   string               `json:"branchesFrom,omitempty"`
		Effects        []string             `json:"effects"`
	}
	tsInput struct {
		Name        string          `json:"name"`
		ID          uint32          `json:"id"`
		Type        string          `json:"type"`
		Required    bool            `json:"required"`
		Default     json.RawMessage `json:"default,omitempty"`
		Ref         string          `json:"ref,omitempty"`
		Description string          `json:"description,omitempty"`
	}
)

// registryTS renders studio/packages/schema/src/registry.gen.ts: complete
// descriptors for Studio's property panels and AI grounding (WGT-002).
func registryTS(r *registry.Registry) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header(LangTS, RegistrySource))
	b.WriteString("import type { JsonValue } from \"./document.gen.ts\";\n\n")
	fmt.Fprintf(&b, "/** A platform a widget supports. */\nexport type Platform = %s;\n\n", tsUnion(registry.Platforms()))
	fmt.Fprintf(&b, "/** The accessibility role of a widget (A11Y-002). */\nexport type AccessibilityRole = %s;\n\n", tsUnion(registry.Roles()))
	fmt.Fprintf(&b, "/** A category of the action catalogue (Appendix D). */\nexport type ActionCategory = %s;\n\n", tsUnion(registry.ActionCategories()))
	fmt.Fprintf(&b, "/** A kind of side effect an action has. */\nexport type ActionEffect = %s;\n\n", tsUnion(registry.ActionEffects()))
	b.WriteString(tsRegistryInterfaces)
	names := func(n int, name func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = name(i)
		}
		return out
	}
	fmt.Fprintf(&b, "\n/** A widget type name. */\nexport type WidgetType = %s;\n", tsUnion(names(len(r.Widgets), func(i int) string { return r.Widgets[i].Type })))
	fmt.Fprintf(&b, "\n/** An action name. */\nexport type ActionName = %s;\n", tsUnion(names(len(r.Actions), func(i int) string { return r.Actions[i].Name })))

	sections := []struct {
		doc, decl string
		value     any
	}{
		{"Every widget descriptor, sorted by type name (WGT-001).", "widgets: readonly WidgetDescriptor[]", tsWidgets(r)},
		{"Every value type, sorted by name.", "valueTypes: readonly ValueTypeDescriptor[]", tsValueTypes(r)},
		{"Every enum, sorted by name.", "enums: readonly EnumDescriptor[]", tsEnums(r)},
		{"Every action, sorted by name (Appendix D).", "actions: readonly ActionDescriptor[]", tsActions(r)},
	}
	for _, s := range sections {
		var js bytes.Buffer
		enc := json.NewEncoder(&js)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(s.value); err != nil {
			return nil, fmt.Errorf("codegen: registry TypeScript: %w", err)
		}
		fmt.Fprintf(&b, "\n/** %s */\nexport const %s = %s;\n", s.doc, s.decl, collapseJSON(bytes.TrimRight(js.Bytes(), "\n")))
	}
	return b.Bytes(), nil
}

// collapseJSON puts nested JSON on one line to keep the file reviewable:
// every object or array opened at an indentation of six or more spaces —
// the members of an entry — and every flat one at four. Input is indented
// JSON, whose strings never contain raw newlines, so lines are tokens.
func collapseJSON(src []byte) []byte {
	lines := strings.Split(string(src), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		indent := len(line) - len(strings.TrimLeft(line, " "))
		open := strings.LastIndexAny(line, "{[")
		if open < 0 || open != len(line)-1 || indent < 4 {
			out = append(out, line)
			continue
		}
		end, flat := closing(lines, i, indent)
		if end < 0 || indent < 6 && !flat {
			out = append(out, line)
			continue
		}
		block := line[open:] + "\n" + strings.Join(lines[i+1:end+1], "\n")
		comma := strings.HasSuffix(block, ",")
		var c bytes.Buffer
		if err := json.Compact(&c, []byte(strings.TrimSuffix(block, ","))); err != nil {
			out = append(out, line)
			continue
		}
		joined := line[:open] + spaceJSON(c.String())
		if comma {
			joined += ","
		}
		out = append(out, joined)
		i = end
	}
	return []byte(strings.Join(out, "\n"))
}

// spaceJSON adds a space after every comma and colon of compact JSON that
// is not inside a string.
func spaceJSON(compact string) string {
	var b strings.Builder
	inString, escaped := false, false
	for _, r := range compact {
		b.WriteRune(r)
		switch {
		case escaped:
			escaped = false
		case inString && r == '\\':
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && (r == ',' || r == ':'):
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// closing returns the line closing the block opened on line start at
// indent, and whether the block contains no nested blocks.
func closing(lines []string, start, indent int) (int, bool) {
	flat := true
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimLeft(lines[j], " ")
		if len(lines[j])-len(t) == indent && (strings.HasPrefix(t, "}") || strings.HasPrefix(t, "]")) {
			return j, flat
		}
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, "[") {
			flat = false
		}
	}
	return -1, false
}

// tsDep converts a deprecation.
func tsDep(d *registry.Deprecation) *tsDeprecation {
	if d == nil {
		return nil
	}
	return &tsDeprecation{Revision: d.Revision, Message: d.Message}
}

// tsWidgets converts the widget descriptors.
func tsWidgets(r *registry.Registry) []tsWidget {
	out := make([]tsWidget, 0, len(r.Widgets))
	for _, w := range r.Widgets {
		tw := tsWidget{
			Type: w.Type, ID: w.ID, Layer: w.Layer, Phase: w.Phase, Revision: w.Revision, Runtimes: runtimes(w.Revisions),
			Category: w.Category, Icon: w.Icon, Description: w.Description, Platforms: w.Platforms, Cost: w.Cost,
			Accessibility: w.Accessibility, TypeParameters: nonNil(w.TypeParameters),
			Props: []tsProp{}, Events: []tsEvent{}, Slots: []tsSlot{}, Deprecated: tsDep(w.Deprecated),
		}
		for _, p := range sortedByID(w.Props, func(p registry.Prop) uint32 { return p.ID }) {
			tp := tsProp{
				Name: p.Name, ID: p.ID, Type: p.Type, Required: p.Required, Default: p.Default, Revision: revisionOr1(p.Revision),
				Deprecated: tsDep(p.Deprecated), Bindable: p.IsBindable(), Description: p.Description,
			}
			if c := p.Constraints; c != nil {
				tp.Constraints = &tsConstraints{Min: c.Min, Max: c.Max, MinLength: c.MinLength, MaxLength: c.MaxLength, Pattern: c.Pattern}
			}
			tw.Props = append(tw.Props, tp)
		}
		for _, e := range sortedByID(w.Events, func(e registry.Event) uint32 { return e.ID }) {
			tw.Events = append(tw.Events, tsEvent{Name: e.Name, ID: e.ID, Payload: e.Payload, Revision: revisionOr1(e.Revision), Deprecated: tsDep(e.Deprecated), Description: e.Description})
		}
		for _, s := range sortedByID(w.Slots, func(s registry.Slot) uint32 { return s.ID }) {
			tw.Slots = append(tw.Slots, tsSlot{
				Name: s.Name, ID: s.ID, List: s.List, Required: s.Required, Template: s.Template,
				Revision: revisionOr1(s.Revision), Deprecated: tsDep(s.Deprecated), Description: s.Description,
			})
		}
		if c := w.Children; c != nil {
			tw.Children = &tsChildren{Min: c.Min, Max: c.Max}
		}
		out = append(out, tw)
	}
	return out
}

// tsValueTypes converts the value types.
func tsValueTypes(r *registry.Registry) []tsValueType {
	out := make([]tsValueType, 0, len(r.Types))
	for _, t := range r.Types {
		tt := tsValueType{
			Name: t.Name, ID: t.ID, Revision: t.Revision, Runtimes: runtimes(t.Revisions), Description: t.Description,
			Fields: []tsField{}, Constants: []tsConstant{},
		}
		for _, f := range sortedByID(t.Fields, func(f registry.Field) uint32 { return f.ID }) {
			tt.Fields = append(tt.Fields, tsField{
				Name: f.Name, ID: f.ID, Type: f.Type, Required: f.Required, Default: f.Default,
				Revision: revisionOr1(f.Revision), Deprecated: tsDep(f.Deprecated), Description: f.Description,
			})
		}
		for _, c := range t.Constants {
			tt.Constants = append(tt.Constants, tsConstant{Name: c.Name, Value: c.Value})
		}
		out = append(out, tt)
	}
	return out
}

// tsEnums converts the enums.
func tsEnums(r *registry.Registry) []tsEnum {
	out := make([]tsEnum, 0, len(r.Enums))
	for _, e := range r.Enums {
		te := tsEnum{Name: e.Name, ID: e.ID, Revision: e.Revision, Runtimes: runtimes(e.Revisions), Description: e.Description, Values: []tsEnumValue{}}
		for _, v := range sortedByID(e.Values, func(v registry.EnumValue) uint32 { return v.ID }) {
			te.Values = append(te.Values, tsEnumValue{Name: v.Name, ID: v.ID, Revision: revisionOr1(v.Revision), Deprecated: tsDep(v.Deprecated), Description: v.Description})
		}
		out = append(out, te)
	}
	return out
}

// tsActions converts the actions.
func tsActions(r *registry.Registry) []tsAction {
	out := make([]tsAction, 0, len(r.Actions))
	for _, a := range r.Actions {
		ta := tsAction{
			Name: a.Name, ID: a.ID, Phase: a.Phase, Category: a.Category, Description: a.Description,
			TypeParameters: nonNil(a.TypeParams), Inputs: []tsInput{}, Output: a.Output, Branches: nonNil(a.Branches),
			BranchesFrom: a.BranchesFrom, Effects: nonNil(a.Effects),
		}
		for _, in := range sortedByID(a.Inputs, func(in registry.Input) uint32 { return in.ID }) {
			ta.Inputs = append(ta.Inputs, tsInput{Name: in.Name, ID: in.ID, Type: in.Type, Required: in.Required, Default: in.Default, Ref: in.Ref, Description: in.Description})
		}
		out = append(out, ta)
	}
	return out
}

// nonNil returns s, or an empty slice for nil, so JSON renders [].
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// tsRegistryInterfaces declares the descriptor types of registry.gen.ts.
const tsRegistryInterfaces = `/** Deprecation of a registry entry or member; members are never removed (WGT-004). */
export interface Deprecation {
  readonly revision: number;
  readonly message: string;
}

/** Constraints on literal values. */
export interface Constraints {
  readonly min?: number;
  readonly max?: number;
  readonly minLength?: number;
  readonly maxLength?: number;
  readonly pattern?: string;
}

/** A widget prop. */
export interface PropDescriptor {
  readonly name: string;
  readonly id: number;
  /** A type expression (SCH-010), value type or enum. */
  readonly type: string;
  readonly required: boolean;
  readonly default?: JsonValue;
  readonly constraints?: Constraints;
  /** The descriptor revision that added the prop (WGT-004). */
  readonly revision: number;
  readonly deprecated?: Deprecation;
  /** False when the prop accepts only literals. */
  readonly bindable: boolean;
  readonly description?: string;
}

/** A widget event; its payload is available as ` + "`event`" + ` in handlers. */
export interface EventDescriptor {
  readonly name: string;
  readonly id: number;
  readonly payload?: string;
  readonly revision: number;
  readonly deprecated?: Deprecation;
  readonly description?: string;
}

/** A named slot. */
export interface SlotDescriptor {
  readonly name: string;
  readonly id: number;
  /** Holds a list of nodes. */
  readonly list: boolean;
  readonly required: boolean;
  /** Built per item with ` + "`item` and `index`" + ` in scope. */
  readonly template: boolean;
  readonly revision: number;
  readonly deprecated?: Deprecation;
  readonly description?: string;
}

/** A widget descriptor (WGT-001). */
export interface WidgetDescriptor {
  readonly type: string;
  readonly id: number;
  readonly layer: number;
  readonly phase: string;
  readonly revision: number;
  /** The first runtime version implementing each revision, in order. */
  readonly runtimes: readonly string[];
  readonly category: string;
  /** Material Symbols name. */
  readonly icon: string;
  readonly description: string;
  readonly platforms: readonly Platform[];
  /** Estimated build cost in microseconds on the mid-tier reference device (CMP-040). */
  readonly cost: number;
  readonly accessibility: { readonly role: AccessibilityRole; readonly interactive: boolean };
  readonly typeParameters: readonly string[];
  readonly props: readonly PropDescriptor[];
  readonly events: readonly EventDescriptor[];
  readonly slots: readonly SlotDescriptor[];
  /** Set when the widget takes a list of children instead of slots. */
  readonly children?: { readonly min?: number; readonly max?: number };
  readonly deprecated?: Deprecation;
}

/** A field of a value type. */
export interface FieldDescriptor {
  readonly name: string;
  readonly id: number;
  readonly type: string;
  readonly required: boolean;
  readonly default?: JsonValue;
  readonly revision: number;
  readonly deprecated?: Deprecation;
  readonly description?: string;
}

/** A structured prop value, such as EdgeInsets. */
export interface ValueTypeDescriptor {
  readonly name: string;
  readonly id: number;
  readonly revision: number;
  readonly runtimes: readonly string[];
  readonly description: string;
  readonly fields: readonly FieldDescriptor[];
  /** Named values; a literal may be written as a constant's name. */
  readonly constants: readonly { readonly name: string; readonly value: JsonValue }[];
}

/** An enum value. */
export interface EnumValueDescriptor {
  readonly name: string;
  readonly id: number;
  readonly revision: number;
  readonly deprecated?: Deprecation;
  readonly description?: string;
}

/** An enumeration used by props and actions. */
export interface EnumDescriptor {
  readonly name: string;
  readonly id: number;
  readonly revision: number;
  readonly runtimes: readonly string[];
  readonly description: string;
  readonly values: readonly EnumValueDescriptor[];
}

/** A typed action input. */
export interface InputDescriptor {
  readonly name: string;
  readonly id: number;
  readonly type: string;
  readonly required: boolean;
  readonly default?: JsonValue;
  /** What a string input names, such as a state entry. */
  readonly ref?: string;
  readonly description?: string;
}

/** A built-in action (Appendix D). */
export interface ActionDescriptor {
  readonly name: string;
  readonly id: number;
  readonly phase: string;
  readonly category: ActionCategory;
  readonly description: string;
  /** Bound by the compiler from the step's context, as each description states. */
  readonly typeParameters: readonly { readonly name: string; readonly description: string }[];
  readonly inputs: readonly InputDescriptor[];
  readonly output?: string;
  /** Named edges besides next, onSuccess and onError. */
  readonly branches: readonly string[];
  /** A list<string> input whose values name further branches. */
  readonly branchesFrom?: string;
  readonly effects: readonly ActionEffect[];
}
`
