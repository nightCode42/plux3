// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"math"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// binaryOp evaluates the typed binary operators.
func binaryOp(op Opcode, a, b Value) (Value, *EvalError) {
	switch op {
	case OpAddInt, OpSubInt, OpMulInt, OpDivInt, OpModInt:
		x, err1 := typed[int64](a)
		y, err2 := typed[int64](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		return intOp(op, x, y)
	case OpAddDouble, OpSubDouble, OpMulDouble, OpDivDouble, OpModDouble:
		x, err1 := typed[float64](a)
		y, err2 := typed[float64](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		return doubleOp(op, x, y)
	case OpAddDec, OpSubDec, OpMulDec:
		x, err1 := typed[decimal.Decimal](a)
		y, err2 := typed[decimal.Decimal](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		return map[Opcode]func(decimal.Decimal) decimal.Decimal{OpAddDec: x.Add, OpSubDec: x.Sub, OpMulDec: x.Mul}[op](y), nil
	case OpAddMoney, OpSubMoney, OpMulMoneyDec, OpMulDecMoney:
		return moneyOp(op, a, b)
	case OpConcatString:
		x, err1 := typed[string](a)
		y, err2 := typed[string](b)
		return x + y, first(err1, err2)
	case OpConcatList:
		x, err1 := typed[List](a)
		y, err2 := typed[List](b)
		return append(append(List{}, x...), y...), first(err1, err2)
	default:
		return timeOp(op, a, b)
	}
}

// first returns the first non-nil error.
func first(errs ...*EvalError) *EvalError {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// intOp evaluates checked 64-bit integer arithmetic.
func intOp(op Opcode, x, y int64) (Value, *EvalError) {
	switch op {
	case OpAddInt, OpSubInt:
		return addInt(op, x, y)
	case OpMulInt:
		if x == 0 || y == 0 {
			return int64(0), nil
		}
		r := x * y
		if r/y != x || (x == -1 && y == math.MinInt64) || (y == -1 && x == math.MinInt64) {
			return nil, evalErr(ErrorOverflow, "%d * %d", x, y)
		}
		return r, nil
	default:
		return divInt(op, x, y)
	}
}

// addInt evaluates checked addition and subtraction.
func addInt(op Opcode, x, y int64) (Value, *EvalError) {
	if op == OpSubInt {
		r := x - y
		if (r < x) != (y > 0) {
			return nil, evalErr(ErrorOverflow, "%d - %d", x, y)
		}
		return r, nil
	}
	r := x + y
	if (r > x) != (y > 0) {
		return nil, evalErr(ErrorOverflow, "%d + %d", x, y)
	}
	return r, nil
}

// divInt evaluates truncated division and remainder.
func divInt(op Opcode, x, y int64) (Value, *EvalError) {
	switch {
	case y == 0:
		return nil, evalErr(ErrorDivisionByZero, "%d by zero", x)
	case op == OpModInt && y == -1:
		return int64(0), nil
	case op == OpModInt:
		return x % y, nil
	case x == math.MinInt64 && y == -1:
		return nil, evalErr(ErrorOverflow, "%d / -1", x)
	default:
		return x / y, nil
	}
}

// doubleOp evaluates double arithmetic; NaN and infinities are errors.
func doubleOp(op Opcode, x, y float64) (Value, *EvalError) {
	var r float64
	switch op {
	case OpAddDouble:
		r = x + y
	case OpSubDouble:
		r = x - y
	case OpMulDouble:
		r = x * y
	case OpDivDouble:
		r = x / y
	default:
		r = math.Mod(x, y)
	}
	return finite(r)
}

// finite rejects NaN and infinities.
func finite(f float64) (Value, *EvalError) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, evalErr(ErrorNonFinite, "the result is not a finite number")
	}
	return f, nil
}

// moneyOp evaluates money arithmetic.
func moneyOp(op Opcode, a, b Value) (Value, *EvalError) {
	switch op {
	case OpMulMoneyDec:
		x, err1 := typed[Money](a)
		y, err2 := typed[decimal.Decimal](b)
		return Money{Amount: x.Amount.Mul(y), Currency: x.Currency}, first(err1, err2)
	case OpMulDecMoney:
		x, err1 := typed[decimal.Decimal](a)
		y, err2 := typed[Money](b)
		return Money{Amount: x.Mul(y.Amount), Currency: y.Currency}, first(err1, err2)
	}
	x, err1 := typed[Money](a)
	y, err2 := typed[Money](b)
	if err := first(err1, err2); err != nil {
		return nil, err
	}
	if x.Currency != y.Currency {
		return nil, evalErr(ErrorCurrencyMismatch, "%s and %s", x.Currency, y.Currency)
	}
	if op == OpAddMoney {
		return Money{Amount: x.Amount.Add(y.Amount), Currency: x.Currency}, nil
	}
	return Money{Amount: x.Amount.Sub(y.Amount), Currency: x.Currency}, nil
}

// timeOp evaluates duration and dateTime arithmetic.
func timeOp(op Opcode, a, b Value) (Value, *EvalError) {
	switch op {
	case OpAddDur, OpSubDur, OpMulDurInt, OpMulIntDur:
		return durationOp(op, a, b)
	case OpAddDtDur, OpAddDurDt, OpSubDtDur:
		if op == OpAddDurDt {
			a, b = b, a
		}
		t, err1 := typed[DateTime](a)
		d, err2 := typed[Duration](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		if op == OpSubDtDur {
			d = -d
		}
		return shiftDateTime(t, int64(d))
	case OpSubDtDt:
		x, err1 := typed[DateTime](a)
		y, err2 := typed[DateTime](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		return Duration(x.Millis - y.Millis), nil
	}
	return nil, evalErr(ErrorInvalidProgram, "unknown opcode %s", op)
}

// durationOp evaluates checked duration arithmetic.
func durationOp(op Opcode, a, b Value) (Value, *EvalError) {
	if op == OpMulIntDur {
		a, b = b, a
	}
	x, err1 := typed[Duration](a)
	var r Value
	var err *EvalError
	switch op {
	case OpMulDurInt, OpMulIntDur:
		y, err2 := typed[int64](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		r, err = intOp(OpMulInt, int64(x), y)
	default:
		y, err2 := typed[Duration](b)
		if err := first(err1, err2); err != nil {
			return nil, err
		}
		r, err = addInt(map[bool]Opcode{true: OpSubInt, false: OpAddInt}[op == OpSubDur], int64(x), int64(y))
	}
	if err != nil {
		return nil, err
	}
	return Duration(r.(int64)), nil //nolint:forcetypeassert // intOp returns int64 without error.
}

// shiftDateTime moves an instant by ms milliseconds, keeping the offset.
func shiftDateTime(t DateTime, ms int64) (Value, *EvalError) {
	r := DateTime{Millis: t.Millis + ms, Offset: t.Offset}
	if (ms > 0 && r.Millis < t.Millis) || (ms < 0 && r.Millis > t.Millis) {
		return nil, evalErr(ErrorInvalidArgument, "the dateTime leaves years 1–9999")
	}
	if days, _ := r.local(); !validDate(days) {
		return nil, evalErr(ErrorInvalidArgument, "the dateTime leaves years 1–9999")
	}
	return r, nil
}
