// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package regex

import (
	"fmt"
	"slices"
)

// maxRune is the largest Unicode code point.
const maxRune = 0x10FFFF

// MaxNesting is the deepest nesting of groups a pattern may have.
const MaxNesting = 100

// nodeKind is the kind of a syntax-tree node.
type nodeKind uint8

const (
	nEmpty nodeKind = iota
	nRune
	nClass
	nBegin
	nEnd
	nCapture
	nConcat
	nAlt
	nRepeat
)

// node is a parsed pattern.
type node struct {
	kind   nodeKind
	r      rune
	ranges []rune // nClass: sorted, disjoint, non-adjacent [lo, hi] pairs
	index  int    // nCapture: the group number
	subs   []*node
	min    int
	max    int // nRepeat: -1 for no upper bound
}

// parser reads a pattern of code points.
type parser struct {
	src    []rune
	pos    int
	lim    Limits
	groups int
	depth  int
}

// syntaxErr builds a syntax error at a code-point offset.
func (p *parser) syntaxErr(at int, format string, args ...any) *Error {
	return &Error{Kind: ErrSyntax, Offset: at, Message: fmt.Sprintf(format, args...)}
}

func (p *parser) more() bool { return p.pos < len(p.src) }

func (p *parser) peek() rune { return p.src[p.pos] }

// parse parses the whole pattern.
func parse(src []rune, lim Limits) (*node, int, *Error) {
	p := &parser{src: src, lim: lim}
	n, err := p.alternation()
	if err != nil {
		return nil, 0, err
	}
	if p.more() { // only an unmatched ')' stops an alternation early
		return nil, 0, p.syntaxErr(p.pos, "unmatched )")
	}
	return n, p.groups, nil
}

// alternation = sequence { "|" sequence }.
func (p *parser) alternation() (*node, *Error) {
	var branches []*node
	for {
		s, err := p.sequence()
		if err != nil {
			return nil, err
		}
		branches = append(branches, s)
		if !p.more() || p.peek() != '|' {
			break
		}
		p.pos++
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return &node{kind: nAlt, subs: branches}, nil
}

// sequence = { term }.
func (p *parser) sequence() (*node, *Error) {
	var items []*node
	for p.more() {
		c := p.peek()
		if c == '|' || c == ')' {
			break
		}
		atom, err := p.atom()
		if err != nil {
			return nil, err
		}
		if p.more() && isQuantifier(p.peek()) {
			if atom.kind == nBegin || atom.kind == nEnd {
				return nil, p.syntaxErr(p.pos, "missing argument to repetition operator")
			}
			if atom, err = p.quantifier(atom); err != nil {
				return nil, err
			}
			if p.more() && isQuantifier(p.peek()) {
				return nil, p.syntaxErr(p.pos, "invalid nested repetition operator")
			}
		}
		items = append(items, atom)
	}
	switch len(items) {
	case 0:
		return &node{kind: nEmpty}, nil
	case 1:
		return items[0], nil
	}
	return &node{kind: nConcat, subs: items}, nil
}

func isQuantifier(c rune) bool { return c == '*' || c == '+' || c == '?' || c == '{' }

// atom parses one atom or anchor.
func (p *parser) atom() (*node, *Error) {
	at := p.pos
	c := p.peek()
	p.pos++
	switch c {
	case '^':
		return &node{kind: nBegin}, nil
	case '$':
		return &node{kind: nEnd}, nil
	case '.':
		return &node{kind: nClass, ranges: []rune{0, '\n' - 1, '\n' + 1, maxRune}}, nil
	case '(':
		return p.group(at)
	case '[':
		return p.class(at)
	case '\\':
		r, ranges, err := p.escape(at)
		if err != nil {
			return nil, err
		}
		if ranges != nil {
			return &node{kind: nClass, ranges: ranges}, nil
		}
		return &node{kind: nRune, r: r}, nil
	case '*', '+', '?', '{':
		return nil, p.syntaxErr(at, "missing argument to repetition operator")
	case ']', '}':
		return nil, p.syntaxErr(at, "unescaped %c", c)
	}
	return &node{kind: nRune, r: c}, nil
}

// group parses "(" [ "?:" ] alternation ")"; the "(" is consumed.
func (p *parser) group(at int) (*node, *Error) {
	if p.depth++; p.depth > MaxNesting {
		return nil, p.syntaxErr(at, "groups nested deeper than %d", MaxNesting)
	}
	capture := true
	if p.more() && p.peek() == '?' {
		if p.pos+1 >= len(p.src) || p.src[p.pos+1] != ':' {
			return nil, p.syntaxErr(at, "unsupported group syntax; only (?: is allowed")
		}
		p.pos += 2
		capture = false
	}
	index := 0
	if capture {
		p.groups++
		index = p.groups
	}
	sub, err := p.alternation()
	if err != nil {
		return nil, err
	}
	if !p.more() || p.peek() != ')' {
		return nil, p.syntaxErr(at, "missing )")
	}
	p.pos++
	p.depth--
	if !capture {
		return sub, nil
	}
	return &node{kind: nCapture, index: index, subs: []*node{sub}}, nil
}

// quantifier applies "*", "+", "?" or "{n}", "{n,}", "{n,m}" to atom.
func (p *parser) quantifier(atom *node) (*node, *Error) {
	at := p.pos
	c := p.peek()
	p.pos++
	switch c {
	case '*':
		return &node{kind: nRepeat, min: 0, max: -1, subs: []*node{atom}}, nil
	case '+':
		return &node{kind: nRepeat, min: 1, max: -1, subs: []*node{atom}}, nil
	case '?':
		return &node{kind: nRepeat, min: 0, max: 1, subs: []*node{atom}}, nil
	}
	lo, ok, err := p.count(at)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.syntaxErr(at, "invalid repetition; write {n}, {n,} or {n,m}")
	}
	hi := lo
	if p.more() && p.peek() == ',' {
		p.pos++
		hi = -1
		if p.more() && p.peek() != '}' {
			if hi, ok, err = p.count(at); err != nil {
				return nil, err
			}
			if !ok {
				return nil, p.syntaxErr(at, "invalid repetition; write {n}, {n,} or {n,m}")
			}
		}
	}
	if !p.more() || p.peek() != '}' {
		return nil, p.syntaxErr(at, "invalid repetition; write {n}, {n,} or {n,m}")
	}
	p.pos++
	if hi >= 0 && hi < lo {
		return nil, p.syntaxErr(at, "invalid repetition: {%d,%d} has its maximum below its minimum", lo, hi)
	}
	return &node{kind: nRepeat, min: lo, max: hi, subs: []*node{atom}}, nil
}

