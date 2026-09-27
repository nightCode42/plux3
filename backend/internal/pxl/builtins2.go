// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// dateFns implements the date functions.
func dateFns() map[string]builtinFn {
	return map[string]builtinFn{
		"date(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, valid := parseDate(arg[string](a, 0))
			return checked(d, valid, "%q is not a date", a[0])
		},
		"date(int,int,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, valid := makeDate(arg[int64](a, 0), arg[int64](a, 1), arg[int64](a, 2))
			return checked(d, valid, "%v-%v-%v is not a date in years 1–9999", a[0], a[1], a[2])
		},
		"date(dateTime)": func(_ *vm, a []Value) (Value, *EvalError) {
			days, _ := arg[DateTime](a, 0).local()
			return just(Date(days))
		},
		"dateTime(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			t, valid := parseDateTime(arg[string](a, 0))
			return checked(t, valid, "%q is not an RFC 3339 dateTime with an offset", a[0])
		},
		"duration(int)": func(_ *vm, a []Value) (Value, *EvalError) { return just(Duration(arg[int64](a, 0))) },
		"duration(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, valid := parseDuration(arg[string](a, 0))
			return checked(d, valid, "%q is not an ISO 8601 duration", a[0])
		},
		"addDays(date,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			return addDays(int64(arg[Date](a, 0)), arg[int64](a, 1), func(d int64) Value { return Date(d) })
		},
		"addDays(dateTime,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			t := arg[DateTime](a, 0)
			days, ms := t.local()
			return addDays(days, arg[int64](a, 1), func(d int64) Value { r, _ := makeDateTime(d, ms, t.Offset); return r })
		},
		"addMonths(date,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			d, err := addMonths(int64(arg[Date](a, 0)), arg[int64](a, 1))
			return Date(d), err
		},
		"addMonths(dateTime,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			t := arg[DateTime](a, 0)
			days, ms := t.local()
			d, err := addMonths(days, arg[int64](a, 1))
			if err != nil {
				return nil, err
			}
			r, _ := makeDateTime(d, ms, t.Offset)
			return r, nil
		},
		"diffDays(date,date)": func(_ *vm, a []Value) (Value, *EvalError) { return just(int64(arg[Date](a, 1) - arg[Date](a, 0))) },
		"startOfDay(dateTime)": func(_ *vm, a []Value) (Value, *EvalError) {
			t := arg[DateTime](a, 0)
			days, _ := t.local()
			r, _ := makeDateTime(days, 0, t.Offset)
			return r, nil
		},
		"weekday(date)": func(_ *vm, a []Value) (Value, *EvalError) { return just(weekday(int64(arg[Date](a, 0)))) },
		"weekday(dateTime)": func(_ *vm, a []Value) (Value, *EvalError) {
			days, _ := arg[DateTime](a, 0).local()
			return just(weekday(days))
		},
		"isBefore(date,date)":         order(CmpDate, -1),
		"isBefore(dateTime,dateTime)": order(CmpDateTime, -1),
		"isAfter(date,date)":          order(CmpDate, 1),
		"isAfter(dateTime,dateTime)":  order(CmpDateTime, 1),
	}
}

// just wraps a value without error.
func just(v Value) (Value, *EvalError) { return v, nil }

// checked returns v, or an invalidArgument error when it is not valid.
func checked(v Value, valid bool, format string, args ...any) (Value, *EvalError) {
	if !valid {
		return nil, evalErr(ErrorInvalidArgument, format, args...)
	}
	return v, nil
}

// addDays adds days to a day number within years 1–9999.
func addDays(days, n int64, build func(int64) Value) (Value, *EvalError) {
	r := days + n
	if n > maxDate-minDate || n < minDate-maxDate || !validDate(r) {
		return nil, evalErr(ErrorInvalidArgument, "the date leaves years 1–9999")
	}
	return build(r), nil
}

// addMonths adds months, clamping the day to the month's end.
func addMonths(days, n int64) (int64, *EvalError) {
	y, m, d := civilFromDays(days)
	if n > 12*9999 || n < -12*9999 {
		return 0, evalErr(ErrorInvalidArgument, "the date leaves years 1–9999")
	}
	total := y*12 + (m - 1) + n
	ny, nm := floorDiv(total, 12), total-floorDiv(total, 12)*12+1
	if ny < 1 || ny > 9999 {
		return 0, evalErr(ErrorInvalidArgument, "the date leaves years 1–9999")
	}
	return daysFromCivil(ny, nm, min(d, daysInMonth(ny, nm))), nil
}

