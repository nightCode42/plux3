// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package jcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SyntaxError reports malformed or non-interoperable JSON at a byte offset.
type SyntaxError struct {
	// Offset is the byte offset of the problem in the input.
	Offset int
	// Msg describes the problem.
	Msg string
}

// Error formats the error with its offset.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("jcs: offset %d: %s", e.Offset, e.Msg)
}

// Parse parses one JSON text strictly and returns it as nil, bool,
// json.Number, string, []any or map[string]any. maxDepth bounds the nesting
// of arrays and objects; it must be positive.
func Parse(data []byte, maxDepth int) (any, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("jcs.Parse: maxDepth must be positive, got %d", maxDepth)
	}
	p := parser{data: data, maxDepth: maxDepth}
	p.skipSpace()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, p.errorf("unexpected data after the JSON value")
	}
	return v, nil
}

// parser is a recursive-descent JSON parser over a byte slice.
type parser struct {
	data     []byte
	pos      int
	maxDepth int
}

// errorf returns a SyntaxError at the current position.
func (p *parser) errorf(format string, args ...any) error {
	return &SyntaxError{Offset: p.pos, Msg: fmt.Sprintf(format, args...)}
}

// skipSpace skips the four JSON whitespace characters.
func (p *parser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value parses any JSON value at the given nesting depth.
func (p *parser) value(depth int) (any, error) {
	if p.pos >= len(p.data) {
		return nil, p.errorf("unexpected end of input")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		return p.string()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case p.literal("true"):
		return true, nil
	case p.literal("false"):
		return false, nil
	case p.literal("null"):
		return nil, nil
	default:
		return nil, p.errorf("unexpected character %q", c)
	}
}

// literal consumes word if the input continues with it.
func (p *parser) literal(word string) bool {
	if bytes.HasPrefix(p.data[p.pos:], []byte(word)) {
		p.pos += len(word)
		return true
	}
	return false
}

// object parses an object, rejecting duplicate member names.
func (p *parser) object(depth int) (any, error) {
	if depth > p.maxDepth {
		return nil, p.errorf("nesting deeper than %d", p.maxDepth)
	}
	p.pos++ // {
	obj := map[string]any{}
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return obj, nil
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return nil, p.errorf("expected a member name")
		}
		keyPos := p.pos
		key, err := p.string()
		if err != nil {
			return nil, err
		}
		if _, dup := obj[key]; dup {
			return nil, &SyntaxError{Offset: keyPos, Msg: fmt.Sprintf("duplicate member name %q", key)}
		}
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return nil, p.errorf("expected ':' after a member name")
		}
		p.pos++
		p.skipSpace()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		obj[key] = v
		if done, err := p.endOfContainer('}'); done || err != nil {
			return obj, err
		}
	}
}

// array parses an array.
func (p *parser) array(depth int) (any, error) {
	if depth > p.maxDepth {
		return nil, p.errorf("nesting deeper than %d", p.maxDepth)
	}
	p.pos++ // [
	arr := []any{}
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		p.skipSpace()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		if done, err := p.endOfContainer(']'); done || err != nil {
			return arr, err
		}
	}
}

// endOfContainer consumes a ',' (not done) or the closing byte (done).
func (p *parser) endOfContainer(closing byte) (bool, error) {
	p.skipSpace()
	if p.pos >= len(p.data) {
		return false, p.errorf("unexpected end of input")
	}
	switch p.data[p.pos] {
	case ',':
		p.pos++
		return false, nil
	case closing:
		p.pos++
		return true, nil
	default:
		return false, p.errorf("expected ',' or %q", closing)
	}
}

// number parses a number with the JSON grammar and keeps its text.
func (p *parser) number() (any, error) {
	start := p.pos
	if p.data[p.pos] == '-' {
		p.pos++
	}
	switch {
	case p.pos < len(p.data) && p.data[p.pos] == '0':
		p.pos++
	case p.pos < len(p.data) && p.data[p.pos] >= '1' && p.data[p.pos] <= '9':
		p.digits()
	default:
		return nil, p.errorf("invalid number")
	}
	isInteger, err := p.fractionAndExponent()
	if err != nil {
		return nil, err
	}
	text := string(p.data[start:p.pos])
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, &SyntaxError{Offset: start, Msg: fmt.Sprintf("number %s is not a finite IEEE 754 double", text)}
	}
	if isInteger {
		if canonical, ok := stableInteger(text, f); !ok {
			return nil, &SyntaxError{Offset: start, Msg: fmt.Sprintf(
				"integer %s would change to %s when canonicalised; write it as a string or in canonical form", text, canonical)}
		}
	}
	return json.Number(text), nil
}

