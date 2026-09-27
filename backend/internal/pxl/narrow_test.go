// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: PXL-002.
func TestGuards(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	for _, tc := range []struct {
		src             string
		whenTrue, whenF []string
	}{
		{"page.result != null", []string{"page.result"}, nil},
		{"null != page.result", []string{"page.result"}, nil},
		{"page.result == null", nil, []string{"page.result"}},
		{"a != null && b.c != null", []string{"a", "b.c"}, nil},
		{"a == null || b == null", nil, []string{"a", "b"}},
		{"!(a == null)", []string{"a"}, nil},
		{"a?.b != null", []string{"a.b"}, nil},
		{"size(a) > 0", nil, nil},
		{"f(a) != null", nil, nil},
		{"a !=", nil, nil},
	} {
		tr, f := Guards(tc.src, opts)
		if !slices.Equal(tr, tc.whenTrue) || !slices.Equal(f, tc.whenF) {
			t.Errorf("%q: %v, %v", tc.src, tr, f)
		}
	}
}

// Verifies: PXL-002.
// A path an enclosing guard checked, and every prefix of it, reads as not
// null; without the guard it is an error.
func TestOptionsNonNull(t *testing.T) {
	t.Parallel()
	env, err := NewEnv(EnvSpec{
		Types: map[string]TypeSpec{"Loan": {Fields: map[string]string{"fee": "money?"}}},
		Roots: map[string]string{"page": "Loan?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	if _, _, diags := Compile("currency(page.fee)", env, opts, plxerr.Location{}); !diags.HasErrors() {
		t.Fatal("an unguarded nullable path compiled")
	}
	opts.NonNull = []string{"page.fee"}
	if _, typ, diags := Compile("currency(page.fee)", env, opts, plxerr.Location{}); diags.HasErrors() || typ.String() != "string" {
		t.Fatalf("guarded: %v %v", typ, diags)
	}
}
