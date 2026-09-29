// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// renderDartPath is the runtime's generated decoders and widget builders
// (WGT-002, ADR-0031).
const renderDartPath = "packages/plux_flutter/lib/src/render/generated/render.g.dart"

// handDecoders are the value types and enums whose Dart decoders are
// written by hand in lib/src/render/decoders.dart, with the Dart type each
// returns: their Flutter counterpart is chosen by which fields are set
// (several classes or named constructors), needs the runtime (icons,
// images), or is a Flutter class of constants rather than an enum.
var handDecoders = map[string]string{
	"Alignment":                    "AlignmentGeometry",
	"Border":                       "BoxBorder",
	"ButtonSegment":                "ButtonSegment<String>",
	"DropdownMenuEntry":            "DropdownMenuEntry<String>",
	"BorderRadius":                 "BorderRadiusGeometry",
	"EdgeInsets":                   "EdgeInsetsGeometry",
	"Gradient":                     "Gradient",
	"IconData":                     "PluxIconSource",
	"ImageSource":                  "ImageProvider<Object>",
	"InputBorder":                  "InputBorder",
	"Radius":                       "Radius",
	"ShapeBorder":                  "ShapeBorder",
	"WidgetStateBorderSide":        "WidgetStateProperty<BorderSide?>",
	"WidgetStateColor":             "WidgetStateProperty<Color?>",
	"WidgetStateDouble":            "WidgetStateProperty<double?>",
	"WidgetStateEdgeInsets":        "WidgetStateProperty<EdgeInsetsGeometry?>",
	"WidgetStateShapeBorder":       "WidgetStateProperty<OutlinedBorder?>",
	"WidgetStateSize":              "WidgetStateProperty<Size?>",
	"WidgetStateTextStyle":         "WidgetStateProperty<TextStyle?>",
	"FloatingActionButtonLocation": "FloatingActionButtonLocation",
	"FloatingLabelAlignment":       "FloatingLabelAlignment",
	"FontWeight":                   "FontWeight",
	"ScrollPhysics":                "ScrollPhysics",
	"TextAlignVertical":            "TextAlignVertical",
	"TextDecoration":               "TextDecoration",
	"TextInputType":                "TextInputType",
	"VisualDensity":                "VisualDensity",
}

// adapters convert a value into a Flutter parameter type its decoder does
// not return directly: `decoder` is a Dart function of (Decoding, Object?).
var adapters = map[[2]string]string{
	{"ShapeBorder", "OutlinedBorder"}:     "decodeOutlinedBorder",
	{"EdgeInsets", "EdgeInsets"}:          "decodeEdgeInsetsResolved",
	{"BorderRadius", "BorderRadius"}:      "decodeBorderRadiusResolved",
	{"Alignment", "AlignmentDirectional"}: "decodeAlignmentDirectional",
	{"IconData", "Widget"}:                "decodeIconWidget",
	{"string", "Locale"}:                  "asLocale",
	{"list<string>", "List<Locale>"}:      "asLocales",
	{"list<string>", "Iterable<String>"}:  "asStrings",
	{"list<string>", "List<String>"}:      "asStrings",
	{"list<string>", "Set<String>"}:       "asStringSet",
	{"list<color>", "List<Color>"}:        "asColors",
	{"list<double>", "List<double>"}:      "asDoubles",
}

// supertypes lists, for a decoder's Dart type, the parameter types it may
// be passed as.
var supertypes = map[string][]string{
	"BoxDecoration": {"Decoration"},
	"TextSpan":      {"InlineSpan"},
	"BoxBorder":     {"BoxBorder"},
}

// scalarDecoders decode the primitive types, by their Dart type.
var scalarDecoders = map[string][2]string{
	"bool":     {"asBool", "bool"},
	"int":      {"asInt", "int"},
	"double":   {"asDouble", "double"},
	"string":   {"asString", "String"},
	"color":    {"asColor", "Color"},
	"duration": {"asDuration", "Duration"},
}

// renderGen writes render.g.dart.
type renderGen struct {
	r        *registry.Registry
	enums    map[string]*registry.Enum
	types    map[string]*registry.ValueType
	api      *registry.FlutterAPI
	decoders map[string]string // value type or enum → Dart type of its decoder
	defaults []string          // top-level default values
	named    map[string]string // default expression → its name
	class    string            // the Flutter class whose call is generated
	lists    map[string]string // element type → its list decoder, declared
	b        bytes.Buffer
}