// fractionAndExponent consumes the optional fraction and exponent of a
// number and reports whether neither was present.
func (p *parser) fractionAndExponent() (isInteger bool, err error) {
	isInteger = true
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		if !p.digits() {
			return false, p.errorf("expected digits after '.'")
		}
		isInteger = false
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		if !p.digits() {
			return false, p.errorf("expected digits in the exponent")
		}
		isInteger = false
	}
	return isInteger, nil
}

// stableInteger reports whether canonicalising the integer literal text,
// whose double value is f, reproduces it, so that no integer silently
// changes (RFC 7493 §2.2: beyond 2^53 doubles lose integer precision). It
// also returns the canonical form.
func stableInteger(text string, f float64) (string, bool) {
	canonical, err := FormatNumber(f)
	if err != nil {
		return "", false
	}
	return canonical, canonical == text || text == "-0"
}

// digits consumes one or more decimal digits and reports whether any were read.
func (p *parser) digits() bool {
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		p.pos++
	}
	return p.pos > start
}

// string parses a string, validating UTF-8 and surrogate pairs.
func (p *parser) string() (string, error) {
	p.pos++ // opening quote
	var b strings.Builder
	for {
		if p.pos >= len(p.data) {
			return "", p.errorf("unterminated string")
		}
		c := p.data[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c == '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		case c < 0x20:
			return "", p.errorf("control character U+%04X must be escaped", c)
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			p.pos++
		default:
			r, size := utf8.DecodeRune(p.data[p.pos:])
			if r == utf8.RuneError && size <= 1 {
				return "", p.errorf("invalid UTF-8")
			}
			b.WriteRune(r)
			p.pos += size
		}
	}
}

// escape decodes one escape sequence starting at the backslash.
func (p *parser) escape(b *strings.Builder) error {
	if p.pos+1 >= len(p.data) {
		return p.errorf("unterminated escape")
	}
	c := p.data[p.pos+1]
	if out, ok := simpleEscape(c); ok {
		b.WriteByte(out)
		p.pos += 2
		return nil
	}
	if c != 'u' {
		return p.errorf("invalid escape \\%c", c)
	}
	r, err := p.hex4(p.pos + 2)
	if err != nil {
		return err
	}
	p.pos += 6
	if utf16.IsSurrogate(r) {
		if r >= 0xDC00 || p.pos+1 >= len(p.data) || p.data[p.pos] != '\\' || p.data[p.pos+1] != 'u' {
			return &SyntaxError{Offset: p.pos - 6, Msg: fmt.Sprintf("unpaired surrogate \\u%04x", r)}
		}
		low, err := p.hex4(p.pos + 2)
		if err != nil {
			return err
		}
		if low < 0xDC00 || low > 0xDFFF {
			return &SyntaxError{Offset: p.pos - 6, Msg: fmt.Sprintf("unpaired surrogate \\u%04x", r)}
		}
		r = utf16.DecodeRune(r, low)
		p.pos += 6
	}
	b.WriteRune(r)
	return nil
}

// simpleEscape returns the byte a one-character escape stands for.
func simpleEscape(c byte) (byte, bool) {
	switch c {
	case '"', '\\', '/':
		return c, true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	default:
		return 0, false
	}
}

// hex4 decodes four hexadecimal digits at offset at.
func (p *parser) hex4(at int) (rune, error) {
	if at+4 > len(p.data) {
		return 0, &SyntaxError{Offset: at, Msg: "truncated \\u escape"}
	}
	var r rune
	for _, c := range p.data[at : at+4] {
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, &SyntaxError{Offset: at, Msg: "invalid \\u escape"}
		}
		r = r<<4 | rune(d)
	}
	return r, nil
}
