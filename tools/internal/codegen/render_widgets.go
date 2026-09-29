// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// controlledPairs are the (value parameter, event parameter) pairs of
// Flutter inputs whose value the node keeps locally (ADR-0031), in the
// order they are looked for.
var controlledPairs = [][2]string{
	{"groupValue", "onChanged"},
	{"groupValue", "onValueChanged"},
	{"value", "onChanged"},
	{"selected", "onSelectionChanged"},
	{"initialSelection", "onSelected"},
}

// widgetBuild collects the arguments of one widget's constructor call.
type widgetBuild struct {
	g       *renderGen
	w       *registry.Widget
	ctor    string
	ps      []registry.Parameter
	byName  map[string]registry.Parameter
	args    map[string]argument
	value   *registry.Prop  // a controlled input's value, kept locally
	changed *registry.Event // and the event that changes it
	text    *registry.Prop  // a text field's text, kept in a controller
	// choice selects the named constructor alt, with parameters altPs,
	// when true (WGT-011).
	choice *registry.Prop
	alt    string
	altPs  []registry.Parameter
}

// builder writes the builder of a widget, or returns why it must be
// written by hand. A widget whose Flutter class is generic in T but whose
// descriptor declares no type parameter holds strings, and is built as
// the class of String; one with several constructors is built with the
// first when every member maps onto it, or, when a bool prop selects a
// named constructor (WGT-011), with that one while the prop is true,
// each call taking the arguments its constructor declares.
func (g *renderGen) builder(w *registry.Widget) (string, string) {
	b, reason := g.newWidgetBuild(w)
	if reason != "" {
		return "", reason
	}
	for _, step := range []func() string{b.props, b.events, b.children, b.slots, b.required} {
		if reason := step(); reason != "" {
			return "", reason
		}
	}
	return b.render()
}

// newWidgetBuild checks that a widget maps onto a Flutter constructor and
// finds its locally kept value, if any.
func (g *renderGen) newWidgetBuild(w *registry.Widget) (*widgetBuild, string) {
	switch {
	case w.Layer == 2:
		return nil, "a Layer 2 component, written in Dart from Layer 1 widgets (WGT-020)"
	case w.Flutter == nil:
		return nil, "a structural primitive with no Flutter counterpart"
	case len(w.TypeParameters) > 0:
		return nil, "type parameters " + strings.Join(w.TypeParameters, ", ")
	}
	c, ok := g.api.Classes[w.Flutter.Key()]
	if !ok {
		return nil, "not in the Flutter snapshot"
	}
	b := &widgetBuild{g: g, w: w, ctor: w.Flutter.Class, byName: map[string]registry.Parameter{}, args: map[string]argument{}}
	class := w.Flutter.Class
	b.ps = slices.Clone(c.Constructors[w.Flutter.Constructors[0]])
	if slices.ContainsFunc(b.ps, func(p registry.Parameter) bool { return typeParamT.MatchString(p.Type) }) {
		class += "<String>"
	}
	b.ctor = constructorName(class, w.Flutter.Constructors[0])
	b.ps = ofString(b.ps)
	for _, p := range b.ps {
		b.byName[p.Name] = p
	}
	if reason := b.choose(c, class); reason != "" {
		return nil, reason
	}
	g.class = w.Flutter.Class
	for _, pair := range controlledPairs {
		vp, okP := findPropByParam(w.Props, pair[0])
		ev, okE := findEventByParam(w.Events, pair[1])
		if okP && okE {
			b.value, b.changed = &vp, &ev
			break
		}
	}
	for i, p := range w.Props {
		if len(p.Flutter) == 1 && strings.TrimSuffix(b.byName[p.Flutter[0]].Type, "?") == "TextEditingController" {
			b.text = &w.Props[i]
		}
	}
	if b.value != nil && b.text != nil {
		return nil, "both a controlled value and a text controller"
	}
	return b, ""
}

// choose finds the prop that selects a second constructor, if any, and
// adds that constructor's own parameters to those members can map.
func (b *widgetBuild) choose(c registry.FlutterClassAPI, class string) string {
	for i, p := range b.w.Props {
		if p.Constructor == "" {
			continue
		}
		ps, ok := c.Constructors[p.Constructor]
		switch {
		case b.choice != nil:
			return "props select two constructors"
		case !ok || p.Constructor == b.w.Flutter.Constructors[0]:
			return "prop " + p.Name + " selects constructor " + p.Constructor + ", which is not a second mirrored one"
		}
		b.choice, b.alt, b.altPs = &b.w.Props[i], constructorName(class, p.Constructor), ofString(ps)
		for _, ap := range b.altPs {
			if _, ok := b.byName[ap.Name]; !ok {
				b.byName[ap.Name] = ap
			}
		}
	}
	return ""
}

// constructorName is a constructor of class as called in Dart.
func constructorName(class, ctor string) string {
	if ctor == "" {
		return class
	}
	return class + "." + ctor
}

// ofString returns parameters with the type parameter T replaced by
// String.
func ofString(ps []registry.Parameter) []registry.Parameter {
	out := slices.Clone(ps)
	for i := range out {
		out[i].Type = substituteT(out[i].Type)
	}
	return out
}

