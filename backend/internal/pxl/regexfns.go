// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl/phone"
	"github.com/nightCode42/plux3/backend/internal/pxl/regex"
)

// regexFns implements the regex and phone groups (pxl.regex.v1,
// pxl.phone.v1).
func regexFns() map[string]builtinFn {
	return map[string]builtinFn{
		"matches(string,string)": matches,
		"isPhone(string,string)": func(_ *vm, a []Value) (Value, *EvalError) {
			return just(phone.IsValid(arg[string](a, 0), arg[string](a, 1)))
		},
	}
}

// matches compiles the pattern — a constant the checker has already
// compiled — and searches s, after charging (n+1)·m operations for n code
// points of s and a program of m instructions, the Pike VM's bound
// (schema/pxl/regex.md §5).
func matches(m *vm, a []Value) (Value, *EvalError) {
	s, pattern := arg[string](a, 0), arg[string](a, 1)
	re, err := regex.Compile(pattern, m.lim.Regex)
	if err != nil {
		return nil, evalErr(ErrorInvalidArgument, "%v", err)
	}
	if err := m.charge((size(s) + 1) * int64(re.Size())); err != nil {
		return nil, err
	}
	return re.MatchString(s), nil
}

// checkGroupArgs checks the literal arguments of the regex and phone
// groups at compile time: a pattern of matches is a constant that compiles
// within the limits (PLX-2020–2022), and a literal region of isPhone has
// metadata (PLX-2023).
func (c *checker) checkGroupArgs(name string, x *ir, n *node) *ir {
	switch name {
	case "matches":
		p := x.args[1]
		s, ok := p.val.(string)
		if p.kind != irConst || !ok {
			return c.errorf(plxerr.PXLRegexNotConstant, n.args[1].span, "the pattern of matches must be a string literal, compiled at publish time")
		}
		if _, err := regex.Compile(s, c.limits.Regex); err != nil {
			code := plxerr.PXLRegexInvalid
			if err.Kind != regex.ErrSyntax {
				code = plxerr.PXLRegexTooLarge
			}
			return c.errorf(code, n.args[1].span, "%s (at code point %d of the pattern)", err.Message, err.Offset)
		}
	case "isPhone":
		if r, ok := x.args[1].val.(string); x.args[1].kind == irConst && ok && !phone.Known(r) {
			return c.errorf(plxerr.PXLUnknownPhoneRegion, n.args[1].span, "%q is not a region with phone metadata", r)
		}
	}
	return x
}