// count reads decimal digits; a count above the repeat limit is an error.
func (p *parser) count(at int) (int, bool, *Error) {
	start, n := p.pos, int64(0)
	if p.pos+1 < len(p.src) && p.peek() == '0' && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9' {
		return 0, false, nil // no leading zeros
	}
	for p.more() && p.peek() >= '0' && p.peek() <= '9' {
		if n = n*10 + int64(p.peek()-'0'); n > p.lim.Repeat {
			return 0, false, &Error{Kind: ErrRepeat, Offset: at, Message: fmt.Sprintf("a repeat count above %d", p.lim.Repeat)}
		}
		p.pos++
	}
	return int(n), p.pos > start, nil
}

// escape parses an escape after its "\": a code point, or a class's ranges.
func (p *parser) escape(at int) (rune, []rune, *Error) {
	if !p.more() {
		return 0, nil, p.syntaxErr(at, "trailing backslash")
	}
	c := p.peek()
	p.pos++
	switch c {
	case 'd':
		return 0, digitRanges, nil
	case 'D':
		return 0, negate(digitRanges), nil
	case 'w':
		return 0, wordRanges, nil
	case 'W':
		return 0, negate(wordRanges), nil
	case 's':
		return 0, spaceRanges, nil
	case 'S':
		return 0, negate(spaceRanges), nil
	case 't':
		return '\t', nil, nil
	case 'n':
		return '\n', nil, nil
	case 'r':
		return '\r', nil, nil
	case 'f':
		return '\f', nil, nil
	case 'v':
		return '\v', nil, nil
	case 'x':
		r, err := p.hex(at)
		return r, nil, err
	}
	if c < 0x80 && isPunct(byte(c)) {
		return c, nil, nil
	}
	return 0, nil, p.syntaxErr(at, "invalid escape \\%c", c)
}

