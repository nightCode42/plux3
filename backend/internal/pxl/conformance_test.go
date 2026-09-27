// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// update rewrites the golden fields of the conformance vectors.
var update = flag.Bool("update", false, "rewrite the golden type, program, asm and reads of schema/testdata/pxl")

// vectorDir holds the conformance vectors shared with the Dart VM.
var vectorDir = filepath.Join("..", "..", "..", "schema", "testdata", "pxl")

// vectorFile is one file of conformance vectors (PXL-007, QA-003).
type vectorFile struct {
	Description string         `json:"description"`
	Env         EnvSpec        `json:"env"`
	Limits      *vectorLimits  `json:"limits,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Cases       []*vectorCase  `json:"cases"`
}

// vectorLimits overrides limits for a file, e.g. a small budget.
type vectorLimits struct {
	Budget         int64 `json:"budget,omitempty"`
	StringLength   int64 `json:"stringLength,omitempty"`
	CollectionSize int64 `json:"collectionSize,omitempty"`
	DecimalDigits  int64 `json:"decimalDigits,omitempty"`
}

// vectorCase is one expression with its expected outcome and goldens.
type vectorCase struct {
	Name        string          `json:"name"`
	Expr        string          `json:"expr"`
	Inputs      map[string]any  `json:"inputs,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Error       string          `json:"error,omitempty"`
	Diagnostics []vectorDiag    `json:"diagnostics,omitempty"`
	Type        string          `json:"type,omitempty"`
	Program     string          `json:"program,omitempty"`
	Asm         []string        `json:"asm,omitempty"`
	Reads       []string        `json:"reads,omitempty"`
}

// vectorDiag is an expected compile-time diagnostic.
type vectorDiag struct {
	Code  string `json:"code"`
	Range [2]int `json:"range"`
}

// limits returns the limits of a file.
func (f *vectorFile) limits() Limits {
	lim := DefaultOptions().Limits
	if l := f.Limits; l != nil {
		lim.Budget = orDefault(l.Budget, lim.Budget)
		lim.StringLength = orDefault(l.StringLength, lim.StringLength)
		lim.CollectionSize = orDefault(l.CollectionSize, lim.CollectionSize)
		lim.DecimalDigits = orDefault(l.DecimalDigits, lim.DecimalDigits)
	}
	return lim
}

func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}

// decodeJSON decodes with json.Number, as FromJSON expects.
func decodeJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// TestConformance runs every vector of schema/testdata/pxl: compile, check
// the diagnostics or the goldens, evaluate the program — also after an
// encode/decode round trip — and compare the value or the error kind.
//
// Verifies: PXL-001, PXL-002, PXL-003, PXL-005, PXL-006, PXL-007, CMP-004, CMP-023, QA-003.
func TestConformance(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(vectorDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors in %s: %v", vectorDir, err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path) //nolint:gosec // G304: test vectors under the repository root.
			if err != nil {
				t.Fatal(err)
			}
			var f vectorFile
			if err := decodeJSON(data, &f); err != nil {
				t.Fatal(err)
			}
			env, err := NewEnv(f.Env)
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]bool{}
			for _, c := range f.Cases {
				if names[c.Name] {
					t.Errorf("duplicate case name %q", c.Name)
				}
				names[c.Name] = true
				runCase(t, env, &f, c)
			}
			if *update {
				writeVectors(t, path, &f)
			}
		})
	}
}

// runCase checks one case.
func runCase(t *testing.T, env *Env, f *vectorFile, c *vectorCase) {
	t.Helper()
	opts := DefaultOptions()
	opts.Limits = f.limits()
	p, typ, diags := Compile(c.Expr, env, opts, plxerr.Location{Path: "/expr"})
	var errs []vectorDiag
	for _, d := range diags {
		if d.Severity == plxerr.SeverityError {
			errs = append(errs, vectorDiag{Code: d.Code.String(), Range: [2]int{d.Range.Start, d.Range.End}})
		}
	}
	if len(c.Diagnostics) > 0 || p == nil {
		if !slices.Equal(errs, c.Diagnostics) {
			t.Errorf("%s: %q: diagnostics %v, want %v (%v)", c.Name, c.Expr, errs, c.Diagnostics, diags)
		}
		return
	}
	golden := vectorCase{Type: typ.String(), Program: base64.StdEncoding.EncodeToString(p.Encode()), Asm: p.Disassemble(), Reads: p.Reads}
	if *update {
		c.Type, c.Program, c.Asm, c.Reads = golden.Type, golden.Program, golden.Asm, golden.Reads
	} else if c.Type != golden.Type || c.Program != golden.Program || !slices.Equal(c.Asm, golden.Asm) || !slices.Equal(c.Reads, golden.Reads) {
		t.Errorf("%s: %q: goldens differ; run the test with -update and review:\n got %s %v %v", c.Name, c.Expr, golden.Type, golden.Reads, golden.Asm)
	}
	inputs := c.Inputs
	if inputs == nil {
		inputs = f.Inputs
	}
	values, err := inputValues(env, inputs)
	if err != nil {
		t.Fatalf("%s: inputs: %v", c.Name, err)
	}
	decoded, err := Decode(p.Encode())
	if err != nil {
		t.Fatalf("%s: decode: %v", c.Name, err)
	}
	for _, prog := range []*Program{p, decoded} {
		v, err := prog.Eval(values, f.limits())
		checkOutcome(t, c, typ, v, err)
	}
}

// inputValues converts the JSON inputs by root type.
func inputValues(env *Env, inputs map[string]any) (map[string]Value, error) {
	out := map[string]Value{}
	for k, v := range inputs {
		t, ok := env.Root(k)
		if !ok {
			return nil, errors.New("unknown root " + k)
		}
		x, err := FromJSON(t, v)
		if err != nil {
			return nil, err
		}
		out[k] = x
	}
	return out, nil
}

// checkOutcome compares a result with the expected value or error.
func checkOutcome(t *testing.T, c *vectorCase, typ *Type, v Value, err error) {
	t.Helper()
	if c.Error != "" {
		var e *EvalError
		if !errors.As(err, &e) || e.Kind.String() != c.Error {
			t.Errorf("%s: %q: got %v, %v; want error %s", c.Name, c.Expr, v, err, c.Error)
		}
		return
	}
	if err != nil {
		t.Errorf("%s: %q: unexpected error %v", c.Name, c.Expr, err)
		return
	}
	got, _ := json.Marshal(ToJSON(typ, v))
	var g, w any
	if decodeJSON(got, &g) != nil || decodeJSON(c.Value, &w) != nil || !jsonEqual(g, w) {
		t.Errorf("%s: %q = %s, want %s", c.Name, c.Expr, got, c.Value)
	}
}

// jsonEqual compares JSON values, numbers by exact value.
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		rx, okx := new(big.Rat).SetString(x.String())
		ry, oky := new(big.Rat).SetString(y.String())
		return okx && oky && rx.Cmp(ry) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if w, present := y[k]; !present || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// writeVectors rewrites a vector file with its goldens, two-space indented
// with one asm line per entry.
func writeVectors(t *testing.T, path string, f *vectorFile) {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
