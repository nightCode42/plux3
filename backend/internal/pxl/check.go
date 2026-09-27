// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// irKind classifies a typed IR node.
type irKind uint8

// IR node kinds.
const (
	irConst    irKind = iota + 1 // val
	irRoot                       // name
	irLocal                      // slot
	irAccess                     // args[0], op (GET_FIELD or MAP_GET), name
	irSafe                       // args[0], op, name: null propagates
	irOp                         // op applied to args
	irCmp                        // args[0], args[1] compared by op, then op2 (LT…)
	irAnd                        // args[0] && args[1]
	irOr                         // args[0] || args[1]
	irCoalesce                   // args[0] ?? args[1]
	irCond                       // args[0] ? args[1] : args[2]
	irCall                       // overload id, args
	irList                       // args
	irMap                        // keys, args
	irMacro                      // macro, args[0] receiver, args[1] body, slot iterator, slot2 variable
)

// ir is a node of the typed intermediate representation.
type ir struct {
	kind   irKind
	typ    *Type
	span   Span
	op     Opcode
	op2    Opcode
	name   string
	val    Value
	id     int
	args   []*ir
	keys   []*ir
	slot   int
	slot2  int
	macro  string
	cmp    CmpKind
	noFold bool // a call of a later-phase group: never folded
}

// diagnostic is a checker finding at a byte span.
type diagnostic struct {
	code plxerr.Code
	span Span
	msg  string
}

// scopeVar is a macro variable in scope.
type scopeVar struct {
	slot int
	typ  *Type
}

// checker type-checks a syntax tree against an environment.
type checker struct {
	env      *Env
	diags    []diagnostic
	scopes   []map[string]scopeVar
	next     int // next free local slot
	locals   int // number of local slots used
	features map[string]bool
	// nonNull holds the paths a guard has checked are not null where the
	// expression being checked runs (narrowing, ADR-0009).
	nonNull map[string]bool
}

// maxLocals is the number of local slots an operand byte addresses.
const maxLocals = 256

func (c *checker) errorf(code plxerr.Code, span Span, format string, args ...any) *ir {
	c.diags = append(c.diags, diagnostic{code: code, span: span, msg: fmt.Sprintf(format, args...)})
	return nil
}

// check types n; it returns nil after recording a diagnostic.
func (c *checker) check(n *node) *ir {
	x := c.checkNode(n)
	if x != nil && x.typ.nullable && (n.kind == nIdent || n.kind == nField || n.kind == nSafe) {
		if p, ok := syntaxPath(n); ok && c.nonNull[p] {
			narrowed := *x
			narrowed.typ = x.typ.nonNull()
			return &narrowed
		}
	}
	return x
}

// checkNode types n by its kind.
func (c *checker) checkNode(n *node) *ir {
	switch n.kind {
	case nLiteral:
		return c.literal(n)
	case nIdent:
		return c.ident(n)
	case nUnary:
		return c.unary(n)
	case nBinary:
		return c.binary(n)
	case nCond:
		return c.cond(n)
	case nField, nSafe:
		return c.access(n)
	case nIndex:
		return c.index(n)
	case nCall:
		return c.call(n)
	case nMacro:
		return c.macro(n)
	case nList:
		return c.list(n)
	default:
		return c.mapLiteral(n)
	}
}

// constant returns a constant node.
func constant(v Value, t *Type, span Span) *ir { return &ir{kind: irConst, typ: t, val: v, span: span} }

// literal types a literal token.
func (c *checker) literal(n *node) *ir {
	t := n.tok
	switch t.kind {
	case tokInt:
		v, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return c.errorf(plxerr.PXLInvalidLiteral, n.span, "integer %s is outside the 64-bit range", t.text)
		}
		return constant(v, typeInt, n.span)
	case tokDouble:
		v, err := strconv.ParseFloat(t.text, 64)
		if err != nil || math.IsInf(v, 0) {
			return c.errorf(plxerr.PXLInvalidLiteral, n.span, "number %s is outside the double range", t.text)
		}
		return constant(v, typeDouble, n.span)
	case tokDecimal:
		d, err := decimal.Parse(t.text)
		if err != nil {
			return c.errorf(plxerr.PXLInvalidLiteral, n.span, "invalid decimal %s", t.text)
		}
		return constant(d, typeDecimal, n.span)
	case tokString:
		return constant(t.str, typeString, n.span)
	default:
		switch t.text {
		case "true":
			return constant(true, typeBool, n.span)
		case "false":
			return constant(false, typeBool, n.span)
		default:
			return constant(nil, typeNull, n.span)
		}
	}
}

