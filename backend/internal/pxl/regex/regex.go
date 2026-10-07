// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package regex is PXL's regular-expression engine (pxl.regex.v1): a
// bounded RE2 subset matched by a Pike VM in O(n·m) time for an input of n
// code points and a program of m instructions, with no backtracking. Its
// syntax, program size and matching semantics are specified once in
// schema/pxl/regex.md; the Dart runtime implements the same specification,
// and the vectors in schema/testdata/regex prove they agree.
package regex

import (
	"fmt"
	"math"
	"unicode/utf8"
)

// Limits bound a pattern; values come from the limits registry (LIM-001).
type Limits struct {
	// PatternLength is the longest pattern in code points.
	PatternLength int64
	// ProgramSize is the most instructions a compiled pattern may have.
	ProgramSize int64
	// Repeat is the largest count of a {n,m} repetition.
	Repeat int64
}

// Trusted are limits for patterns that come with the runtime, such as the
// phone metadata, rather than from a document.
var Trusted = Limits{PatternLength: math.MaxInt32, ProgramSize: math.MaxInt32, Repeat: 1000}

// ErrorKind classifies why a pattern is rejected.
type ErrorKind uint8

// The kinds of pattern errors.
const (
	// ErrSyntax: the pattern is not in the grammar.
	ErrSyntax ErrorKind = iota + 1
	// ErrPatternLength: the pattern is longer than Limits.PatternLength.
	ErrPatternLength
	// ErrProgramSize: the program would exceed Limits.ProgramSize.
	ErrProgramSize
	// ErrRepeat: a repeat count exceeds Limits.Repeat.
	ErrRepeat
)

// String returns the kind's name, as the shared vectors write it.
func (k ErrorKind) String() string {
	switch k {
	case ErrSyntax:
		return "syntax"
	case ErrPatternLength:
		return "patternLength"
	case ErrProgramSize:
		return "programSize"
	case ErrRepeat:
		return "repeat"
	}
	return fmt.Sprintf("ErrorKind(%d)", uint8(k))
}

// Error is a rejected pattern, with the code-point offset of the problem.
type Error struct {
	Kind    ErrorKind
	Offset  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("regex: %s at %d: %s", e.Kind, e.Offset, e.Message)
}

// opcode is a program instruction.
type opcode uint8

const (
	opRune  opcode = iota // consume the code point r
	opClass               // consume a code point in ranges
	opBegin               // assert the start of the input
	opEnd                 // assert the end of the input
	opSave                // record the position in capture slot arg
	opSplit               // continue at x, then (lower priority) at y
	opMatch               // a match ends here
)

// inst is one instruction; x is the next instruction of all but opMatch.
type inst struct {
	op     opcode
	r      rune
	ranges []rune
	arg    int
	x, y   int
}

// Regexp is a compiled pattern. It is immutable and safe for concurrent use.
type Regexp struct {
	prog   []inst
	start  int
	groups int
}

// Compile parses and compiles a pattern within lim.
func Compile(pattern string, lim Limits) (*Regexp, *Error) {
	if !utf8.ValidString(pattern) {
		at := 0
		for i := 0; ; at++ {
			r, w := utf8.DecodeRuneInString(pattern[i:])
			if r == utf8.RuneError && w == 1 {
				return nil, &Error{Kind: ErrSyntax, Offset: at, Message: "the pattern is not valid UTF-8"}
			}
			i += w
		}
	}
	src := []rune(pattern)
	if int64(len(src)) > lim.PatternLength {
		return nil, &Error{Kind: ErrPatternLength, Message: fmt.Sprintf("the pattern has %d code points; the limit is %d", len(src), lim.PatternLength)}
	}
	tree, groups, err := parse(src, lim)
	if err != nil {
		return nil, err
	}
	size := sizeOf(tree, lim.ProgramSize) + 3
	if size > lim.ProgramSize {
		return nil, &Error{Kind: ErrProgramSize, Message: fmt.Sprintf("the program would have more than %d instructions", lim.ProgramSize)}
	}
	c := &emitter{prog: make([]inst, 0, size)}
	match := c.add(inst{op: opMatch})
	end := c.add(inst{op: opSave, arg: 1, x: match})
	start := c.add(inst{op: opSave, arg: 0, x: c.emit(tree, end)})
	return &Regexp{prog: c.prog, start: start, groups: groups}, nil
}

// Size is the number of instructions of the program (schema/pxl/regex.md §3).
func (re *Regexp) Size() int { return len(re.prog) }

// Groups is the number of capturing groups.
func (re *Regexp) Groups() int { return re.groups }

