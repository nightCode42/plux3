// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Range is a half-open range of Unicode code points inside a PXL expression
// string, so that editors can underline the exact characters (CMP-004).
type Range struct {
	// Start is the offset of the first code point.
	Start int `json:"start"`
	// End is the offset after the last code point.
	End int `json:"end"`
}

// Location identifies a value inside a project.
type Location struct {
	// File is the document's path in the project layout, with forward slashes.
	File string `json:"file,omitempty"`
	// Path is a JSON Pointer (RFC 6901) to the value inside the document.
	Path string `json:"path"`
	// Range narrows the location to characters inside a PXL expression.
	Range *Range `json:"range,omitempty"`
}

// PatchOp is one JSON Patch (RFC 6902) operation of a machine-applicable fix.
type PatchOp struct {
	// Op is "add", "remove", "replace", "move", "copy" or "test".
	Op string `json:"op"`
	// Path is the JSON Pointer the operation applies to.
	Path string `json:"path"`
	// From is the source pointer of "move" and "copy".
	From string `json:"from,omitempty"`
	// Value is the JSON value of "add", "replace" and "test".
	Value json.RawMessage `json:"value,omitempty"`
}

// Diagnostic is a finding about a document (ADR-0018).
type Diagnostic struct {
	// Code identifies the problem; Reason is its stable name.
	Code   Code   `json:"code"`
	Reason Reason `json:"reason"`
	// Severity is at least the definition's default.
	Severity Severity `json:"severity"`
	// Location is where the problem is.
	Location
	// Message states the problem with the specific values involved.
	Message string `json:"message"`
	// Cause and Fix come from the definition.
	Cause string `json:"cause"`
	Fix   string `json:"fix"`
	// Patch is an optional machine-applicable fix.
	Patch []PatchOp `json:"patch,omitempty"`
	// DocURL links to the code's entry in the published catalogue.
	DocURL string `json:"docURL"`
	// Related lists other locations involved, e.g. an earlier duplicate.
	Related []Location `json:"related,omitempty"`
}

// NewDiagnostic builds a diagnostic for a registered code at loc. The message
// is formatted from format and args; it must not contain sensitive values.
// An unregistered code is a programming error and yields an internal
// compiler error diagnostic instead, so the problem is still reported.
func NewDiagnostic(code Code, loc Location, format string, args ...any) Diagnostic {
	def, ok := Lookup(code)
	msg := fmt.Sprintf(format, args...)
	if !ok {
		def, _ = Lookup(InternalCompilerError)
		msg = fmt.Sprintf("unregistered diagnostic code %s: %s", code, msg)
	}
	return Diagnostic{
		Code:     def.Code,
		Reason:   def.Reason,
		Severity: def.Severity,
		Location: loc,
		Message:  msg,
		Cause:    def.Cause,
		Fix:      def.Fix,
		DocURL:   def.Code.DocURL(),
	}
}

// WithSeverity returns a copy with the severity raised to s. A severity below
// the definition's default is ignored: policy may raise, never lower.
func (d Diagnostic) WithSeverity(s Severity) Diagnostic {
	if s > d.Severity {
		d.Severity = s
	}
	return d
}

// WithFix returns a copy with a specialised fix text and an optional patch.
func (d Diagnostic) WithFix(fix string, patch ...PatchOp) Diagnostic {
	d.Fix = fix
	d.Patch = append([]PatchOp(nil), patch...)
	return d
}

// WithRelated returns a copy with additional related locations.
func (d Diagnostic) WithRelated(locs ...Location) Diagnostic {
	d.Related = append(append([]Location(nil), d.Related...), locs...)
	return d
}

// String formats the diagnostic for terminals:
// "file#path[start:end]: severity PLX-NNNN message".
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.File)
	b.WriteString("#")
	b.WriteString(d.Path)
	if d.Range != nil {
		fmt.Fprintf(&b, "[%d:%d]", d.Range.Start, d.Range.End)
	}
	fmt.Fprintf(&b, ": %s %s %s", d.Severity, d.Code, d.Message)
	return b.String()
}

// Diagnostics is a list of diagnostics.
type Diagnostics []Diagnostic

// HasErrors reports whether any diagnostic has error severity.
func (ds Diagnostics) HasErrors() bool {
	return slices.ContainsFunc(ds, func(d Diagnostic) bool { return d.Severity == SeverityError })
}

// Count returns the number of diagnostics with the given severity.
func (ds Diagnostics) Count(s Severity) int {
	n := 0
	for _, d := range ds {
		if d.Severity == s {
			n++
		}
	}
	return n
}

// Sort orders diagnostics by file, path, range, code and message, so output
// is deterministic whatever order the checks ran in.
func (ds Diagnostics) Sort() {
	slices.SortStableFunc(ds, compareDiagnostics)
}

// compareDiagnostics is the total order used by Sort.
func compareDiagnostics(a, b Diagnostic) int {
	return cmp.Or(
		cmp.Compare(a.File, b.File),
		cmp.Compare(a.Path, b.Path),
		compareRanges(a.Range, b.Range),
		cmp.Compare(a.Code, b.Code),
		cmp.Compare(a.Message, b.Message),
	)
}

// compareRanges orders absent ranges first, then by start and end.
func compareRanges(a, b *Range) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.End, b.End))
}

// Pointer builds an RFC 6901 JSON Pointer from reference tokens, escaping
// "~" as "~0" and "/" as "~1". Pointer() is "", the whole document.
func Pointer(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}
