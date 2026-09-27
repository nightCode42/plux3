// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package decimal

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func mustParse(t testing.TB, s string) Decimal {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseAndString(t *testing.T) {
	t.Parallel()
	valid := map[string]string{
		"0": "0", "12.50": "12.50", "-0.001": "-0.001", "007.5": "7.5", "-0": "0", "123456789012345678901234567890": "123456789012345678901234567890",
	}
	for in, want := range valid {
		if got := mustParse(t, in).String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "-", ".5", "5.", "1e3", "+1", " 1", "1,5", "1.2.3", "--1", "0x10"} {
		if _, err := Parse(in); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) = %v, want ErrSyntax", in, err)
		}
	}
	if got := New(-1250, 2).String(); got != "-12.50" {
		t.Errorf("New = %s", got)
	}
	if got := (Decimal{}).String(); got != "0" {
		t.Errorf("zero value = %s", got)
	}
}

// Verifies: PXL-005.
func TestArithmeticIsExact(t *testing.T) {
	t.Parallel()
	a, b := mustParse(t, "0.1"), mustParse(t, "0.2")
	if got := a.Add(b); got.String() != "0.3" || got.Cmp(mustParse(t, "0.30")) != 0 {
		t.Errorf("0.1 + 0.2 = %s", got)
	}
	if got := mustParse(t, "1.25").Mul(mustParse(t, "-0.4")).String(); got != "-0.500" {
		t.Errorf("mul = %s", got)
	}
	if got := mustParse(t, "1").Sub(mustParse(t, "0.001")).String(); got != "0.999" {
		t.Errorf("sub = %s", got)
	}
	if got := mustParse(t, "-2.5").Abs().Neg().String(); got != "-2.5" {
		t.Errorf("abs/neg = %s", got)
	}
}

// Verifies: PXL-005.
func TestRoundingModes(t *testing.T) {
	t.Parallel()
	inputs := []string{"5.5", "2.5", "1.6", "1.1", "1.0", "-1.0", "-1.1", "-1.6", "-2.5", "-5.5"}
	want := map[RoundingMode][]string{
		Up:       {"6", "3", "2", "2", "1", "-1", "-2", "-2", "-3", "-6"},
		Down:     {"5", "2", "1", "1", "1", "-1", "-1", "-1", "-2", "-5"},
		Ceiling:  {"6", "3", "2", "2", "1", "-1", "-1", "-1", "-2", "-5"},
		Floor:    {"5", "2", "1", "1", "1", "-1", "-2", "-2", "-3", "-6"},
		HalfUp:   {"6", "3", "2", "1", "1", "-1", "-1", "-2", "-3", "-6"},
		HalfEven: {"6", "2", "2", "1", "1", "-1", "-1", "-2", "-2", "-6"},
	}
	for mode, results := range want {
		for i, in := range inputs {
			if got := mustParse(t, in).Round(0, mode).String(); got != results[i] {
				t.Errorf("%s(%s) = %s, want %s", mode, in, got, results[i])
			}
		}
	}
	if got := mustParse(t, "1.5").Round(3, HalfEven).String(); got != "1.500" {
		t.Errorf("widening round = %s", got)
	}
	if got := mustParse(t, "0.125").Round(2, HalfEven).String(); got != "0.12" {
		t.Errorf("half-even tie = %s", got)
	}
}

// Verifies: PXL-005.
func TestDiv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b  string
		scale uint16
		mode  RoundingMode
		want  string
	}{
		{"10", "3", 2, HalfEven, "3.33"},
		{"-10", "3", 2, Floor, "-3.34"},
		{"1", "8", 2, HalfEven, "0.12"},
		{"1", "8", 2, HalfUp, "0.13"},
		{"100.00", "0.5", 0, Down, "200"},
		{"0.001", "1000", 5, Up, "0.00001"},
		{"7", "-2", 0, HalfEven, "-4"},
	}
	for _, tt := range tests {
		got, err := mustParse(t, tt.a).Div(mustParse(t, tt.b), tt.scale, tt.mode)
		if err != nil || got.String() != tt.want {
			t.Errorf("%s / %s (%d, %s) = %s, %v, want %s", tt.a, tt.b, tt.scale, tt.mode, got, err, tt.want)
		}
	}
	if _, err := FromInt64(1).Div(mustParse(t, "0.00"), 2, HalfEven); !errors.Is(err, ErrDivisionByZero) {
		t.Errorf("division by zero: %v", err)
	}
}