// sizeOf computes a node's instruction count, saturating above limit.
func sizeOf(n *node, limit int64) int64 {
	sat := func(v int64) int64 { return min(v, limit+1) }
	switch n.kind {
	case nEmpty:
		return 0
	case nRune, nClass, nBegin, nEnd:
		return 1
	case nCapture:
		return sat(sizeOf(n.subs[0], limit) + 2)
	case nConcat, nAlt:
		var total int64
		for _, s := range n.subs {
			total = sat(total + sizeOf(s, limit))
		}
		if n.kind == nAlt {
			total = sat(total + int64(len(n.subs)-1))
		}
		return total
	}
	s := sizeOf(n.subs[0], limit)
	switch {
	case n.max < 0 && n.min == 0:
		if nullable(n.subs[0]) {
			return sat(s + 2)
		}
		return sat(s + 1)
	case n.max < 0:
		return sat(sat(int64(n.min-1)*s) + s + 1)
	}
	return sat(sat(int64(n.min)*s) + sat(int64(n.max-n.min)*(s+1)))
}

// nullable reports whether a node can match the empty string.
func nullable(n *node) bool {
	switch n.kind {
	case nEmpty, nBegin, nEnd:
		return true
	case nRune, nClass:
		return false
	case nCapture:
		return nullable(n.subs[0])
	case nConcat:
		for _, s := range n.subs {
			if !nullable(s) {
				return false
			}
		}
		return true
	case nAlt:
		for _, s := range n.subs {
			if nullable(s) {
				return true
			}
		}
		return false
	}
	return n.min == 0 || nullable(n.subs[0])
}

// emitter builds a program back to front: each node is emitted with the
// instruction that follows it, so no jumps are needed.
type emitter struct{ prog []inst }

func (c *emitter) add(i inst) int {
	c.prog = append(c.prog, i)
	return len(c.prog) - 1
}

// emit emits n followed by next and returns n's entry.
func (c *emitter) emit(n *node, next int) int {
	switch n.kind {
	case nEmpty:
		return next
	case nRune:
		return c.add(inst{op: opRune, r: n.r, x: next})
	case nClass:
		return c.add(inst{op: opClass, ranges: n.ranges, x: next})
	case nBegin:
		return c.add(inst{op: opBegin, x: next})
	case nEnd:
		return c.add(inst{op: opEnd, x: next})
	case nCapture:
		end := c.add(inst{op: opSave, arg: 2*n.index + 1, x: next})
		return c.add(inst{op: opSave, arg: 2 * n.index, x: c.emit(n.subs[0], end)})
	case nConcat:
		for i := len(n.subs) - 1; i >= 0; i-- {
			next = c.emit(n.subs[i], next)
		}
		return next
	case nAlt:
		entries := make([]int, len(n.subs))
		for i, s := range n.subs {
			entries[i] = c.emit(s, next)
		}
		e := entries[len(entries)-1]
		for i := len(entries) - 2; i >= 0; i-- {
			e = c.add(inst{op: opSplit, x: entries[i], y: e})
		}
		return e
	}
	return c.repeat(n, next)
}

// repeat emits x*, x+, x? and x{n,m}.
func (c *emitter) repeat(n *node, next int) int {
	sub := n.subs[0]
	switch {
	case n.max < 0 && n.min == 0:
		if nullable(sub) { // x* is (x+)? when x can match empty
			plus := c.plus(sub, next)
			return c.add(inst{op: opSplit, x: plus, y: next})
		}
		loop := c.add(inst{op: opSplit, y: next})
		c.prog[loop].x = c.emit(sub, loop)
		return loop
	case n.max < 0:
		e := c.plus(sub, next)
		for range n.min - 1 {
			e = c.emit(sub, e)
		}
		return e
	}
	e := next
	for range n.max - n.min { // nested: x{0,2} is (x(x)?)?
		e = c.add(inst{op: opSplit, x: c.emit(sub, e), y: next})
	}
	for range n.min {
		e = c.emit(sub, e)
	}
	return e
}

// plus emits x+ and returns its entry, the body.
func (c *emitter) plus(sub *node, next int) int {
	loop := c.add(inst{op: opSplit, y: next})
	body := c.emit(sub, loop)
	c.prog[loop].x = body
	return body
}

// Mode selects where a match may start and end.
type mode uint8

const (
	search mode = iota // anywhere
	prefix             // at the start
	full               // the whole input
)

// MatchString reports whether the pattern matches anywhere in s.
func (re *Regexp) MatchString(s string) bool { return re.run(decode(s), search, false) != nil }

// FullMatch reports whether the pattern matches all of s.
func (re *Regexp) FullMatch(s string) bool { return re.run(decode(s), full, false) != nil }

// Find returns the leftmost-first match of the pattern in s: pairs of
// code-point offsets, the whole match first and then each group, with -1
// for a group that did not participate; nil when nothing matches.
func (re *Regexp) Find(s string) []int { return re.run(decode(s), search, true) }

