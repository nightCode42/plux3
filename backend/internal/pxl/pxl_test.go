// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// Verifies: PXL-002.
func TestNewEnv(t *testing.T) {
	t.Parallel()
	env, err := NewEnv(EnvSpec{
		Types: map[string]TypeSpec{"Node": {Fields: map[string]string{"children": "list<Node>", "label": "string?"}}},
		Roots: map[string]string{"tree": "Node"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, _ := env.Root("tree")
	if tree.Kind() != KindObject || tree.Nullable() || tree.Named().Fields["children"].Elem().Named().Name != "Node" {
		t.Errorf("recursive type: %v", tree)
	}
	if typ, err := env.ParseType("map<string,RoundingMode>?"); err != nil || typ.String() != "map<string,RoundingMode>?" {
		t.Errorf("ParseType = %v, %v", typ, err)
	}
	now, _ := env.Root(NowRoot)
	if now.Kind() != KindDateTime {
		t.Errorf("now is %s", now)
	}
	bad := []EnvSpec{
		{Types: map[string]TypeSpec{"RoundingMode": {Enum: []string{"a"}}}},
		{Types: map[string]TypeSpec{"lower": {Enum: []string{"a"}}}},
		{Types: map[string]TypeSpec{"Both": {Enum: []string{"a"}, Fields: map[string]string{}}}},
		{Types: map[string]TypeSpec{"Bad": {Fields: map[string]string{"x": "Missing"}}}},
		{Roots: map[string]string{"now": "int"}},
		{Roots: map[string]string{"in": "int"}},
		{Roots: map[string]string{"x": "list<int"}},
	}
	for i, spec := range bad {
		if _, err := NewEnv(spec); err == nil {
			t.Errorf("spec %d accepted", i)
		}
	}
}

// Verifies: PXL-002.
func TestValuesRoundTripThroughJSON(t *testing.T) {
	t.Parallel()
	env, err := NewEnv(EnvSpec{Types: map[string]TypeSpec{
		"E": {Enum: []string{"a"}}, "O": {Fields: map[string]string{"x": "int", "y": "string?"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		typ, in string
		ok      bool
	}{
		{"int", `-9007199254740993`, true},
		{"int", `1.5`, false},
		{"double", `1e400`, false},
		{"double", `0.5`, true},
		{"bool", `true`, true},
		{"string", `"x"`, true},
		{"decimal", `"12.50"`, true},
		{"decimal", `12.5`, false},
		{"money", `{"amount": "1.00", "currency": "EUR"}`, true},
		{"money", `{"amount": "1", "currency": "XXX"}`, false},
		{"money", `{"amount": "1"}`, false},
		{"date", `"2024-02-29"`, true},
		{"date", `"2023-02-29"`, false},
		{"dateTime", `"2026-09-26T10:00:00.5-03:30"`, true},
		{"dateTime", `"2026-09-26 10:00:00Z"`, false},
		{"duration", `1500`, true}, // whole milliseconds, not negative (document-model.md §3)
		{"duration", `-1`, false},
		{"duration", `1.5`, false},
		{"duration", `"PT1S"`, false},
		{"color", `"#5B3DF5"`, true},
		{"color", `"#5B3DF5CC"`, true},
		{"color", `"red"`, false},
		{"asset", `"logo"`, true},
		{"route", `"home"`, true},
		{"E", `"a"`, true},
		{"E", `"b"`, false},
		{"O", `{"x": 1}`, true},
		{"O", `{"y": "a"}`, false},
		{"O", `{"x": 1, "z": 2}`, false},
		{"O", `[]`, false},
		{"list<int>", `[1, 2]`, true},
		{"list<int>", `[1, "2"]`, false},
		{"list<int>", `{}`, false},
		{"map<string,int>", `{"a": 1}`, true},
		{"map<string,int>", `{"a": null}`, false},
		{"int?", `null`, true},
		{"int", `null`, false},
	}
	for _, tt := range tests {
		typ, err := env.ParseType(tt.typ)
		if err != nil {
			t.Fatal(err)
		}
		var raw any
		if err := decodeJSON([]byte(tt.in), &raw); err != nil {
			t.Fatal(err)
		}
		v, err := FromJSON(typ, raw)
		if (err == nil) != tt.ok {
			t.Errorf("FromJSON(%s, %s) = %v, %v", tt.typ, tt.in, v, err)
			continue
		}
		if err != nil {
			continue
		}
		back, _ := json.Marshal(ToJSON(typ, v))
		var again any
		if err := decodeJSON(back, &again); err != nil {
			t.Fatal(err)
		}
		v2, err := FromJSON(typ, again)
		if err != nil || !equal(v, v2, new(int64)) {
			t.Errorf("%s %s does not round-trip: %s", tt.typ, tt.in, back)
		}
	}
}

// Verifies: PXL-003.
func TestDecodeRejectsMalformedPrograms(t *testing.T) {
	t.Parallel()
	env := propertyEnv(t)
	p, _, diags := Compile("n > 1 ? size(xs.map(v, v)) : 0", env, DefaultOptions(), plxerr.Location{})
	if p == nil {
		t.Fatal(diags)
	}
	good := p.Encode()
	if _, err := Decode(good); err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(q *Program)) []byte {
		q := *p
		q.Code = append([]byte(nil), p.Code...)
		f(&q)
		return q.Encode()
	}
	tests := map[string][]byte{
		"empty":              nil,
		"version":            append([]byte{2}, good[1:]...),
		"truncated":          good[:len(good)-1],
		"trailing data":      append(append([]byte(nil), good...), 0),
		"unknown opcode":     mutate(func(q *Program) { q.Code[0] = 0xFF }),
		"jump into operands": mutate(func(q *Program) { q.Code = append(q.Code, byte(OpJump), 1, 0, 0, 0) }),
		"local out of range": mutate(func(q *Program) { q.Locals = 0 }),
		"stack too deep":     mutate(func(q *Program) { q.MaxStack = len(q.Code) + 1 }),
		"no code":            mutate(func(q *Program) { q.Code = nil }),
		"constant index":     mutate(func(q *Program) { q.Code = []byte{byte(OpConst), 99, 0} }),
		"call argc":          mutate(func(q *Program) { q.Code = []byte{byte(OpPushNull), byte(OpCall), 1, 0, 2} }),
		"sort kind":          mutate(func(q *Program) { q.Code = []byte{byte(OpSortBy), 99} }),
		"root not a string":  mutate(func(q *Program) { q.Constants = []Value{int64(1)}; q.Code = []byte{byte(OpLoadRoot), 0, 0} }),
		"invalid UTF-8":      mutate(func(q *Program) { q.Result = "\xff" }),
	}
	for name, data := range tests {
		if _, err := Decode(data); !errors.Is(err, ErrInvalidProgram) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestMalformedCodeFailsCleanly runs verified but ill-typed code: the VM
// reports invalidProgram instead of panicking.
//
// Verifies: PXL-001.
func TestMalformedCodeFailsCleanly(t *testing.T) {
	t.Parallel()
	programs := map[string]*Program{
		"underflow":        {MaxStack: 1, Code: []byte{byte(OpAddInt)}},
		"wrong operand":    {MaxStack: 2, Code: []byte{byte(OpPushTrue), byte(OpPushTrue), byte(OpAddInt)}},
		"two results":      {MaxStack: 2, Code: []byte{byte(OpPushTrue), byte(OpPushTrue)}},
		"missing root":     {MaxStack: 3, Constants: []Value{"page"}, Code: []byte{byte(OpLoadRoot), 0, 0}},
		"compare types":    {MaxStack: 3, Constants: []Value{"a"}, Code: []byte{byte(OpPushTrue), byte(OpConst), 0, 0, byte(OpCmpString)}},
		"bad call args":    {MaxStack: 3, Code: []byte{byte(OpPushTrue), byte(OpCall), 1, 0, 1}},
		"no iterator":      {Locals: 2, MaxStack: 8, Code: []byte{byte(OpIterNext), 0, 1, 8, 0, 0, 0, byte(OpPushTrue)}},
		"stack overflow":   {MaxStack: 1, Code: []byte{byte(OpPushTrue), byte(OpPushTrue), byte(OpPop)}},
		"jump bool on int": {MaxStack: 3, Constants: []Value{int64(1)}, Code: []byte{byte(OpConst), 0, 0, byte(OpJumpIfTrue), 8, 0, 0, 0}},
	}
	for name, p := range programs {
		_, err := p.Eval(map[string]Value{}, DefaultOptions().Limits)
		var e *EvalError
		if !errors.As(err, &e) || (e.Kind != ErrorInvalidProgram && e.Kind != ErrorInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDisassembleAndErrors(t *testing.T) {
	t.Parallel()
	env := propertyEnv(t)
	p, _, _ := Compile("round(d, 2) > 1.5d ? \"yes\" : s ?? \"no\"", env, DefaultOptions(), plxerr.Location{})
	asm := strings.Join(p.Disassemble(), "\n")
	for _, want := range []string{"LOAD_ROOT 0 ; \"d\"", "CALL", "; round", "1.5d", "COALESCE_JUMP"} {
		if !strings.Contains(asm, want) {
			t.Errorf("disassembly lacks %q:\n%s", want, asm)
		}
	}
	if got := (&Program{Code: []byte{0xFF}}).Disassemble(); len(got) != 1 || !strings.Contains(got[0], "???") {
		t.Errorf("unknown opcode: %v", got)
	}
	if Opcode(0xFF).String() != "OP(255)" || ErrorKind(0xFF).String() != "error(255)" || OpAddInt.String() != "ADD_INT" {
		t.Error("names")
	}
	e := evalErr(ErrorOverflow, "x")
	if e.Error() != "pxl: overflow: x" {
		t.Errorf("Error() = %s", e.Error())
	}
	if describe(nil) != "null" || describe(0.5) != "0.5" || describe(decimal.New(5, 1)) != "0.5d" {
		t.Error("describe")
	}
}

// Verifies: PXL-001, CMP-004.
func TestCompileLimits(t *testing.T) {
	t.Parallel()
	env := propertyEnv(t)
	opts := DefaultOptions()
	opts.MaxLength, opts.MaxDepth = 10, 3
	for src, code := range map[string]plxerr.Code{
		"1 + 2 + 3 + 4 + 5": plxerr.PXLExpressionTooComplex,
		"((((1))))":         plxerr.PXLExpressionTooComplex,
		"\xff":              plxerr.PXLSyntaxError,
		"é @":               plxerr.PXLSyntaxError,
	} {
		_, _, diags := Compile(src, env, opts, plxerr.Location{Path: "/p"})
		if len(diags) != 1 || diags[0].Code != code {
			t.Errorf("%q: %v, want %s", src, diags, code)
		}
	}
	_, _, diags := Compile(`"é" @`, env, DefaultOptions(), plxerr.Location{})
	if r := diags[0].Range; r.Start != 4 || r.End != 5 {
		t.Errorf("ranges count code points: %+v", r)
	}
}

func TestAssignable(t *testing.T) {
	t.Parallel()
	env := propertyEnv(t)
	parse := func(s string) *Type {
		typ, err := env.ParseType(s)
		if err != nil {
			t.Fatal(err)
		}
		return typ
	}
	for _, tt := range []struct {
		from, to string
		want     bool
	}{
		{"int", "int", true},
		{"int", "int?", true},
		{"int?", "int", false},
		{"int", "double", false},
		{"list<int>", "list<int>", true},
		{"list<int>", "list<string>", false},
	} {
		if got := Assignable(parse(tt.from), parse(tt.to)); got != tt.want {
			t.Errorf("Assignable(%s, %s) = %t", tt.from, tt.to, got)
		}
	}
}
