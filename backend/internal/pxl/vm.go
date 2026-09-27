// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Limits bound one evaluation (PXL-001); values come from the limits
// registry (LIM-001).
type Limits struct {
	// Budget is the number of operations the evaluation may perform.
	Budget int64
	// StringLength, CollectionSize and DecimalDigits bound the values it
	// produces, in code points, entries and digits.
	StringLength   int64
	CollectionSize int64
	DecimalDigits  int64
}

// LimitsFrom reads the PXL limits from a resolved set of the registry.
func LimitsFrom(set limits.Set) Limits {
	return Limits{
		Budget:         set.Get(limits.PXLOperationBudget),
		StringLength:   set.Get(limits.PXLStringLength),
		CollectionSize: set.Get(limits.PXLCollectionSize),
		DecimalDigits:  set.Get(limits.PXLDecimalDigits),
	}
}

// EvalError is a typed run-time error: evaluation stops and returns it.
type EvalError struct {
	Kind    ErrorKind
	Message string
}

func (e *EvalError) Error() string { return "pxl: " + e.Kind.String() + ": " + e.Message }

// evalErr builds an EvalError.
func evalErr(kind ErrorKind, format string, args ...any) *EvalError {
	return &EvalError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// iterator is the state of a macro loop in a local slot.
type iterator struct {
	list List
	next int
}

// vm evaluates one program once.
type vm struct {
	p      *Program
	lim    Limits
	inputs map[string]Value
	stack  []Value
	locals []any
	used   int64
}

// Eval evaluates the program with the given root values, which must match
// the types the program was checked against (use FromJSON), within lim.
// The result, or an *EvalError, never a panic, is returned.
func (p *Program) Eval(inputs map[string]Value, lim Limits) (Value, error) {
	m := &vm{p: p, lim: lim, inputs: inputs, stack: make([]Value, 0, min(p.MaxStack, len(p.Code))), locals: make([]any, p.Locals)}
	v, err := m.run()
	if err != nil {
		return nil, err
	}
	return v, nil
}

// charge spends operations of the budget.
func (m *vm) charge(n int64) *EvalError {
	m.used += n
	if m.used > m.lim.Budget {
		return evalErr(ErrorBudgetExceeded, "more than %d operations", m.lim.Budget)
	}
	return nil
}

// push adds a value; a program that exceeds its declared stack depth is
// malformed and stops at the next instruction.
func (m *vm) push(v Value) { m.stack = append(m.stack, v) }

// pop removes the top value; an empty stack means malformed code.
func (m *vm) pop() (Value, *EvalError) {
	if len(m.stack) == 0 {
		return nil, evalErr(ErrorInvalidProgram, "stack underflow")
	}
	v := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return v, nil
}

// popN removes the top n values, first pushed first.
func (m *vm) popN(n int) ([]Value, *EvalError) {
	if n > len(m.stack) {
		return nil, evalErr(ErrorInvalidProgram, "stack underflow")
	}
	out := slices.Clone(m.stack[len(m.stack)-n:])
	m.stack = m.stack[:len(m.stack)-n]
	return out, nil
}

// typed asserts the type of an operand.
func typed[T any](v Value) (T, *EvalError) {
	x, ok := v.(T)
	if !ok {
		var zero T
		return zero, evalErr(ErrorInvalidProgram, "operand %T is not a %T", v, zero)
	}
	return x, nil
}

// checkSize enforces the size limits on a produced value.
func (m *vm) checkSize(v Value) *EvalError {
	switch x := v.(type) {
	case string:
		if int64(len(x)) > m.lim.StringLength && size(x) > m.lim.StringLength {
			return evalErr(ErrorSizeLimit, "a string longer than %d code points", m.lim.StringLength)
		}
	case List:
		if int64(len(x)) > m.lim.CollectionSize {
			return evalErr(ErrorSizeLimit, "a list of more than %d items", m.lim.CollectionSize)
		}
	case Map:
		if int64(len(x)) > m.lim.CollectionSize {
			return evalErr(ErrorSizeLimit, "a map of more than %d entries", m.lim.CollectionSize)
		}
	case decimal.Decimal:
		if int64(x.Digits()) > m.lim.DecimalDigits {
			return evalErr(ErrorSizeLimit, "a decimal of more than %d digits", m.lim.DecimalDigits)
		}
	case Money:
		return m.checkSize(x.Amount)
	}
	return nil
}

// run executes the code.
func (m *vm) run() (Value, *EvalError) {
	for pc := 0; pc < len(m.p.Code); {
		in, ok := m.p.decodeAt(pc)
		if !ok {
			return nil, evalErr(ErrorInvalidProgram, "bad instruction at %d", pc)
		}
		if err := m.charge(1); err != nil {
			return nil, err
		}
		next, err := m.step(in)
		if err != nil {
			return nil, err
		}
		if len(m.stack) > m.p.MaxStack {
			return nil, evalErr(ErrorInvalidProgram, "the stack exceeds its declared depth %d", m.p.MaxStack)
		}
		if next < 0 || next > len(m.p.Code) {
			return nil, evalErr(ErrorInvalidProgram, "jump out of the code")
		}
		pc = next
	}
	if len(m.stack) != 1 {
		return nil, evalErr(ErrorInvalidProgram, "%d values left on the stack", len(m.stack))
	}
	return m.stack[0], nil
}

// step executes one instruction and returns the next pc.
func (m *vm) step(in instr) (int, *EvalError) {
	var err *EvalError
	switch in.op {
	case OpPushNull, OpPushTrue, OpPushFalse, OpConst, OpPop, OpLoadRoot, OpLoadLocal:
		err = m.stackOp(in)
	case OpGetField, OpMapGet, OpIndexList, OpIndexMap:
		err = m.access(in)
	case OpJump, OpJumpIfFalse, OpJumpIfTrue, OpAndJump, OpOrJump, OpNullJump, OpCoalesceJump:
		return m.jump(in)
	case OpIterInit, OpIterNext, OpIterList, OpSortBy, OpNewList, OpNewMap, OpListAppend:
		return m.collection(in)
	case OpCall:
		err = m.call(in)
	default:
		err = m.arith(in.op)
	}
	return in.next, err
}

// stackOp executes constants, roots and locals.
func (m *vm) stackOp(in instr) *EvalError {
	switch in.op {
	case OpPushNull:
		m.push(nil)
	case OpPushTrue:
		m.push(true)
	case OpPushFalse:
		m.push(false)
	case OpConst:
		m.push(m.p.Constants[in.operands[0]])
	case OpPop:
		_, err := m.pop()
		return err
	case OpLoadRoot:
		name, _ := m.p.Constants[in.operands[0]].(string)
		v, ok := m.inputs[name]
		if !ok {
			return evalErr(ErrorInvalidInput, "no value for root %q", name)
		}
		m.push(v)
	default: // OpLoadLocal
		m.push(m.locals[in.operands[0]])
	}
	return nil
}

// access executes field, map and list access.
func (m *vm) access(in instr) *EvalError {
	if in.op == OpIndexList {
		return m.indexList()
	}
	var (
		obj Map
		key string
		err *EvalError
	)
	if in.op == OpIndexMap {
		obj, key, err = popMapKey(m)
	} else {
		var v Value
		if v, err = m.pop(); err == nil {
			obj, err = typed[Map](v)
		}
		key, _ = m.p.Constants[in.operands[0]].(string)
	}
	if err != nil {
		return err
	}
	x, found := obj[key]
	if !found && in.op != OpGetField {
		return evalErr(ErrorMissingKey, "no entry %q", key)
	}
	m.push(x)
	return nil
}

// indexList executes list[index].
func (m *vm) indexList() *EvalError {
	vi, err := m.pop()
	if err != nil {
		return err
	}
	vl, err := m.pop()
	if err != nil {
		return err
	}
	i, err1 := typed[int64](vi)
	l, err2 := typed[List](vl)
	if err := first(err1, err2); err != nil {
		return err
	}
	if i < 0 || i >= int64(len(l)) {
		return evalErr(ErrorIndexOutOfRange, "index %d of a list of %d", i, len(l))
	}
	m.push(l[i])
	return nil
}

// popMapKey pops a string key and a map below it.
func popMapKey(m *vm) (Map, string, *EvalError) {
	vk, err := m.pop()
	if err != nil {
		return nil, "", err
	}
	vmap, err := m.pop()
	if err != nil {
		return nil, "", err
	}
	key, err1 := typed[string](vk)
	mp, err2 := typed[Map](vmap)
	if err1 != nil || err2 != nil {
		return nil, "", evalErr(ErrorInvalidProgram, "bad map operands")
	}
	return mp, key, nil
}

// jump executes the jumps.
func (m *vm) jump(in instr) (int, *EvalError) {
	target := int(in.operands[0])
	if in.op == OpJump {
		return target, nil
	}
	if len(m.stack) == 0 {
		return 0, evalErr(ErrorInvalidProgram, "stack underflow")
	}
	top := m.stack[len(m.stack)-1]
	switch in.op {
	case OpNullJump:
		if top == nil {
			return target, nil
		}
		return in.next, nil
	case OpCoalesceJump:
		if top != nil {
			return target, nil
		}
		m.stack = m.stack[:len(m.stack)-1]
		return in.next, nil
	}
	b, err := typed[bool](top)
	if err != nil {
		return 0, err
	}
	switch in.op {
	case OpJumpIfFalse, OpJumpIfTrue:
		m.stack = m.stack[:len(m.stack)-1]
		if b == (in.op == OpJumpIfTrue) {
			return target, nil
		}
	default: // OpAndJump, OpOrJump keep the deciding value
		if b == (in.op == OpOrJump) {
			return target, nil
		}
		m.stack = m.stack[:len(m.stack)-1]
	}
	return in.next, nil
}

// collection executes list and map construction and the macro loops.
func (m *vm) collection(in instr) (int, *EvalError) {
	switch in.op {
	case OpNewList:
		items, err := m.popN(int(in.operands[0]))
		if err == nil {
			err = m.charge(int64(len(items)))
		}
		if err == nil {
			err = m.checkSize(List(items))
		}
		m.push(List(items))
		return in.next, err
	case OpNewMap:
		return in.next, m.newMap(int(in.operands[0]))
	case OpListAppend:
		v, err := m.pop()
		if err != nil {
			return 0, err
		}
		acc, err := m.pop()
		if err != nil {
			return 0, err
		}
		l, err := typed[List](acc)
		if err != nil {
			return 0, err
		}
		l = append(l, v)
		m.push(l)
		return in.next, m.checkSize(l)
	case OpIterInit:
		v, err := m.pop()
		if err != nil {
			return 0, err
		}
		l, err := typed[List](v)
		m.locals[in.operands[0]] = &iterator{list: l}
		return in.next, err
	case OpIterNext:
		it, ok := m.locals[in.operands[0]].(*iterator)
		if !ok {
			return 0, evalErr(ErrorInvalidProgram, "no iterator in local %d", in.operands[0])
		}
		if it.next >= len(it.list) {
			return int(in.operands[2]), nil
		}
		m.locals[in.operands[1]] = it.list[it.next]
		it.next++
		return in.next, nil
	case OpIterList:
		it, ok := m.locals[in.operands[0]].(*iterator)
		if !ok {
			return 0, evalErr(ErrorInvalidProgram, "no iterator in local %d", in.operands[0])
		}
		m.push(it.list)
		return in.next, nil
	default: // OpSortBy
		return in.next, m.sortBy(CmpKind(in.operands[0])) //nolint:gosec // G115: verified at decode.
	}
}

// newMap builds a map from n key–value pairs.
func (m *vm) newMap(n int) *EvalError {
	pairs, err := m.popN(2 * n)
	if err != nil {
		return err
	}
	if err := m.charge(int64(n)); err != nil {
		return err
	}
	out := make(Map, n)
	for i := 0; i < len(pairs); i += 2 {
		k, err := typed[string](pairs[i])
		if err != nil {
			return err
		}
		if _, dup := out[k]; dup {
			return evalErr(ErrorDuplicateKey, "key %q appears twice", k)
		}
		out[k] = pairs[i+1]
	}
	m.push(out)
	return m.checkSize(out)
}

// sortBy stably sorts items by precomputed keys.
func (m *vm) sortBy(kind CmpKind) *EvalError {
	vi, err := m.pop()
	if err != nil {
		return err
	}
	vk, err := m.pop()
	if err != nil {
		return err
	}
	items, err1 := typed[List](vi)
	keys, err2 := typed[List](vk)
	if err1 != nil || err2 != nil || len(items) != len(keys) {
		return evalErr(ErrorInvalidProgram, "bad sortBy operands")
	}
	n := int64(len(items))
	if err := m.charge(n * int64(bits.Len64(uint64(n)))); err != nil { //nolint:gosec // G115: n ≥ 0.
		return err
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	var failure *EvalError
	slices.SortStableFunc(idx, func(a, b int) int {
		c, err := compareValues(kind, keys[a], keys[b])
		if err != nil && failure == nil {
			failure = err
		}
		return c
	})
	if failure != nil {
		return failure
	}
	out := make(List, len(items))
	for i, j := range idx {
		out[i] = items[j]
	}
	m.push(out)
	return nil
}

// call executes a standard-library overload.
func (m *vm) call(in instr) *EvalError {
	id, argc := int(in.operands[0]), int(in.operands[1])
	args, err := m.popN(argc)
	if err != nil {
		return err
	}
	var cost int64
	for _, a := range args {
		cost += size(a)
	}
	if err := m.charge(cost); err != nil {
		return err
	}
	if !argsMatch(signatures()[stdOverloads[id-1].name], id, args) {
		return evalErr(ErrorInvalidProgram, "arguments of %s do not match its signature", stdOverloads[id-1].name)
	}
	fn := builtins()[id-1]
	if fn == nil {
		return evalErr(ErrorUnsupported, "%s is evaluated from feature %s", stdOverloads[id-1].name, groupFeature(stdOverloads[id-1].group))
	}
	v, err := fn(m, args)
	if err != nil {
		return err
	}
	if err := m.charge(size(v)); err != nil {
		return err
	}
	if err := m.checkSize(v); err != nil {
		return err
	}
	m.push(v)
	return nil
}

// arith executes the typed operators.
func (m *vm) arith(op Opcode) *EvalError {
	switch op {
	case OpNot:
		v, err := m.pop()
		if err != nil {
			return err
		}
		b, err := typed[bool](v)
		m.push(!b)
		return err
	case OpEq, OpNe, OpInList, OpInMap:
		return m.equality(op)
	case OpCmpInt, OpCmpDouble, OpCmpDec, OpCmpMoney, OpCmpString, OpCmpDate, OpCmpDatetime, OpCmpDur:
		return m.compare(op)
	case OpLt, OpLe, OpGt, OpGe:
		v, err := m.pop()
		if err != nil {
			return err
		}
		c, err := typed[int64](v)
		m.push(map[Opcode]bool{OpLt: c < 0, OpLe: c <= 0, OpGt: c > 0, OpGe: c >= 0}[op])
		return err
	}
	return m.numeric(op)
}

// equality executes ==, != and in, charging for deep comparisons.
func (m *vm) equality(op Opcode) *EvalError {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	var visits int64
	var result bool
	switch op {
	case OpEq, OpNe:
		result = equal(a, b, &visits) == (op == OpEq)
	case OpInList:
		l, err := typed[List](b)
		if err != nil {
			return err
		}
		for _, x := range l {
			visits++
			if equal(a, x, &visits) {
				result = true
				break
			}
		}
	default: // OpInMap
		mp, err1 := typed[Map](b)
		k, err2 := typed[string](a)
		if err1 != nil || err2 != nil {
			return evalErr(ErrorInvalidProgram, "bad in operands")
		}
		_, result = mp[k]
	}
	m.push(result)
	return m.charge(visits)
}

// compare executes the CMP_ opcodes.
func (m *vm) compare(op Opcode) *EvalError {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	kinds := map[Opcode]CmpKind{
		OpCmpInt: CmpInt, OpCmpDouble: CmpDouble, OpCmpDec: CmpDecimal, OpCmpMoney: CmpMoney,
		OpCmpString: CmpString, OpCmpDate: CmpDate, OpCmpDatetime: CmpDateTime, OpCmpDur: CmpDuration,
	}
	c, cerr := compareValues(kinds[op], a, b)
	if cerr != nil {
		return cerr
	}
	m.push(int64(c))
	return nil
}

// numeric executes arithmetic and conversions.
func (m *vm) numeric(op Opcode) *EvalError {
	switch op {
	case OpNegInt, OpNegDouble, OpNegDec, OpNegMoney, OpNegDur, OpIntToDouble, OpIntToDec:
		v, err := m.pop()
		if err != nil {
			return err
		}
		r, err := unaryOp(op, v)
		if err != nil {
			return err
		}
		m.push(r)
		return m.checkSize(r)
	}
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	r, err := binaryOp(op, a, b)
	if err != nil {
		return err
	}
	if op == OpConcatString || op == OpConcatList {
		if err := m.charge(size(r)); err != nil {
			return err
		}
	}
	m.push(r)
	return m.checkSize(r)
}

// unaryOp evaluates negation and widening.
func unaryOp(op Opcode, v Value) (Value, *EvalError) {
	switch op {
	case OpNegInt, OpIntToDouble, OpIntToDec:
		i, err := typed[int64](v)
		if err != nil {
			return nil, err
		}
		switch op {
		case OpIntToDouble:
			return float64(i), nil
		case OpIntToDec:
			return decimal.FromInt64(i), nil
		}
		if i == math.MinInt64 {
			return nil, evalErr(ErrorOverflow, "-(%d)", i)
		}
		return -i, nil
	case OpNegDouble:
		f, err := typed[float64](v)
		return -f, err
	case OpNegDec:
		d, err := typed[decimal.Decimal](v)
		return d.Neg(), err
	case OpNegMoney:
		x, err := typed[Money](v)
		return Money{Amount: x.Amount.Neg(), Currency: x.Currency}, err
	default: // OpNegDur
		d, err := typed[Duration](v)
		if d == math.MinInt64 {
			return nil, evalErr(ErrorOverflow, "negated duration")
		}
		return -d, err
	}
}

// argsMatch checks the run-time types of call arguments against the
// signature, so malformed bytecode cannot reach an implementation.
func argsMatch(sigs []signature, id int, args []Value) bool {
	for _, s := range sigs {
		if s.def.id != id {
			continue
		}
		for i, a := range args {
			if !valueMatches(s.params[min(i, len(s.params)-1)], a) {
				return false
			}
		}
		return len(args) == len(s.params) || s.def.variadic
	}
	return false
}

// valueMatches reports whether a value fits a signature pattern.
func valueMatches(p *pattern, v Value) bool {
	if v == nil {
		return p.nullable || p.variable != ""
	}
	if p.variable != "" {
		return true
	}
	var ok bool
	switch p.kind {
	case KindList:
		_, ok = v.(List)
	case KindMap:
		_, ok = v.(Map)
	case KindInt:
		_, ok = v.(int64)
	case KindDouble:
		_, ok = v.(float64)
	case KindBool:
		_, ok = v.(bool)
	case KindString, KindEnum:
		_, ok = v.(string)
	case KindDecimal:
		_, ok = v.(decimal.Decimal)
	case KindMoney:
		_, ok = v.(Money)
	case KindDate:
		_, ok = v.(Date)
	case KindDateTime:
		_, ok = v.(DateTime)
	case KindDuration:
		_, ok = v.(Duration)
	case KindColor:
		_, ok = v.(Color)
	}
	return ok
}
