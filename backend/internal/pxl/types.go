// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"slices"
	"strings"
)

// Kind classifies a type.
type Kind uint8

// Type kinds. Never and Null are internal: Never is the element type of
// an empty list or map literal, Null the type of the literal null.
const (
	KindNever Kind = iota
	KindNull
	KindBool
	KindInt
	KindDouble
	KindString
	KindDecimal
	KindMoney
	KindDate
	KindDateTime
	KindDuration
	KindColor
	KindAsset
	KindRoute
	KindEnum
	KindObject
	KindList
	KindMap
)

// primitiveNames maps the SCH-010 primitive names to their kinds.
var primitiveNames = map[string]Kind{
	"bool": KindBool, "int": KindInt, "double": KindDouble, "string": KindString, "decimal": KindDecimal,
	"money": KindMoney, "date": KindDate, "dateTime": KindDateTime, "duration": KindDuration,
	"color": KindColor, "asset": KindAsset, "route": KindRoute,
}

// Type is a PXL type. Types are immutable; compare them with Equal.
type Type struct {
	kind     Kind
	nullable bool
	elem     *Type      // list and map element
	named    *NamedType // enum and object
}

// NamedType is a declared enum or object type.
type NamedType struct {
	// Name is the declared name.
	Name string
	// Members are an enum's values in declaration order.
	Members []string
	// Fields are an object's field types; FieldOrder lists their names.
	Fields     map[string]*Type
	FieldOrder []string
}

// IsEnum reports whether n is an enum.
func (n *NamedType) IsEnum() bool { return n.Fields == nil }

// Predeclared types.
var (
	typeNever    = &Type{kind: KindNever}
	typeNull     = &Type{kind: KindNull, nullable: true}
	typeBool     = &Type{kind: KindBool}
	typeInt      = &Type{kind: KindInt}
	typeDouble   = &Type{kind: KindDouble}
	typeString   = &Type{kind: KindString}
	typeDecimal  = &Type{kind: KindDecimal}
	typeDateTime = &Type{kind: KindDateTime}
)

// Kind returns the kind of t.
func (t *Type) Kind() Kind { return t.kind }

// Nullable reports whether t admits null.
func (t *Type) Nullable() bool { return t.nullable }

// Elem returns the element type of a list or map.
func (t *Type) Elem() *Type { return t.elem }

// Named returns the declaration of an enum or object type.
func (t *Type) Named() *NamedType { return t.named }

// listOf returns list<elem>.
func listOf(elem *Type) *Type { return &Type{kind: KindList, elem: elem} }

// mapOf returns map<string,elem>.
func mapOf(elem *Type) *Type { return &Type{kind: KindMap, elem: elem} }

// orNull returns t made nullable.
func (t *Type) orNull() *Type {
	if t.nullable {
		return t
	}
	c := *t
	c.nullable = true
	return &c
}

// nonNull returns t without null.
func (t *Type) nonNull() *Type {
	if !t.nullable || t.kind == KindNull {
		return t
	}
	c := *t
	c.nullable = false
	return &c
}

// Equal reports whether t and u are the same type. Named types are equal
// when they have the same name.
func (t *Type) Equal(u *Type) bool {
	if t.kind != u.kind || t.nullable != u.nullable {
		return false
	}
	switch t.kind {
	case KindList, KindMap:
		return t.elem.Equal(u.elem)
	case KindEnum, KindObject:
		return t.named.Name == u.named.Name
	default:
		return true
	}
}

// String returns the type expression of t, e.g. "list<decimal>?".
func (t *Type) String() string {
	var s string
	switch t.kind {
	case KindNever:
		s = "never"
	case KindNull:
		return "null"
	case KindList:
		s = "list<" + t.elem.String() + ">"
	case KindMap:
		s = "map<string," + t.elem.String() + ">"
	case KindEnum, KindObject:
		s = t.named.Name
	default:
		for name, k := range primitiveNames {
			if k == t.kind {
				s = name
			}
		}
	}
	if t.nullable {
		s += "?"
	}
	return s
}

// ordered reports whether values of t have an order (<, sortBy).
func (t *Type) ordered() bool {
	switch t.kind {
	case KindInt, KindDouble, KindDecimal, KindMoney, KindString, KindDate, KindDateTime, KindDuration:
		return !t.nullable
	default:
		return false
	}
}

// scalar reports whether t is not a collection or object.
func (t *Type) scalar() bool {
	return t.kind != KindList && t.kind != KindMap && t.kind != KindObject && t.kind != KindNever && t.kind != KindNull
}

// cmpKind returns the comparison kind of an ordered type.
func (t *Type) cmpKind() CmpKind {
	return map[Kind]CmpKind{
		KindInt: CmpInt, KindDouble: CmpDouble, KindDecimal: CmpDecimal, KindMoney: CmpMoney,
		KindString: CmpString, KindDate: CmpDate, KindDateTime: CmpDateTime, KindDuration: CmpDuration,
	}[t.kind]
}

