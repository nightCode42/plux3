// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// TestNewDiagnosticFillsTheDefinition_CMP_004 checks that a diagnostic
// carries code, reason, severity, location, message, cause, fix and link.
// Verifies: CMP-004, DX-003.
func TestNewDiagnosticFillsTheDefinition_CMP_004(t *testing.T) {
	t.Parallel()
	loc := Location{File: "plugins/loans/pages/calculator.page.json", Path: "/root/props/value/$expr", Range: &Range{Start: 5, End: 11}}

	d := NewDiagnostic(PXLUnknownIdentifier, loc, "unknown identifier %q", "amout")

	def, _ := Lookup(PXLUnknownIdentifier)
	if d.Code != PXLUnknownIdentifier || d.Reason != def.Reason || d.Severity != def.Severity {
		t.Errorf("identity = %s %s %s", d.Code, d.Reason, d.Severity)
	}
	if d.Location != loc || d.Message != `unknown identifier "amout"` {
		t.Errorf("location or message wrong: %+v", d)
	}
	if d.Cause != def.Cause || d.Fix != def.Fix || d.DocURL != PXLUnknownIdentifier.DocURL() {
		t.Errorf("definition fields not copied: %+v", d)
	}
}

// TestNewDiagnosticReportsUnregisteredCodesAsInternalErrors checks that a
// programming error still produces a visible diagnostic.
func TestNewDiagnosticReportsUnregisteredCodesAsInternalErrors(t *testing.T) {
	t.Parallel()
	d := NewDiagnostic(Code(1999), Location{Path: "/x"}, "boom")
	if d.Code != InternalCompilerError || !strings.Contains(d.Message, "PLX-1999") {
		t.Errorf("got %s %q", d.Code, d.Message)
	}
}

// TestWithSeverityOnlyRaises checks that policy can raise but never lower a
// severity below the definition's default.
func TestWithSeverityOnlyRaises(t *testing.T) {
	t.Parallel()
	d := NewDiagnostic(PageNodeBudget, Location{Path: "/root"}, "6000 nodes")
	if got := d.WithSeverity(SeverityError).Severity; got != SeverityError {
		t.Errorf("raise: got %s", got)
	}
	if got := d.WithSeverity(SeverityInfo).Severity; got != SeverityWarning {
		t.Errorf("lower: got %s, want warning", got)
	}
}

// TestWithFixAndRelatedDoNotAlias checks the builders copy their slices.
func TestWithFixAndRelatedDoNotAlias(t *testing.T) {
	t.Parallel()
	base := NewDiagnostic(DuplicateKey, Location{Path: "/pages/1"}, "duplicate key %q", "home")
	patch := []PatchOp{{Op: "replace", Path: "/pages/1/key", Value: json.RawMessage(`"home-2"`)}}
	a := base.WithFix("Rename the second page.", patch...).WithRelated(Location{Path: "/pages/0"})
	patch[0].Op = "remove"
	b := a.WithRelated(Location{Path: "/pages/5"})

	if a.Patch[0].Op != "replace" || a.Fix != "Rename the second page." {
		t.Errorf("patch aliased or fix lost: %+v", a.Patch)
	}
	if len(a.Related) != 1 || len(b.Related) != 2 || base.Related != nil {
		t.Errorf("related aliased: base %d, a %d, b %d", len(base.Related), len(a.Related), len(b.Related))
	}
}

