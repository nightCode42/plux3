// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"errors"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Options bound compilation and constant folding; values come from the
// limits registry (LIM-001).
type Options struct {
	// Limits bound the evaluation of constant subexpressions.
	Limits Limits
	// MaxLength is the longest expression in code points.
	MaxLength int64
	// MaxDepth is the deepest syntax tree.
	MaxDepth int64
}

// OptionsFrom reads the options from a resolved set of the registry.
func OptionsFrom(set limits.Set) Options {
	return Options{
		Limits:    LimitsFrom(set),
		MaxLength: set.Get(limits.PXLExpressionLength),
		MaxDepth:  set.Get(limits.PXLNestingDepth),
	}
}

// DefaultOptions are the options of the registry defaults.
func DefaultOptions() Options { return OptionsFrom(limits.Defaults()) }

// Compile parses, type-checks and folds src against env and returns the
// program, its result type and the diagnostics, located at loc with the
// code-point range of each problem (CMP-004). The program is nil when an
// error is reported. Compile never panics on any input.
func Compile(src string, env *Env, opts Options, loc plxerr.Location) (*Program, *Type, plxerr.Diagnostics) {
	return compile(src, env, opts, loc, true)
}

// compile implements Compile; folding can be switched off to test that
// folding and evaluation agree.
func compile(src string, env *Env, opts Options, loc plxerr.Location, folding bool) (*Program, *Type, plxerr.Diagnostics) {
	d := &diagnostics{src: src, loc: loc}
	if n := utf8.RuneCountInString(src); int64(n) > opts.MaxLength {
		d.add(plxerr.PXLExpressionTooComplex, Span{0, len(src)}, "the expression has %d code points; the limit is %d", n, opts.MaxLength)
		return nil, nil, d.out
	}
	if !utf8.ValidString(src) {
		d.add(plxerr.PXLSyntaxError, Span{0, len(src)}, "the expression is not valid UTF-8")
		return nil, nil, d.out
	}
	tree, serr := parse(src, int(opts.MaxDepth))
	if serr != nil {
		code := plxerr.PXLSyntaxError
		if int64(len(src)) > 0 && serr.msg == nestingMessage(opts.MaxDepth) {
			code = plxerr.PXLExpressionTooComplex
		}
		d.add(code, serr.span, "%s", serr.msg)
		return nil, nil, d.out
	}
	c := &checker{env: env, features: map[string]bool{}}
	root := c.check(tree)
	for _, x := range c.diags {
		d.add(x.code, x.span, "%s", x.msg)
	}
	if root == nil || len(c.diags) > 0 {
		return nil, nil, d.out
	}
	if folding {
		root = fold(root, c.locals, opts.Limits, d)
	}
	if d.hasErrors() {
		return nil, nil, d.out
	}
	p := emitProgram(root, c.locals)
	for f := range c.features {
		p.Features = append(p.Features, f)
		d.add(plxerr.PXLFeatureRequired, Span{0, len(src)}, "the expression needs the runtime feature %s", f)
	}
	slices.Sort(p.Features)
	return p, root.typ, d.out
}

// nestingMessage is the parser's message for too deep an expression.
func nestingMessage(depth int64) string {
	return "nesting deeper than " + strconv.FormatInt(depth, 10)
}

// diagnostics collects diagnostics with code-point ranges.
type diagnostics struct {
	src string
	loc plxerr.Location
	out plxerr.Diagnostics
}

// add records a diagnostic at a byte span.
func (d *diagnostics) add(code plxerr.Code, span Span, format string, args ...any) {
	loc := d.loc
	loc.Range = &plxerr.Range{
		Start: utf8.RuneCountInString(d.src[:min(span.Start, len(d.src))]),
		End:   utf8.RuneCountInString(d.src[:min(span.End, len(d.src))]),
	}
	d.out = append(d.out, plxerr.NewDiagnostic(code, loc, format, args...))
}

// hasErrors reports whether an error was recorded.
func (d *diagnostics) hasErrors() bool {
	for _, x := range d.out {
		if x.Severity == plxerr.SeverityError {
			return true
		}
	}
	return false
}

// fold replaces the largest constant subtrees with their values (CMP-022),
// evaluating them with the VM so folding and evaluation cannot disagree. A
// constant subtree that always fails is reported.
func fold(n *ir, locals int, lim Limits, d *diagnostics) *ir {
	if n.kind != irConst && constantTree(n, map[int]bool{}) {
		p := emitProgram(n, locals)
		v, err := p.Eval(nil, lim)
		if err == nil {
			return &ir{kind: irConst, typ: n.typ, val: v, span: n.span}
		}
		var e *EvalError
		if !errors.As(err, &e) {
			e = &EvalError{Kind: ErrorInvalidProgram, Message: err.Error()}
		}
		code := plxerr.PXLConstantError
		switch e.Kind {
		case ErrorBudgetExceeded:
			code = plxerr.PXLBudgetExceeded
		case ErrorCurrencyMismatch:
			code = plxerr.PXLCurrencyMismatch
		}
		d.add(code, n.span, "evaluating it fails with %s: %s", e.Kind, e.Message)
		return n
	}
	for i, a := range n.args {
		n.args[i] = fold(a, locals, lim, d)
	}
	for i, k := range n.keys {
		n.keys[i] = fold(k, locals, lim, d)
	}
	return n
}

// constantTree reports whether n reads no root, no variable bound outside
// it and calls no function of a later-phase group; bound holds the slots
// of enclosing macros inside the tree.
func constantTree(n *ir, bound map[int]bool) bool {
	switch {
	case n.kind == irRoot || n.noFold:
		return false
	case n.kind == irLocal:
		return bound[n.slot]
	case n.kind == irMacro:
		if !constantTree(n.args[0], bound) {
			return false
		}
		inner := map[int]bool{n.slot: true, n.slot2: true}
		for k := range bound {
			inner[k] = true
		}
		return constantTree(n.args[1], inner)
	}
	for _, a := range n.args {
		if !constantTree(a, bound) {
			return false
		}
	}
	for _, k := range n.keys {
		if !constantTree(k, bound) {
			return false
		}
	}
	return true
}
