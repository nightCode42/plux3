// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package decimal implements the exact decimal numbers of PXL (PXL-005,
// ADR-0009): an arbitrary-precision integer scaled by a power of ten.
//
// Addition, subtraction and multiplication are exact. Every operation that
// can lose digits — division and rounding — takes an explicit scale and one
// of six rounding modes. A value keeps the scale it was written or computed
// with, so 12.50 prints as "12.50"; comparison is numeric, so 12.5 equals
// 12.50. Floating-point is never used.
//
// Values are immutable and safe for concurrent use. Size limits are the
// caller's concern: the PXL VM checks Digits against the limits registry.
package decimal

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Decimal is unscaled × 10^-scale. The zero value is 0 with scale 0.
type Decimal struct {
	unscaled *big.Int // never mutated after construction; nil means zero
	scale    int      // never negative
}

// ErrDivisionByZero is returned by Div when the divisor is zero.
var ErrDivisionByZero = errors.New("decimal: division by zero")

// ErrSyntax is returned by Parse for malformed input.
var ErrSyntax = errors.New("decimal: invalid syntax")

// ErrNotFinite is returned by FromFloat64 for NaN and infinities.
var ErrNotFinite = errors.New("decimal: not a finite number")

// New returns unscaled × 10^-scale.
func New(unscaled int64, scale uint16) Decimal {
	return Decimal{unscaled: big.NewInt(unscaled), scale: int(scale)}
}

// FromInt64 returns v with scale 0.
func FromInt64(v int64) Decimal { return New(v, 0) }