// TestDiagnosticsSortIsDeterministic_CMP_004 checks the total order by file,
// path, range, code and message.
// Verifies: CMP-004.
func TestDiagnosticsSortIsDeterministic_CMP_004(t *testing.T) {
	t.Parallel()
	mk := func(file, path string, r *Range, c Code, msg string) Diagnostic {
		return NewDiagnostic(c, Location{File: file, Path: path, Range: r}, "%s", msg)
	}
	want := Diagnostics{
		mk("a.json", "/x", nil, UnknownProperty, "m"),
		mk("a.json", "/x", &Range{Start: 0, End: 2}, PXLSyntaxError, "m"),
		mk("a.json", "/x", &Range{Start: 0, End: 3}, PXLSyntaxError, "m"),
		mk("a.json", "/x", &Range{Start: 1, End: 2}, PXLSyntaxError, "m"),
		mk("a.json", "/y", nil, UnknownProperty, "a"),
		mk("a.json", "/y", nil, UnknownProperty, "b"),
		mk("a.json", "/y", nil, MissingProperty, "a"),
		mk("b.json", "", nil, InvalidJSON, "m"),
	}
	got := Diagnostics{want[7], want[5], want[3], want[0], want[6], want[2], want[4], want[1]}

	got.Sort()

	for i := range want {
		if got[i].String() != want[i].String() {
			t.Errorf("position %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestDiagnosticsCountsBySeverity checks HasErrors and Count.
func TestDiagnosticsCountsBySeverity(t *testing.T) {
	t.Parallel()
	ds := Diagnostics{
		NewDiagnostic(PageNodeBudget, Location{}, "w"),
		NewDiagnostic(PXLFeatureRequired, Location{}, "i"),
	}
	if ds.HasErrors() || ds.Count(SeverityWarning) != 1 || ds.Count(SeverityInfo) != 1 {
		t.Fatalf("counts wrong: %v", ds)
	}
	ds = append(ds, NewDiagnostic(UnknownProp, Location{}, "e"))
	if !ds.HasErrors() || ds.Count(SeverityError) != 1 {
		t.Fatal("error not counted")
	}
}

// TestDiagnosticStringAndJSONForms_CMP_004 pins the human-readable and JSON
// output formats used by the CLI.
// Verifies: CMP-004.
func TestDiagnosticStringAndJSONForms_CMP_004(t *testing.T) {
	t.Parallel()
	d := NewDiagnostic(PXLTypeMismatch, Location{File: "p.json", Path: "/a~1b", Range: &Range{Start: 2, End: 4}}, "cannot add string and int")

	if got, want := d.String(), "p.json#/a~1b[2:4]: error PLX-2002 cannot add string and int"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"code":"PLX-2002"`, `"reason":"PXL_TYPE_MISMATCH"`, `"severity":"error"`, `"file":"p.json"`,
		`"path":"/a~1b"`, `"range":{"start":2,"end":4}`, `"docURL":"` + CatalogueURL + `#plx-2002"`,
	} {
		if !strings.Contains(string(out), fragment) {
			t.Errorf("JSON %s lacks %s", out, fragment)
		}
	}
	var back Diagnostic
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.String() != d.String() || back.Cause != d.Cause {
		t.Errorf("round trip lost data: %+v", back)
	}
}

// TestPointerEscapesReferenceTokens checks RFC 6901 escaping.
func TestPointerEscapesReferenceTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tokens []string
		want   string
	}{
		{nil, ""},
		{[]string{"root"}, "/root"},
		{[]string{"props", "a/b", "m~n", ""}, "/props/a~1b/m~0n/"},
		{[]string{"~1"}, "/~01"},
	}
	for _, tt := range tests {
		if got := Pointer(tt.tokens...); got != tt.want {
			t.Errorf("Pointer(%q) = %q, want %q", tt.tokens, got, tt.want)
		}
	}
}

// TestCodeTextForms checks formatting, parsing and text marshalling of codes.
func TestCodeTextForms(t *testing.T) {
	t.Parallel()
	if UnknownProperty.String() != "PLX-1001" || Code(7).String() != "PLX-0007" || UnknownProperty.Anchor() != "plx-1001" {
		t.Fatal("String or Anchor wrong")
	}
	var c Code
	if err := c.UnmarshalText([]byte("PLX-2002")); err != nil || c != PXLTypeMismatch {
		t.Fatalf("UnmarshalText = %v, %v", c, err)
	}
	for _, bad := range []string{"", "PLX-", "PLX-12", "PLX-12345", "PLX-12a4", "plx-1001", "PLX-+001", "PLX-9999x"} {
		if _, err := ParseCode(bad); err == nil {
			t.Errorf("ParseCode(%q) accepted", bad)
		}
	}
	if err := c.UnmarshalText([]byte("E1")); err == nil {
		t.Error("UnmarshalText accepted E1")
	}
}

// TestSeverityTextForms checks severity marshalling and ordering.
func TestSeverityTextForms(t *testing.T) {
	t.Parallel()
	for _, s := range []Severity{SeverityInfo, SeverityWarning, SeverityError} {
		text, err := s.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var back Severity
		if err := back.UnmarshalText(text); err != nil || back != s {
			t.Errorf("round trip of %s gave %s, %v", s, back, err)
		}
	}
	if _, err := Severity(0).MarshalText(); err == nil {
		t.Error("zero severity marshalled")
	}
	if Severity(9).String() != "severity(9)" {
		t.Errorf("String of invalid severity = %s", Severity(9))
	}
	var s Severity
	if err := s.UnmarshalText([]byte("fatal")); err == nil {
		t.Error("unknown severity accepted")
	}
	if SeverityInfo >= SeverityWarning || SeverityWarning >= SeverityError {
		t.Error("severities are not ordered by gravity")
	}
}

// TestErrorFormatsAndMatchesByCode checks the Error type's text, wrapping and
// comparison by code.
func TestErrorFormatsAndMatchesByCode(t *testing.T) {
	t.Parallel()
	cause := fs.ErrNotExist
	var base *Error
	if !errors.As(Wrap(FileSystemError, cause, "read %s", "app.json"), &base) {
		t.Fatal("Wrap did not return *Error")
	}
	err := error(base.WithDetail("op", "read").WithDetail("a", "1"))

	want := "PLX-9102 FILE_SYSTEM_ERROR: read app.json (a=1, op=read): file does not exist"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if len(base.Details) != 0 {
		t.Error("WithDetail mutated the original")
	}
	if !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, Sentinel(FileSystemError)) || errors.Is(err, Sentinel(InvalidJSON)) {
		t.Error("errors.Is does not follow code and cause")
	}
	wrapped := fmt.Errorf("loader: %w", err)
	if code, ok := CodeOf(wrapped); !ok || code != FileSystemError {
		t.Errorf("CodeOf = %s, %v", code, ok)
	}
	if _, ok := CodeOf(cause); ok {
		t.Error("CodeOf found a code in a plain error")
	}
	if got := New(ProjectNotFound, "no app.json in %s", "x").Error(); got != "PLX-9101 PROJECT_NOT_FOUND: no app.json in x" {
		t.Errorf("New().Error() = %q", got)
	}
}