// ident resolves a macro variable or a root.
func (c *checker) ident(n *node) *ir {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if v, ok := c.scopes[i][n.op]; ok {
			return &ir{kind: irLocal, typ: v.typ, slot: v.slot, span: n.span}
		}
	}
	if t, ok := c.env.Root(n.op); ok {
		return &ir{kind: irRoot, typ: t, name: n.op, span: n.span}
	}
	return c.errorf(plxerr.PXLUnknownIdentifier, n.span, "%q is not available here", n.op)
}

// unary types ! and -.
func (c *checker) unary(n *node) *ir {
	if n.op == "-" && n.a.kind == nLiteral && n.a.tok.kind == tokInt && n.a.tok.text == "9223372036854775808" {
		return constant(int64(math.MinInt64), typeInt, n.span)
	}
	a := c.check(n.a)
	if a == nil {
		return nil
	}
	if a.typ.nullable {
		return c.errorf(plxerr.PXLTypeMismatch, n.a.span, "the operand of %s may be null; provide a default with ??", n.op)
	}
	ops := map[Kind]Opcode{KindInt: OpNegInt, KindDouble: OpNegDouble, KindDecimal: OpNegDec, KindMoney: OpNegMoney, KindDuration: OpNegDur}
	if n.op == "!" {
		ops = map[Kind]Opcode{KindBool: OpNot}
	}
	op, ok := ops[a.typ.kind]
	if !ok {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "%s does not apply to %s", n.op, a.typ)
	}
	return &ir{kind: irOp, op: op, typ: a.typ, args: []*ir{a}, span: n.span}
}

// arithmetic lists the typed opcodes of the arithmetic operators by
// operator and operand kinds, with the result kind.
var arithmetic = map[string]map[[2]Kind]struct {
	op     Opcode
	result Kind
}{
	"+": {
		{KindInt, KindInt}: {OpAddInt, KindInt}, {KindDouble, KindDouble}: {OpAddDouble, KindDouble},
		{KindDecimal, KindDecimal}: {OpAddDec, KindDecimal}, {KindMoney, KindMoney}: {OpAddMoney, KindMoney},
		{KindString, KindString}: {OpConcatString, KindString}, {KindList, KindList}: {OpConcatList, KindList},
		{KindDateTime, KindDuration}: {OpAddDtDur, KindDateTime}, {KindDuration, KindDateTime}: {OpAddDurDt, KindDateTime},
		{KindDuration, KindDuration}: {OpAddDur, KindDuration},
	},
	"-": {
		{KindInt, KindInt}: {OpSubInt, KindInt}, {KindDouble, KindDouble}: {OpSubDouble, KindDouble},
		{KindDecimal, KindDecimal}: {OpSubDec, KindDecimal}, {KindMoney, KindMoney}: {OpSubMoney, KindMoney},
		{KindDateTime, KindDuration}: {OpSubDtDur, KindDateTime}, {KindDateTime, KindDateTime}: {OpSubDtDt, KindDuration},
		{KindDuration, KindDuration}: {OpSubDur, KindDuration},
	},
	"*": {
		{KindInt, KindInt}: {OpMulInt, KindInt}, {KindDouble, KindDouble}: {OpMulDouble, KindDouble},
		{KindDecimal, KindDecimal}: {OpMulDec, KindDecimal}, {KindMoney, KindDecimal}: {OpMulMoneyDec, KindMoney},
		{KindDecimal, KindMoney}: {OpMulDecMoney, KindMoney}, {KindDuration, KindInt}: {OpMulDurInt, KindDuration},
		{KindInt, KindDuration}: {OpMulIntDur, KindDuration},
	},
	"/": {{KindInt, KindInt}: {OpDivInt, KindInt}, {KindDouble, KindDouble}: {OpDivDouble, KindDouble}},
	"%": {{KindInt, KindInt}: {OpModInt, KindInt}, {KindDouble, KindDouble}: {OpModDouble, KindDouble}},
}

