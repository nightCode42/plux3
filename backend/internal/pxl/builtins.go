// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// builtinFn implements one overload; arguments have the declared types.
type builtinFn func(m *vm, args []Value) (Value, *EvalError)

// builtins returns the implementations indexed by overload ID − 1; nil for
// overloads of groups this evaluator does not implement (ADR-0009) and for
// those the checker expands inline.
var builtins = sync.OnceValue(func() []builtinFn {
	impl := implementations()
	out := make([]builtinFn, len(stdOverloads))
	for i, def := range stdOverloads {
		out[i] = impl[overloadKey(def)]
	}
	return out
})

// overloadKey identifies an overload by name and parameter types.
func overloadKey(def overloadDef) string {
	types := make([]string, len(def.params))
	for i, p := range def.params {
		types[i] = p.typ
	}
	return def.name + "(" + strings.Join(types, ",") + ")"
}

// arg returns argument i as a T.
func arg[T any](args []Value, i int) T {
	v, _ := args[i].(T) // the checker guarantees the type; a zero value is harmless
	return v
}

// implementations maps overload keys to their implementations.
func implementations() map[string]builtinFn {
	impl := map[string]builtinFn{}
	for _, table := range []map[string]builtinFn{stringFns(), numberFns(), decimalFns(), moneyFns(), dateFns(), collectionFns(), logicFns(), regexFns()} {
		for k, f := range table {
			impl[k] = f
		}
	}
	return impl
}

// stringFns implements the string functions on code points.
func stringFns() map[string]builtinFn {
	return map[string]builtinFn{
		"len(string)": func(_ *vm, a []Value) (Value, *EvalError) { return just(size(a[0])) },
		"upper(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.Map(unicode.ToUpper, arg[string](a, 0)))
		},
		"lower(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.Map(unicode.ToLower, arg[string](a, 0)))
		},
		"trim(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.TrimFunc(arg[string](a, 0), func(r rune) bool { return unicode.Is(unicode.White_Space, r) }))
		},
		"contains(string,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.Contains(arg[string](a, 0), arg[string](a, 1)))
		},
		"startsWith(string,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.HasPrefix(arg[string](a, 0), arg[string](a, 1)))
		},
		"endsWith(string,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(strings.HasSuffix(arg[string](a, 0), arg[string](a, 1)))
		},
		"replace(string,string,string)": replace,
		"split(string,string)":          split,
		"join(list<string>,string)":     join,
		"substring(string,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			return substring(arg[string](a, 0), arg[int64](a, 1), size(a[0]))
		},
		"substring(string,int,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			return substring(arg[string](a, 0), arg[int64](a, 1), arg[int64](a, 2))
		},
		"padLeft(string,int,string)":  func(m *vm, a []Value) (Value, *EvalError) { return pad(m, a, true) },
		"padRight(string,int,string)": func(m *vm, a []Value) (Value, *EvalError) { return pad(m, a, false) },
		"format.iban(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			s := strings.ToUpper(strings.ReplaceAll(arg[string](a, 0), " ", ""))
			var b strings.Builder
			for i, r := range []rune(s) {
				if i > 0 && i%4 == 0 {
					b.WriteByte(' ')
				}
				b.WriteRune(r)
			}
			return just(b.String())
		},
	}
}

// replace substitutes every non-overlapping occurrence, checking the
// result size before building it.
func replace(m *vm, a []Value) (Value, *EvalError) {
	s, old, repl := arg[string](a, 0), arg[string](a, 1), arg[string](a, 2)
	if old == "" {
		return s, nil
	}
	n := int64(strings.Count(s, old))
	if size(s)+n*(size(repl)-size(old)) > m.lim.StringLength {
		return nil, evalErr(ErrorSizeLimit, "a string longer than %d code points", m.lim.StringLength)
	}
	return strings.ReplaceAll(s, old, repl), nil
}

