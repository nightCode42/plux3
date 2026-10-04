// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"strings"
)

// typ is a parsed type expression of SCH-010: a scalar, a declared type
// by name, `list<T>` or `map<string,T>`, optionally nullable.
type typ struct {
	kind     string // a scalar's name, "named", "list" or "map"
	name     string // a declared type's name
	elem     *typ   // a list's element or a map's value
	nullable bool
}

// scalars are the built-in types and their Dart types.
var scalars = map[string]string{
	"string": "String", "int": "int", "double": "double", "bool": "bool",
	"decimal": "String", "money": "({String amount, String currency})",
	"date": "DateTime", "dateTime": "DateTime", "duration": "Duration",
	"color": "w.Color", "asset": "String", "route": "String",
}

// parseType parses a type expression.
func parseType(s string) (*typ, error) {
	p := &typeParser{s: strings.ReplaceAll(s, " ", "")}
	t, err := p.parse()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.s) {
		return nil, fmt.Errorf("type %q: unexpected %q", s, p.s[p.i:])
	}
	return t, nil
}

type typeParser struct {
	s string
	i int
}

func (p *typeParser) parse() (*typ, error) {
	start := p.i
	for p.i < len(p.s) && isIdent(p.s[p.i]) {
		p.i++
	}
	word := p.s[start:p.i]
	if word == "" {
		return nil, fmt.Errorf("type %q: a name is missing at %d", p.s, start)
	}
	var t *typ
	switch {
	case word == "list" || word == "map":
		if !p.eat('<') {
			return nil, fmt.Errorf("type %q: %s needs <…>", p.s, word)
		}
		if word == "map" {
			if !strings.HasPrefix(p.s[p.i:], "string,") {
				return nil, fmt.Errorf("type %q: map keys are strings", p.s)
			}
			p.i += len("string,")
		}
		elem, err := p.parse()
		if err != nil {
			return nil, err
		}
		if !p.eat('>') {
			return nil, fmt.Errorf("type %q: > is missing", p.s)
		}
		t = &typ{kind: word, elem: elem}
	case scalars[word] != "":
		t = &typ{kind: word}
	case word[0] >= 'A' && word[0] <= 'Z':
		t = &typ{kind: "named", name: word}
	default:
		return nil, fmt.Errorf("type %q: unknown type %s", p.s, word)
	}
	t.nullable = p.eat('?')
	return t, nil
}

func (p *typeParser) eat(c byte) bool {
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

func isIdent(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// nonNull is t without its nullability.
func (t *typ) nonNull() *typ {
	c := *t
	c.nullable = false
	return &c
}

// keywords are Dart's reserved words and built-in identifiers, which a
// generated member may not be named.
var keywords = func() map[string]bool {
	set := map[string]bool{}
	for _, k := range strings.Fields(`abstract as assert async await base break case catch class const
continue covariant default deferred do dynamic else enum export extends extension external factory
false final finally for Function get hide if implements import in interface is late library mixin new
null of on operator part required rethrow return sealed set show static super switch sync this throw
true try type typedef var void when while with yield`) {
		set[k] = true
	}
	return set
}()

// objectMembers are the members every Dart object has, which a generated
// member may not shadow.
var objectMembers = map[string]bool{"hashCode": true, "runtimeType": true, "toString": true, "noSuchMethod": true}

// member is a Dart member name for an identifier: unchanged unless it is
// reserved, an Object member or one of taken, then with a `$` suffix.
func member(name string, taken ...string) string {
	if keywords[name] || objectMembers[name] {
		return name + "$"
	}
	for _, t := range taken {
		if name == t {
			return name + "$"
		}
	}
	return name
}

// DartString is a single-quoted Dart string literal of s.
func DartString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `$`, `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return "'" + r.Replace(s) + "'"
}

// camel turns a kebab-case route or key into a lowerCamelCase member.
func camel(kebab string) string {
	parts := strings.Split(kebab, "-")
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return member(b.String())
}

// upper turns a lowerCamelCase or kebab-case name into UpperCamelCase.
func upper(name string) string {
	c := strings.TrimSuffix(camel(name), "$")
	return strings.ToUpper(c[:1]) + c[1:]
}