// hex parses \xHH or \x{H…} after the "x".
func (p *parser) hex(at int) (rune, *Error) {
	bad := p.syntaxErr(at, "invalid hexadecimal escape; write \\xHH or \\x{H…}")
	var v rune
	if p.more() && p.peek() == '{' {
		p.pos++
		digits := 0
		for p.more() && p.peek() != '}' {
			d := hexValue(p.peek())
			if d < 0 || digits == 6 {
				return 0, bad
			}
			v = v*16 + d
			digits++
			p.pos++
		}
		if !p.more() || digits == 0 {
			return 0, bad
		}
		p.pos++
	} else {
		for range 2 {
			if !p.more() || hexValue(p.peek()) < 0 {
				return 0, bad
			}
			v = v*16 + hexValue(p.peek())
			p.pos++
		}
	}
	if v > maxRune || v >= 0xD800 && v <= 0xDFFF {
		return 0, p.syntaxErr(at, "\\x escapes a surrogate or a value beyond U+10FFFF")
	}
	return v, nil
}

func hexValue(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// isPunct reports ASCII punctuation, which an escape makes literal.
func isPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

// class parses a bracketed class; the "[" is consumed.
func (p *parser) class(at int) (*node, *Error) {
	negated := false
	if p.more() && p.peek() == '^' {
		negated = true
		p.pos++
	}
	var ranges []rune
	first := true
	for {
		if !p.more() {
			return nil, p.syntaxErr(at, "missing ]")
		}
		c := p.peek()
		if c == ']' {
			if first {
				return nil, p.syntaxErr(at, "empty class; escape a literal ] as \\]")
			}
			p.pos++
			break
		}
		itemAt := p.pos
		lo, set, err := p.classPoint(first)
		if err != nil {
			return nil, err
		}
		first = false
		if set != nil {
			if p.more() && p.peek() == '-' && !p.closesNext() {
				return nil, p.syntaxErr(p.pos, "a class escape cannot start a range")
			}
			ranges = append(ranges, set...)
			continue
		}
		hi := lo
		if p.more() && p.peek() == '-' && !p.closesNext() {
			p.pos++
			if !p.more() {
				return nil, p.syntaxErr(at, "missing ]")
			}
			var hset []rune
			if hi, hset, err = p.classPoint(false); err != nil {
				return nil, err
			}
			if hset != nil {
				return nil, p.syntaxErr(itemAt, "a class escape cannot end a range")
			}
			if hi < lo {
				return nil, p.syntaxErr(itemAt, "invalid range %c-%c", lo, hi)
			}
		}
		ranges = append(ranges, lo, hi)
	}
	ranges = normalize(ranges)
	if negated {
		ranges = negate(ranges)
	}
	return &node{kind: nClass, ranges: ranges}, nil
}

// closesNext reports whether the "-" at the position is followed by "]".
func (p *parser) closesNext() bool { return p.pos+1 < len(p.src) && p.src[p.pos+1] == ']' }

// classPoint reads one code point or class escape inside a class.
func (p *parser) classPoint(first bool) (rune, []rune, *Error) {
	at := p.pos
	c := p.peek()
	switch c {
	case '\\':
		p.pos++
		return p.escape(at)
	case '[':
		return 0, nil, p.syntaxErr(at, "unescaped [ in a class")
	case '-':
		if !first && !p.closesNext() {
			return 0, nil, p.syntaxErr(at, "unescaped - in a class; escape it or put it first or last")
		}
	}
	p.pos++
	return c, nil, nil
}

// The class escapes \d, \w and \s, ASCII as in RE2.
var (
	digitRanges = []rune{'0', '9'}
	wordRanges  = []rune{'0', '9', 'A', 'Z', '_', '_', 'a', 'z'}
	spaceRanges = []rune{'\t', '\n', '\f', '\r', ' ', ' '}
)

// normalize sorts ranges and merges overlapping and adjacent ones.
func normalize(r []rune) []rune {
	type span struct{ lo, hi rune }
	spans := make([]span, 0, len(r)/2)
	for i := 0; i < len(r); i += 2 {
		spans = append(spans, span{r[i], r[i+1]})
	}
	slices.SortFunc(spans, func(a, b span) int { return int(a.lo - b.lo) })
	var out []rune
	for _, s := range spans {
		if n := len(out); n > 0 && s.lo <= out[n-1]+1 {
			out[n-1] = max(out[n-1], s.hi)
			continue
		}
		out = append(out, s.lo, s.hi)
	}
	return out
}

// negate returns the complement of normalized ranges over all code points.
func negate(r []rune) []rune {
	var out []rune
	next := rune(0)
	for i := 0; i < len(r); i += 2 {
		if r[i] > next {
			out = append(out, next, r[i]-1)
		}
		next = r[i+1] + 1
	}
	if next <= maxRune {
		out = append(out, next, maxRune)
	}
	return out
}
