// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"encoding/json"
	"testing"

	"pgregory.net/rapid"

	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// parse reads a JSON value the way the server does.
func parse(t testing.TB, s string) any {
	t.Helper()
	v, err := jcs.Parse([]byte(s), 64)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}

// canon renders a value canonically.
func canon(t testing.TB, v any) string {
	t.Helper()
	b, err := jcs.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Verifies: SRV-030.
// The examples of RFC 6902 Appendix A, and the ways a patch must fail.
func TestApplyFollowsRFC6902(t *testing.T) {
	t.Parallel()
	cases := []struct{ doc, patch, want string }{
		{`{"foo":"bar"}`, `[{"op":"add","path":"/baz","value":"qux"}]`, `{"baz":"qux","foo":"bar"}`},
		{`{"foo":["bar","baz"]}`, `[{"op":"add","path":"/foo/1","value":"qux"}]`, `{"foo":["bar","qux","baz"]}`},
		{`{"baz":"qux","foo":"bar"}`, `[{"op":"remove","path":"/baz"}]`, `{"foo":"bar"}`},
		{`{"foo":["bar","qux","baz"]}`, `[{"op":"remove","path":"/foo/1"}]`, `{"foo":["bar","baz"]}`},
		{`{"baz":"qux","foo":"bar"}`, `[{"op":"replace","path":"/baz","value":"boo"}]`, `{"baz":"boo","foo":"bar"}`},
		{`{"foo":{"bar":"baz","waldo":"fred"},"qux":{"corge":"grault"}}`, `[{"op":"move","from":"/foo/waldo","path":"/qux/thud"}]`, `{"foo":{"bar":"baz"},"qux":{"corge":"grault","thud":"fred"}}`},
		{`{"foo":["all","grass","cows","eat"]}`, `[{"op":"move","from":"/foo/1","path":"/foo/3"}]`, `{"foo":["all","cows","eat","grass"]}`},
		{`{"baz":"qux","foo":["a",2,"c"]}`, `[{"op":"test","path":"/baz","value":"qux"},{"op":"test","path":"/foo/1","value":2.0}]`, `{"baz":"qux","foo":["a",2,"c"]}`},
		{`{"foo":"bar"}`, `[{"op":"add","path":"/child","value":{"grandchild":{}}}]`, `{"child":{"grandchild":{}},"foo":"bar"}`},
		{`{"foo":["bar"]}`, `[{"op":"add","path":"/foo/-","value":["abc","def"]}]`, `{"foo":["bar",["abc","def"]]}`},
		{`{"/":1,"~":2}`, `[{"op":"copy","from":"/~1","path":"/~0x"}]`, `{"/":1,"~":2,"~x":1}`},
		{`{"a":1}`, `[{"op":"replace","path":"","value":[1]}]`, `[1]`},
		{`{"a":{"b":1}}`, `[{"op":"move","from":"/a","path":"/a"}]`, `{"a":{"b":1}}`},
	}
	for _, c := range cases {
		got, err := Apply(parse(t, c.doc), ops(t, c.patch))
		if err != nil {
			t.Errorf("%s + %s: %v", c.doc, c.patch, err)
			continue
		}
		if canon(t, got) != canon(t, parse(t, c.want)) {
			t.Errorf("%s + %s = %s; want %s", c.doc, c.patch, canon(t, got), c.want)
		}
	}
	for _, bad := range []struct{ doc, patch string }{
		{`{"baz":"qux"}`, `[{"op":"test","path":"/baz","value":"bar"}]`},
		{`{"foo":"bar"}`, `[{"op":"add","path":"/baz/bat","value":"qux"}]`},
		{`{"foo":"bar"}`, `[{"op":"remove","path":"/nope"}]`},
		{`{"foo":[1]}`, `[{"op":"remove","path":"/foo/01"}]`},
		{`{"foo":[1]}`, `[{"op":"add","path":"/foo/5","value":1}]`},
		{`{"foo":[1]}`, `[{"op":"replace","path":"/foo/-","value":1}]`},
		{`{"a":{"b":1}}`, `[{"op":"move","from":"/a","path":"/a/b/c"}]`},
		{`{"a":1}`, `[{"op":"frobnicate","path":"/a"}]`},
		{`{"a":1}`, `[{"op":"remove","path":""}]`},
		{`{"a":1}`, `[{"op":"add","path":"a","value":1}]`},
		{`{"a":"x"}`, `[{"op":"add","path":"/a/b","value":1}]`},
		{`{"a":1}`, `[{"op":"copy","from":"/nope","path":"/b"}]`},
	} {
		doc := parse(t, bad.doc)
		if _, err := Apply(doc, ops(t, bad.patch)); err == nil {
			t.Errorf("%s + %s was applied", bad.doc, bad.patch)
		}
		if canon(t, doc) != canon(t, parse(t, bad.doc)) {
			t.Errorf("a failed patch changed its input: %s", canon(t, doc))
		}
	}
}

// ops parses a patch written as JSON.
func ops(t testing.TB, s string) []Op {
	t.Helper()
	var raw []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		From  string          `json:"from"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatal(err)
	}
	out := make([]Op, len(raw))
	for i, r := range raw {
		out[i] = Op{Op: r.Op, Path: r.Path, From: r.From}
		if r.Value != nil {
			out[i].Value = parse(t, string(r.Value))
		}
	}
	return out
}

// Verifies: SRV-031.
// Diff produces a patch that turns one document into the other, for any
// two documents.
func TestDiffRoundTrips(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(r *rapid.T) {
		a, b := value(r, 3), value(r, 3)
		got, err := Apply(a, Diff(a, b))
		if err != nil {
			r.Fatalf("applying the diff: %v", err)
		}
		if !equal(got, b) {
			r.Fatalf("the diff of two documents did not turn one into the other")
		}
		if len(Diff(a, clone(a))) != 0 {
			r.Fatal("a document differs from its copy")
		}
	})
}

// value draws a JSON value of bounded depth.
func value(r *rapid.T, depth int) any {
	kinds := 4
	if depth > 0 {
		kinds = 6
	}
	switch rapid.IntRange(0, kinds-1).Draw(r, "kind") {
	case 0:
		return nil
	case 1:
		return rapid.Bool().Draw(r, "bool")
	case 2:
		return json.Number(rapid.SampledFrom([]string{"0", "1", "-2", "3.5"}).Draw(r, "number"))
	case 3:
		return rapid.SampledFrom([]string{"", "a", "b/c", "~d"}).Draw(r, "string")
	case 4:
		n := rapid.IntRange(0, 3).Draw(r, "len")
		out := make([]any, n)
		for i := range out {
			out[i] = value(r, depth-1)
		}
		return out
	default:
		out := map[string]any{}
		for _, k := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "b", "c/d", "~e"}), 0, 3, rapid.ID[string]).Draw(r, "keys") {
			out[k] = value(r, depth-1)
		}
		return out
	}
}
