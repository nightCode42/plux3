// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// Value is a PXL value: nil (null), bool, int64, float64, string (also
// enums, assets and routes), decimal.Decimal, Money, Date, DateTime,
// Duration, Color, List or Map (also objects). Values are immutable.
type Value = any

// Money is an exact amount in an ISO 4217 currency.
type Money struct {
	Amount   decimal.Decimal
	Currency string
}

// String returns the amount and the code, e.g. "12.50 EUR".
func (m Money) String() string { return m.Amount.String() + " " + m.Currency }

// Color is a color as red, green, blue and alpha bytes, most significant
// first.
type Color uint32

// String returns #RRGGBBAA.
func (c Color) String() string { return fmt.Sprintf("#%08X", uint32(c)) }

// List is a list value.
type List []Value

// Map is a map or object value; iteration is in code-point key order.
type Map map[string]Value

// minorUnits returns the minor units of an ISO 4217 currency.
func minorUnits(code string) (int, bool) {
	i, ok := slices.BinarySearchFunc(currencies[:], code, func(c currencyDef, k string) int { return strings.Compare(c.code, k) })
	if !ok {
		return 0, false
	}
	return currencies[i].minor, true
}

// equal is deep equality of two values of one type; visits counts nested
// values compared, for the operation budget.
func equal(a, b Value, visits *int64) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case decimal.Decimal:
		y, ok := b.(decimal.Decimal)
		return ok && x.Cmp(y) == 0
	case Money:
		y, ok := b.(Money)
		return ok && x.Currency == y.Currency && x.Amount.Cmp(y.Amount) == 0
	case DateTime:
		y, ok := b.(DateTime)
		return ok && x.Millis == y.Millis
	case List:
		y, ok := b.(List)
		return ok && listEqual(x, y, visits)
	case Map:
		y, ok := b.(Map)
		return ok && mapEqual(x, y, visits)
	default:
		return a == b
	}
}

// listEqual compares lists element by element.
func listEqual(x, y List, visits *int64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		*visits++
		if !equal(x[i], y[i], visits) {
			return false
		}
	}
	return true
}

// mapEqual compares maps entry by entry in key order.
func mapEqual(x, y Map, visits *int64) bool {
	if len(x) != len(y) {
		return false
	}
	for _, k := range sortedKeys(x) {
		*visits++
		v, present := y[k]
		if !present || !equal(x[k], v, visits) {
			return false
		}
	}
	return true
}

// compareValues orders two values of an ordered type; money values must
// share a currency. Operands of the wrong type mean malformed code.
func compareValues(kind CmpKind, a, b Value) (int, *EvalError) {
	switch kind {
	case CmpInt:
		return ordered[int64](a, b)
	case CmpDouble:
		return ordered[float64](a, b)
	case CmpString:
		return ordered[string](a, b) // UTF-8 byte order is code-point order
	case CmpDate:
		return ordered[Date](a, b)
	case CmpDuration:
		return ordered[Duration](a, b)
	case CmpDecimal:
		x, err1 := typed[decimal.Decimal](a)
		y, err2 := typed[decimal.Decimal](b)
		if err := first(err1, err2); err != nil {
			return 0, err
		}
		return x.Cmp(y), nil
	case CmpMoney:
		x, err1 := typed[Money](a)
		y, err2 := typed[Money](b)
		if err := first(err1, err2); err != nil {
			return 0, err
		}
		if x.Currency != y.Currency {
			return 0, evalErr(ErrorCurrencyMismatch, "%s and %s", x.Currency, y.Currency)
		}
		return x.Amount.Cmp(y.Amount), nil
	case CmpDateTime:
		x, err1 := typed[DateTime](a)
		y, err2 := typed[DateTime](b)
		if err := first(err1, err2); err != nil {
			return 0, err
		}
		return cmp.Compare(x.Millis, y.Millis), nil
	default:
		return 0, evalErr(ErrorInvalidProgram, "unknown comparison kind %d", kind)
	}
}

// ordered compares two values of an ordered Go type.
func ordered[T cmp.Ordered](a, b Value) (int, *EvalError) {
	x, err1 := typed[T](a)
	y, err2 := typed[T](b)
	if err := first(err1, err2); err != nil {
		return 0, err
	}
	return cmp.Compare(x, y), nil
}

// size is the cost weight of a value: code points of a string, entries of
// a collection, zero otherwise.
func size(v Value) int64 {
	switch x := v.(type) {
	case string:
		return int64(utf8.RuneCountInString(x))
	case List:
		return int64(len(x))
	case Map:
		return int64(len(x))
	default:
		return 0
	}
}

