// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
)

// emitter writes the bytecode of a typed IR tree.
type emitter struct {
	code      []byte
	consts    []Value
	constKeys map[string]int
	depth     int
	maxDepth  int
}

// emitProgram compiles root into a program.
func emitProgram(root *ir, locals int) *Program {
	e := &emitter{constKeys: map[string]int{}}
	e.emit(root)
	reads := map[string]bool{}
	collectReads(root, reads)
	return &Program{
		Locals:    locals,
		MaxStack:  e.maxDepth,
		Result:    root.typ.String(),
		Constants: e.consts,
		Reads:     minimalPaths(reads),
		Code:      e.code,
	}
}

// op appends an instruction and applies its stack effect.
func (e *emitter) op(op Opcode, effect int, operands ...uint32) int {
	at := len(e.code)
	e.code = append(e.code, byte(op))
	for i, w := range opcodeInfo[op].operands {
		for b := range w {
			e.code = append(e.code, byte(operands[i]>>(8*b))) //nolint:gosec // G115: little-endian byte extraction.
		}
	}
	e.depth += effect
	e.maxDepth = max(e.maxDepth, e.depth)
	return at
}

// patch sets the u32 jump target of the instruction at `at`, whose target
// is its last operand, to the current end of code.
func (e *emitter) patch(at int) {
	end := at + 1
	for _, w := range opcodeInfo[e.code[at]].operands {
		end += w
	}
	binary.LittleEndian.PutUint32(e.code[end-4:end], uint32(len(e.code))) //nolint:gosec // G115: code is far below 4 GiB.
}

// constant returns the pool index of v, deduplicated by encoding.
func (e *emitter) constant(v Value) uint32 {
	var b bytes.Buffer
	encodeValue(&b, v)
	key := b.String()
	if i, ok := e.constKeys[key]; ok {
		return uint32(i) //nolint:gosec // G115: bounded by the pool size.
	}
	e.consts = append(e.consts, v)
	e.constKeys[key] = len(e.consts) - 1
	return uint32(len(e.consts) - 1) //nolint:gosec // G115: bounded by the pool size.
}

// emit writes n; afterwards the stack holds one more value.
func (e *emitter) emit(n *ir) {
	switch n.kind {
	case irConst:
		e.emitConst(n.val)
	case irRoot:
		e.op(OpLoadRoot, 1, e.constant(n.name))
	case irLocal:
		e.op(OpLoadLocal, 1, uint32(n.slot)) //nolint:gosec // G115: slots are below 256.
	case irAccess:
		e.emit(n.args[0])
		e.op(n.op, 0, e.constant(n.name))
	case irSafe:
		e.emit(n.args[0])
		j := e.op(OpNullJump, 0, 0)
		e.op(n.op, 0, e.constant(n.name))
		e.patch(j)
	case irCmp:
		e.emit(n.args[0])
		e.emit(n.args[1])
		e.op(n.op, -1)
		e.op(n.op2, 0)
	case irAnd, irOr, irCoalesce, irCond:
		e.emitControl(n)
	case irMacro:
		e.macro(n)
	default:
		e.emitOperation(n)
	}
}

// emitConst pushes a constant.
func (e *emitter) emitConst(v Value) {
	switch x := v.(type) {
	case nil:
		e.op(OpPushNull, 1)
	case bool:
		e.pushBool(x)
	default:
		e.op(OpConst, 1, e.constant(v))
	}
}

// emitControl writes the short-circuit operators and conditionals.
func (e *emitter) emitControl(n *ir) {
	if n.kind == irCond {
		e.emit(n.args[0])
		j := e.op(OpJumpIfFalse, -1, 0)
		e.emit(n.args[1])
		end := e.op(OpJump, -1, 0)
		e.patch(j)
		e.emit(n.args[2])
		e.patch(end)
		return
	}
	jump := map[irKind]Opcode{irAnd: OpAndJump, irOr: OpOrJump, irCoalesce: OpCoalesceJump}[n.kind]
	e.emit(n.args[0])
	j := e.op(jump, -1, 0)
	e.emit(n.args[1])
	e.patch(j)
}