// renderDart renders the runtime's decoders and builders: a decoder for
// every enum and value type whose Flutter counterpart is one constructor
// taking its fields, and a builder for every Layer 1 widget whose props,
// events, children and slots map onto one Flutter constructor. Everything
// else is listed as hand-written with the reason, and the runtime's tests
// check that each has a hand-written builder (ADR-0031).
func renderDart(r *registry.Registry) ([]byte, error) {
	g := newRenderGen(r)
	typeCode, err := g.valueTypes()
	if err != nil {
		return nil, err
	}
	widgetCode, generated, hand := g.widgets()
	b := &g.b
	b.WriteString(header(LangDart, RegistrySource))
	b.WriteString("/// Decoders and widget builders generated from the widget descriptors\n/// (WGT-002, ADR-0031).\nlibrary;\n\n")
	b.WriteString(g.imports())
	b.WriteString("// ── Enums ─────────────────────────────────────────────────────────────────\n\n")
	for _, e := range r.Enums {
		g.enumDecoders(e)
	}
	b.WriteString("// ── Value types ───────────────────────────────────────────────────────────\n\n")
	fmt.Fprintf(b, "/// Value types whose decoders are written by hand (decoders.dart).\nconst List<String> handWrittenDecoders = [%s];\n\n", quoteList(g.handTypes()))
	b.WriteString(typeCode)
	for _, elem := range slices.Sorted(maps.Keys(g.lists)) {
		b.WriteString(g.lists[elem])
	}
	b.WriteString("// ── Widgets ───────────────────────────────────────────────────────────────\n\n")
	b.WriteString(widgetCode)
	b.WriteString("/// The generated builders, by permanent widget ID.\nconst Map<int, NodeBuilder> generatedBuilders = {\n")
	for _, w := range generated {
		fmt.Fprintf(b, "  %d: _build%s, // %s\n", w.ID, w.Type, w.Type)
	}
	b.WriteString("};\n\n/// The P3 widgets whose builders are written by hand, by permanent ID,\n/// with the reason the generator cannot write them.\nconst Map<int, String> handWrittenBuilders = {\n")
	for _, h := range hand {
		fmt.Fprintf(b, "  %d: %s, // %s\n", h.w.ID, quoteDart(h.w.Type+": "+h.reason), h.w.Type)
	}
	b.WriteString("};\n\n")
	g.writeIDs(hand)
	if len(g.defaults) > 0 {
		b.WriteString("\n// ── Default values ────────────────────────────────────────────────────────\n\n")
		for _, d := range g.defaults {
			b.WriteString(d)
		}
	}
	return b.Bytes(), nil
}

// newRenderGen indexes the registry and the Dart types of the decoders.
func newRenderGen(r *registry.Registry) *renderGen {
	g := &renderGen{r: r, enums: map[string]*registry.Enum{}, types: map[string]*registry.ValueType{}, api: r.API, decoders: map[string]string{}, named: map[string]string{}, lists: map[string]string{}}
	for _, e := range r.Enums {
		g.enums[e.Name] = e
		if e.Flutter != nil {
			g.decoders[e.Name] = e.Flutter.Enum
		}
	}
	for _, t := range r.Types {
		g.types[t.Name] = t
		if _, hand := handDecoders[t.Name]; !hand && len(t.Flutter) == 1 {
			g.decoders[t.Name] = t.Flutter[0].Class
		}
	}
	maps.Copy(g.decoders, handDecoders)
	return g
}

// handTypes lists the value types whose decoders are written by hand.
func (g *renderGen) handTypes() []string {
	var out []string
	for _, t := range g.r.Types {
		if _, hand := handDecoders[t.Name]; hand {
			out = append(out, t.Name)
		}
	}
	return out
}

// valueTypes writes the generated value-type decoders.
func (g *renderGen) valueTypes() (string, error) {
	var b strings.Builder
	for _, t := range g.r.Types {
		if _, hand := handDecoders[t.Name]; hand || len(t.Flutter) == 0 {
			continue
		}
		code, err := g.valueTypeDecoder(t)
		if err != nil {
			return "", err
		}
		b.WriteString(code)
	}
	return b.String(), nil
}

// handWidget is a widget whose builder is written by hand.
type handWidget struct {
	w      *registry.Widget
	reason string
}

