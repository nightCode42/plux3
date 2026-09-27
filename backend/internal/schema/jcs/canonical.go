// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package jcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Canonicalize parses a JSON text strictly (see Parse) and returns its RFC
// 8785 canonical form.
func Canonicalize(data []byte, maxDepth int) ([]byte, error) {
	v, err := Parse(data, maxDepth)
	if err != nil {
		return nil, err
	}
	return Marshal(v)
}

// Format parses a JSON text strictly and writes it in canonical order and
// number form with two-space indentation and a final newline, for files in
// the Git layout (SCH-006).
func Format(data []byte, maxDepth int) ([]byte, error) {
	v, err := Parse(data, maxDepth)
	if err != nil {
		return nil, err
	}
	w := writer{indent: "  "}
	if err := w.value(v, 0); err != nil {
		return nil, err
	}
	w.buf.WriteByte('\n')
	return w.buf.Bytes(), nil
}

// Marshal writes the canonical form of a tree as produced by Parse or by
// encoding/json with UseNumber: nil, bool, json.Number, float64, string,
// []any and map[string]any.
func Marshal(v any) ([]byte, error) {
	var w writer
	if err := w.value(v, 0); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// writer serialises a tree; indent is empty for the canonical form.
type writer struct {
	buf    bytes.Buffer
	indent string
}

// value writes one value at the given indentation level.
func (w *writer) value(v any, level int) error {
	switch x := v.(type) {
	case nil:
		w.buf.WriteString("null")
	case bool:
		w.buf.WriteString(strconv.FormatBool(x))
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return fmt.Errorf("jcs.Marshal: number %q: %w", x, err)
		}
		return w.number(f)
	case float64:
		return w.number(x)
	case string:
		writeString(&w.buf, x)
	case []any:
		return w.array(x, level)
	case map[string]any:
		return w.object(x, level)
	default:
		return fmt.Errorf("jcs.Marshal: unsupported type %T", v)
	}
	return nil
}

// number writes a finite double in ECMAScript form.
func (w *writer) number(f float64) error {
	s, err := FormatNumber(f)
	if err != nil {
		return err
	}
	w.buf.WriteString(s)
	return nil
}

// array writes an array, one element per line when indenting.
func (w *writer) array(a []any, level int) error {
	if len(a) == 0 {
		w.buf.WriteString("[]")
		return nil
	}
	w.buf.WriteByte('[')
	for i, e := range a {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.newline(level + 1)
		if err := w.value(e, level+1); err != nil {
			return err
		}
	}
	w.newline(level)
	w.buf.WriteByte(']')
	return nil
}

// object writes an object with members sorted by UTF-16 code units.
func (w *writer) object(m map[string]any, level int) error {
	if len(m) == 0 {
		w.buf.WriteString("{}")
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, CompareUTF16)
	w.buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.newline(level + 1)
		writeString(&w.buf, k)
		w.buf.WriteByte(':')
		if w.indent != "" {
			w.buf.WriteByte(' ')
		}
		if err := w.value(m[k], level+1); err != nil {
			return err
		}
	}
	w.newline(level)
	w.buf.WriteByte('}')
	return nil
}

// newline starts a new indented line when formatting; canonical output has
// no whitespace.
func (w *writer) newline(level int) {
	if w.indent == "" {
		return
	}
	w.buf.WriteByte('\n')
	w.buf.WriteString(strings.Repeat(w.indent, level))
}

// CompareUTF16 orders strings by their UTF-16 code units, as RFC 8785 §3.2.3
// requires for member names.
func CompareUTF16(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			ua, ub := utf16Units(ra), utf16Units(rb)
			if c := slices.Compare(ua[:], ub[:]); c != 0 {
				return c
			}
		}
		a, b = a[na:], b[nb:]
	}
	return len(a) - len(b)
}

// utf16Units returns the one or two UTF-16 code units of r; a single unit is
// followed by zero, which sorts before any second unit.
func utf16Units(r rune) [2]uint16 {
	if r < 0x10000 {
		return [2]uint16{uint16(r), 0} //nolint:gosec // G115: r < 0x10000 fits.
	}
	hi, lo := utf16.EncodeRune(r)
	return [2]uint16{uint16(hi), uint16(lo)} //nolint:gosec // G115: surrogates fit in 16 bits.
}

// writeString writes s with the minimal escaping of RFC 8785 §3.2.2.2.
func writeString(buf *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			buf.WriteString(`\"`)
		case c == '\\':
			buf.WriteString(`\\`)
		case c == '\b':
			buf.WriteString(`\b`)
		case c == '\f':
			buf.WriteString(`\f`)
		case c == '\n':
			buf.WriteString(`\n`)
		case c == '\r':
			buf.WriteString(`\r`)
		case c == '\t':
			buf.WriteString(`\t`)
		case c < 0x20:
			buf.WriteString(`\u00`)
			buf.WriteByte(hex[c>>4])
			buf.WriteByte(hex[c&0xF])
		default:
			buf.WriteByte(c)
		}
	}
	buf.WriteByte('"')
}

// FormatNumber formats a finite double with the ECMAScript Number.prototype
// .toString algorithm (ECMA-262 §7.1.12.1), as RFC 8785 §3.2.2.3 requires:
// the shortest digits that round-trip, in fixed notation for exponents in
// [-6, 21) and exponential notation otherwise. Negative zero is "0".
func FormatNumber(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("jcs.FormatNumber: %v is not representable in JSON", f)
	}
	if f == 0 {
		return "0", nil
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// Shortest round-trip digits d1.d2…dk × 10^exp.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expText, _ := strings.Cut(e, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(expText)
	k, n := len(digits), exp+1 // value = 0.d1…dk × 10^n
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k), nil
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:], nil
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits, nil
	}
	expSign := "+"
	if n-1 < 0 {
		expSign = "-"
	}
	frac := ""
	if k > 1 {
		frac = "." + digits[1:]
	}
	return sign + digits[:1] + frac + "e" + expSign + strconv.Itoa(abs(n-1)), nil
}

// abs returns the absolute value of an int.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