// split splits at a separator, or into code points for an empty one.
func split(m *vm, a []Value) (Value, *EvalError) {
	s, sep := arg[string](a, 0), arg[string](a, 1)
	n := int64(strings.Count(s, sep)) + 1
	if sep == "" {
		n = size(s)
	}
	if n > m.lim.CollectionSize {
		return nil, evalErr(ErrorSizeLimit, "a list of more than %d items", m.lim.CollectionSize)
	}
	parts := strings.Split(s, sep)
	out := make(List, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out, nil
}

// join joins strings with a separator.
func join(m *vm, a []Value) (Value, *EvalError) {
	items := arg[List](a, 0)
	parts := make([]string, len(items))
	var total int64
	for i, it := range items {
		parts[i] = arg[string](List{it}, 0)
		total += size(parts[i])
	}
	if total+int64(max(len(items)-1, 0))*size(a[1]) > m.lim.StringLength {
		return nil, evalErr(ErrorSizeLimit, "a string longer than %d code points", m.lim.StringLength)
	}
	return strings.Join(parts, arg[string](a, 1)), nil
}

// substring returns code points [start, end).
func substring(s string, start, end int64) (Value, *EvalError) {
	n := size(s)
	if start < 0 || start > end || end > n {
		return nil, evalErr(ErrorIndexOutOfRange, "substring(%d, %d) of %d code points", start, end, n)
	}
	r := []rune(s)
	return string(r[start:end]), nil
}

// pad pads with a single code point up to a width.
func pad(m *vm, a []Value, left bool) (Value, *EvalError) {
	s, width, p := arg[string](a, 0), arg[int64](a, 1), arg[string](a, 2)
	if utf8.RuneCountInString(p) != 1 {
		return nil, evalErr(ErrorInvalidArgument, "the padding must be one code point, not %q", p)
	}
	if width > m.lim.StringLength {
		return nil, evalErr(ErrorSizeLimit, "a string longer than %d code points", m.lim.StringLength)
	}
	missing := width - size(s)
	if missing <= 0 {
		return s, nil
	}
	fill := strings.Repeat(p, int(missing))
	if left {
		return fill + s, nil
	}
	return s + fill, nil
}

// scaleArg validates a scale argument.
func scaleArg(m *vm, v Value) (uint16, *EvalError) {
	s := arg[int64](List{v}, 0)
	if s < 0 {
		return 0, evalErr(ErrorInvalidArgument, "negative scale %d", s)
	}
	if s > m.lim.DecimalDigits || s > math.MaxUint16 {
		return 0, evalErr(ErrorSizeLimit, "scale %d exceeds %d digits", s, m.lim.DecimalDigits)
	}
	return uint16(s), nil
}

// modeArg reads a RoundingMode argument.
func modeArg(v Value) (decimal.RoundingMode, *EvalError) {
	mode, found := decimal.ParseRoundingMode(arg[string](List{v}, 0))
	if !found {
		return 0, evalErr(ErrorInvalidArgument, "unknown rounding mode %v", v)
	}
	return mode, nil
}

// minorScale returns the minor units of a money value's currency.
func minorScale(x Money) (uint16, *EvalError) {
	units, found := minorUnits(x.Currency)
	if !found {
		return 0, evalErr(ErrorUnknownCurrency, "%q", x.Currency)
	}
	return uint16(units), nil //nolint:gosec // G115: minor units are 0–4.
}

// roundDouble rounds a double with mode to an int.
func roundDouble(f float64, mode decimal.RoundingMode) (Value, *EvalError) {
	d, err := decimal.FromFloat64(f)
	if err != nil {
		return nil, evalErr(ErrorNonFinite, "%v", f)
	}
	return decToInt(d.Round(0, mode))
}

// decToInt converts an integral decimal to an int.
func decToInt(d decimal.Decimal) (Value, *EvalError) {
	n, fits := d.Int64()
	if !fits {
		return nil, evalErr(ErrorOverflow, "%s is outside the int range", d)
	}
	return n, nil
}

// numberFns implements the number functions.
func numberFns() map[string]builtinFn {
	f := map[string]builtinFn{
		"abs(int)": func(_ *vm, a []Value) (Value, *EvalError) {
			x := arg[int64](a, 0)
			if x == math.MinInt64 {
				return nil, evalErr(ErrorOverflow, "abs(%d)", x)
			}
			return just(max(x, -x))
		},
		"abs(double)":  func(_ *vm, a []Value) (Value, *EvalError) { return just(math.Abs(arg[float64](a, 0))) },
		"abs(decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[decimal.Decimal](a, 0).Abs()) },
		"abs(money)": func(_ *vm, a []Value) (Value, *EvalError) {
			x := arg[Money](a, 0)
			return just(Money{Amount: x.Amount.Abs(), Currency: x.Currency})
		},
		"round(double)": func(_ *vm, a []Value) (Value, *EvalError) { return roundDouble(arg[float64](a, 0), decimal.HalfEven) },
		"floor(double)": func(_ *vm, a []Value) (Value, *EvalError) { return roundDouble(arg[float64](a, 0), decimal.Floor) },
		"ceil(double)":  func(_ *vm, a []Value) (Value, *EvalError) { return roundDouble(arg[float64](a, 0), decimal.Ceiling) },
		"int(double)":   func(_ *vm, a []Value) (Value, *EvalError) { return roundDouble(arg[float64](a, 0), decimal.Down) },
		"int(decimal)": func(_ *vm, a []Value) (Value, *EvalError) {
			return decToInt(arg[decimal.Decimal](a, 0).Round(0, decimal.Down))
		},
		"int(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			s := arg[string](a, 0)
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || strings.HasPrefix(s, "+") {
				return nil, evalErr(ErrorInvalidArgument, "%q is not an int", s)
			}
			return n, nil
		},
		"double(int)":     func(_ *vm, a []Value) (Value, *EvalError) { return just(float64(arg[int64](a, 0))) },
		"double(decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return finite(arg[decimal.Decimal](a, 0).Float64()) },
		"double(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			s := arg[string](a, 0)
			if !jsonNumber(s) {
				return nil, evalErr(ErrorInvalidArgument, "%q is not a number", s)
			}
			v, _ := strconv.ParseFloat(s, 64)
			return finite(v)
		},
	}
	for _, t := range []string{"int", "double", "decimal", "money", "duration"} {
		f["min("+t+","+t+")"] = extremum(t, -1)
		f["max("+t+","+t+")"] = extremum(t, 1)
	}
	for _, t := range []string{"int", "double", "decimal"} {
		f["clamp("+t+","+t+","+t+")"] = clamp(t)
	}
	return f
}