// binary types the binary operators.
func (c *checker) binary(n *node) *ir {
	a := c.check(n.a)
	var b *ir
	switch whenTrue, whenFalse := facts(n.a); n.op {
	case "&&":
		b = c.assuming(whenTrue, n.b)
	case "||":
		b = c.assuming(whenFalse, n.b)
	default:
		b = c.check(n.b)
	}
	if a == nil || b == nil {
		return nil
	}
	switch n.op {
	case "&&", "||":
		if !isBool(a.typ) || !isBool(b.typ) {
			return c.errorf(plxerr.PXLTypeMismatch, n.span, "%s needs two bools, not %s and %s", n.op, a.typ, b.typ)
		}
		kind := irAnd
		if n.op == "||" {
			kind = irOr
		}
		return &ir{kind: kind, typ: typeBool, args: []*ir{a, b}, span: n.span}
	case "??":
		return c.coalesce(a, b, n.span)
	case "==", "!=":
		return c.equality(n, a, b)
	case "<", "<=", ">", ">=":
		return c.order(n, a, b)
	case "in":
		return c.in(n, a, b)
	default:
		return c.arith(n, a, b)
	}
}

// isBool reports whether t is a non-null bool.
func isBool(t *Type) bool { return t.kind == KindBool && !t.nullable }

// widenPair converts an int operand to the double or decimal kind of the
// other operand.
func widenPair(a, b *ir) (*ir, *ir) {
	if a.typ.kind == KindInt && (b.typ.kind == KindDouble || b.typ.kind == KindDecimal) {
		return convert(a, b.typ.nonNull()), b
	}
	if b.typ.kind == KindInt && (a.typ.kind == KindDouble || a.typ.kind == KindDecimal) {
		return a, convert(b, a.typ.nonNull())
	}
	if a.typ.kind == KindMoney && b.typ.kind == KindInt {
		return a, convert(b, typeDecimal)
	}
	if a.typ.kind == KindInt && b.typ.kind == KindMoney {
		return convert(a, typeDecimal), b
	}
	return a, b
}

// convert widens x to type t when needed (int to double or decimal).
func convert(x *ir, t *Type) *ir {
	if x.typ.kind == t.kind || x.typ.kind == KindNull || x.typ.kind == KindNever {
		return x
	}
	if op, ok := widening(x.typ, t); ok {
		return &ir{kind: irOp, op: op, typ: &Type{kind: t.kind}, args: []*ir{x}, span: x.span}
	}
	return x
}

// arith types + - * / %.
func (c *checker) arith(n *node, a, b *ir) *ir {
	if a.typ.nullable || b.typ.nullable {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "an operand of %s may be null; provide a default with ??", n.op)
	}
	if n.op == "/" && (a.typ.kind == KindDecimal || b.typ.kind == KindDecimal || a.typ.kind == KindMoney) {
		return c.errorf(plxerr.PXLDecimalDivision, n.span, "decimal division must state a scale and rounding mode")
	}
	a, b = widenPair(a, b)
	entry, ok := arithmetic[n.op][[2]Kind{a.typ.kind, b.typ.kind}]
	if !ok {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "%s does not apply to %s and %s", n.op, a.typ, b.typ)
	}
	t := &Type{kind: entry.result}
	if entry.result == KindList {
		t = unify(a.typ, b.typ)
		if t == nil {
			return c.errorf(plxerr.PXLTypeMismatch, n.span, "cannot concatenate %s and %s", a.typ, b.typ)
		}
	}
	return &ir{kind: irOp, op: entry.op, typ: t, args: []*ir{a, b}, span: n.span}
}

// coalesce types a ?? b.
func (c *checker) coalesce(a, b *ir, span Span) *ir {
	t := unify(a.typ.nonNull(), b.typ)
	if t == nil {
		return c.errorf(plxerr.PXLTypeMismatch, span, "?? needs a default of type %s, not %s", a.typ.nonNull(), b.typ)
	}
	if !b.typ.nullable {
		t = t.nonNull()
	}
	return &ir{kind: irCoalesce, typ: t, args: []*ir{convert(a, t), convert(b, t)}, span: span}
}

// enumLiteral types a string constant compared with or passed as an enum.
func (c *checker) enumLiteral(x *ir, enum *Type) (*ir, bool) {
	s, ok := x.val.(string)
	if x.kind != irConst || !ok || x.typ.kind != KindString {
		return x, true
	}
	if !enum.named.hasMember(s) {
		c.errorf(plxerr.PXLUnknownEnumMember, x.span, "%q is not a member of %s: %s", s, enum.named.Name, strings.Join(enum.named.Members, ", "))
		return nil, false
	}
	return constant(s, enum.nonNull(), x.span), true
}

