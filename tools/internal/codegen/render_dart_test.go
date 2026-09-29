// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// TestRenderDartCoversEveryWidget checks that every widget of phase P3 has
// a generated builder or is listed as hand-written, exactly once, and that
// the hand-written ones are the widgets the generator cannot express.
//
// Verifies: WGT-002.
func TestRenderDartCoversEveryWidget(t *testing.T) {
	t.Parallel()
	r := loadRegistry(t)
	src, err := renderDart(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(src)
	section := func(start string) string {
		i := strings.Index(out, start)
		if i < 0 {
			t.Fatalf("no %q", start)
		}
		j := strings.Index(out[i:], "};")
		return out[i : i+j]
	}
	entry := regexp.MustCompile(`(?m)^  (\d+): `)
	ids := func(s string) []uint32 {
		var got []uint32
		for _, m := range entry.FindAllStringSubmatch(s, -1) {
			n, _ := strconv.ParseUint(m[1], 10, 32)
			got = append(got, uint32(n))
		}
		return got
	}
	generated := ids(section("const Map<int, NodeBuilder> generatedBuilders"))
	hand := ids(section("const Map<int, String> handWrittenBuilders"))
	var handNames []string
	for _, w := range r.Widgets {
		if w.Phase != "P3" {
			continue
		}
		inGen, inHand := slices.Contains(generated, w.ID), slices.Contains(hand, w.ID)
		if inGen == inHand {
			t.Errorf("%s: generated %v, hand-written %v", w.Type, inGen, inHand)
		}
		if inHand {
			handNames = append(handNames, w.Type)
		}
	}
	want := []string{
		"CupertinoSlidingSegmentedControl", "EmptyState", "ErrorState", "FloatingActionButton",
		"ForEach", "GridView", "If", "Image", "ListView", "Match", "OfflineBanner", "PageView",
		"Responsive", "SkeletonLoader", "SliverGrid", "SliverList", "Slot", "Transform",
	}
	if !slices.Equal(handNames, want) {
		t.Errorf("hand-written builders %v, want %v", handNames, want)
	}
	for _, name := range want {
		if !strings.Contains(out, "static const int "+dartMember(lowerFirst(name))+" = ") {
			t.Errorf("WidgetIds lacks %s", name)
		}
	}
	for _, typ := range []string{"EdgeInsets", "WidgetStateColor", "ShapeBorder"} {
		if !strings.Contains(out, "abstract final class "+typ+"Fields") {
			t.Errorf("no field IDs for %s", typ)
		}
	}
	again, err := renderDart(r)
	if err != nil || string(again) != out {
		t.Error("renderDart is not deterministic")
	}
}

// TestCallbackShapes covers the Flutter callback types an event wires.
func TestCallbackShapes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ   string
		arity int
		async bool
		ok    bool
	}{
		{"VoidCallback", 0, false, true},
		{"GestureTapCallback", 0, false, true},
		{"ValueChanged<bool?>", 1, false, true},
		{"void Function()", 0, false, true},
		{"void Function(bool)", 1, false, true},
		{"void Function(Object, StackTrace?)", 2, false, true},
		{"void Function(Map<String, int>, int)", 2, false, true},
		{"Future<void> Function()", 0, true, true},
		{"FutureOr<void> Function()", 0, true, true},
		{"bool Function(int)", 0, false, false},
		{"DragUpdateCallback", 0, false, false},
	} {
		arity, async, ok := callbackShape(c.typ)
		if arity != c.arity || async != c.async || ok != c.ok {
			t.Errorf("%s: %d %v %v", c.typ, arity, async, ok)
		}
	}
	got, reason := eventArg(registry.Event{ID: 2}, registry.Parameter{Type: "void Function(Object, StackTrace?)?"})
	if reason != "" || got != "c.handles(2) ? (v, _) => c.fire(2, v) : null" {
		t.Errorf("eventArg: %q %q", got, reason)
	}
	got, _ = eventArg(registry.Event{ID: 3}, registry.Parameter{Type: "Future<void> Function()?"})
	if got != "c.handles(3) ? () async => c.fire(3) : null" {
		t.Errorf("async eventArg: %q", got)
	}
	if _, reason := eventArg(registry.Event{ID: 1}, registry.Parameter{Type: "DragUpdateCallback?"}); reason == "" {
		t.Error("an unknown callback typedef must be written by hand")
	}
}

