// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Definitions of the generated tables (tables_gen.go).
type (
	// Opcode is a bytecode instruction.
	Opcode uint8
	// CmpKind selects how SORT_BY compares keys.
	CmpKind uint8
	// ErrorKind classifies a run-time error.
	ErrorKind uint8
	// constTag tags a constant in the program encoding.
	constTag uint8

	opcodeDef struct {
		name     string
		operands []int // widths in bytes
	}
	groupDef struct{ name, feature, phase string }
	enumDef  struct {
		name   string
		values []string
	}
	paramDef    struct{ name, typ string }
	currencyDef struct {
		code  string
		minor int
	}
	overloadDef struct {
		id       int
		name     string
		group    string
		params   []paramDef
		result   string
		variadic bool
	}
)

// String returns the name of the error kind.
func (k ErrorKind) String() string {
	if int(k) < len(errorKindNames) && errorKindNames[k] != "" {
		return errorKindNames[k]
	}
	return fmt.Sprintf("error(%d)", k)
}

// String returns the name of the opcode.
func (op Opcode) String() string {
	if int(op) < len(opcodeInfo) && opcodeInfo[op].name != "" {
		return opcodeInfo[op].name
	}
	return fmt.Sprintf("OP(%d)", op)
}

// pattern is a parsed parameter or result type of a signature.
type pattern struct {
	kind     Kind   // KindList, KindMap, a primitive, KindEnum (any enum, or name)
	name     string // enum name; empty for any enum
	variable string // type variable, e.g. "T"
	elem     *pattern
	nullable bool
}

// parsePattern parses a signature type; it cannot fail on the generated
// table, which schemagen has checked.
func parsePattern(s string) *pattern {
	p := &pattern{}
	if strings.HasSuffix(s, "?") {
		p.nullable, s = true, strings.TrimSuffix(s, "?")
	}
	switch {
	case strings.HasPrefix(s, "list<"):
		p.kind, p.elem = KindList, parsePattern(s[5:len(s)-1])
	case strings.HasPrefix(s, "map<string,"):
		p.kind, p.elem = KindMap, parsePattern(s[11:len(s)-1])
	case s == "enum":
		p.kind = KindEnum
	case len(s) == 1:
		p.variable = s
	case primitiveNames[s] != 0:
		p.kind = primitiveNames[s]
	default:
		p.kind, p.name = KindEnum, s
	}
	return p
}

// signature is an overload with parsed types.
type signature struct {
	def    overloadDef
	params []*pattern
	result *pattern
}

// signatures indexes the standard library by function name.
var signatures = sync.OnceValue(func() map[string][]signature {
	out := map[string][]signature{}
	for _, def := range stdOverloads {
		s := signature{def: def, result: parsePattern(def.result)}
		for _, p := range def.params {
			s.params = append(s.params, parsePattern(p.typ))
		}
		out[def.name] = append(out[def.name], s)
	}
	return out
})

// groupFeature returns the feature of a standard-library group.
func groupFeature(group string) string {
	for _, g := range stdGroups {
		if g.name == group {
			return g.feature
		}
	}
	return ""
}

// call types a call of a standard-library function.
func (c *checker) call(n *node) *ir {
	sigs, ok := signatures()[n.op]
	if !ok {
		return c.errorf(plxerr.PXLUnknownFunction, n.span, "%q is not a PXL function", n.op)
	}
	args := make([]*ir, len(n.args))
	for i, a := range n.args {
		if args[i] = c.check(a); args[i] == nil {
			return nil
		}
	}
	if x, handled := c.inline(n, args); handled {
		return x
	}
	var (
		best     *ir
		bestCost = -1
		arityOK  bool
	)
	for _, s := range sigs {
		if len(args) != len(s.params) && (!s.def.variadic || len(args) == 0) {
			continue
		}
		arityOK = true
		x, cost, ok := c.bind(s, args, n.span)
		if ok && (bestCost < 0 || cost < bestCost) {
			best, bestCost = x, cost
		}
	}
	switch {
	case best != nil:
		return c.checkCall(best, n)
	case !arityOK:
		return c.errorf(plxerr.PXLWrongArgumentCount, n.span, "%s does not take %d arguments: %s", n.op, len(args), signatureList(sigs))
	default:
		types := make([]string, len(args))
		for i, a := range args {
			types[i] = a.typ.String()
		}
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "%s does not accept (%s): %s", n.op, strings.Join(types, ", "), signatureList(sigs))
	}
}

// signatureList renders the overloads of a function for messages.
func signatureList(sigs []signature) string {
	out := make([]string, len(sigs))
	for i, s := range sigs {
		ps := make([]string, len(s.def.params))
		for j, p := range s.def.params {
			ps[j] = p.name + " " + p.typ
		}
		out[i] = s.def.name + "(" + strings.Join(ps, ", ") + ") " + s.def.result
	}
	return strings.Join(out, "; ")
}

