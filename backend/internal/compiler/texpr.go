// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"errors"
	"fmt"
	"strings"
)

// texpr is a parsed type expression (SCH-010): a name — a primitive, a
// declared or registry type, or a type parameter — or list<T> or
// map<string,T>, optionally nullable. The compiler uses it to check
// declarations and to bind type parameters; PXL parses the same grammar.
type texpr struct {
	name     string // "list", "map" or a type name
	elem     *texpr // element of list and map
	nullable bool
}

// primitives are the built-in type names of SCH-010.
var primitives = map[string]bool{
	"string": true, "int": true, "double": true, "bool": true, "decimal": true, "money": true, "date": true,
	"dateTime": true, "duration": true, "color": true, "asset": true, "route": true,
}

// errTypeSyntax reports a malformed type expression.
var errTypeSyntax = errors.New("malformed type expression")

// parseTypeExpr parses a type expression.
func parseTypeExpr(src string) (*texpr, error) {
	p := &texprParser{src: src}
	t, err := p.parse()
	if err == nil && p.pos != len(src) {
		err = fmt.Errorf("%w: unexpected %q", errTypeSyntax, src[p.pos:])
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

type texprParser struct {
	src string
	pos int
}

func (p *texprParser) parse() (*texpr, error) {
	start := p.pos
	for p.pos < len(p.src) && (isAlpha(p.src[p.pos]) || p.pos > start && isDigit(p.src[p.pos])) {
		p.pos++
	}
	t := &texpr{name: p.src[start:p.pos]}
	if t.name == "" {
		return nil, fmt.Errorf("%w: a type name is expected at %d", errTypeSyntax, start)
	}
	if t.name == "list" || t.name == "map" {
		if !p.eat("<") || t.name == "map" && !p.eat("string,") {
			return nil, fmt.Errorf("%w: write list<T> or map<string,T>", errTypeSyntax)
		}
		elem, err := p.parse()
		if err != nil {
			return nil, err
		}
		if !p.eat(">") {
			return nil, fmt.Errorf("%w: missing '>'", errTypeSyntax)
		}
		t.elem = elem
	}
	t.nullable = p.eat("?")
	return t, nil
}

func (p *texprParser) eat(lit string) bool {
	if strings.HasPrefix(p.src[p.pos:], lit) {
		p.pos += len(lit)
		return true
	}
	return false
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// String returns the canonical text of t.
func (t *texpr) String() string {
	var b strings.Builder
	t.write(&b)
	return b.String()
}

func (t *texpr) write(b *strings.Builder) {
	switch t.name {
	case "list":
		b.WriteString("list<")
		t.elem.write(b)
		b.WriteString(">")
	case "map":
		b.WriteString("map<string,")
		t.elem.write(b)
		b.WriteString(">")
	default:
		b.WriteString(t.name)
	}
	if t.nullable {
		b.WriteString("?")
	}
}

// names calls f for every type name in t.
func (t *texpr) names(f func(string)) {
	if t.elem != nil {
		t.elem.names(f)
		return
	}
	f(t.name)
}

// subst replaces type parameters by their bindings, given as expressions.
func (t *texpr) subst(bind map[string]*texpr) *texpr {
	if t.elem != nil {
		return &texpr{name: t.name, elem: t.elem.subst(bind), nullable: t.nullable}
	}
	b, ok := bind[t.name]
	if !ok {
		return t
	}
	return &texpr{name: b.name, elem: b.elem, nullable: b.nullable || t.nullable}
}

// match binds the type parameters of pattern t to the parts of actual,
// or reports that the shapes differ. A nullable parameter matches the
// non-null form of a nullable actual.
func (t *texpr) match(actual *texpr, params map[string]bool, bind map[string]*texpr) bool {
	if params[t.name] {
		b := &texpr{name: actual.name, elem: actual.elem, nullable: actual.nullable && !t.nullable}
		if prev, ok := bind[t.name]; ok {
			return prev.String() == b.String()
		}
		bind[t.name] = b
		return true
	}
	if t.name != actual.name || (t.elem == nil) != (actual.elem == nil) {
		return false
	}
	if t.elem != nil {
		return t.elem.match(actual.elem, params, bind)
	}
	return true
}
