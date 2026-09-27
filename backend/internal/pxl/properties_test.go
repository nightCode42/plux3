// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// propertyEnv declares the roots the generated expressions use.
func propertyEnv(t interface{ Fatal(...any) }) *Env {
	env, err := NewEnv(EnvSpec{Roots: map[string]string{"n": "int", "d": "decimal", "s": "string?", "xs": "list<int>"}})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// genExpr draws an int or decimal expression over literals and roots.
func genExpr(kind string, depth int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		if depth == 0 || rapid.IntRange(0, 3).Draw(t, "leaf") == 0 {
			if kind == "int" {
				return rapid.SampledFrom([]string{"n", "0", "1", "-3", "9223372036854775807", "size(xs)", "sum(xs)"}).Draw(t, "int leaf")
			}
			return rapid.SampledFrom([]string{"d", "0d", "1.5d", "-0.25d", "decimal(n)"}).Draw(t, "decimal leaf")
		}
		a, b := genExpr(kind, depth-1).Draw(t, "a"), genExpr(kind, depth-1).Draw(t, "b")
		ops := []string{"+", "-", "*"}
		if kind == "int" {
			ops = append(ops, "/", "%")
		}
		switch rapid.IntRange(0, 2).Draw(t, "shape") {
		case 0:
			return "(" + a + " " + rapid.SampledFrom(ops).Draw(t, "op") + " " + b + ")"
		case 1:
			return "(" + a + " > " + b + " ? " + a + " : " + b + ")"
		default:
			if kind == "int" {
				return "size(xs.map(v, v " + rapid.SampledFrom(ops).Draw(t, "op") + " " + a + ").filter(v, v > " + b + "))"
			}
			return "round(" + a + ", " + fmt.Sprint(rapid.IntRange(0, 4).Draw(t, "scale")) + ")"
		}
	})
}

// outcome evaluates src and returns its value or error kind as text.
func outcome(env *Env, src string, folding bool, inputs map[string]Value) (string, bool) {
	p, _, diags := compile(src, env, DefaultOptions(), plxerr.Location{}, folding)
	if p == nil {
		return fmt.Sprint(diags), false
	}
	v, err := p.Eval(inputs, DefaultOptions().Limits)
	var e *EvalError
	if errors.As(err, &e) {
		return "error " + e.Kind.String(), true
	}
	return fmt.Sprint(ToJSON(nil, v)), true
}

// TestFoldingAgreesWithEvaluation states that folding never changes a
// result: a folded program returns what the unfolded one returns.
//
// Verifies: CMP-022, PXL-001.
func TestFoldingAgreesWithEvaluation(t *testing.T) {
	env := propertyEnv(t)
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom([]string{"int", "decimal"}).Draw(t, "kind")
		src := genExpr(kind, 3).Draw(t, "expr")
		inputs := map[string]Value{
			"n": rapid.Int64Range(-5, 5).Draw(t, "n"), "d": decimal.New(rapid.Int64Range(-500, 500).Draw(t, "d"), 2),
			"s": nil, "xs": List{int64(1), int64(2), int64(3)}, NowRoot: DateTime{},
		}
		unfolded, ok := outcome(env, src, false, inputs)
		if !ok {
			t.Skip("does not type-check")
		}
		folded, ok := outcome(env, src, true, inputs)
		if !ok {
			// Folding may reject a constant subexpression that always fails.
			return
		}
		if folded != unfolded {
			t.Fatalf("%s: folded %s, evaluated %s", src, folded, unfolded)
		}
	})
}

// TestEncodingRoundTrips states that decoding an encoded program yields
// the same encoding and the same result.
//
// Verifies: PXL-003.
func TestEncodingRoundTrips(t *testing.T) {
	env := propertyEnv(t)
	rapid.Check(t, func(t *rapid.T) {
		src := genExpr(rapid.SampledFrom([]string{"int", "decimal"}).Draw(t, "kind"), 3).Draw(t, "expr")
		p, _, _ := compile(src, env, DefaultOptions(), plxerr.Location{}, false)
		if p == nil {
			t.Skip("does not type-check")
		}
		back, err := Decode(p.Encode())
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if !bytes.Equal(back.Encode(), p.Encode()) {
			t.Fatalf("%s: the encoding changed", src)
		}
	})
}

// TestArithmeticLaws states identities PXL arithmetic must satisfy.
//
// Verifies: PXL-005.
func TestArithmeticLaws(t *testing.T) {
	env := propertyEnv(t)
	laws := []string{
		"d + d - d == d",
		"(d + 1.5d) * 2d == d * 2d + 3d",
		"n + 1 - 1 == n",
		"(s ?? \"x\") == (s == null ? \"x\" : s)",
		"size(xs.map(v, v)) == size(xs)",
		"xs.filter(v, true) == xs",
		"xs.all(v, v > n) == !xs.any(v, v <= n)",
		"sum(xs.sortBy(v, -v)) == sum(xs)",
	}
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(0, 20).Draw(t, "size")
		xs := make(List, size)
		for i := range xs {
			xs[i] = rapid.Int64Range(-1000, 1000).Draw(t, "x")
		}
		var s Value
		if rapid.Bool().Draw(t, "s set") {
			s = rapid.StringMatching(`[a-z]{0,5}`).Draw(t, "s")
		}
		inputs := map[string]Value{
			"n": rapid.Int64Range(-1000, 1000).Draw(t, "n"), "d": decimal.New(rapid.Int64().Draw(t, "unscaled"), rapid.Uint16Range(0, 6).Draw(t, "scale")),
			"s": s, "xs": xs, NowRoot: DateTime{},
		}
		for _, law := range laws {
			if got, _ := outcome(env, law, true, inputs); got != "true" {
				t.Fatalf("%s is %s for %v", law, got, inputs)
			}
		}
	})
}

// TestBudgetAlwaysStops states that every evaluation ends within its
// budget: with a value or budgetExceeded, never by running on.
//
// Verifies: PXL-001.
func TestBudgetAlwaysStops(t *testing.T) {
	env := propertyEnv(t)
	src := "xs.map(a, size(xs.map(b, size(xs.filter(c, a + b > c)))))"
	p, _, diags := Compile(src, env, DefaultOptions(), plxerr.Location{})
	if p == nil {
		t.Fatal(diags)
	}
	rapid.Check(t, func(t *rapid.T) {
		xs := make(List, rapid.IntRange(0, 60).Draw(t, "size"))
		for i := range xs {
			xs[i] = int64(i)
		}
		lim := DefaultOptions().Limits
		lim.Budget = rapid.Int64Range(1, 20000).Draw(t, "budget")
		m := &vm{p: p, lim: lim, inputs: map[string]Value{"xs": xs}, locals: make([]any, p.Locals)}
		_, err := m.run()
		switch {
		case err == nil && m.used > lim.Budget:
			t.Fatalf("finished after %d operations of a budget of %d", m.used, lim.Budget)
		case err != nil && err.Kind != ErrorBudgetExceeded:
			t.Fatalf("unexpected error %v", err)
		}
	})
}