func TestConversions(t *testing.T) {
	t.Parallel()
	for f, want := range map[float64]string{0.1: "0.1", 1e21: "1000000000000000000000", -2.5e-7: "-0.00000025", 0: "0"} {
		d, err := FromFloat64(f)
		if err != nil || d.String() != want {
			t.Errorf("FromFloat64(%v) = %s, %v, want %s", f, d, err, want)
		}
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := FromFloat64(f); !errors.Is(err, ErrNotFinite) {
			t.Errorf("FromFloat64(%v) = %v", f, err)
		}
	}
	if f := mustParse(t, "0.1").Float64(); f != 0.1 {
		t.Errorf("Float64 = %v", f)
	}
	if f := New(1, 0).Add(mustParse(t, "1"+strings.Repeat("0", 400))).Float64(); !math.IsInf(f, 1) {
		t.Errorf("Float64 beyond range = %v", f)
	}
	if n, ok := mustParse(t, "-12.99").Int64(); !ok || n != -12 {
		t.Errorf("Int64 = %d, %t", n, ok)
	}
	if _, ok := mustParse(t, "9223372036854775808").Int64(); ok {
		t.Error("Int64 accepted an overflow")
	}
	if got := mustParse(t, "1.500").Reduced().String(); got != "1.5" {
		t.Errorf("Reduced = %s", got)
	}
	if got := mustParse(t, "200.00").Reduced().String(); got != "200" {
		t.Errorf("Reduced = %s", got)
	}
	if got := mustParse(t, "0.000").Reduced(); got.String() != "0" || got.Scale() != 0 {
		t.Errorf("Reduced zero = %s", got)
	}
	for in, want := range map[string]int{"0": 1, "0.001": 4, "12.34": 4, "-1000": 4} {
		if got := mustParse(t, in).Digits(); got != want {
			t.Errorf("Digits(%s) = %d, want %d", in, got, want)
		}
	}
	if !mustParse(t, "0.00").IsZero() || mustParse(t, "-0.01").Sign() != -1 {
		t.Error("IsZero or Sign")
	}
}

func TestRoundingModeNames(t *testing.T) {
	t.Parallel()
	for m := HalfEven; m <= Floor; m++ {
		got, ok := ParseRoundingMode(m.String())
		if !ok || got != m {
			t.Errorf("ParseRoundingMode(%s) = %v, %t", m, got, ok)
		}
	}
	if _, ok := ParseRoundingMode("halfDown"); ok {
		t.Error("unknown mode accepted")
	}
	if RoundingMode(0).String() != "invalid" {
		t.Error("invalid mode name")
	}
}

// genDecimal draws decimals with up to 40 digits and scale up to 10.
func genDecimal() *rapid.Generator[Decimal] {
	return rapid.Custom(func(t *rapid.T) Decimal {
		digits := rapid.StringMatching(`-?[0-9]{1,30}`).Draw(t, "digits")
		u, _ := new(big.Int).SetString(digits, 10)
		return Decimal{unscaled: u, scale: rapid.IntRange(0, 10).Draw(t, "scale")}
	})
}

// rat returns d as an exact rational.
func rat(d Decimal) *big.Rat {
	return new(big.Rat).SetFrac(d.int(), pow10(d.scale))
}

// TestProperties states the algebraic laws the VMs rely on.
//
// Verifies: PXL-005.
func TestProperties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a, b := genDecimal().Draw(t, "a"), genDecimal().Draw(t, "b")
		if rat(a.Add(b)).Cmp(new(big.Rat).Add(rat(a), rat(b))) != 0 {
			t.Fatal("Add is not exact")
		}
		if rat(a.Mul(b)).Cmp(new(big.Rat).Mul(rat(a), rat(b))) != 0 {
			t.Fatal("Mul is not exact")
		}
		if a.Add(b).Sub(b).Cmp(a) != 0 {
			t.Fatal("(a + b) − b ≠ a")
		}
		if a.Cmp(b) != rat(a).Cmp(rat(b)) {
			t.Fatal("Cmp disagrees with the rationals")
		}
		if back, err := Parse(a.String()); err != nil || back.Cmp(a) != 0 || back.Scale() != a.Scale() {
			t.Fatal("String does not round-trip")
		}
		if a.Reduced().Cmp(a) != 0 {
			t.Fatal("Reduced changed the value")
		}
		scale := rapid.Uint16Range(0, 12).Draw(t, "scale")
		mode := RoundingMode(rapid.Uint8Range(uint8(HalfEven), uint8(Floor)).Draw(t, "mode"))
		r := a.Round(scale, mode)
		if r.Round(scale, mode).Cmp(r) != 0 {
			t.Fatal("Round is not idempotent")
		}
		// The rounding error is below one unit of the target scale.
		unit := new(big.Rat).SetFrac(big.NewInt(1), pow10(int(scale)))
		if diff := new(big.Rat).Sub(rat(r), rat(a)); new(big.Rat).Abs(diff).Cmp(unit) >= 0 {
			t.Fatal("Round moved by a unit or more")
		}
		if !b.IsZero() {
			q, err := a.Div(b, scale, mode)
			if err != nil {
				t.Fatal(err)
			}
			exact := new(big.Rat).Quo(rat(a), rat(b))
			if diff := new(big.Rat).Sub(rat(q), exact); new(big.Rat).Abs(diff).Cmp(unit) >= 0 {
				t.Fatal("Div is off by a unit or more")
			}
		}
	})
}

func TestUnscaledIsACopy(t *testing.T) {
	t.Parallel()
	d, err := Parse("-12.50")
	if err != nil {
		t.Fatal(err)
	}
	u := d.Unscaled()
	if u.String() != "-1250" || d.Scale() != 2 {
		t.Fatalf("Unscaled = %s, scale %d", u, d.Scale())
	}
	u.SetInt64(7)
	if d.String() != "-12.50" {
		t.Errorf("modifying the result changed the decimal to %s", d)
	}
	if (Decimal{}).Unscaled().Sign() != 0 {
		t.Error("the zero decimal is not zero")
	}
}