func (b *widgetBuild) set(p registry.Parameter, expr string) {
	b.args[p.Name] = argument{param: p, expr: expr}
}

// props passes each prop to its parameter.
func (b *widgetBuild) props() string {
	for _, p := range b.w.Props {
		if b.choice != nil && p.Name == b.choice.Name {
			continue
		}
		if len(p.Flutter) != 1 {
			return fmt.Sprintf("prop %s maps onto %d parameters", p.Name, len(p.Flutter))
		}
		fp, ok := b.byName[p.Flutter[0]]
		switch {
		case !ok:
			return "prop " + p.Name + ": no parameter " + p.Flutter[0] + " in the mirrored constructors"
		case b.value != nil && p.Name == b.value.Name:
			b.set(fp, "value")
			continue
		case b.text != nil && p.Name == b.text.Name:
			b.set(fp, "controller")
			continue
		}
		expr, omit, reason := b.g.arg(p.Type, fp, fmt.Sprintf("c.prop(%d)", p.ID), "c", p.Default, p.Required, "c.missing("+quoteDart(p.Name)+")")
		if reason != "" {
			return "prop " + p.Name + ": " + reason
		}
		b.args[fp.Name] = argument{param: fp, expr: expr, omit: omit}
	}
	return ""
}

// events wires each event.
func (b *widgetBuild) events() string {
	for _, e := range b.w.Events {
		if len(e.Flutter) != 1 {
			return "event " + e.Name + " has no single Flutter parameter"
		}
		fp, ok := b.byName[e.Flutter[0]]
		switch {
		case !ok:
			return "event " + e.Name + ": no parameter " + e.Flutter[0]
		case b.changed != nil && e.Name == b.changed.Name:
			b.set(fp, "onChanged")
			continue
		}
		expr, reason := eventArg(e, fp)
		if reason != "" {
			return "event " + e.Name + ": " + reason
		}
		b.set(fp, expr)
	}
	return ""
}

// children passes the child nodes as a list.
func (b *widgetBuild) children() string {
	ch := b.w.Children
	if ch == nil {
		return ""
	}
	if len(ch.Flutter) != 1 {
		return "children map onto no single parameter"
	}
	fp := b.byName[ch.Flutter[0]]
	if strings.TrimSuffix(fp.Type, "?") != "List<Widget>" {
		return "children are a " + fp.Type
	}
	b.set(fp, "c.children()")
	return ""
}

// slots passes each slot's nodes.
func (b *widgetBuild) slots() string {
	for _, s := range b.w.Slots {
		if s.Template {
			return "slot " + s.Name + " is an item template"
		}
		if len(s.Flutter) != 1 {
			return "slot " + s.Name + " has no single Flutter parameter"
		}
		fp, ok := b.byName[s.Flutter[0]]
		if !ok {
			return "slot " + s.Name + ": no parameter " + s.Flutter[0]
		}
		expr, reason := slotArg(s, fp)
		if reason != "" {
			return reason
		}
		b.set(fp, expr)
	}
	return ""
}

// slotArg passes a slot as a widget, a preferred-size widget or a list.
func slotArg(s registry.Slot, fp registry.Parameter) (string, string) {
	typ := strings.TrimSuffix(fp.Type, "?")
	switch {
	case s.List && typ == "List<Widget>":
		return fmt.Sprintf("c.slotList(%d)", s.ID), ""
	case s.List || (typ != "Widget" && typ != "PreferredSizeWidget"):
		return "", "slot " + s.Name + " is a " + fp.Type
	}
	arg := fmt.Sprintf("c.slot(%d)", s.ID)
	if typ == "PreferredSizeWidget" {
		arg = fmt.Sprintf("c.preferredSizeSlot(%d)", s.ID)
	}
	switch {
	case strings.HasSuffix(fp.Type, "?"):
		return arg, ""
	case fp.Required:
		return arg + " ?? c.missing(" + quoteDart(s.Name) + ")", ""
	}
	return arg + " ?? const SizedBox.shrink()", ""
}

// required checks that every required parameter of each constructor
// called has an argument.
func (b *widgetBuild) required() string {
	for _, p := range slices.Concat(b.ps, b.altPs) {
		if _, ok := b.args[p.Name]; !ok && p.Required {
			return "required parameter " + p.Name + " is not covered"
		}
	}
	return ""
}

// call renders the constructor call, choosing between the two
// constructors when a prop selects one.
func (b *widgetBuild) call(indent string) ([]string, string, string) {
	locals, call, reason := renderCall(b.ctor, b.ps, b.args, indent)
	if reason != "" || b.choice == nil {
		return locals, call, reason
	}
	altLocals, alt, reason := renderCall(b.alt, b.altPs, b.args, indent+"    ")
	switch {
	case reason != "":
		return nil, "", reason
	case len(locals) > 0 || len(altLocals) > 0:
		return nil, "", "a constructor choice with private defaults"
	}
	call = strings.ReplaceAll(call, "\n", "\n    ")
	return nil, fmt.Sprintf("c.decode(%d, asBool) == true\n%s    ? %s\n%s    : %s", b.choice.ID, indent, alt, indent, call), ""
}

