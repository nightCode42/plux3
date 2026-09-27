// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"slices"
	"strings"
)

// TypeKind classifies a type expression.
type TypeKind int

// Type expression kinds.
const (
	KindPrimitive TypeKind = iota + 1 // Name is one of primitives
	KindNamed                         // Name is a value type or an enum
	KindParam                         // Name is a type parameter of the descriptor
	KindList                          // Elem is the element type
	KindMap                           // string keys, Elem is the value type
)

// Type is a parsed type expression of SCH-010, extended with the value
// types and enums of the registry and descriptor type parameters.
type Type struct {
	Kind     TypeKind
	Name     string
	Elem     *Type
	Nullable bool
}

// primitives are the built-in types of SCH-010, in the order of the spec.
var primitives = []string{
	"string", "int", "double", "bool", "decimal", "money", "date", "dateTime",
	"duration", "color", "asset", "route",
}

// String returns the canonical spelling of t.
func (t Type) String() string {
	var s string
	switch t.Kind {
	case KindList:
		s = "list<" + t.Elem.String() + ">"
	case KindMap:
		s = "map<string," + t.Elem.String() + ">"
	default:
		s = t.Name
	}
	if t.Nullable {
		s += "?"
	}
	return s
}

// ParseType parses a type expression. Only the canonical spelling is
// accepted — no spaces, `map<string,T>` — so every type has one spelling.
func ParseType(s string) (Type, error) {
	p := typeParser{src: s}
	t, err := p.parse()
	if err != nil {
		return Type{}, fmt.Errorf("type %q: %w", s, err)
	}
	if p.pos != len(s) {
		return Type{}, fmt.Errorf("type %q: unexpected %q at offset %d", s, s[p.pos:], p.pos)
	}
	return t, nil
}

// typeParser is a recursive-descent parser over one type expression.
type typeParser struct {
	src string
	pos int
}

// parse reads `base ["?"]`.
func (p *typeParser) parse() (Type, error) {
	name := p.ident()
	var t Type
	switch {
	case name == "":
		return Type{}, fmt.Errorf("expected a type at offset %d", p.pos)
	case name == "list":
		elem, err := p.args(false)
		if err != nil {
			return Type{}, err
		}
		t = Type{Kind: KindList, Elem: &elem}
	case name == "map":
		elem, err := p.args(true)
		if err != nil {
			return Type{}, err
		}
		t = Type{Kind: KindMap, Elem: &elem}
	case slices.Contains(primitives, name):
		t = Type{Kind: KindPrimitive, Name: name}
	case len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z':
		t = Type{Kind: KindParam, Name: name}
	case name[0] >= 'A' && name[0] <= 'Z':
		t = Type{Kind: KindNamed, Name: name}
	default:
		return Type{}, fmt.Errorf("unknown type %q", name)
	}
	if p.pos < len(p.src) && p.src[p.pos] == '?' {
		p.pos++
		t.Nullable = true
	}
	return t, nil
}

// args reads `<T>` or, for maps, `<string,T>`.
func (p *typeParser) args(isMap bool) (Type, error) {
	if !p.eat("<") {
		return Type{}, fmt.Errorf("expected '<' at offset %d", p.pos)
	}
	if isMap && !p.eat("string,") {
		return Type{}, fmt.Errorf("map keys are strings: write map<string,T>")
	}
	elem, err := p.parse()
	if err != nil {
		return Type{}, err
	}
	if !p.eat(">") {
		return Type{}, fmt.Errorf("expected '>' at offset %d", p.pos)
	}
	return elem, nil
}

// ident reads an identifier.
func (p *typeParser) ident() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' && p.pos > start {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

// eat consumes lit if it comes next.
func (p *typeParser) eat(lit string) bool {
	if strings.HasPrefix(p.src[p.pos:], lit) {
		p.pos += len(lit)
		return true
	}
	return false
}

// walk calls fn for t and every type nested in it.
func (t Type) walk(fn func(Type)) {
	fn(t)
	if t.Elem != nil {
		t.Elem.walk(fn)
	}
}
