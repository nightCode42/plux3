// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
)

// Program is compiled PXL: what bundles store and runtimes evaluate
// (PXL-003). It is immutable once built.
type Program struct {
	// Locals is the number of local slots the macros use.
	Locals int
	// MaxStack is the largest operand-stack depth.
	MaxStack int
	// Result is the type expression of the result.
	Result string
	// Constants is the constant pool.
	Constants []Value
	// Reads are the state paths the program reads, sorted (CMP-023).
	Reads []string
	// Code is the instruction stream.
	Code []byte
	// Features are the standard-library features the program needs
	// besides pxl.v1 (BND-008). They are recorded by the compiler, not
	// encoded.
	Features []string
}

// ErrInvalidProgram reports malformed program bytes.
var ErrInvalidProgram = errors.New("pxl: invalid program")

// Encode returns the canonical binary encoding (version 1): version,
// locals, max stack, result type, constants, reads and code. Counts and
// lengths are unsigned LEB128.
func (p *Program) Encode() []byte {
	var b bytes.Buffer
	putUvarint(&b, BytecodeVersion)
	putUvarint(&b, uint64(p.Locals))   //nolint:gosec // G115: non-negative by construction.
	putUvarint(&b, uint64(p.MaxStack)) //nolint:gosec // G115: non-negative by construction.
	putString(&b, p.Result)
	putUvarint(&b, uint64(len(p.Constants)))
	for _, c := range p.Constants {
		encodeValue(&b, c)
	}
	putUvarint(&b, uint64(len(p.Reads)))
	for _, r := range p.Reads {
		putString(&b, r)
	}
	putUvarint(&b, uint64(len(p.Code)))
	b.Write(p.Code)
	return b.Bytes()
}

func putUvarint(b *bytes.Buffer, n uint64) { b.Write(binary.AppendUvarint(nil, n)) }

func putVarint(b *bytes.Buffer, n int64) { b.Write(binary.AppendVarint(nil, n)) }

func putString(b *bytes.Buffer, s string) {
	putUvarint(b, uint64(len(s)))
	b.WriteString(s)
}

// encodeValue writes a tagged constant.
func encodeValue(b *bytes.Buffer, v Value) {
	switch x := v.(type) {
	case nil:
		b.WriteByte(byte(tagNull))
	case bool:
		b.WriteByte(byte(tagBool))
		if x {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	case int64:
		b.WriteByte(byte(tagInt))
		b.Write(binary.LittleEndian.AppendUint64(nil, uint64(x))) //nolint:gosec // G115: two's complement bits.
	case float64:
		b.WriteByte(byte(tagDouble))
		b.Write(binary.LittleEndian.AppendUint64(nil, math.Float64bits(x)))
	case string:
		b.WriteByte(byte(tagString))
		putString(b, x)
	case decimal.Decimal:
		b.WriteByte(byte(tagDecimal))
		putString(b, x.String())
	case Money:
		b.WriteByte(byte(tagMoney))
		putString(b, x.Amount.String())
		b.WriteString(x.Currency)
	case Date:
		b.WriteByte(byte(tagDate))
		putVarint(b, int64(x))
	case DateTime:
		b.WriteByte(byte(tagDateTime))
		putVarint(b, x.Millis)
		putVarint(b, int64(x.Offset))
	case Duration:
		b.WriteByte(byte(tagDuration))
		putVarint(b, int64(x))
	case Color:
		b.WriteByte(byte(tagColor))
		b.Write(binary.BigEndian.AppendUint32(nil, uint32(x)))
	case List:
		b.WriteByte(byte(tagList))
		putUvarint(b, uint64(len(x)))
		for _, e := range x {
			encodeValue(b, e)
		}
	case Map:
		b.WriteByte(byte(tagMap))
		putUvarint(b, uint64(len(x)))
		for _, k := range sortedKeys(x) {
			putString(b, k)
			encodeValue(b, x[k])
		}
	}
}

// reader decodes program bytes; every read is bounds-checked.
type reader struct {
	data []byte
	pos  int
	err  error
}

func (r *reader) fail(msg string) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s at byte %d", ErrInvalidProgram, msg, r.pos)
	}
}

func (r *reader) uvarint() uint64 {
	n, w := binary.Uvarint(r.data[r.pos:])
	if w <= 0 {
		r.fail("bad varint")
		return 0
	}
	r.pos += w
	return n
}

func (r *reader) varint() int64 {
	n, w := binary.Varint(r.data[r.pos:])
	if w <= 0 {
		r.fail("bad varint")
		return 0
	}
	r.pos += w
	return n
}

func (r *reader) bytes(n uint64) []byte {
	if r.err != nil || n > uint64(len(r.data)-r.pos) { //nolint:gosec // G115: pos ≤ len(data).
		r.fail("truncated")
		return nil
	}
	out := r.data[r.pos : r.pos+int(n)] //nolint:gosec // G115: bounded by the length check.
	r.pos += int(n)                     //nolint:gosec // G115: bounded by the length check.
	return out
}

