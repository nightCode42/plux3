// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package phone validates phone numbers by region (pxl.phone.v1) from the
// libphonenumber metadata in table_gen.go, generated from
// schema/pxl/phone.json by make gen. The algorithm is specified in
// schema/pxl/phone.md — libphonenumber's parse and isValidNumber on a
// stricter input syntax — and the Dart runtime implements the same; the
// metadata's patterns run on the pxl.regex.v1 engine.
package phone

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/pxl/regex"
)

// Bounds of libphonenumber's parser (schema/pxl/phone.md §3).
const (
	maxInput     = 250
	minNational  = 2
	maxNational  = 17
	maxCodeWidth = 3
)

// pattern is a metadata pattern, compiled on first use.
type pattern struct {
	src  string
	once func() *regex.Regexp
}

func newPattern(src string) *pattern {
	if src == "" {
		return nil
	}
	return &pattern{src: src, once: sync.OnceValue(func() *regex.Regexp {
		re, _ := regex.Compile(src, regex.Trusted) // every pattern compiles: TestTablePatternsCompile
		return re
	})}
}

// full reports whether the pattern matches all of s.
func (p *pattern) full(s string) bool {
	if p == nil {
		return false
	}
	re := p.once()
	return re != nil && re.FullMatch(s)
}

// prefix returns the leftmost-first match at the start of s, or nil.
func (p *pattern) prefix(s string) []int {
	if p == nil {
		return nil
	}
	re := p.once()
	if re == nil {
		return nil
	}
	return re.Prefix(s)
}

// desc is a pattern with the national lengths it applies to.
type desc struct {
	lengths []int
	pattern *pattern
}

// matches is libphonenumber's isNumberMatchingDesc.
func (d desc) matches(n string) bool {
	return (len(d.lengths) == 0 || slices.Contains(d.lengths, len(n))) && d.pattern.full(n)
}

// territory is one line of the table.
type territory struct {
	id                         string
	code                       int
	main                       bool
	leading, idd, nationalPref *pattern
	transform                  string
	general                    desc
	localOnly                  []int
	types                      []desc
}

// tables indexes the territories.
type tables struct {
	byRegion map[string]*territory
	byCode   map[int][]*territory // the main territory first
}

var load = sync.OnceValue(func() *tables { return parseTable(table) })

// parseTable reads the generated table (schema/pxl/phone.md §2).
func parseTable(text string) *tables {
	t := &tables{byRegion: map[string]*territory{}, byCode: map[int][]*territory{}}
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		f := strings.Split(line, " ")
		if len(f) < 10 || len(f)%2 != 0 {
			continue
		}
		for i, s := range f {
			if s == "~" {
				f[i] = ""
			}
		}
		code, _ := strconv.Atoi(f[1])
		x := &territory{
			id: f[0], code: code, main: f[2] == "1",
			leading: newPattern(f[3]), idd: newPattern(f[4]), nationalPref: newPattern(f[5]), transform: f[6],
			general: desc{lengths: lengths(f[8]), pattern: newPattern(f[7])}, localOnly: lengths(f[9]),
		}
		for i := 10; i+1 < len(f); i += 2 {
			x.types = append(x.types, desc{lengths: lengths(f[i]), pattern: newPattern(f[i+1])})
		}
		if x.id != "001" {
			t.byRegion[x.id] = x
		}
		if x.main {
			t.byCode[code] = append([]*territory{x}, t.byCode[code]...)
		} else {
			t.byCode[code] = append(t.byCode[code], x)
		}
	}
	return t
}

func lengths(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	for p := range strings.SplitSeq(s, ",") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// Known reports whether region, two ASCII letters in either case, has
// metadata.
func Known(region string) bool { return load().region(region) != nil }

func (t *tables) region(r string) *territory {
	if len(r) != 2 {
		return nil
	}
	return t.byRegion[strings.ToUpper(r)]
}

// IsValid reports whether number is a valid phone number, read with region
// as the default for numbers without a country calling code (schema/pxl/
// phone.md §3). An unknown region validates only international numbers.
func IsValid(number, region string) bool {
	if utf8.RuneCountInString(number) > maxInput {
		return false
	}
	digits, plus, ok := normalise(number)
	if !ok {
		return false
	}
	t := load()
	def := t.region(region)
	code, national, fromNumber, ok := t.extractCode(digits, plus, def)
	if !ok || len(national) < minNational {
		return false
	}
	meta := def
	if fromNumber {
		meta = t.byCode[code][0]
	}
	if p := stripNationalPrefix(national, meta); possibleOrLong(testLength(p, meta)) {
		national = p
	}
	if len(national) < minNational || len(national) > maxNational {
		return false
	}
	g := t.regionForNumber(code, national)
	return g != nil && g.valid(national)
}

// normalise removes separators and returns the digits and whether a "+"
// preceded them.
func normalise(s string) (string, bool, bool) {
	var b strings.Builder
	plus := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && !plus && b.Len() == 0:
			plus = true
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')' || r == '/' || r == ' ':
		default:
			return "", false, false
		}
	}
	return b.String(), plus, b.Len() > 0
}