// weekday returns the ISO weekday of a day number; 1970-01-01 was a Thursday.
func weekday(days int64) int64 { return days + 3 - floorDiv(days+3, 7)*7 + 1 }

// order implements isBefore (sign −1) and isAfter (sign 1).
func order(kind CmpKind, sign int) builtinFn {
	return func(_ *vm, a []Value) (Value, *EvalError) {
		c, err := compareValues(kind, a[0], a[1])
		return c == sign, err
	}
}

// collectionFns implements the list and map functions.
func collectionFns() map[string]builtinFn {
	f := map[string]builtinFn{
		"size(list<T>)":       func(_ *vm, a []Value) (Value, *EvalError) { return just(size(a[0])) },
		"size(map<string,T>)": func(_ *vm, a []Value) (Value, *EvalError) { return just(size(a[0])) },
		"isEmpty(list<T>)":    func(_ *vm, a []Value) (Value, *EvalError) { return just(size(a[0]) == 0) },
		"isEmpty(map<string,T>)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(size(a[0]) == 0)
		},
		"isEmpty(string)": func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[string](a, 0) == "") },
		"first(list<T>)":  func(_ *vm, a []Value) (Value, *EvalError) { return just(at(arg[List](a, 0), 0)) },
		"last(list<T>)": func(_ *vm, a []Value) (Value, *EvalError) {
			l := arg[List](a, 0)
			return just(at(l, int64(len(l))-1))
		},
		"at(list<T>,int)": func(_ *vm, a []Value) (Value, *EvalError) { return just(at(arg[List](a, 0), arg[int64](a, 1))) },
		"slice(list<T>,int,int)": func(_ *vm, a []Value) (Value, *EvalError) {
			l, start, end := arg[List](a, 0), arg[int64](a, 1), arg[int64](a, 2)
			if start < 0 || start > end || end > int64(len(l)) {
				return nil, evalErr(ErrorIndexOutOfRange, "slice(%d, %d) of %d items", start, end, len(l))
			}
			return append(List{}, l[start:end]...), nil
		},
		"distinct(list<T>)": distinct,
		"keys(map<string,T>)": func(_ *vm, a []Value) (Value, *EvalError) {
			out := List{}
			for _, k := range sortedKeys(arg[Map](a, 0)) {
				out = append(out, k)
			}
			return out, nil
		},
		"values(map<string,T>)": func(_ *vm, a []Value) (Value, *EvalError) {
			mp, out := arg[Map](a, 0), List{}
			for _, k := range sortedKeys(mp) {
				out = append(out, mp[k])
			}
			return out, nil
		},
		"has(map<string,T>,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			_, present := arg[Map](a, 0)[arg[string](a, 1)]
			return just(present)
		},
		"sum(list<int>)": func(_ *vm, a []Value) (Value, *EvalError) {
			return sum(arg[List](a, 0), int64(0), func(x, y Value) (Value, *EvalError) { return intOp(OpAddInt, x.(int64), y.(int64)) })
		},
		"sum(list<double>)": func(_ *vm, a []Value) (Value, *EvalError) {
			return sum(arg[List](a, 0), 0.0, func(x, y Value) (Value, *EvalError) { return finite(x.(float64) + y.(float64)) })
		},
		"sum(list<decimal>)": func(m *vm, a []Value) (Value, *EvalError) {
			return sum(arg[List](a, 0), decimal.Decimal{}, func(x, y Value) (Value, *EvalError) {
				r := x.(decimal.Decimal).Add(y.(decimal.Decimal))
				return r, m.checkSize(r)
			})
		},
		"sum(list<duration>)": func(_ *vm, a []Value) (Value, *EvalError) {
			return sum(arg[List](a, 0), Duration(0), func(x, y Value) (Value, *EvalError) { return timeOp(OpAddDur, x, y) })
		},
	}
	return f
}

// at returns the item at i, or null outside the list.
func at(l List, i int64) Value {
	if i < 0 || i >= int64(len(l)) {
		return nil
	}
	return l[i]
}

// sum folds a list with add, starting from zero; items have the checked type.
func sum(l List, zero Value, add func(x, y Value) (Value, *EvalError)) (Value, *EvalError) {
	acc := zero
	for _, x := range l {
		if fmt.Sprintf("%T", x) != fmt.Sprintf("%T", zero) {
			return nil, evalErr(ErrorInvalidProgram, "sum over %T", x)
		}
		var err *EvalError
		if acc, err = add(acc, x); err != nil {
			return nil, err
		}
	}
	return acc, nil
}