// equality types == and !=.
func (c *checker) equality(n *node, a, b *ir) *ir {
	var ok bool
	if a.typ.kind == KindEnum {
		if b, ok = c.enumLiteral(b, a.typ); !ok {
			return nil
		}
	}
	if b.typ.kind == KindEnum {
		if a, ok = c.enumLiteral(a, b.typ); !ok {
			return nil
		}
	}
	a, b = widenPair(a, b)
	if unify(a.typ, b.typ) == nil {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "cannot compare %s with %s", a.typ, b.typ)
	}
	op := OpEq
	if n.op == "!=" {
		op = OpNe
	}
	return &ir{kind: irOp, op: op, typ: typeBool, args: []*ir{a, b}, span: n.span}
}

// order types < <= > >=.
func (c *checker) order(n *node, a, b *ir) *ir {
	a, b = widenPair(a, b)
	if !a.typ.ordered() || !b.typ.Equal(a.typ) {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "%s does not order %s and %s", n.op, a.typ, b.typ)
	}
	tests := map[string]Opcode{"<": OpLt, "<=": OpLe, ">": OpGt, ">=": OpGe}
	cmps := map[CmpKind]Opcode{
		CmpInt: OpCmpInt, CmpDouble: OpCmpDouble, CmpDecimal: OpCmpDec, CmpMoney: OpCmpMoney,
		CmpString: OpCmpString, CmpDate: OpCmpDate, CmpDateTime: OpCmpDatetime, CmpDuration: OpCmpDur,
	}
	return &ir{kind: irCmp, op: cmps[a.typ.cmpKind()], op2: tests[n.op], typ: typeBool, args: []*ir{a, b}, span: n.span}
}

// in types `x in list` and `key in map`.
func (c *checker) in(n *node, a, b *ir) *ir {
	switch {
	case b.typ.nullable:
		return c.errorf(plxerr.PXLNullableAccess, n.b.span, "the right side of in may be null")
	case b.typ.kind == KindMap && a.typ.kind == KindString && !a.typ.nullable:
		return &ir{kind: irOp, op: OpInMap, typ: typeBool, args: []*ir{a, b}, span: n.span}
	case b.typ.kind == KindList:
		elem := b.typ.elem
		var ok bool
		if elem.kind == KindEnum {
			if a, ok = c.enumLiteral(a, elem); !ok {
				return nil
			}
		}
		a = convert(a, elem)
		if !assignable(a.typ, elem) && elem.kind != KindNever {
			return c.errorf(plxerr.PXLTypeMismatch, n.span, "a %s cannot be in a %s", a.typ, b.typ)
		}
		return &ir{kind: irOp, op: OpInList, typ: typeBool, args: []*ir{a, b}, span: n.span}
	default:
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "in needs a list, or a string and a map, not %s and %s", a.typ, b.typ)
	}
}

// cond types c ? a : b.
func (c *checker) cond(n *node) *ir {
	whenTrue, whenFalse := facts(n.a)
	cnd, a, b := c.check(n.a), c.assuming(whenTrue, n.b), c.assuming(whenFalse, n.c)
	if cnd == nil || a == nil || b == nil {
		return nil
	}
	if !isBool(cnd.typ) {
		return c.errorf(plxerr.PXLTypeMismatch, n.a.span, "the condition is a %s, not a bool", cnd.typ)
	}
	if a.typ.kind == KindEnum {
		if b, _ = c.enumLiteral(b, a.typ); b == nil {
			return nil
		}
	} else if b.typ.kind == KindEnum {
		if a, _ = c.enumLiteral(a, b.typ); a == nil {
			return nil
		}
	}
	t := unify(a.typ, b.typ)
	if t == nil {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "the branches have different types: %s and %s", a.typ, b.typ)
	}
	return &ir{kind: irCond, typ: t, args: []*ir{cnd, convert(a, t), convert(b, t)}, span: n.span}
}

