// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `<phoneNumberMetadata><territories>
<territory id="GB" mainCountryForCode="true" countryCode="44" internationalPrefix="00" nationalPrefix="0">
  <availableFormats><numberFormat pattern="(\d)"><format>$1</format></numberFormat></availableFormats>
  <generalDesc><nationalNumberPattern>
    [1-9]\d{9}
  </nationalNumberPattern></generalDesc>
  <fixedLine><possibleLengths national="10" localOnly="[6-7]"/><exampleNumber>1212345678</exampleNumber>
    <nationalNumberPattern>1\d{9}</nationalNumberPattern></fixedLine>
  <mobile><possibleLengths national="9,10"/><nationalNumberPattern>7\d{8,9}</nationalNumberPattern></mobile>
  <noInternationalDialling><possibleLengths national="[11-12]"/><nationalNumberPattern>8\d{10,11}</nationalNumberPattern></noInternationalDialling>
</territory>
</territories></phoneNumberMetadata>`

// TestDerive checks the resolution of lengths, prefixes and patterns, and
// that the output is deterministic.
func TestDerive(t *testing.T) {
	src := source{Project: "libphonenumber", Version: "v1.0.0", Commit: strings.Repeat("a", 40), File: "f"}
	a, err := derive([]byte(sample), src)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := derive([]byte(sample), src)
	if !bytes.Equal(a, b) {
		t.Error("not deterministic")
	}
	for _, want := range []string{`"nationalPrefixForParsing": "0"`, `"pattern": "[1-9]\\d{9}"`, `"localOnly": [`, `"example": "1212345678"`, `"sha256": "`} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("output lacks %s:\n%s", want, a)
		}
	}
	if bytes.Contains(a, []byte("11")) && bytes.Contains(a, []byte("8\\d{10")) {
		t.Error("noInternationalDialling is not a type")
	}
	for _, bad := range []string{
		strings.Replace(sample, `countryCode="44"`, `countryCode="x"`, 1),
		strings.Replace(sample, `national="9,10"`, `national="[9]"`, 1),
		strings.Replace(sample, `7\d{8,9}`, `7(\d`, 1),
		strings.Replace(sample, `nationalPrefix="0"`, `nationalPrefix="0" nationalPrefixForParsing="0(1)" nationalPrefixTransformRule="\1"`, 1),
		"<x",
		"<phoneNumberMetadata/>",
	} {
		if _, err := derive([]byte(bad), src); err == nil {
			t.Errorf("accepted %.60q", bad)
		}
	}
}

// TestRun checks the command's usage and output.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "m.xml"), filepath.Join(dir, "phone.json")
	if err := os.WriteFile(in, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-xml", in, "-version", "v1.0.0", "-commit", "c", "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Errorf("usage exit %d", code)
	}
	if code := run([]string{"-xml", filepath.Join(dir, "none"), "-version", "v", "-commit", "c", "-out", out}, &stdout, &stderr); code != 2 {
		t.Errorf("missing input exit %d", code)
	}
}
