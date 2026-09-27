// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Error is a Plux error with a registered code: a failure that is not a
// finding about a document, such as an unreadable file or a corrupt bundle.
// It wraps its cause, and errors.Is matches any *Error with the same code.
type Error struct {
	// Code identifies the failure.
	Code Code
	// Message describes this occurrence; it must not contain sensitive values.
	Message string
	// Details carries structured context, e.g. a section kind or a limit key.
	Details map[string]string
	// Err is the underlying cause, if any.
	Err error
}

// New returns an error with a registered code.
func New(code Code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap returns an error with a registered code that wraps cause.
func Wrap(code Code, cause error, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Err: cause}
}

// WithDetail returns a copy of e with one more detail.
func (e *Error) WithDetail(key, value string) *Error {
	c := *e
	c.Details = maps.Clone(e.Details)
	if c.Details == nil {
		c.Details = map[string]string{}
	}
	c.Details[key] = value
	return &c
}

// Reason returns the registered reason of the error's code.
func (e *Error) Reason() Reason {
	def, _ := Lookup(e.Code)
	return def.Reason
}

// Error formats "PLX-NNNN REASON: message (key=value, …): cause", with
// details in key order.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s", e.Code, e.Reason(), e.Message)
	if len(e.Details) > 0 {
		b.WriteString(" (")
		for i, k := range slices.Sorted(maps.Keys(e.Details)) {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%s", k, e.Details[k])
		}
		b.WriteString(")")
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is a *Error with the same code.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code
}

// CodeOf returns the code of the first *Error in err's chain.
func CodeOf(err error) (Code, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return 0, false
}

// Sentinel returns a comparable error for code, for use with errors.Is:
// errors.Is(err, plxerr.Sentinel(plxerr.BundleMalformed)).
func Sentinel(code Code) error { return &Error{Code: code} }