// access types a.name and a?.name.
func (c *checker) access(n *node) *ir {
	a := c.check(n.a)
	if a == nil {
		return nil
	}
	if a.typ.nullable && n.kind == nField {
		return c.errorf(plxerr.PXLNullableAccess, n.span, "%s may be null; write ?.%s or provide a default with ??", a.typ, n.op)
	}
	base := a.typ.nonNull()
	var (
		t  *Type
		op Opcode
	)
	switch base.kind {
	case KindObject:
		t, op = base.named.Fields[n.op], OpGetField
		if t == nil {
			return c.errorf(plxerr.PXLUnknownField, n.span, "%s has no field %q", base.named.Name, n.op)
		}
	case KindMap:
		t, op = base.elem, OpMapGet
	default:
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "a %s has no fields", a.typ)
	}
	kind := irAccess
	if n.kind == nSafe {
		kind, t = irSafe, t.orNull()
	}
	return &ir{kind: kind, op: op, name: n.op, typ: t, args: []*ir{a}, span: n.span}
}

// index types a[b].
func (c *checker) index(n *node) *ir {
	a, b := c.check(n.a), c.check(n.b)
	if a == nil || b == nil {
		return nil
	}
	if a.typ.nullable {
		return c.errorf(plxerr.PXLNullableAccess, n.span, "%s may be null", a.typ)
	}
	switch {
	case a.typ.kind == KindList && b.typ.kind == KindInt && !b.typ.nullable:
		return &ir{kind: irOp, op: OpIndexList, typ: a.typ.elem, args: []*ir{a, b}, span: n.span}
	case a.typ.kind == KindMap && b.typ.kind == KindString && !b.typ.nullable:
		return &ir{kind: irOp, op: OpIndexMap, typ: a.typ.elem, args: []*ir{a, b}, span: n.span}
	default:
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "cannot index a %s with a %s", a.typ, b.typ)
	}
}

// list types a list literal.
func (c *checker) list(n *node) *ir {
	elem := typeNever
	items := make([]*ir, 0, len(n.args))
	for _, a := range n.args {
		x := c.check(a)
		if x == nil {
			return nil
		}
		if elem = unify(elem, x.typ); elem == nil {
			return c.errorf(plxerr.PXLTypeMismatch, a.span, "a list cannot hold both %s and earlier items", x.typ)
		}
		items = append(items, x)
	}
	for i, x := range items {
		items[i] = convert(x, elem)
	}
	return &ir{kind: irList, typ: listOf(elem), args: items, span: n.span}
}

// mapLiteral types a map literal.
func (c *checker) mapLiteral(n *node) *ir {
	elem := typeNever
	out := &ir{kind: irMap, span: n.span}
	seen := map[string]bool{}
	for i, kn := range n.keys {
		k, v := c.check(kn), c.check(n.args[i])
		if k == nil || v == nil {
			return nil
		}
		if k.typ.kind != KindString || k.typ.nullable {
			return c.errorf(plxerr.PXLTypeMismatch, kn.span, "map keys are strings, not %s", k.typ)
		}
		if s, ok := k.val.(string); k.kind == irConst && ok {
			if seen[s] {
				return c.errorf(plxerr.PXLInvalidLiteral, kn.span, "duplicate key %q", s)
			}
			seen[s] = true
		}
		if elem = unify(elem, v.typ); elem == nil {
			return c.errorf(plxerr.PXLTypeMismatch, n.args[i].span, "a map cannot hold both %s and earlier values", v.typ)
		}
		out.keys, out.args = append(out.keys, k), append(out.args, v)
	}
	for i, v := range out.args {
		out.args[i] = convert(v, elem)
	}
	out.typ = mapOf(elem)
	return out
}