func (r *reader) str() string {
	b := r.bytes(r.uvarint())
	if !utf8.Valid(b) {
		r.fail("invalid UTF-8")
	}
	return string(b)
}

// count reads a collection length, bounded by the remaining bytes so that
// a forged count cannot allocate.
func (r *reader) count() int {
	n := r.uvarint()
	if n > uint64(len(r.data)-r.pos) { //nolint:gosec // G115: pos ≤ len(data).
		r.fail("count exceeds the data")
		return 0
	}
	return int(n) //nolint:gosec // G115: bounded by the data length.
}

// value decodes a tagged constant, at most depth levels deep.
func (r *reader) value(depth int) Value {
	if depth == 0 {
		r.fail("constant nested too deeply")
		return nil
	}
	tag := r.bytes(1)
	if tag == nil {
		return nil
	}
	switch constTag(tag[0]) {
	case tagNull:
		return nil
	case tagBool:
		b := r.bytes(1)
		if b == nil || b[0] > 1 {
			r.fail("bad bool")
			return nil
		}
		return b[0] == 1
	case tagInt:
		b := r.bytes(8)
		if b == nil {
			return nil
		}
		return int64(binary.LittleEndian.Uint64(b)) //nolint:gosec // G115: two's complement bits.
	case tagDouble:
		b := r.bytes(8)
		if b == nil {
			return nil
		}
		f := math.Float64frombits(binary.LittleEndian.Uint64(b))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			r.fail("non-finite double")
		}
		return f
	case tagString:
		return r.str()
	case tagDecimal:
		d, err := decimal.Parse(r.str())
		if err != nil {
			r.fail("bad decimal")
		}
		return d
	case tagMoney:
		d, err := decimal.Parse(r.str())
		code := string(r.bytes(3))
		if _, known := minorUnits(code); err != nil || !known {
			r.fail("bad money")
		}
		return Money{Amount: d, Currency: code}
	default:
		return r.value2(constTag(tag[0]), depth)
	}
}

// value2 decodes the remaining constant kinds.
func (r *reader) value2(tag constTag, depth int) Value {
	switch tag {
	case tagDate:
		d := r.varint()
		if !validDate(d) {
			r.fail("date out of range")
		}
		return Date(d)
	case tagDateTime:
		ms, off := r.varint(), r.varint()
		if off < -maxOffset || off > maxOffset {
			r.fail("offset out of range")
			return nil
		}
		t := DateTime{Millis: ms, Offset: int32(off)}
		if days, _ := t.local(); !validDate(days) {
			r.fail("dateTime out of range")
		}
		return t
	case tagDuration:
		return Duration(r.varint())
	case tagColor:
		b := r.bytes(4)
		if b == nil {
			return nil
		}
		return Color(binary.BigEndian.Uint32(b))
	case tagList:
		n := r.count()
		out := make(List, 0, n)
		for range n {
			out = append(out, r.value(depth-1))
		}
		return out
	case tagMap:
		n := r.count()
		out := make(Map, n)
		prev := ""
		for i := range n {
			k := r.str()
			if i > 0 && k <= prev {
				r.fail("map keys out of order")
			}
			prev = k
			out[k] = r.value(depth - 1)
		}
		return out
	default:
		r.fail("unknown constant tag")
		return nil
	}
}

// maxConstantDepth bounds the nesting of decoded constants.
const maxConstantDepth = 64

// Decode reads and verifies an encoded program.
func Decode(data []byte) (*Program, error) {
	r := &reader{data: data}
	if v := r.uvarint(); v != BytecodeVersion && r.err == nil {
		return nil, fmt.Errorf("%w: version %d is not supported", ErrInvalidProgram, v)
	}
	p := &Program{}
	p.Locals = int(min(r.uvarint(), maxLocals))       //nolint:gosec // G115: clamped.
	p.MaxStack = int(min(r.uvarint(), math.MaxInt32)) //nolint:gosec // G115: clamped.
	p.Result = r.str()
	n := r.count()
	for range n {
		p.Constants = append(p.Constants, r.value(maxConstantDepth))
	}
	n = r.count()
	for range n {
		p.Reads = append(p.Reads, r.str())
	}
	p.Code = slices.Clone(r.bytes(r.uvarint()))
	if r.err == nil && r.pos != len(data) {
		r.fail("trailing data")
	}
	if r.err != nil {
		return nil, r.err
	}
	if err := p.verify(); err != nil {
		return nil, err
	}
	return p, nil
}

// instr is a decoded instruction.
type instr struct {
	op       Opcode
	operands []uint32
	at, next int
}