// cmpKinds maps signature type names to comparison kinds.
var cmpKinds = map[string]CmpKind{
	"int": CmpInt, "double": CmpDouble, "decimal": CmpDecimal, "money": CmpMoney, "string": CmpString,
	"date": CmpDate, "dateTime": CmpDateTime, "duration": CmpDuration,
}

// extremum returns min (sign −1) or max (sign 1) of two values; ties keep
// the first.
func extremum(t string, sign int) builtinFn {
	return func(_ *vm, a []Value) (Value, *EvalError) {
		c, err := compareValues(cmpKinds[t], a[0], a[1])
		if err != nil {
			return nil, err
		}
		if c*sign < 0 {
			return a[1], nil
		}
		return a[0], nil
	}
}

// clamp limits x to [lo, hi].
func clamp(t string) builtinFn {
	return func(_ *vm, a []Value) (Value, *EvalError) {
		kind := cmpKinds[t]
		if c, err := compareValues(kind, a[1], a[2]); err != nil || c > 0 {
			return nil, first(err, evalErr(ErrorInvalidArgument, "clamp bounds %v > %v", a[1], a[2]))
		}
		if c, _ := compareValues(kind, a[0], a[1]); c < 0 {
			return a[1], nil
		}
		if c, _ := compareValues(kind, a[0], a[2]); c > 0 {
			return a[2], nil
		}
		return a[0], nil
	}
}

// jsonNumber reports whether s follows the JSON number grammar.
func jsonNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	start := i
	i = skipDigits(s, i)
	if i == start || (s[start] == '0' && i-start > 1) {
		return false
	}
	if i < len(s) && s[i] == '.' {
		if j := skipDigits(s, i+1); j > i+1 {
			i = j
		} else {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		j := skipDigits(s, i)
		if j == i {
			return false
		}
		i = j
	}
	return i == len(s)
}

// skipDigits returns the offset after the ASCII digits at i.
func skipDigits(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

// decimalFns implements the decimal and money functions.
func decimalFns() map[string]builtinFn {
	f := map[string]builtinFn{
		"decimal(int)": func(_ *vm, a []Value) (Value, *EvalError) { return just(decimal.FromInt64(arg[int64](a, 0))) },
		"decimal(double)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, err := decimal.FromFloat64(arg[float64](a, 0))
			if err != nil {
				return nil, evalErr(ErrorNonFinite, "%v", a[0])
			}
			return d, nil
		},
		"decimal(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, err := decimal.Parse(arg[string](a, 0))
			if err != nil {
				return nil, evalErr(ErrorInvalidArgument, "%q is not a decimal", a[0])
			}
			return d, nil
		},
		"add(decimal,decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpAddDec, a[0], a[1]) },
		"sub(decimal,decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpSubDec, a[0], a[1]) },
		"mul(decimal,decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpMulDec, a[0], a[1]) },
		"div(decimal,decimal,int)": func(m *vm, a []Value) (Value, *EvalError) {
			return divDecimal(m, a[0], a[1], a[2], decimal.HalfEven)
		},
		"div(decimal,decimal,int,RoundingMode)": func(m *vm, a []Value) (Value, *EvalError) {
			mode, err := modeArg(a[3])
			if err != nil {
				return nil, err
			}
			return divDecimal(m, a[0], a[1], a[2], mode)
		},
		"round(decimal)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(arg[decimal.Decimal](a, 0).Round(0, decimal.HalfEven))
		},
		"round(decimal,int)": func(m *vm, a []Value) (Value, *EvalError) {
			s, err := scaleArg(m, a[1])
			return arg[decimal.Decimal](a, 0).Round(s, decimal.HalfEven), err
		},
		"round(decimal,int,RoundingMode)": func(m *vm, a []Value) (Value, *EvalError) {
			s, err := scaleArg(m, a[1])
			mode, err2 := modeArg(a[2])
			if err := first(err, err2); err != nil {
				return nil, err
			}
			return arg[decimal.Decimal](a, 0).Round(s, mode), nil
		},
		"floor(decimal)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(arg[decimal.Decimal](a, 0).Round(0, decimal.Floor))
		},
		"ceil(decimal)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(arg[decimal.Decimal](a, 0).Round(0, decimal.Ceiling))
		},
		"isZero(int)":     func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[int64](a, 0) == 0) },
		"isZero(double)":  func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[float64](a, 0) == 0) },
		"isZero(decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[decimal.Decimal](a, 0).IsZero()) },
		"isZero(money)":   func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[Money](a, 0).Amount.IsZero()) },
	}
	for t, kind := range cmpKinds {
		f["compare("+t+","+t+")"] = func(_ *vm, a []Value) (Value, *EvalError) {
			c, err := compareValues(kind, a[0], a[1])
			return int64(c), err
		}
	}
	return f
}