// checkCall applies the rules a signature cannot express and records the
// feature of a later-phase group.
func (c *checker) checkCall(x *ir, n *node) *ir {
	def := stdOverloads[x.id-1]
	if def.name == "distinct" && !x.typ.elem.scalar() && x.typ.elem.kind != KindNever {
		return c.errorf(plxerr.PXLTypeMismatch, n.span, "distinct needs a list of scalars, not %s", x.typ)
	}
	if def.group != "core" {
		c.features[groupFeature(def.group)] = true
		x.noFold = true
		return c.checkGroupArgs(def.name, x, n)
	}
	return x
}

// bind matches arguments against a signature. It returns the call, the
// number of implicit conversions, and whether the signature applies.
func (c *checker) bind(s signature, args []*ir, span Span) (*ir, int, bool) {
	vars := map[string]*Type{}
	out := &ir{kind: irCall, id: s.def.id, span: span, args: make([]*ir, len(args))}
	cost := 0
	for i, a := range args {
		p := s.params[min(i, len(s.params)-1)]
		x, conv, ok := c.matchArg(p, a, vars)
		if !ok {
			return nil, 0, false
		}
		out.args[i], cost = x, cost+conv
	}
	out.typ = substitute(s.result, vars)
	return out, cost, out.typ != nil
}

// matchArg matches one argument, allowing int widening and enum literals.
func (c *checker) matchArg(p *pattern, a *ir, vars map[string]*Type) (*ir, int, bool) {
	t := a.typ
	switch {
	case t.kind == KindNull:
		return a, 0, p.nullable
	case t.nullable && !p.nullable && p.variable == "":
		return nil, 0, false
	case p.variable == "" && p.kind == KindEnum && p.name != "" && t.kind == KindString:
		s, ok := a.val.(string)
		named, _ := c.env.lookup(p.name)
		if a.kind != irConst || !ok || named == nil || !named.hasMember(s) {
			return nil, 0, false
		}
		return constant(s, &Type{kind: KindEnum, named: named}, a.span), 0, true
	case p.variable == "" && p.kind != t.kind && p.elem == nil:
		if op, ok := widening(t, &Type{kind: p.kind}); ok && p.kind != KindEnum {
			return &ir{kind: irOp, op: op, typ: &Type{kind: p.kind}, args: []*ir{a}, span: a.span}, 1, true
		}
		return nil, 0, false
	}
	return a, 0, matchType(p, t, vars)
}

// matchType matches a type against a pattern exactly, binding variables.
func matchType(p *pattern, t *Type, vars map[string]*Type) bool {
	if p.variable != "" {
		bound := t
		if p.nullable {
			bound = t.nonNull()
		}
		if prev, ok := vars[p.variable]; ok {
			u := unify(prev, bound)
			if u == nil {
				return false
			}
			vars[p.variable] = u
			return true
		}
		vars[p.variable] = bound
		return true
	}
	if t.nullable && !p.nullable {
		return false
	}
	switch {
	case p.kind != t.kind:
		return false
	case p.kind == KindList || p.kind == KindMap:
		if t.elem.kind == KindNever { // an empty literal fits any element type
			return true
		}
		return matchType(p.elem, t.elem, vars)
	case p.kind == KindEnum:
		return p.name == "" || t.named.Name == p.name
	default:
		return true
	}
}

// substitute builds the result type of a signature.
func substitute(p *pattern, vars map[string]*Type) *Type {
	var t *Type
	switch {
	case p.variable != "":
		t = vars[p.variable]
		if t == nil {
			t = typeNever
		}
	case p.kind == KindList:
		t = listOf(substitute(p.elem, vars))
	case p.kind == KindMap:
		t = mapOf(substitute(p.elem, vars))
	case p.kind == KindEnum:
		return nil // no function returns an enum
	default:
		t = &Type{kind: p.kind}
	}
	if p.nullable {
		return t.orNull()
	}
	return t
}

// inline expands the logic functions that compile to operators or
// constants: coalesce, ifNull, isNull, typeOf and string of an enum.
func (c *checker) inline(n *node, args []*ir) (*ir, bool) {
	switch {
	case n.op == "typeOf" && len(args) == 1:
		return constant(args[0].typ.String(), typeString, n.span), true
	case n.op == "isNull" && len(args) == 1:
		return &ir{kind: irOp, op: OpEq, typ: typeBool, args: []*ir{args[0], constant(nil, typeNull, n.span)}, span: n.span}, true
	case (n.op == "coalesce" && len(args) >= 1) || (n.op == "ifNull" && len(args) == 2):
		acc := args[len(args)-1]
		for i := len(args) - 2; i >= 0; i-- {
			if acc = c.coalesce(args[i], acc, n.span); acc == nil {
				return nil, true
			}
		}
		if n.op == "ifNull" && acc.typ.nullable {
			return c.errorf(plxerr.PXLTypeMismatch, n.args[1].span, "the default of ifNull may be null"), true
		}
		return acc, true
	case n.op == "string" && len(args) == 1 && args[0].typ.kind == KindEnum && !args[0].typ.nullable:
		x := *args[0]
		x.typ = typeString
		return &x, true
	default:
		return nil, false
	}
}