// widgets writes the builders of the P3 widgets the generator can
// express and lists the others.
func (g *renderGen) widgets() (string, []*registry.Widget, []handWidget) {
	var b strings.Builder
	var generated []*registry.Widget
	var hand []handWidget
	for _, w := range g.r.Widgets {
		if w.Phase != "P3" {
			continue
		}
		code, reason := g.builder(w)
		if reason != "" {
			hand = append(hand, handWidget{w, reason})
			continue
		}
		generated = append(generated, w)
		b.WriteString(code)
	}
	return b.String(), generated, hand
}

// writeIDs writes the permanent IDs the hand-written code reads.
func (g *renderGen) writeIDs(hand []handWidget) {
	b := &g.b
	b.WriteString("// ── Permanent IDs for hand-written code (BND-011) ────────────────────────\n\n")
	b.WriteString("/// Permanent widget IDs of the hand-written builders.\nabstract final class WidgetIds {\n")
	for _, h := range hand {
		fmt.Fprintf(b, "  /// %s.\n  static const int %s = %d;\n", h.w.Type, dartMember(lowerFirst(h.w.Type)), h.w.ID)
	}
	b.WriteString("}\n\n")
	for _, h := range hand {
		w := h.w
		writeIDClass(b, w.Type+"Props", "props of "+w.Type, w.Props, func(p registry.Prop) (string, uint32) { return p.Name, p.ID })
		writeIDClass(b, w.Type+"Events", "events of "+w.Type, w.Events, func(e registry.Event) (string, uint32) { return e.Name, e.ID })
		writeIDClass(b, w.Type+"Slots", "slots of "+w.Type, w.Slots, func(s registry.Slot) (string, uint32) { return s.Name, s.ID })
	}
	for _, name := range g.handTypes() {
		t := g.types[name]
		writeIDClass(b, t.Name+"Fields", "fields of "+t.Name, t.Fields, func(f registry.Field) (string, uint32) { return f.Name, f.ID })
	}
}

// imports lists the Flutter libraries the mirrored classes and enums
// live in; names from dart:ui are imported by name, since the Flutter
// libraries redefine some of its others.
func (g *renderGen) imports() string {
	libs := map[string]bool{"package:flutter/material.dart": true, "package:flutter/cupertino.dart": true}
	var ui []string
	for _, e := range g.r.Enums {
		if e.Flutter == nil {
			continue
		}
		if e.Flutter.Library == "dart:ui" {
			ui = append(ui, e.Flutter.Enum)
		} else {
			libs[e.Flutter.Library] = true
		}
	}
	for _, t := range g.r.Types {
		for _, c := range t.Flutter {
			if c.Library != "dart:ui" {
				libs[c.Library] = true
			}
		}
	}
	for _, w := range g.r.Widgets {
		if w.Flutter != nil && w.Flutter.Library != "dart:ui" {
			libs[w.Flutter.Library] = true
		}
	}
	var b strings.Builder
	if len(ui) > 0 {
		slices.Sort(ui)
		b.WriteString("import 'dart:ui' show " + strings.Join(slices.Compact(ui), ", ") + ";\n\n")
	}
	names := slices.Sorted(maps.Keys(libs))
	names = append(names, "package:plux_flutter/src/render/decoders.dart", "package:plux_flutter/src/render/decoding.dart", "package:plux_flutter/src/render/node_context.dart")
	for _, l := range names {
		b.WriteString("import '" + l + "';\n")
	}
	b.WriteString("\n")
	return b.String()
}

// enumDecoders writes the name decoder of an enum and, for one mirroring
// a Flutter enum, its typed decoder. A literal carries the permanent value
// ID; a PXL value carries the member name.
func (g *renderGen) enumDecoders(e *registry.Enum) {
	b := &g.b
	values := sortedByID(e.Values, func(v registry.EnumValue) uint32 { return v.ID })
	fmt.Fprintf(b, "/// The member name of a %s, from its permanent value ID or name.\nString? name%s(Decoding d, Object? v) => switch (v) {\n", e.Name, e.Name)
	for _, v := range values {
		fmt.Fprintf(b, "  %d || %s => %s,\n", v.ID, quoteDart(v.Name), quoteDart(v.Name))
	}
	b.WriteString("  _ => null,\n};\n\n")
	if e.Flutter == nil {
		return
	}
	fmt.Fprintf(b, "/// Decodes a %s.\n%s? decode%s(Decoding d, Object? v) => switch (v) {\n", e.Name, e.Flutter.Enum, e.Name)
	for _, v := range values {
		fmt.Fprintf(b, "  %d || %s => %s.%s,\n", v.ID, quoteDart(v.Name), e.Flutter.Enum, dartMember(v.Name))
	}
	b.WriteString("  _ => null,\n};\n\n")
}