// extractCode is libphonenumber's maybeExtractCountryCode: it returns the
// calling code, the national number, whether the code came from the
// number, and false when the number cannot be read.
func (t *tables) extractCode(num string, plus bool, def *territory) (int, string, bool, bool) {
	if plus {
		return t.withCode(num)
	}
	if def == nil {
		return 0, "", false, false
	}
	if m := def.idd.prefix(num); m != nil && (m[1] == len(num) || num[m[1]] != '0') {
		return t.withCode(num[m[1]:])
	}
	cc := strconv.Itoa(def.code)
	if rest, ok := strings.CutPrefix(num, cc); ok {
		p := stripNationalPrefix(rest, def)
		if !def.general.pattern.full(num) && def.general.pattern.full(p) || testLength(num, def) == tooLong {
			return def.code, p, true, true
		}
	}
	return def.code, num, false, true
}

// withCode reads a calling code at the start of digits that follow "+" or
// an international prefix.
func (t *tables) withCode(s string) (int, string, bool, bool) {
	if len(s) <= minNational || s[0] == '0' {
		return 0, "", false, false
	}
	for i := 1; i <= maxCodeWidth && i <= len(s); i++ {
		code, _ := strconv.Atoi(s[:i])
		if _, ok := t.byCode[code]; ok {
			return code, s[i:], true, true
		}
	}
	return 0, "", false, false
}

// stripNationalPrefix is libphonenumber's
// maybeStripNationalPrefixAndCarrierCode.
func stripNationalPrefix(p string, m *territory) string {
	if p == "" || m.nationalPref == nil {
		return p
	}
	g := m.nationalPref.prefix(p)
	if g == nil {
		return p
	}
	viable := m.general.pattern.full(p)
	last := len(g)/2 - 1
	candidate := p[g[1]:]
	if m.transform != "" && g[2*last] >= 0 {
		candidate = expand(m.transform, g, p) + candidate
	}
	if viable && !m.general.pattern.full(candidate) {
		return p
	}
	return candidate
}

// expand substitutes $1–$9 of a transform rule with the groups of g.
func expand(rule string, g []int, s string) string {
	var b strings.Builder
	for i := 0; i < len(rule); i++ {
		if rule[i] == '$' && i+1 < len(rule) {
			n := int(rule[i+1] - '0')
			if 2*n+1 < len(g) && g[2*n] >= 0 {
				b.WriteString(s[g[2*n]:g[2*n+1]])
			}
			i++
			continue
		}
		b.WriteByte(rule[i])
	}
	return b.String()
}

// lengthResult is libphonenumber's ValidationResult.
type lengthResult uint8

const (
	possible lengthResult = iota
	localOnly
	tooShort
	tooLong
	invalidLength
)

func possibleOrLong(r lengthResult) bool { return r == possible || r == tooLong }

// testLength is libphonenumber's testNumberLength for an unknown type.
func testLength(n string, m *territory) lengthResult {
	ls := m.general.lengths
	switch l := len(n); {
	case len(ls) == 0:
		return invalidLength
	case slices.Contains(m.localOnly, l):
		return localOnly
	case l == ls[0]:
		return possible
	case l < ls[0]:
		return tooShort
	case l > ls[len(ls)-1]:
		return tooLong
	case slices.Contains(ls, l):
		return possible
	}
	return invalidLength
}

// regionForNumber is libphonenumber's getRegionCodeForNumber.
func (t *tables) regionForNumber(code int, n string) *territory {
	list := t.byCode[code]
	if len(list) == 1 {
		return list[0]
	}
	for _, r := range list {
		if r.leading != nil {
			if r.leading.prefix(n) != nil {
				return r
			}
		} else if r.valid(n) {
			return r
		}
	}
	return nil
}

// valid is getNumberTypeHelper(n) != UNKNOWN.
func (x *territory) valid(n string) bool {
	if !x.general.matches(n) {
		return false
	}
	for _, d := range x.types {
		if d.matches(n) {
			return true
		}
	}
	return false
}