// moneyFns implements the money functions.
func moneyFns() map[string]builtinFn {
	return map[string]builtinFn{
		"money(decimal,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return newMoney(arg[decimal.Decimal](a, 0), arg[string](a, 1))
		},
		"money(string,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, err := decimal.Parse(arg[string](a, 0))
			if err != nil {
				return nil, evalErr(ErrorInvalidArgument, "%q is not a decimal", a[0])
			}
			return newMoney(d, arg[string](a, 1))
		},
		"add(money,money)":   func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpAddMoney, a[0], a[1]) },
		"sub(money,money)":   func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpSubMoney, a[0], a[1]) },
		"mul(money,decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return binaryOp(OpMulMoneyDec, a[0], a[1]) },
		"div(money,decimal)": func(_ *vm, a []Value) (Value, *EvalError) { return divMoney(a[0], a[1], decimal.HalfEven) },
		"div(money,decimal,RoundingMode)": func(_ *vm, a []Value) (Value, *EvalError) {
			mode, err := modeArg(a[2])
			if err != nil {
				return nil, err
			}
			return divMoney(a[0], a[1], mode)
		},
		"round(money)": func(_ *vm, a []Value) (Value, *EvalError) { return roundMoney(a[0], decimal.HalfEven) },
		"round(money,RoundingMode)": func(_ *vm, a []Value) (Value, *EvalError) {
			mode, err := modeArg(a[1])
			if err != nil {
				return nil, err
			}
			return roundMoney(a[0], mode)
		},
		"currency(money)": func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[Money](a, 0).Currency) },
		"amount(money)":   func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[Money](a, 0).Amount) },
	}
}

// newMoney checks the currency of a new money value.
func newMoney(d decimal.Decimal, code string) (Value, *EvalError) {
	if _, known := minorUnits(code); !known {
		return nil, evalErr(ErrorUnknownCurrency, "%q is not an ISO 4217 currency", code)
	}
	return Money{Amount: d, Currency: code}, nil
}

// divDecimal divides decimals to a scale.
func divDecimal(m *vm, a, b, scale Value, mode decimal.RoundingMode) (Value, *EvalError) {
	s, err := scaleArg(m, scale)
	if err != nil {
		return nil, err
	}
	q, derr := arg[decimal.Decimal](List{a}, 0).Div(arg[decimal.Decimal](List{b}, 0), s, mode)
	if derr != nil {
		return nil, evalErr(ErrorDivisionByZero, "decimal division by zero")
	}
	return q, nil
}

// divMoney divides money to its currency's minor units.
func divMoney(a, b Value, mode decimal.RoundingMode) (Value, *EvalError) {
	x := arg[Money](List{a}, 0)
	s, err := minorScale(x)
	if err != nil {
		return nil, err
	}
	q, derr := x.Amount.Div(arg[decimal.Decimal](List{b}, 0), s, mode)
	if derr != nil {
		return nil, evalErr(ErrorDivisionByZero, "money division by zero")
	}
	return Money{Amount: q, Currency: x.Currency}, nil
}

// roundMoney rounds money to its currency's minor units.
func roundMoney(a Value, mode decimal.RoundingMode) (Value, *EvalError) {
	x := arg[Money](List{a}, 0)
	s, err := minorScale(x)
	if err != nil {
		return nil, err
	}
	return Money{Amount: x.Amount.Round(s, mode), Currency: x.Currency}, nil
}