// macro types list.map(x, e) and the other receiver macros.
func (c *checker) macro(n *node) *ir {
	known := false
	for _, m := range stdMacros {
		known = known || m == n.op
	}
	if !known {
		return c.errorf(plxerr.PXLUnknownFunction, n.span, "%q is not a macro; only map, filter, any, all and sortBy are called on a value", n.op)
	}
	recv := c.check(n.a)
	if recv == nil {
		return nil
	}
	if len(n.args) != 2 || n.args[0].kind != nIdent || recv.typ.kind != KindList || recv.typ.nullable {
		return c.errorf(plxerr.PXLInvalidMacro, n.span, "call %s on a list with a variable name and an expression, e.g. items.%s(x, …)", n.op, n.op)
	}
	name := n.args[0].op
	if _, isRoot := c.env.Root(name); isRoot || c.inScope(name) {
		return c.errorf(plxerr.PXLInvalidMacro, n.args[0].span, "the variable %q hides a root or an outer variable", name)
	}
	if c.next+2 > maxLocals {
		return c.errorf(plxerr.PXLExpressionTooComplex, n.span, "macros are nested too deeply")
	}
	iter, slot := c.next, c.next+1
	c.next += 2
	c.locals = max(c.locals, c.next)
	c.scopes = append(c.scopes, map[string]scopeVar{name: {slot: slot, typ: recv.typ.elem}})
	body := c.check(n.args[1])
	c.scopes = c.scopes[:len(c.scopes)-1]
	c.next -= 2
	if body == nil {
		return nil
	}
	m := &ir{kind: irMacro, macro: n.op, args: []*ir{recv, body}, slot: iter, slot2: slot, span: n.span}
	switch n.op {
	case "map":
		m.typ = listOf(body.typ)
	case "sortBy":
		if !body.typ.ordered() {
			return c.errorf(plxerr.PXLTypeMismatch, n.args[1].span, "sortBy needs a key of an ordered type, not %s", body.typ)
		}
		m.typ, m.cmp = recv.typ, body.typ.cmpKind()
	default:
		if !isBool(body.typ) {
			return c.errorf(plxerr.PXLTypeMismatch, n.args[1].span, "%s needs a bool predicate, not %s", n.op, body.typ)
		}
		m.typ = recv.typ
		if n.op != "filter" {
			m.typ = typeBool
		}
	}
	return m
}

// inScope reports whether a macro variable of that name is in scope.
func (c *checker) inScope(name string) bool {
	for _, s := range c.scopes {
		if _, ok := s[name]; ok {
			return true
		}
	}
	return false
}

// syntaxPath returns the path an identifier or a chain of field accesses
// names, such as "page.result.amount".
func syntaxPath(n *node) (string, bool) {
	switch n.kind {
	case nIdent:
		return n.op, true
	case nField, nSafe:
		p, ok := syntaxPath(n.a)
		return p + "." + n.op, ok
	default:
		return "", false
	}
}

// isNull reports whether n is the literal null.
func isNull(n *node) bool { return n.kind == nLiteral && n.tok.text == "null" }

// facts returns the paths that are not null when the condition n is true,
// and when it is false: p != null, p == null, and their combinations with
// !, && and ||.
func facts(n *node) (whenTrue, whenFalse []string) {
	switch {
	case n.kind == nBinary && (n.op == "!=" || n.op == "=="):
		p, ok := syntaxPath(n.a)
		if !ok || !isNull(n.b) {
			p, ok = syntaxPath(n.b)
			ok = ok && isNull(n.a)
		}
		if !ok {
			return nil, nil
		}
		if n.op == "!=" {
			return []string{p}, nil
		}
		return nil, []string{p}
	case n.kind == nBinary && n.op == "&&":
		a, _ := facts(n.a)
		b, _ := facts(n.b)
		return append(a, b...), nil
	case n.kind == nBinary && n.op == "||":
		_, a := facts(n.a)
		_, b := facts(n.b)
		return nil, append(a, b...)
	case n.kind == nUnary && n.op == "!":
		t, f := facts(n.a)
		return f, t
	default:
		return nil, nil
	}
}

// Guards returns the paths that are not null when the expression src is
// true and when it is false; the compiler narrows the slots of an If
// widget with them. A malformed expression has none.
func Guards(src string, opts Options) (whenTrue, whenFalse []string) {
	if int64(len(src)) > opts.MaxLength*4 {
		return nil, nil
	}
	tree, err := parse(src, int(opts.MaxDepth))
	if err != nil {
		return nil, nil
	}
	return facts(tree)
}

// assume records paths as not null, with every prefix: when a.b is not
// null, neither is a. It returns the paths it added.
func (c *checker) assume(paths []string) []string {
	var added []string
	for _, p := range paths {
		for {
			if !c.nonNull[p] {
				c.nonNull[p] = true
				added = append(added, p)
			}
			i := strings.LastIndexByte(p, '.')
			if i < 0 {
				break
			}
			p = p[:i]
		}
	}
	return added
}

// assuming checks n with paths known not to be null.
func (c *checker) assuming(paths []string, n *node) *ir {
	added := c.assume(paths)
	defer func() {
		for _, p := range added {
			delete(c.nonNull, p)
		}
	}()
	return c.check(n)
}