// TestRenderHelpers covers the literal, default and type helpers.
func TestRenderHelpers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"strokeAlignInside":                   "BorderSide.strokeAlignInside",
		"kDefaultTrackpadScrollToScaleFactor": "kDefaultTrackpadScrollToScaleFactor",
		"double.infinity":                     "double.infinity",
		"const CircleBorder()":                "const CircleBorder()",
		"null":                                "null",
		"Icons":                               "Icons",
	} {
		if got := qualifyDefault(in, "BorderSide"); got != want {
			t.Errorf("qualifyDefault(%q) = %q, want %q", in, got, want)
		}
	}
	if got := qualifyDefault("x", ""); got != "x" {
		t.Errorf("no class: %q", got)
	}
	lit := dartLiteral(map[string]any{"b": []any{1.0, "s", true, nil}, "a": 2.5})
	if lit != "const <String, Object?>{'a': 2.5, 'b': <Object?>[1.0, 's', true, null]}" {
		t.Errorf("dartLiteral: %s", lit)
	}
	if dartLiteral(struct{}{}) != "null" {
		t.Error("an unknown value is null")
	}
	if got := substituteT("Map<T, Widget> Function(T?)"); got != "Map<String, Widget> Function(String?)" {
		t.Errorf("substituteT: %s", got)
	}
	if dartMember("if") != "if_" || dartMember("start") != "start" {
		t.Error("dartMember")
	}
	params := []registry.Parameter{{Name: "a", Named: true}, {Name: "b", Named: false}, {Name: "c", Named: true}}
	locals, call, reason := renderCall("W", params, map[string]argument{
		"a": {expr: "x", omit: true},
		"b": {expr: "y"},
	}, "  ")
	if reason != "" || len(locals) != 1 || !strings.Contains(call, "o0 == null") || !strings.Contains(call, "a: o0") {
		t.Errorf("renderCall: %v %s %s", locals, call, reason)
	}
	many := map[string]argument{}
	var ps []registry.Parameter
	for _, n := range []string{"p", "q", "r", "s"} {
		ps = append(ps, registry.Parameter{Name: n, Named: true})
		many[n] = argument{expr: n, omit: true}
	}
	if _, _, reason := renderCall("W", ps, many, ""); reason == "" {
		t.Error("more than three omitted arguments must be written by hand")
	}
}

// TestDecoderChoice covers the decoders chosen for a Plux type and a
// Flutter parameter type.
func TestDecoderChoice(t *testing.T) {
	t.Parallel()
	g := &renderGen{decoders: map[string]string{"EdgeInsets": "EdgeInsetsGeometry", "BoxDecoration": "BoxDecoration", "Shadow": "Shadow"}, lists: map[string]string{}}
	for _, c := range []struct{ typ, want, fn string }{
		{"double", "double", "asDouble"},
		{"string?", "String", "asString"},
		{"EdgeInsets", "EdgeInsetsGeometry", "decodeEdgeInsets"},
		{"EdgeInsets", "EdgeInsets", "decodeEdgeInsetsResolved"},
		{"BoxDecoration", "Decoration", "decodeBoxDecoration"},
		{"list<Shadow>", "List<Shadow>", "listOfShadow"},
		{"list<string>", "Set<String>", "asStringSet"},
	} {
		fn, reason := g.decoderFor(c.typ, c.want)
		if fn != c.fn || reason != "" {
			t.Errorf("%s as %s: %q %q", c.typ, c.want, fn, reason)
		}
	}
	for _, c := range [][2]string{{"double", "SliverGridDelegate"}, {"list<Shadow>", "int"}, {"Unknown", "X"}, {"EdgeInsets", "int"}} {
		if _, reason := g.decoderFor(c[0], c[1]); reason == "" {
			t.Errorf("%s as %s must be rejected", c[0], c[1])
		}
	}
	if !strings.Contains(g.lists["Shadow"], "List<Shadow>? listOfShadow(") {
		t.Errorf("list decoder: %q", g.lists["Shadow"])
	}
}