// Parse reads the plain notation `-?[0-9]+(\.[0-9]+)?`: no exponent, no
// plus sign, no spaces. The scale is the number of fraction digits, and
// leading zeros are allowed ("007.50" is 7.50).
func Parse(s string) (Decimal, error) {
	body := strings.TrimPrefix(s, "-")
	intPart, frac, hasPoint := strings.Cut(body, ".")
	if intPart == "" || hasPoint && frac == "" || !digitsOnly(intPart) || !digitsOnly(frac) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	u, ok := new(big.Int).SetString(intPart+frac, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	if body != s {
		u.Neg(u)
	}
	return Decimal{unscaled: u, scale: len(frac)}, nil
}

// digitsOnly reports whether s consists of ASCII digits (true for "").
func digitsOnly(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FromFloat64 returns the decimal written by the shortest representation
// that reads back as f, in plain notation: FromFloat64(0.1) is 0.1, not the
// binary value 0.1000000000000000055511151231257827… NaN and infinities are
// rejected.
func FromFloat64(f float64) (Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, ErrNotFinite
	}
	return Parse(strconv.FormatFloat(f, 'f', -1, 64))
}

// int returns the unscaled value; never nil.
func (d Decimal) int() *big.Int {
	if d.unscaled == nil {
		return new(big.Int)
	}
	return d.unscaled
}

// Scale returns the number of fraction digits.
func (d Decimal) Scale() int { return d.scale }

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int { return d.int().Sign() }

// IsZero reports whether d is zero at any scale.
func (d Decimal) IsZero() bool { return d.Sign() == 0 }

// Digits returns the number of digits of d in plain notation, excluding the
// sign and the point: 4 for 0.001 and for 12.34, 1 for 0.
func (d Decimal) Digits() int {
	precision := len(new(big.Int).Abs(d.int()).Text(10))
	return max(precision, d.scale+1)
}

// String returns the plain notation with d's scale, e.g. "-12.50".
func (d Decimal) String() string {
	u := d.int()
	digits := new(big.Int).Abs(u).Text(10)
	if d.scale > 0 {
		if pad := d.scale + 1 - len(digits); pad > 0 {
			digits = strings.Repeat("0", pad) + digits
		}
		cut := len(digits) - d.scale
		digits = digits[:cut] + "." + digits[cut:]
	}
	if u.Sign() < 0 {
		return "-" + digits
	}
	return digits
}

// rescale returns u × 10^by for by ≥ 0.
func rescale(u *big.Int, by int) *big.Int {
	if by == 0 {
		return u
	}
	return new(big.Int).Mul(u, pow10(by))
}

// pow10 returns 10^n.
func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// align returns the unscaled values of d and e at their common scale.
func align(d, e Decimal) (a, b *big.Int, scale int) {
	scale = max(d.scale, e.scale)
	return rescale(d.int(), scale-d.scale), rescale(e.int(), scale-e.scale), scale
}

// Add returns d + e exactly, at the larger of the two scales.
func (d Decimal) Add(e Decimal) Decimal {
	a, b, scale := align(d, e)
	return Decimal{unscaled: new(big.Int).Add(a, b), scale: scale}
}

// Sub returns d − e exactly, at the larger of the two scales.
func (d Decimal) Sub(e Decimal) Decimal {
	a, b, scale := align(d, e)
	return Decimal{unscaled: new(big.Int).Sub(a, b), scale: scale}
}

// Mul returns d × e exactly; the scale is the sum of the scales.
func (d Decimal) Mul(e Decimal) Decimal {
	return Decimal{unscaled: new(big.Int).Mul(d.int(), e.int()), scale: d.scale + e.scale}
}

// Neg returns −d.
func (d Decimal) Neg() Decimal {
	return Decimal{unscaled: new(big.Int).Neg(d.int()), scale: d.scale}
}

// Abs returns |d|.
func (d Decimal) Abs() Decimal {
	return Decimal{unscaled: new(big.Int).Abs(d.int()), scale: d.scale}
}

// Cmp compares d and e numerically and returns -1, 0 or +1.
func (d Decimal) Cmp(e Decimal) int {
	a, b, _ := align(d, e)
	return a.Cmp(b)
}

// Round returns d at the given scale. A larger scale appends zeros and is
// exact; a smaller one rounds with mode.
func (d Decimal) Round(scale uint16, mode RoundingMode) Decimal {
	target := int(scale)
	if target >= d.scale {
		return Decimal{unscaled: rescale(d.int(), target-d.scale), scale: target}
	}
	return Decimal{unscaled: divRound(d.int(), pow10(d.scale-target), mode), scale: target}
}

// Div returns d ÷ e rounded with mode to the given scale.
func (d Decimal) Div(e Decimal, scale uint16, mode RoundingMode) (Decimal, error) {
	if e.IsZero() {
		return Decimal{}, ErrDivisionByZero
	}
	// d/e at the target scale is (du × 10^k) / eu with k = scale + e.scale − d.scale.
	num, den := d.int(), e.int()
	if k := int(scale) + e.scale - d.scale; k >= 0 {
		num = rescale(num, k)
	} else {
		den = rescale(den, -k)
	}
	return Decimal{unscaled: divRound(num, den, mode), scale: int(scale)}, nil
}

// divRound returns num ÷ den rounded to an integer with mode; den ≠ 0.
func divRound(num, den *big.Int, mode RoundingMode) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	sign := num.Sign() * den.Sign() // the sign of the exact quotient
	var away bool
	switch mode {
	case Down:
		away = false
	case Up:
		away = true
	case Ceiling:
		away = sign > 0
	case Floor:
		away = sign < 0
	case HalfUp, HalfEven:
		twice := new(big.Int).Abs(r)
		twice.Lsh(twice, 1)
		switch c := twice.Cmp(new(big.Int).Abs(den)); {
		case c > 0:
			away = true
		case c == 0:
			away = mode == HalfUp || q.Bit(0) == 1
		}
	}
	if away {
		q.Add(q, big.NewInt(int64(sign)))
	}
	return q
}

// Float64 returns the double nearest to d, rounding half to even; values
// beyond the double range become infinities.
func (d Decimal) Float64() float64 {
	f, _ := strconv.ParseFloat(d.String(), 64) // the error only reports range, and f is ±Inf then
	return f
}

// Int64 returns d truncated toward zero, and false when that does not fit
// in 64 bits.
func (d Decimal) Int64() (int64, bool) {
	t := d.Round(0, Down).int()
	if !t.IsInt64() {
		return 0, false
	}
	return t.Int64(), true
}

// Reduced returns d without trailing fraction zeros, so equal numbers
// have equal representations: 1.500 becomes 1.5 and 2.00 becomes 2.
func (d Decimal) Reduced() Decimal {
	u, scale := d.int(), d.scale
	if u.Sign() == 0 {
		return Decimal{}
	}
	ten := big.NewInt(10)
	q, r := new(big.Int), new(big.Int)
	for scale > 0 {
		q.QuoRem(u, ten, r)
		if r.Sign() != 0 {
			break
		}
		u, scale = new(big.Int).Set(q), scale-1
	}
	return Decimal{unscaled: u, scale: scale}
}
