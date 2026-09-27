// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPXLFiles renders the committed sources.
func TestPXLFiles(t *testing.T) {
	t.Parallel()
	p, err := LoadPXL("../../..")
	if err != nil {
		t.Fatal(err)
	}
	files, err := PXLFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		for _, want := range []string{"DO NOT EDIT", "len"} {
			if !bytes.Contains(f.Content, []byte(want)) {
				t.Errorf("%s lacks %q", f.Path, want)
			}
		}
	}
}

// TestLoadPXLRejects mutates copies of the sources.
func TestLoadPXLRejects(t *testing.T) {
	t.Parallel()
	tests := map[string]struct{ file, old, repl, want string }{
		"opcode numbering": {"bytecode.json", `"code": 2,`, `"code": 3,`, "numbered 2"},
		"operand kind":     {"bytecode.json", `"u16"`, `"u64"`, "unknown operand"},
		"error name":       {"bytecode.json", `"name": "overflow"`, `"name": "Overflow"`, "lowerCamelCase"},
		"overload IDs":     {"stdlib.json", "\"id\": 2,\n          \"params\"", "\"id\": 9,\n          \"params\"", "sequential"},
		"signature type":   {"stdlib.json", `"type": "string"`, `"type": "Text"`, "unknown type Text"},
		"group":            {"stdlib.json", `"group": "regex"`, `"group": "regexp"`, "unknown group"},
		"macro numbering": {"stdlib.json", `"id": 1,
      "description": "Transforms`, `"id": 7,
      "description": "Transforms`, "macro map"},
		"currency order": {"currencies.json", `"code": "AED"`, `"code": "ZZZ"`, "out of order"},
		"currency units": {"currencies.json", `"minorUnits": 3`, `"minorUnits": 7`, "malformed"},
		"unknown field":  {"currencies.json", `"minorUnits": 0`, `"minorUnits": 0, "x": 1`, "unknown field"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "schema", "pxl")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{"bytecode.json", "stdlib.json", "currencies.json"} {
				data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "pxl", f))
				if err != nil {
					t.Fatal(err)
				}
				if f == tt.file {
					if !bytes.Contains(data, []byte(tt.old)) {
						t.Fatalf("%s lacks %q", f, tt.old)
					}
					data = bytes.Replace(data, []byte(tt.old), []byte(tt.repl), 1)
				}
				if err := os.WriteFile(filepath.Join(dir, f), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadPXL(root); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
		})
	}
}