// Prefix returns the leftmost-first match that starts at the beginning of
// s, as Find does; nil when none does.
func (re *Regexp) Prefix(s string) []int { return re.run(decode(s), prefix, true) }

// decode reads s as code points; an invalid UTF-8 byte reads as U+FFFD.
func decode(s string) []rune {
	out := make([]rune, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		out = append(out, r)
	}
	return out
}

// thread is a program position with its capture slots.
type thread struct {
	pc  int
	cap []int
}

// queue is the ordered set of threads at one input position.
type queue struct {
	seen    []bool
	threads []thread
}

func (q *queue) reset() {
	clear(q.seen)
	q.threads = q.threads[:0]
}

// job is a pending step of add: follow pc, or restore a capture slot.
type job struct {
	pc, slot, old int
	restore       bool
}

// run executes the Pike VM. With captures it returns the slots of the
// match; without, a non-nil empty slice when there is one.
func (re *Regexp) run(in []rune, m mode, captures bool) []int {
	ncap := 0
	if captures {
		ncap = 2 * (re.groups + 1)
	}
	v := &vm{re: re, in: in, ncap: ncap, scratch: make([]int, ncap)}
	cur := &queue{seen: make([]bool, len(re.prog))}
	next := &queue{seen: make([]bool, len(re.prog))}
	fresh := make([]int, ncap)
	for i := range fresh {
		fresh[i] = -1
	}
	var matched []int
	for pos := 0; ; pos++ {
		if matched == nil && (pos == 0 || m == search) {
			v.add(cur, re.start, pos, fresh)
		}
		next.reset()
		if caps, ok := v.step(cur, next, pos, m); ok {
			matched = caps
			if !captures {
				return []int{}
			}
		}
		if pos == len(in) {
			break
		}
		cur, next = next, cur
		if len(cur.threads) == 0 && (matched != nil || m != search) {
			break
		}
	}
	return matched
}

// vm is the state of one run: the program, the input, and the scratch
// buffers add reuses for every position.
type vm struct {
	re      *Regexp
	in      []rune
	ncap    int // the capture slots tracked; 0 without captures
	stack   []job
	scratch []int
}

// add adds the thread at pc to q, following splits, saves and anchors, so
// q holds only consuming instructions and matches, in priority order.
func (v *vm) add(q *queue, pc, pos int, caps []int) {
	copy(v.scratch, caps)
	v.stack = append(v.stack[:0], job{pc: pc})
	for len(v.stack) > 0 {
		j := v.stack[len(v.stack)-1]
		v.stack = v.stack[:len(v.stack)-1]
		if j.restore {
			v.scratch[j.slot] = j.old
			continue
		}
		if q.seen[j.pc] {
			continue
		}
		q.seen[j.pc] = true
		v.follow(q, j.pc, pos)
	}
}

// follow expands the instruction at pc: it pushes what an empty
// instruction leads to, and queues a consuming one or a match.
func (v *vm) follow(q *queue, pc, pos int) {
	i := &v.re.prog[pc]
	switch i.op {
	case opSplit:
		v.stack = append(v.stack, job{pc: i.y}, job{pc: i.x})
	case opSave:
		if i.arg < v.ncap {
			v.stack = append(v.stack, job{restore: true, slot: i.arg, old: v.scratch[i.arg]})
			v.scratch[i.arg] = pos
		}
		v.stack = append(v.stack, job{pc: i.x})
	case opBegin:
		if pos == 0 {
			v.stack = append(v.stack, job{pc: i.x})
		}
	case opEnd:
		if pos == len(v.in) {
			v.stack = append(v.stack, job{pc: i.x})
		}
	default:
		q.threads = append(q.threads, thread{pc: pc, cap: append([]int(nil), v.scratch...)})
	}
}

// step advances the threads of cur over the input at pos into next, in
// priority order. It stops at the first thread that matches and returns
// its slots, as lower-priority threads can only find worse matches.
func (v *vm) step(cur, next *queue, pos int, m mode) (caps []int, matched bool) {
	for _, t := range cur.threads {
		i := &v.re.prog[t.pc]
		if i.op == opMatch {
			if m == full && pos != len(v.in) {
				continue
			}
			return t.cap, true
		}
		if pos < len(v.in) && accepts(i, v.in[pos]) {
			v.add(next, i.x, pos+1, t.cap)
		}
	}
	return nil, false
}

// accepts reports whether a consuming instruction accepts r.
func accepts(i *inst, r rune) bool {
	if i.op == opRune {
		return r == i.r
	}
	lo, hi := 0, len(i.ranges)/2
	for lo < hi {
		mid := (lo + hi) / 2
		switch {
		case r < i.ranges[2*mid]:
			hi = mid
		case r > i.ranges[2*mid+1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}
