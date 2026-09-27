// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// fuzzSeeds returns the expressions and programs of the conformance vectors.
func fuzzSeeds(t testing.TB) (exprs []string, programs [][]byte) {
	files, _ := filepath.Glob(filepath.Join(vectorDir, "*.json"))
	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // G304: test vectors under the repository root.
		if err != nil {
			t.Fatal(err)
		}
		var f vectorFile
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		for _, c := range f.Cases {
			exprs = append(exprs, c.Expr)
			if b, err := base64.StdEncoding.DecodeString(c.Program); err == nil && len(b) > 0 {
				programs = append(programs, b)
			}
		}
	}
	return exprs, programs
}

// fuzzInputs are values for the roots of propertyEnv.
func fuzzInputs() map[string]Value {
	return map[string]Value{"n": int64(3), "d": nil, "s": "ab", "xs": List{int64(1), int64(2)}, NowRoot: DateTime{}}
}

// FuzzCompile checks that compiling and evaluating any text never panics
// and that every program the compiler accepts decodes to itself.
//
// Verifies: PXL-001, CMP-004.
func FuzzCompile(f *testing.F) {
	exprs, _ := fuzzSeeds(f)
	for _, e := range exprs {
		f.Add(e)
	}
	env := propertyEnv(f)
	opts := DefaultOptions()
	opts.Limits.Budget = 2000
	f.Fuzz(func(t *testing.T, src string) {
		p, _, diags := Compile(src, env, opts, plxerr.Location{})
		if p == nil {
			if len(diags) == 0 {
				t.Fatalf("%q: rejected without a diagnostic", src)
			}
			for _, d := range diags {
				if d.Range == nil || d.Range.Start > d.Range.End {
					t.Fatalf("%q: bad range %v", src, d)
				}
			}
			return
		}
		back, err := Decode(p.Encode())
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		_, _ = back.Eval(fuzzInputs(), opts.Limits)
	})
}

// FuzzDecode checks that decoding and evaluating arbitrary bytes never
// panics: the runtime evaluates only verified programs, but a malformed
// one must fail with an error.
//
// Verifies: PXL-001, PXL-003.
func FuzzDecode(f *testing.F) {
	_, programs := fuzzSeeds(f)
	for _, p := range programs {
		f.Add(p)
	}
	lim := DefaultOptions().Limits
	lim.Budget = 2000
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Decode(data)
		if err != nil {
			return
		}
		_ = p.Disassemble()
		_, _ = p.Eval(fuzzInputs(), lim)
	})
}
