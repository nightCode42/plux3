// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Span is a half-open byte range of the source.
type Span struct{ Start, End int }

// tokenKind classifies a token.
type tokenKind uint8

// Token kinds.
const (
	tokEOF tokenKind = iota
	tokIdent
	tokInt
	tokDouble
	tokDecimal
	tokString
	tokOp // operators and punctuation; text holds the symbol
)

// token is one lexeme.
type token struct {
	kind tokenKind
	text string // identifier, symbol, or the literal's source
	str  string // the decoded value of a string literal
	span Span
}

// keywords are reserved identifiers.
var keywords = map[string]bool{"true": true, "false": true, "null": true, "in": true}

// isKeyword reports whether s is reserved.
func isKeyword(s string) bool { return keywords[s] }

// symbols are the operators, longest first.
var symbols = []string{"?.", "??", "||", "&&", "==", "!=", "<=", ">=", "<", ">", "+", "-", "*", "/", "%", "!", "?", ":", ".", ",", "(", ")", "[", "]", "{", "}"}

// syntaxError is a lexing or parsing error at a byte span.
type syntaxError struct {
	span Span
	msg  string
}

func (e *syntaxError) Error() string { return e.msg }

// lex splits src into tokens.
func lex(src string) ([]token, *syntaxError) {
	var toks []token
	for i := 0; i < len(src); {
		c := src[i]
		var (
			t   token
			err *syntaxError
		)
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		switch {
		case isLetter(c):
			j := i
			for j < len(src) && (isLetter(src[j]) || isDigit(src[j])) {
				j++
			}
			t = token{kind: tokIdent, text: src[i:j], span: Span{i, j}}
		case isDigit(c):
			t, err = lexNumber(src, i)
		case isQuote(c):
			t, err = lexString(src, i)
		default:
			t, err = lexSymbol(src, i)
		}
		if err != nil {
			return nil, err
		}
		toks = append(toks, t)
		i = t.span.End
	}
	return append(toks, token{kind: tokEOF, span: Span{len(src), len(src)}}), nil
}

// isQuote reports whether c opens a string literal.
func isQuote(c byte) bool { return c == '"' || c == '\'' }

// lexSymbol reads the longest operator at i.
func lexSymbol(src string, i int) (token, *syntaxError) {
	for _, s := range symbols {
		if strings.HasPrefix(src[i:], s) {
			return token{kind: tokOp, text: s, span: Span{i, i + len(s)}}, nil
		}
	}
	_, w := utf8.DecodeRuneInString(src[i:])
	return token{}, &syntaxError{Span{i, i + w}, fmt.Sprintf("unexpected character %q", src[i:i+w])}
}

// lexNumber reads an int, a double (fraction or exponent) or a decimal
// (d suffix, no exponent).
func lexNumber(src string, i int) (token, *syntaxError) {
	j := skipDigits(src, i)
	kind := tokInt
	if j+1 < len(src) && src[j] == '.' && isDigit(src[j+1]) {
		j = skipDigits(src, j+1)
		kind = tokDouble
	}
	if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
		k := j + 1
		if k < len(src) && (src[k] == '+' || src[k] == '-') {
			k++
		}
		if k >= len(src) || !isDigit(src[k]) {
			return token{}, &syntaxError{Span{i, k}, "malformed exponent"}
		}
		j, kind = skipDigits(src, k), tokDouble
	}
	text := src[i:j]
	if j < len(src) && src[j] == 'd' {
		if kind == tokDouble && strings.ContainsAny(text, "eE") {
			return token{}, &syntaxError{Span{i, j + 1}, "a decimal literal has no exponent"}
		}
		j, kind = j+1, tokDecimal
	}
	if j < len(src) && (isLetter(src[j]) || isDigit(src[j])) {
		return token{}, &syntaxError{Span{i, j + 1}, "malformed number"}
	}
	return token{kind: kind, text: text, span: Span{i, j}}, nil
}

// lexString reads a quoted string with escapes \n \t \r \\ \" \' \u{hex}.
func lexString(src string, i int) (token, *syntaxError) {
	quote := src[i]
	var b strings.Builder
	j := i + 1
	for j < len(src) {
		switch c := src[j]; c {
		case quote:
			return token{kind: tokString, text: src[i : j+1], str: b.String(), span: Span{i, j + 1}}, nil
		case '\n':
			return token{}, &syntaxError{Span{i, j}, "unterminated string"}
		case '\\':
			n, err := lexEscape(src, j, &b)
			if err != nil {
				return token{}, err
			}
			j = n
		default:
			r, w := utf8.DecodeRuneInString(src[j:])
			if r == utf8.RuneError && w == 1 {
				return token{}, &syntaxError{Span{j, j + 1}, "invalid UTF-8"}
			}
			b.WriteString(src[j : j+w])
			j += w
		}
	}
	return token{}, &syntaxError{Span{i, len(src)}, "unterminated string"}
}