// distinct keeps first occurrences of equal scalars.
func distinct(_ *vm, a []Value) (Value, *EvalError) {
	seen := map[string]bool{}
	out := List{}
	for _, x := range arg[List](a, 0) {
		k := scalarKey(x)
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out, nil
}

// scalarKey returns a key equal for equal scalars (numerically equal
// decimals, the same instant) and different otherwise.
func scalarKey(v Value) string {
	switch x := v.(type) {
	case decimal.Decimal:
		return "d" + x.Reduced().String()
	case Money:
		return "m" + x.Currency + x.Amount.Reduced().String()
	case DateTime:
		return "t" + strconv.FormatInt(x.Millis, 10)
	case float64:
		if x == 0 {
			x = 0 // -0 equals 0
		}
		return "f" + strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprintf("%T:%v", v, v)
	}
}

// logicFns implements string conversion and the validation functions.
func logicFns() map[string]builtinFn {
	f := map[string]builtinFn{
		"string(int)":     func(_ *vm, a []Value) (Value, *EvalError) { return just(strconv.FormatInt(arg[int64](a, 0), 10)) },
		"string(double)":  func(_ *vm, a []Value) (Value, *EvalError) { return just(formatDouble(arg[float64](a, 0))) },
		"string(bool)":    func(_ *vm, a []Value) (Value, *EvalError) { return just(strconv.FormatBool(arg[bool](a, 0))) },
		"string(enum)":    func(_ *vm, a []Value) (Value, *EvalError) { return just(arg[string](a, 0)) },
		"isEmail(string)": func(_ *vm, a []Value) (Value, *EvalError) { return just(isEmail(arg[string](a, 0))) },
		"isIban(string)":  func(_ *vm, a []Value) (Value, *EvalError) { return just(isIban(arg[string](a, 0))) },
		"isNumeric(string)": func(_ *vm, a []Value) (Value, *EvalError) {
			_, err := decimal.Parse(arg[string](a, 0))
			return just(err == nil)
		},
		"luhn(string)": func(_ *vm, a []Value) (Value, *EvalError) { return just(luhn(arg[string](a, 0))) },
	}
	for _, t := range []string{"decimal", "money", "date", "dateTime", "duration", "color"} {
		f["string("+t+")"] = func(_ *vm, a []Value) (Value, *EvalError) { return just(fmt.Sprint(a[0])) }
	}
	return f
}

// isEmail checks the ASCII address rules of the standard library.
func isEmail(s string) bool {
	local, domain, found := strings.Cut(s, "@")
	return found && len(s) <= 254 && validLocalPart(local) && validDomain(domain)
}

// validLocalPart checks the part before the @.
func validLocalPart(local string) bool {
	if local == "" || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for i := range len(local) {
		if c := local[i]; !isAlnum(c) && c != '_' && !strings.ContainsRune("!#$%&'*+/=?^`{|}~.-", rune(c)) {
			return false
		}
	}
	return true
}

// validDomain checks the labels after the @.
func validDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' || !allASCII(l, func(c byte) bool { return isAlnum(c) || c == '-' }) {
			return false
		}
	}
	tld := labels[len(labels)-1]
	return len(tld) >= 2 && allASCII(tld, isAlpha)
}

// allASCII reports whether every byte of s satisfies ok; non-ASCII bytes
// never do.
func allASCII(s string, ok func(byte) bool) bool {
	for i := range len(s) {
		if s[i] >= 0x80 || !ok(s[i]) {
			return false
		}
	}
	return true
}

// isAlpha reports whether c is an ASCII letter.
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// isIban checks an IBAN's structure and mod-97 check digits.
func isIban(s string) bool {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(s) < 15 || len(s) > 34 || !isUpper(s[0]) || !isUpper(s[1]) || !isDigit(s[2]) || !isDigit(s[3]) {
		return false
	}
	var digits strings.Builder
	for _, c := range []byte(s[4:] + s[:4]) {
		switch {
		case isDigit(c):
			digits.WriteByte(c)
		case isUpper(c):
			digits.WriteString(strconv.Itoa(int(c-'A') + 10))
		default:
			return false
		}
	}
	n, _ := new(big.Int).SetString(digits.String(), 10)
	return new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// isUpper reports whether c is an ASCII capital letter.
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// luhn checks the Luhn checksum of a string of at least two digits.
func luhn(s string) bool {
	if len(s) < 2 {
		return false
	}
	total := 0
	for i := range len(s) {
		c := s[len(s)-1-i]
		if !isDigit(c) {
			return false
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		total += d
	}
	return total%10 == 0
}