// dartReserved are Dart's reserved words, which cannot name a member.
var dartReserved = map[string]bool{
	"assert": true, "break": true, "case": true, "catch": true, "class": true, "const": true,
	"continue": true, "default": true, "do": true, "else": true, "enum": true, "extends": true,
	"false": true, "final": true, "finally": true, "for": true, "if": true, "in": true, "is": true,
	"new": true, "null": true, "rethrow": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "true": true, "try": true, "var": true, "void": true,
	"while": true, "with": true,
}

// dartMember escapes a member name that is a Dart reserved word.
func dartMember(name string) string {
	if dartReserved[name] {
		return name + "_"
	}
	return name
}

// valueTypeDecoder writes the decoder of a value type mirroring one
// constructor that takes its fields.
func (g *renderGen) valueTypeDecoder(t *registry.ValueType) (string, error) {
	if len(t.Flutter) != 1 || len(t.Flutter[0].Constructors) != 1 {
		return "", fmt.Errorf("codegen: value type %s maps onto several constructors; add it to handDecoders", t.Name)
	}
	fl := t.Flutter[0]
	g.class = fl.Class
	params, ok := g.params(fl.Library, fl.Class, fl.Constructors[0])
	if !ok {
		return "", fmt.Errorf("codegen: value type %s: %s is not in the Flutter snapshot", t.Name, fl.Key())
	}
	args := map[string]argument{}
	for _, f := range sortedByID(t.Fields, func(f registry.Field) uint32 { return f.ID }) {
		if len(f.Flutter) != 1 {
			return "", fmt.Errorf("codegen: value type %s: field %s maps onto %d parameters; add the type to handDecoders", t.Name, f.Name, len(f.Flutter))
		}
		p, ok := params[f.Flutter[0]]
		if !ok {
			return "", fmt.Errorf("codegen: value type %s: field %s: no parameter %s", t.Name, f.Name, f.Flutter[0])
		}
		expr, omit, reason := g.arg(f.Type, p, fmt.Sprintf("f.get(%d, %s)", f.ID, quoteDart(f.Name)), "d", f.Default, f.Required, "f.missing("+quoteDart(f.Name)+")")
		if reason != "" {
			return "", fmt.Errorf("codegen: value type %s: field %s: %s; add the type to handDecoders", t.Name, f.Name, reason)
		}
		args[p.Name] = argument{param: p, expr: expr, omit: omit}
	}
	locals, call, reason := renderCall(fl.Class, g.api.Classes[fl.Key()].Constructors[fl.Constructors[0]], args, "  ")
	if reason != "" {
		return "", fmt.Errorf("codegen: value type %s: %s; add it to handDecoders", t.Name, reason)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/// Decodes a %s.\n%s? decode%s(Decoding d, Object? v) {\n  final f = Fields.of(d, v);\n  if (f == null) return null;\n", t.Name, fl.Class, t.Name)
	for _, l := range locals {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("  return " + call + ";\n}\n\n")
	return b.String(), nil
}

// params returns a constructor's parameters by name.
func (g *renderGen) params(library, class, ctor string) (map[string]registry.Parameter, bool) {
	c, ok := g.api.Classes[library+"#"+class]
	if !ok {
		return nil, false
	}
	ps, ok := c.Constructors[ctor]
	if !ok {
		return nil, false
	}
	out := map[string]registry.Parameter{}
	for _, p := range ps {
		out[p.Name] = p
	}
	return out, true
}

// arg returns the Dart expression passing value (an Object? expression)
// of Plux type typ as Flutter parameter p, with the descriptor default or
// the Flutter default where the parameter cannot be null; or the reason
// no expression can be generated. When omit is true the expression is
// nullable and the argument is left out while it is null, because
// Flutter's default is private. env names the Decoding in scope.
func (g *renderGen) arg(typ string, p registry.Parameter, value, env string, def json.RawMessage, required bool, missing string) (expr string, omit bool, reason string) {
	want := strings.TrimSuffix(p.Type, "?")
	nullable := strings.HasSuffix(p.Type, "?")
	fn, reason := g.decoderFor(typ, want)
	if reason != "" {
		return "", false, reason
	}
	call := decodeCall(env, fn, value)
	switch {
	case def != nil:
		d, reason := g.defaultValue(typ, fn, def)
		if reason != "" {
			return "", false, reason
		}
		return call + " ?? " + d, false, ""
	case nullable:
		return call, false, ""
	case required || p.Required:
		return call + " ?? " + missing, false, ""
	case p.Default != "" && !strings.Contains(p.Default, "_"):
		return call + " ?? " + qualifyDefault(p.Default, g.class), false, ""
	}
	return call, true, ""
}

// argument is one argument of a constructor call.
type argument struct {
	param registry.Parameter
	expr  string
	omit  bool // left out while null; expr is bound to a local
}

// maxOmitted bounds the arguments left out while null: each doubles the
// branches of the call.
const maxOmitted = 3

// renderCall renders a constructor call with args in parameter order; an
// omitted argument is bound to a local first and the call branches on it.
// It returns the local declarations and the call expression.
func renderCall(ctor string, params []registry.Parameter, args map[string]argument, indent string) (locals []string, call string, reason string) {
	c := callShape{ctor: ctor, params: params, args: args, indent: indent}
	for _, p := range params {
		if a, ok := args[p.Name]; ok && a.omit {
			c.omitted = append(c.omitted, p.Name)
		}
	}
	if len(c.omitted) > maxOmitted {
		return nil, "", fmt.Sprintf("%d parameters with private defaults", len(c.omitted))
	}
	for i, name := range c.omitted {
		locals = append(locals, fmt.Sprintf("final o%d = %s;", i, args[name].expr))
	}
	return locals, c.branch(0, map[string]bool{}), ""
}

// callShape is a constructor call being rendered.
type callShape struct {
	ctor    string
	params  []registry.Parameter
	args    map[string]argument
	indent  string
	omitted []string
}

// branch renders the call for the omitted arguments from k on: a
// conditional on whether each is null.
func (c *callShape) branch(k int, present map[string]bool) string {
	if k < len(c.omitted) {
		with := maps.Clone(present)
		with[c.omitted[k]] = true
		return fmt.Sprintf("o%d == null\n%s    ? %s\n%s    : %s", k, c.indent, c.branch(k+1, present), c.indent, c.branch(k+1, with))
	}
	var b strings.Builder
	b.WriteString(c.ctor + "(\n")
	for _, p := range c.params {
		a, ok := c.args[p.Name]
		if !ok || (a.omit && !present[p.Name]) {
			continue
		}
		expr := a.expr
		if a.omit {
			expr = fmt.Sprintf("o%d", slices.Index(c.omitted, p.Name))
		}
		if p.Named {
			fmt.Fprintf(&b, "%s  %s: %s,\n", c.indent, p.Name, expr)
		} else {
			fmt.Fprintf(&b, "%s  %s,\n", c.indent, expr)
		}
	}
	b.WriteString(c.indent + ")")
	return b.String()
}

// qualifyDefault prefixes a Flutter default that names a static member of
// the constructor's own class, which the snapshot records unqualified.
func qualifyDefault(def, class string) string {
	if class == "" || def == "" || strings.ContainsAny(def, ".()<>[]{} '\"") || dartReserved[def] {
		return def
	}
	// Flutter names top-level constants kName; a lower-case name otherwise
	// is a static member of the class.
	if len(def) > 1 && def[0] == 'k' && def[1] >= 'A' && def[1] <= 'Z' {
		return def
	}
	if r := def[0]; r >= 'a' && r <= 'z' {
		return class + "." + def
	}
	return def
}

// decodeCall reads value through fn: on a NodeContext, through its decode
// method, which reports values that cannot be decoded (PLX-4002).
func decodeCall(env, fn, value string) string {
	if env == "c" {
		id := strings.TrimSuffix(strings.TrimPrefix(value, "c.prop("), ")")
		return fmt.Sprintf("c.decode(%s, %s)", id, fn)
	}
	return fmt.Sprintf("%s(%s, %s)", fn, env, value)
}

// decoderFor returns the Dart decoder of Plux type typ producing a value
// assignable to Dart type want.
func (g *renderGen) decoderFor(typ, want string) (string, string) {
	typ = strings.TrimSuffix(typ, "?")
	if typ == "IconData" && want == "IconData" {
		return "", "an icon is a glyph of the release's icon font, which a constant IconData cannot name (THM-005)"
	}
	if a, ok := adapters[[2]string{typ, want}]; ok {
		return a, ""
	}
	if s, ok := scalarDecoders[typ]; ok {
		if s[1] == want {
			return s[0], ""
		}
		return "", "a " + typ + " cannot be a " + want
	}
	if strings.HasPrefix(typ, "list<") && strings.HasSuffix(typ, ">") {
		elem := typ[len("list<") : len(typ)-1]
		dt, ok := g.decoders[elem]
		if !ok || !(want == "List<"+dt+">" || slices.ContainsFunc(supertypes[dt], func(s string) bool { return want == "List<"+s+">" })) {
			return "", "a " + typ + " cannot be a " + want
		}
		name := "listOf" + elem
		if _, ok := g.lists[elem]; !ok {
			g.lists[elem] = fmt.Sprintf("/// A list of %s.\nList<%s>? %s(Decoding d, Object? v) => asList(d, v, decode%s);\n\n", elem, dt, name, elem)
		}
		return name, ""
	}
	dt, ok := g.decoders[typ]
	if !ok {
		return "", "no decoder for " + typ
	}
	if dt != want && !slices.Contains(supertypes[dt], want) {
		return "", "a " + typ + " (" + dt + ") cannot be a " + want
	}
	return "decode" + typ, ""
}

// defaultValue returns a Dart expression for a descriptor default.
func (g *renderGen) defaultValue(typ, fn string, def json.RawMessage) (string, string) {
	var v any
	if err := json.Unmarshal(def, &v); err != nil {
		return "", "default " + string(def) + " is not JSON"
	}
	switch typ {
	case "bool":
		return fmt.Sprint(v), ""
	case "int":
		return strconv.FormatFloat(v.(float64), 'f', -1, 64), ""
	case "double":
		s := strconv.FormatFloat(v.(float64), 'f', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s, ""
	case "string":
		return quoteDart(v.(string)), ""
	}
	if e, ok := g.enums[typ]; ok {
		name, _ := v.(string)
		if e.Flutter != nil && fn == "decode"+typ {
			return e.Flutter.Enum + "." + dartMember(name), ""
		}
		return g.topLevelDefault(fn, quoteDart(name)), ""
	}
	if t, ok := g.types[typ]; ok {
		if name, isName := v.(string); isName {
			for _, k := range t.Constants {
				if k.Name == name {
					_ = json.Unmarshal(k.Value, &v)
				}
			}
		}
		return g.topLevelDefault(fn, dartLiteral(v)), ""
	}
	return "", "no default form for " + typ
}

// topLevelDefault declares a default value decoded once and returns its
// name.
func (g *renderGen) topLevelDefault(fn, literal string) string {
	expr := fmt.Sprintf("%s(plainDecoding, %s)!", fn, literal)
	if name, ok := g.named[expr]; ok {
		return name
	}
	name := fmt.Sprintf("_default%d", len(g.defaults))
	g.named[expr] = name
	g.defaults = append(g.defaults, fmt.Sprintf("final %s = %s;\n", name, expr))
	return name
}

// dartLiteral renders a decoded JSON value as a const Dart literal.
func dartLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprint(x)
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s
	case string:
		return quoteDart(x)
	case []any:
		items := make([]string, len(x))
		for i, it := range x {
			items[i] = dartLiteral(it)
		}
		return "const <Object?>[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := make([]string, len(keys))
		for i, k := range keys {
			items[i] = quoteDart(k) + ": " + strings.TrimPrefix(dartLiteral(x[k]), "const ")
		}
		return "const <String, Object?>{" + strings.Join(items, ", ") + "}"
	}
	return "null"
}

// writeIDClass writes a class of permanent member IDs; nothing when there
// are no members.
func writeIDClass[T any](b *bytes.Buffer, class, what string, members []T, key func(T) (string, uint32)) {
	if len(members) == 0 {
		return
	}
	fmt.Fprintf(b, "/// Permanent IDs of the %s.\nabstract final class %s {\n", what, class)
	for _, m := range sortedByID(members, func(m T) uint32 { _, id := key(m); return id }) {
		name, id := key(m)
		fmt.Fprintf(b, "  /// %s.\n  static const int %s = %d;\n", name, dartMember(name), id)
	}
	b.WriteString("}\n\n")
}

// lowerFirst lower-cases the first letter of a type name.
func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

// quoteList renders strings as Dart list elements.
func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = quoteDart(s)
	}
	return strings.Join(q, ", ")
}