// lexEscape decodes the escape at src[j] into b and returns the offset after it.
func lexEscape(src string, j int, b *strings.Builder) (int, *syntaxError) {
	if j+1 >= len(src) {
		return 0, &syntaxError{Span{j, j + 1}, "unterminated escape"}
	}
	simple := map[byte]string{'n': "\n", 't': "\t", 'r': "\r", '\\': `\`, '"': `"`, '\'': "'"}
	if s, ok := simple[src[j+1]]; ok {
		b.WriteString(s)
		return j + 2, nil
	}
	if src[j+1] == 'u' && j+2 < len(src) && src[j+2] == '{' {
		end := strings.IndexByte(src[j:], '}')
		if end > 3 && end <= 9 {
			n, err := strconv.ParseUint(src[j+3:j+end], 16, 32)
			if err == nil && n <= utf8.MaxRune && utf8.ValidRune(rune(n)) { //nolint:gosec // G115: n ≤ MaxRune.
				b.WriteRune(rune(n)) //nolint:gosec // G115: n ≤ MaxRune.
				return j + end + 1, nil
			}
		}
	}
	return 0, &syntaxError{Span{j, j + 2}, "invalid escape"}
}

// nodeKind classifies a syntax node.
type nodeKind uint8

// Syntax node kinds.
const (
	nLiteral nodeKind = iota + 1
	nIdent
	nUnary  // op, a
	nBinary // op, a, b
	nCond   // a ? b : c
	nField  // a.name
	nSafe   // a?.name
	nIndex  // a[b]
	nCall   // name(args), with name possibly "ns.name"
	nMacro  // a.name(var, b)
	nList   // [args]
	nMapLit // {keys: args}
)

// node is a syntax tree node.
type node struct {
	kind  nodeKind
	span  Span
	op    string // operator, identifier, field or function name
	tok   token  // literal token
	a, b  *node
	c     *node
	args  []*node
	keys  []*node // map literal keys
	vspan Span    // macro variable span
}

// parser is a precedence-climbing parser over tokens (spec Appendix E.1).
type parser struct {
	toks     []token
	pos      int
	depth    int
	maxDepth int
}

// parse parses src into a syntax tree.
func parse(src string, maxDepth int) (*node, *syntaxError) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, maxDepth: maxDepth}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, &syntaxError{t.span, fmt.Sprintf("unexpected %q", t.text)}
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

// isOp reports whether the next token is the symbol or keyword s.
func (p *parser) isOp(s string) bool {
	t := p.peek()
	return (t.kind == tokOp || t.kind == tokIdent) && t.text == s
}

func (p *parser) expect(s string) (token, *syntaxError) {
	if !p.isOp(s) {
		t := p.peek()
		return t, &syntaxError{t.span, fmt.Sprintf("expected %q", s)}
	}
	return p.next(), nil
}

// enter guards the nesting depth (pxl.nestingDepth).
func (p *parser) enter() *syntaxError {
	p.depth++
	if p.depth > p.maxDepth {
		t := p.peek()
		return &syntaxError{t.span, fmt.Sprintf("nesting deeper than %d", p.maxDepth)}
	}
	return nil
}

// levels are the binary operators by increasing precedence.
var levels = [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<", "<=", ">", ">=", "in"}, {"??"}, {"+", "-"}, {"*", "/", "%"}}

// expr parses `or [ "?" expr ":" expr ]`.
func (p *parser) expr() (*node, *syntaxError) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	c, err := p.binary(0)
	if err != nil || !p.isOp("?") {
		return c, err
	}
	p.next()
	a, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(":"); err != nil {
		return nil, err
	}
	b, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &node{kind: nCond, a: c, b: a, c: b, span: Span{c.span.Start, b.span.End}}, nil
}

// binary parses left-associative operators of level and above.
func (p *parser) binary(level int) (*node, *syntaxError) {
	if level == len(levels) {
		return p.unary()
	}
	left, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		for _, s := range levels[level] {
			if p.isOp(s) {
				op = s
			}
		}
		if op == "" {
			return left, nil
		}
		p.next()
		if err := p.enter(); err != nil {
			return nil, err
		}
		right, err := p.binary(level + 1)
		p.depth--
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, op: op, a: left, b: right, span: Span{left.span.Start, right.span.End}}
	}
}

// unary parses `[ "!" | "-" ] member`.
func (p *parser) unary() (*node, *syntaxError) {
	if p.isOp("!") || p.isOp("-") {
		t := p.next()
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		a, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &node{kind: nUnary, op: t.text, a: a, span: Span{t.span.Start, a.span.End}}, nil
	}
	return p.member()
}