// render writes the builder function.
func (b *widgetBuild) render() (string, string) {
	indent := "  "
	if b.value != nil || b.text != nil {
		indent = "    "
	}
	locals, call, reason := b.call(indent)
	if reason != "" {
		return "", reason
	}
	var out strings.Builder
	fmt.Fprintf(&out, "/// Builds a %s node.\nWidget _build%s(NodeContext c) {\n", b.w.Type, b.w.Type)
	for _, l := range locals {
		out.WriteString("  " + l + "\n")
	}
	switch {
	case b.text != nil:
		fmt.Fprintf(&out, "  return c.text(\n    c.decode(%d, asString) ?? '',\n    (controller) => %s,\n  );\n}\n\n", b.text.ID, call)
		return out.String(), ""
	case b.value == nil:
		out.WriteString("  return " + call + ";\n}\n\n")
		return out.String(), ""
	}
	value, changed := b.byName[b.value.Flutter[0]], b.byName[b.changed.Flutter[0]]
	vt := strings.TrimSuffix(value.Type, "?")
	cb := strings.TrimSuffix(changed.Type, "?")
	if !slices.Contains([]string{"ValueChanged<" + value.Type + ">", "ValueChanged<" + vt + ">", "void Function(" + value.Type + ")", "void Function(" + vt + ")"}, cb) {
		return "", b.changed.Name + " is a " + changed.Type
	}
	initial, omit, reason := b.g.arg(b.value.Type, value, fmt.Sprintf("c.prop(%d)", b.value.ID), "c", b.value.Default, b.value.Required, "c.missing("+quoteDart(b.value.Name)+")")
	if reason != "" || omit {
		return "", "prop " + b.value.Name + ": " + reason
	}
	fmt.Fprintf(&out, "  return c.controlled<%s>(\n    %d,\n    %s,\n    (value, onChanged) => %s,\n  );\n}\n\n", value.Type, b.changed.ID, initial, call)
	return out.String(), ""
}

// substituteT replaces the type parameter T by String in a Dart type.
func substituteT(typ string) string {
	return typeParamT.ReplaceAllString(typ, "String")
}

var typeParamT = regexp.MustCompile(`\bT\b`)

// findPropByParam finds the prop mapped onto Flutter parameter name.
func findPropByParam(ps []registry.Prop, name string) (registry.Prop, bool) {
	for _, p := range ps {
		if len(p.Flutter) == 1 && p.Flutter[0] == name {
			return p, true
		}
	}
	return registry.Prop{}, false
}

// findEventByParam finds the event mapped onto Flutter parameter name.
func findEventByParam(es []registry.Event, name string) (registry.Event, bool) {
	for _, e := range es {
		if len(e.Flutter) == 1 && e.Flutter[0] == name {
			return e, true
		}
	}
	return registry.Event{}, false
}

// voidCallbacks are the Flutter callback typedefs that take no argument.
var voidCallbacks = map[string]bool{
	"VoidCallback": true, "GestureTapCallback": true, "GestureLongPressCallback": true,
	"GestureDoubleTapCallback": true, "GestureTapCancelCallback": true,
}

// eventArg wires an event: while the node handles it, a callback of the
// parameter's shape that fires it, which in P3 reports PLX-4010 and does
// nothing else; null otherwise, so the widget shows as disabled
// (ADR-0031). The first argument, if any, is the event's payload.
func eventArg(e registry.Event, fp registry.Parameter) (string, string) {
	arity, async, ok := callbackShape(strings.TrimSuffix(fp.Type, "?"))
	if !ok {
		return "", "a " + fp.Type + " callback"
	}
	params := make([]string, arity)
	for i := range params {
		params[i] = "_"
	}
	fire := fmt.Sprintf("c.fire(%d)", e.ID)
	if arity > 0 {
		params[0] = "v"
		fire = fmt.Sprintf("c.fire(%d, v)", e.ID)
	}
	body := "(" + strings.Join(params, ", ") + ") => " + fire
	if async {
		body = "(" + strings.Join(params, ", ") + ") async => " + fire
	}
	return fmt.Sprintf("c.handles(%d) ? %s : null", e.ID, body), ""
}

// callbackShape returns the number of arguments of a Flutter callback
// type and whether it returns a future.
func callbackShape(typ string) (arity int, async, ok bool) {
	if voidCallbacks[typ] {
		return 0, false, true
	}
	if strings.HasPrefix(typ, "ValueChanged<") {
		return 1, false, true
	}
	for _, ret := range []string{"void", "Future<void>", "FutureOr<void>"} {
		prefix := ret + " Function("
		if strings.HasPrefix(typ, prefix) && strings.HasSuffix(typ, ")") {
			return countArgs(typ[len(prefix) : len(typ)-1]), ret != "void", true
		}
	}
	return 0, false, false
}

// countArgs counts the top-level comma-separated arguments of a list.
func countArgs(args string) int {
	if args == "" {
		return 0
	}
	depth, n := 0, 1
	for _, r := range args {
		switch r {
		case '<', '(':
			depth++
		case '>', ')':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}