// formatDouble returns the ES6 text of a finite double.
func formatDouble(f float64) string {
	s, err := jcs.FormatNumber(f)
	if err != nil { // unreachable: PXL doubles are finite
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return s
}

// FromJSON converts a JSON value, decoded with json.Decoder.UseNumber, into
// a value of type t, following the literal forms of the document model
// (docs/reference/document-model.md §3).
func FromJSON(t *Type, v any) (Value, error) {
	if v == nil {
		if t.nullable || t.kind == KindNull {
			return nil, nil
		}
		return nil, fmt.Errorf("null is not a %s", t)
	}
	switch t.kind {
	case KindBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case KindInt, KindDouble, KindDuration:
		if n, ok := v.(json.Number); ok {
			return numberFromJSON(t, n)
		}
	case KindList:
		return listFromJSON(t, v)
	case KindMap, KindObject:
		return mapFromJSON(t, v)
	case KindMoney:
		return moneyFromJSON(v)
	default:
		if s, ok := v.(string); ok {
			return scalarFromString(t, s)
		}
	}
	return nil, fmt.Errorf("%v is not a %s", v, t)
}

// numberFromJSON converts a JSON number into an int, a double or a
// duration (whole milliseconds, not negative).
func numberFromJSON(t *Type, n json.Number) (Value, error) {
	switch t.kind {
	case KindDouble:
		if f, err := n.Float64(); err == nil && !math.IsInf(f, 0) {
			return f, nil
		}
	default:
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err == nil && t.kind == KindInt {
			return i, nil
		}
		if err == nil && i >= 0 {
			return Duration(i), nil
		}
	}
	return nil, fmt.Errorf("%s is not a %s", n, t)
}

// listFromJSON converts a JSON array.
func listFromJSON(t *Type, v any) (Value, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("want a list for %s", t)
	}
	out := make(List, len(arr))
	for i, e := range arr {
		x, err := FromJSON(t.elem, e)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out[i] = x
	}
	return out, nil
}

// mapFromJSON converts a JSON object into a map or an object; absent
// nullable fields of an object become null.
func mapFromJSON(t *Type, v any) (Value, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("want an object for %s", t)
	}
	out := make(Map, len(obj))
	for _, k := range sortedKeys(obj) {
		et := t.elem
		if t.kind == KindObject {
			et = t.named.Fields[k]
			if et == nil {
				return nil, fmt.Errorf("%s has no field %q", t.named.Name, k)
			}
		}
		x, err := FromJSON(et, obj[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = x
	}
	if t.kind == KindObject {
		for _, f := range t.named.FieldOrder {
			if _, present := out[f]; !present {
				if !t.named.Fields[f].nullable {
					return nil, fmt.Errorf("%s requires field %q", t.named.Name, f)
				}
				out[f] = nil
			}
		}
	}
	return out, nil
}

// moneyFromJSON converts {"amount": "<decimal>", "currency": "<code>"}.
func moneyFromJSON(v any) (Value, error) {
	obj, ok := v.(map[string]any)
	amount, ok1 := obj["amount"].(string)
	code, ok2 := obj["currency"].(string)
	if !ok || !ok1 || !ok2 || len(obj) != 2 {
		return nil, fmt.Errorf("want {\"amount\", \"currency\"} for money")
	}
	d, err := decimal.Parse(amount)
	if _, known := minorUnits(code); err != nil || !known {
		return nil, fmt.Errorf("invalid money %v", v)
	}
	return Money{Amount: d, Currency: code}, nil
}

// scalarFromString converts the types written as JSON strings.
func scalarFromString(t *Type, s string) (Value, error) {
	var (
		v  Value
		ok = true
	)
	switch t.kind {
	case KindString, KindAsset, KindRoute:
		v = s
	case KindEnum:
		v, ok = s, t.named.hasMember(s)
	case KindDecimal:
		d, err := decimal.Parse(s)
		v, ok = d, err == nil
	case KindDate:
		v, ok = parseDate(s)
	case KindDateTime:
		v, ok = parseDateTime(s)
	case KindColor:
		v, ok = parseColor(s)
	default:
		ok = false
	}
	if !ok {
		return nil, fmt.Errorf("%q is not a %s", s, t)
	}
	return v, nil
}

// parseColor reads #RRGGBB or #RRGGBBAA.
func parseColor(s string) (Color, bool) {
	if len(s) != 7 && len(s) != 9 || s[0] != '#' {
		return 0, false
	}
	hex := s[1:]
	if len(hex) == 6 {
		hex += "FF"
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	return Color(n), err == nil
}

// ToJSON converts a value of type t into its JSON form (the inverse of
// FromJSON), using json.Number for numbers.
func ToJSON(t *Type, v Value) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case int64:
		return json.Number(strconv.FormatInt(x, 10))
	case float64:
		return json.Number(formatDouble(x))
	case decimal.Decimal:
		return x.String()
	case Money:
		return map[string]any{"amount": x.Amount.String(), "currency": x.Currency}
	case Date, DateTime, Color:
		return fmt.Sprint(x)
	case Duration:
		return json.Number(strconv.FormatInt(int64(x), 10))
	case List:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = ToJSON(elemType(t, ""), e)
		}
		return out
	case Map:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = ToJSON(elemType(t, k), e)
		}
		return out
	default:
		return x
	}
}

// elemType returns the type of an element or field of t.
func elemType(t *Type, field string) *Type {
	if t != nil && t.kind == KindObject {
		return t.named.Fields[field]
	}
	if t != nil && t.elem != nil {
		return t.elem
	}
	return typeNever
}