// decodeAt decodes the instruction at pc.
func (p *Program) decodeAt(pc int) (instr, bool) {
	op := Opcode(p.Code[pc])
	if int(op) >= len(opcodeInfo) || opcodeInfo[op].name == "" {
		return instr{}, false
	}
	in := instr{op: op, at: pc}
	pos := pc + 1
	for _, w := range opcodeInfo[op].operands {
		if pos+w > len(p.Code) {
			return instr{}, false
		}
		var v uint32
		for i := range w {
			v |= uint32(p.Code[pos+i]) << (8 * i)
		}
		in.operands = append(in.operands, v)
		pos += w
	}
	in.next = pos
	return in, true
}

// verify checks that every instruction decodes and every operand is in
// range: constants, locals, jump targets on instruction boundaries,
// overloads and comparison kinds. The VM still checks operand types.
func (p *Program) verify() error {
	starts := map[int]bool{}
	var jumps []int
	for pc := 0; pc < len(p.Code); {
		in, ok := p.decodeAt(pc)
		if !ok {
			return fmt.Errorf("%w: bad instruction at %d", ErrInvalidProgram, pc)
		}
		starts[pc] = true
		if err := p.verifyOperands(in, &jumps); err != nil {
			return err
		}
		pc = in.next
	}
	for _, t := range jumps {
		if !starts[t] && t != len(p.Code) {
			return fmt.Errorf("%w: jump into an instruction at %d", ErrInvalidProgram, t)
		}
	}
	if len(p.Code) == 0 {
		return fmt.Errorf("%w: empty code", ErrInvalidProgram)
	}
	// Every instruction adds at most one value, so no valid program needs
	// more stack than it has bytes of code.
	if p.MaxStack < 1 || p.MaxStack > len(p.Code) {
		return fmt.Errorf("%w: max stack %d for %d bytes of code", ErrInvalidProgram, p.MaxStack, len(p.Code))
	}
	return nil
}

// verifyOperands checks the operands of one instruction.
func (p *Program) verifyOperands(in instr, jumps *[]int) error {
	bad := func(what string) error {
		return fmt.Errorf("%w: %s %s at %d", ErrInvalidProgram, in.op, what, in.at)
	}
	switch in.op {
	case OpConst, OpLoadRoot, OpGetField, OpMapGet:
		if int(in.operands[0]) >= len(p.Constants) {
			return bad("constant out of range")
		}
		if _, isString := p.Constants[in.operands[0]].(string); in.op != OpConst && !isString {
			return bad("needs a string constant")
		}
	case OpLoadLocal, OpIterInit, OpIterList, OpIterNext:
		for _, slot := range in.operands[:min(len(in.operands), 2)] {
			if int(slot) >= p.Locals {
				return bad("local out of range")
			}
		}
	case OpCall:
		return verifyCall(in, bad)
	case OpSortBy:
		if in.operands[0] == 0 || in.operands[0] > uint32(CmpDuration) {
			return bad("unknown comparison kind")
		}
	}
	if isJump(in.op) {
		*jumps = append(*jumps, int(in.operands[len(in.operands)-1]))
	}
	return nil
}

// isJump reports whether the last operand of op is a jump target.
func isJump(op Opcode) bool {
	switch op {
	case OpJump, OpJumpIfFalse, OpJumpIfTrue, OpAndJump, OpOrJump, OpNullJump, OpCoalesceJump, OpIterNext:
		return true
	default:
		return false
	}
}

// verifyCall checks the function and argument count of a CALL.
func verifyCall(in instr, bad func(string) error) error {
	if in.operands[0] == 0 || int(in.operands[0]) > len(stdOverloads) {
		return bad("unknown function")
	}
	def := stdOverloads[in.operands[0]-1]
	if n := int(in.operands[1]); n != len(def.params) && (!def.variadic || n == 0) {
		return bad("wrong argument count")
	}
	return nil
}

// Disassemble returns one line per instruction: offset, opcode and
// operands, with constants, roots and functions spelled out.
func (p *Program) Disassemble() []string {
	var out []string
	for pc := 0; pc < len(p.Code); {
		in, ok := p.decodeAt(pc)
		if !ok {
			return append(out, fmt.Sprintf("%04d ???", pc))
		}
		parts := []string{fmt.Sprintf("%04d %s", pc, in.op)}
		for _, v := range in.operands {
			parts = append(parts, fmt.Sprint(v))
		}
		switch in.op {
		case OpConst, OpLoadRoot, OpGetField, OpMapGet:
			parts = append(parts, "; "+describe(p.Constants[in.operands[0]]))
		case OpCall:
			parts = append(parts, "; "+stdOverloads[in.operands[0]-1].name)
		}
		out = append(out, strings.Join(parts, " "))
		pc = in.next
	}
	return out
}

// describe renders a constant for the disassembly.
func describe(v Value) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case decimal.Decimal:
		return x.String() + "d"
	case float64:
		return formatDouble(x)
	case nil:
		return "null"
	default:
		return fmt.Sprint(x)
	}
}