// emitOperation writes operators, calls and collection literals.
func (e *emitter) emitOperation(n *ir) {
	for i, a := range n.args {
		if n.kind == irMap {
			e.emit(n.keys[i])
		}
		e.emit(a)
	}
	count := uint32(len(n.args)) //nolint:gosec // G115: bounded by the expression length.
	switch n.kind {
	case irCall:
		e.op(OpCall, 1-len(n.args), uint32(n.id), count) //nolint:gosec // G115: overload IDs are small.
	case irList:
		e.op(OpNewList, 1-len(n.args), count)
	case irMap:
		e.op(OpNewMap, 1-2*len(n.args), count)
	default:
		e.op(n.op, 1-len(n.args))
	}
}

// macro writes the loop of a receiver macro. The iterator lives in local
// slot, the variable in slot2.
func (e *emitter) macro(n *ir) {
	it, v := uint32(n.slot), uint32(n.slot2) //nolint:gosec // G115: slots are below 256.
	e.emit(n.args[0])
	e.op(OpIterInit, -1, it)
	switch n.macro {
	case "map", "filter", "sortBy":
		e.op(OpNewList, 1, 0)
		loop := len(e.code)
		next := e.op(OpIterNext, 0, it, v, 0)
		e.emit(n.args[1])
		if n.macro == "filter" {
			skip := e.op(OpJumpIfFalse, -1, 0)
			e.op(OpLoadLocal, 1, v)
			e.op(OpListAppend, -1)
			e.patch(skip)
		} else {
			e.op(OpListAppend, -1)
		}
		e.jumpTo(loop)
		e.patch(next)
		if n.macro == "sortBy" {
			e.op(OpIterList, 1, it)
			e.op(OpSortBy, -1, uint32(n.cmp))
		}
	default: // any, all: stop at the first item that decides the result
		exit := OpJumpIfTrue
		if n.macro == "all" {
			exit = OpJumpIfFalse
		}
		loop := len(e.code)
		next := e.op(OpIterNext, 0, it, v, 0)
		e.emit(n.args[1])
		found := e.op(exit, -1, 0)
		e.jumpTo(loop)
		e.patch(next)
		e.pushBool(n.macro == "all")
		end := e.op(OpJump, -1, 0)
		e.patch(found)
		e.pushBool(n.macro == "any")
		e.patch(end)
	}
}

// jumpTo appends a jump to an earlier offset.
func (e *emitter) jumpTo(target int) {
	e.op(OpJump, 0, uint32(target)) //nolint:gosec // G115: code is far below 4 GiB.
}

// pushBool pushes a boolean constant.
func (e *emitter) pushBool(b bool) {
	if b {
		e.op(OpPushTrue, 1)
	} else {
		e.op(OpPushFalse, 1)
	}
}

// pathOf returns the state path an expression reads, such as
// "page.loan.amount", when it is a root followed by constant accesses.
func pathOf(n *ir) (string, bool) {
	switch n.kind {
	case irRoot:
		return n.name, true
	case irAccess, irSafe:
		if p, ok := pathOf(n.args[0]); ok {
			return p + "." + n.name, true
		}
	}
	return "", false
}

// collectReads records the maximal state paths n reads (CMP-023).
func collectReads(n *ir, out map[string]bool) {
	if p, ok := pathOf(n); ok {
		out[p] = true
		return
	}
	for _, a := range n.args {
		collectReads(a, out)
	}
	for _, k := range n.keys {
		collectReads(k, out)
	}
}

// minimalPaths sorts the paths and drops those under another path.
func minimalPaths(paths map[string]bool) []string {
	out := []string{}
	for _, p := range sortedKeys(paths) {
		covered := false
		for q := range paths {
			covered = covered || strings.HasPrefix(p, q+".")
		}
		if !covered {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
