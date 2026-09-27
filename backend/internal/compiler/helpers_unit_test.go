// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"pgregory.net/rapid"
)

// Verifies: SCH-010.
func TestParseTypeExpr(t *testing.T) {
	t.Parallel()
	for _, src := range []string{"int", "string?", "list<Item>", "map<string,list<int?>>?", "LoanSchedule", "T"} {
		te, err := parseTypeExpr(src)
		if err != nil || te.String() != src {
			t.Errorf("%q: %v, %v", src, te, err)
		}
	}
	for _, src := range []string{"", "list", "list<int", "map<int,string>", "int??", "1x", "list<>", "int string"} {
		if _, err := parseTypeExpr(src); !errors.Is(err, errTypeSyntax) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestTypeExprMatch(t *testing.T) {
	t.Parallel()
	parse := func(s string) *texpr {
		te, err := parseTypeExpr(s)
		if err != nil {
			t.Fatal(err)
		}
		return te
	}
	params := map[string]bool{"T": true}
	for _, tc := range []struct {
		pattern, actual, bound string
		ok                     bool
	}{
		{"list<T>", "list<Item>", "Item", true},
		{"list<T?>", "list<Item?>", "Item", true},
		{"T", "int?", "int?", true},
		{"list<T>", "map<string,Item>", "", false},
		{"list<T>", "int", "", false},
		{"int", "int", "", true},
	} {
		bind := map[string]*texpr{}
		ok := parse(tc.pattern).match(parse(tc.actual), params, bind)
		if ok != tc.ok || tc.bound != "" && bind["T"].String() != tc.bound {
			t.Errorf("%s ~ %s: %v %v", tc.pattern, tc.actual, ok, bind)
		}
	}
	bind := map[string]*texpr{"T": parse("int")}
	if parse("T").match(parse("string"), params, bind) {
		t.Error("a parameter bound twice to different types matched")
	}
	if got := parse("map<string,T?>").subst(map[string]*texpr{"T": parse("Item")}).String(); got != "map<string,Item?>" {
		t.Errorf("subst: %s", got)
	}
}

// Verifies: BND-015.
// Decimal unscaled values are the minimal big-endian two's complement.
func TestTwosComplement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   int64
		want []byte
	}{
		{0, nil},
		{1, []byte{1}},
		{127, []byte{0x7f}},
		{128, []byte{0, 0x80}},
		{255, []byte{0, 0xff}},
		{-1, []byte{0xff}},
		{-128, []byte{0x80}},
		{-129, []byte{0xff, 0x7f}},
		{-256, []byte{0xff, 0}},
	} {
		if got := twosComplement(big.NewInt(tc.in)); !bytes.Equal(got, tc.want) {
			t.Errorf("%d: % x, want % x", tc.in, got, tc.want)
		}
	}
	rapid.Check(t, func(rt *rapid.T) {
		x := big.NewInt(rapid.Int64().Draw(rt, "x"))
		x.Mul(x, big.NewInt(rapid.Int64().Draw(rt, "y")))
		b := twosComplement(x)
		back := new(big.Int).SetBytes(b)
		if len(b) > 0 && b[0]&0x80 != 0 {
			back.Sub(back, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
		}
		if back.Cmp(x) != 0 {
			rt.Fatalf("%v encodes as % x, which reads %v", x, b, back)
		}
		if len(b) > 1 && (b[0] == 0 && b[1]&0x80 == 0 || b[0] == 0xff && b[1]&0x80 != 0) {
			rt.Fatalf("% x is not minimal", b)
		}
	})
}

// Verifies: WGT-004.
func TestSemverLess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		less bool
	}{
		{"0.1.0", "0.1.0", false},
		{"0.0.9", "0.1.0", true},
		{"0.10.0", "0.9.0", false},
		{"1.0.0", "1.0.1", true},
		{"1.0", "1.0.1", true},
		{"2.0.0", "1.9.9", false},
	} {
		if got := semverLess(tc.a, tc.b); got != tc.less {
			t.Errorf("%s < %s = %v", tc.a, tc.b, got)
		}
	}
}

// Verifies: CMP-002, BND-014.
// Derived IDs are stable, distinct for distinct parts, and valid version
// 8 UUIDs.
func TestDerivedID(t *testing.T) {
	t.Parallel()
	a, b := derivedID("page", "onInit"), derivedID("page", "onInit")
	if a != b {
		t.Fatal("derived IDs are not stable")
	}
	if derivedID("page", "onInit") == derivedID("pageon", "Init") {
		t.Fatal("the parts are not separated")
	}
	if a[6]>>4 != 8 || a[8]>>6 != 2 {
		t.Errorf("not a version 8 UUID: %s", uuidString(a))
	}
}

func TestIsBindingRaw(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]bool{
		`{"$expr": "1"}`: true, `{"$token": "space.md"}`: true, `{"all": 8}`: false, `"text"`: false, `[1]`: false, `{`: false,
	} {
		if got := isBindingRaw(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
}