// member parses a primary with field access, indexing and calls.
func (p *parser) member() (*node, *syntaxError) {
	n, err := p.primary()
	for err == nil {
		switch {
		case p.isOp(".") || p.isOp("?."):
			n, err = p.dot(n)
		case p.isOp("["):
			n, err = p.subscript(n)
		default:
			return n, nil
		}
	}
	return nil, err
}

// dot parses `.name`, `?.name` or a receiver call after n.
func (p *parser) dot(n *node) (*node, *syntaxError) {
	dot := p.next()
	name := p.next()
	if name.kind != tokIdent {
		return nil, &syntaxError{name.span, "expected a name"}
	}
	if dot.text == "." && p.isOp("(") {
		return p.receiverCall(n, name)
	}
	kind := nField
	if dot.text == "?." {
		kind = nSafe
	}
	return &node{kind: kind, op: name.text, a: n, span: Span{n.span.Start, name.span.End}}, nil
}

// subscript parses `[expr]` after n.
func (p *parser) subscript(n *node) (*node, *syntaxError) {
	p.next()
	idx, err := p.expr()
	if err != nil {
		return nil, err
	}
	end, err := p.expect("]")
	if err != nil {
		return nil, err
	}
	return &node{kind: nIndex, a: n, b: idx, span: Span{n.span.Start, end.span.End}}, nil
}

// receiverCall parses `recv.name(args)`: a namespaced function when recv
// is a bare identifier such as `format`, otherwise a macro.
func (p *parser) receiverCall(recv *node, name token) (*node, *syntaxError) {
	p.next() // "("
	args, end, err := p.args(")")
	if err != nil {
		return nil, err
	}
	span := Span{recv.span.Start, end.span.End}
	if recv.kind == nIdent && namespaces[recv.op] {
		return &node{kind: nCall, op: recv.op + "." + name.text, args: args, span: span}, nil
	}
	n := &node{kind: nMacro, op: name.text, a: recv, span: span}
	if len(args) == 2 && args[0].kind == nIdent {
		n.vspan, n.args = args[0].span, args
	} else {
		n.args = args
	}
	return n, nil
}

// namespaces are the function namespaces of the standard library.
var namespaces = map[string]bool{"format": true}

// args parses comma-separated expressions up to the closing symbol.
func (p *parser) args(closing string) ([]*node, token, *syntaxError) {
	var args []*node
	if p.isOp(closing) {
		return nil, p.next(), nil
	}
	for {
		a, err := p.expr()
		if err != nil {
			return nil, token{}, err
		}
		args = append(args, a)
		if p.isOp(",") {
			p.next()
			continue
		}
		end, err := p.expect(closing)
		return args, end, err
	}
}

// primary parses literals, identifiers, calls, parentheses, lists and maps.
func (p *parser) primary() (*node, *syntaxError) {
	t := p.next()
	switch {
	case t.kind == tokInt || t.kind == tokDouble || t.kind == tokDecimal || t.kind == tokString:
		return &node{kind: nLiteral, tok: t, span: t.span}, nil
	case t.kind == tokIdent && (t.text == "true" || t.text == "false" || t.text == "null"):
		return &node{kind: nLiteral, tok: t, span: t.span}, nil
	case t.kind == tokIdent && !isKeyword(t.text):
		if p.isOp("(") {
			p.next()
			args, end, err := p.args(")")
			if err != nil {
				return nil, err
			}
			return &node{kind: nCall, op: t.text, args: args, span: Span{t.span.Start, end.span.End}}, nil
		}
		return &node{kind: nIdent, op: t.text, span: t.span}, nil
	case t.kind == tokOp && t.text == "(":
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		end, err := p.expect(")")
		if err != nil {
			return nil, err
		}
		n.span = Span{t.span.Start, end.span.End}
		return n, nil
	case t.kind == tokOp && t.text == "[":
		args, end, err := p.args("]")
		if err != nil {
			return nil, err
		}
		return &node{kind: nList, args: args, span: Span{t.span.Start, end.span.End}}, nil
	case t.kind == tokOp && t.text == "{":
		return p.mapLiteral(t)
	case t.kind == tokEOF:
		return nil, &syntaxError{t.span, "unexpected end of expression"}
	default:
		return nil, &syntaxError{t.span, fmt.Sprintf("unexpected %q", t.text)}
	}
}

// mapLiteral parses `{ key: value, … }` after the opening brace.
func (p *parser) mapLiteral(open token) (*node, *syntaxError) {
	n := &node{kind: nMapLit}
	if !p.isOp("}") {
		for {
			k, err := p.expr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(":"); err != nil {
				return nil, err
			}
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			n.keys, n.args = append(n.keys, k), append(n.args, v)
			if !p.isOp(",") {
				break
			}
			p.next()
		}
	}
	end, err := p.expect("}")
	if err != nil {
		return nil, err
	}
	n.span = Span{open.span.Start, end.span.End}
	return n, nil
}