// resolver looks up declared types by name.
type resolver func(name string) (*NamedType, bool)

// parseType parses a type expression of SCH-010 with named types from
// resolve. vars, when not nil, maps single-letter type variables.
func parseType(src string, resolve resolver, vars map[string]*Type) (*Type, error) {
	p := typeParser{src: src, resolve: resolve, vars: vars}
	t, err := p.parse()
	if err != nil {
		return nil, fmt.Errorf("type %q: %w", src, err)
	}
	if p.pos != len(src) {
		return nil, fmt.Errorf("type %q: unexpected %q", src, src[p.pos:])
	}
	return t, nil
}

// typeParser reads one type expression.
type typeParser struct {
	src     string
	pos     int
	resolve resolver
	vars    map[string]*Type
}

// parse reads `base ["?"]`.
func (p *typeParser) parse() (*Type, error) {
	start := p.pos
	for p.pos < len(p.src) && (isLetter(p.src[p.pos]) || p.pos > start && isDigit(p.src[p.pos])) {
		p.pos++
	}
	name := p.src[start:p.pos]
	var (
		t   *Type
		err error
	)
	switch {
	case name == "list" || name == "map":
		t, err = p.collection(name)
	case primitiveNames[name] != 0:
		t = &Type{kind: primitiveNames[name]}
	case p.vars != nil && p.vars[name] != nil:
		t = p.vars[name]
	default:
		n, ok := p.resolve(name)
		if !ok {
			return nil, fmt.Errorf("unknown type %q", name)
		}
		t = &Type{kind: KindObject, named: n}
		if n.IsEnum() {
			t.kind = KindEnum
		}
	}
	if err == nil && p.eat("?") {
		t = t.orNull()
	}
	return t, err
}

// collection reads the element type of list<T> or map<string,T>.
func (p *typeParser) collection(name string) (*Type, error) {
	if !p.eat("<") || name == "map" && !p.eat("string,") {
		return nil, fmt.Errorf("write list<T> or map<string,T>")
	}
	elem, err := p.parse()
	if err != nil {
		return nil, err
	}
	if !p.eat(">") {
		return nil, fmt.Errorf("missing '>'")
	}
	if name == "map" {
		return mapOf(elem), nil
	}
	return listOf(elem), nil
}

// eat consumes lit if it comes next.
func (p *typeParser) eat(lit string) bool {
	if strings.HasPrefix(p.src[p.pos:], lit) {
		p.pos += len(lit)
		return true
	}
	return false
}

// isLetter reports whether c is an ASCII letter.
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// widening reports whether a value of type from converts implicitly to to
// (int to double or decimal), and to which conversion opcode.
func widening(from, to *Type) (Opcode, bool) {
	if from.kind != KindInt || from.nullable {
		return 0, false
	}
	switch to.kind {
	case KindDouble:
		return OpIntToDouble, true
	case KindDecimal:
		return OpIntToDec, true
	default:
		return 0, false
	}
}

// assignable reports whether a value of type from is accepted where to is
// expected without conversion.
func assignable(from, to *Type) bool {
	switch {
	case from.kind == KindNull:
		return to.nullable
	case from.nullable && !to.nullable:
		return false
	case from.kind == KindNever:
		return true
	case from.kind != to.kind:
		return false
	case from.kind == KindList || from.kind == KindMap:
		return from.elem.kind == KindNever || from.elem.Equal(to.elem)
	case from.kind == KindEnum || from.kind == KindObject:
		return from.named.Name == to.named.Name
	default:
		return true
	}
}

// unify returns the common type of two branches or elements, or nil.
func unify(a, b *Type) *Type {
	switch {
	case a.kind == KindNull && b.kind == KindNull:
		return a
	case a.kind == KindNull:
		return b.orNull()
	case b.kind == KindNull:
		return a.orNull()
	}
	switch {
	case a.kind == KindNever:
		return b
	case b.kind == KindNever:
		return a
	}
	nullable := a.nullable || b.nullable
	x, y := a.nonNull(), b.nonNull()
	var t *Type
	switch {
	case x.Equal(y):
		t = x
	case x.kind == KindInt && (y.kind == KindDouble || y.kind == KindDecimal):
		t = y
	case y.kind == KindInt && (x.kind == KindDouble || x.kind == KindDecimal):
		t = x
	case (x.kind == KindList || x.kind == KindMap) && x.kind == y.kind && x.elem.kind == KindNever:
		t = y
	case (x.kind == KindList || x.kind == KindMap) && x.kind == y.kind && y.elem.kind == KindNever:
		t = x
	default:
		return nil
	}
	if nullable {
		return t.orNull()
	}
	return t
}

// hasMember reports whether an enum declares the member.
func (n *NamedType) hasMember(m string) bool { return slices.Contains(n.Members, m) }
