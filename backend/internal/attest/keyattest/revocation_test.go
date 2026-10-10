// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"math/big"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

func TestParseRevocations(t *testing.T) {
	// Verifies: SEC-002.
	r, err := ParseRevocations([]byte(`{"entries":{
		"c35747a084470c3135aeefe2b8d40921":{"status":"REVOKED","reason":"KEY_COMPROMISE"},
		"0A":{"status":"SUSPENDED","reason":"SOFTWARE_FLAW"}}}`))
	if err != nil {
		t.Fatalf("ParseRevocations: %v", err)
	}
	big1, _ := new(big.Int).SetString("c35747a084470c3135aeefe2b8d40921", 16)
	tests := []struct {
		serial *big.Int
		want   string
		listed bool
	}{
		{big1, "REVOKED", true},
		{big.NewInt(10), "SUSPENDED", true},
		{big.NewInt(11), "", false},
		{nil, "", false},
	}
	for _, tt := range tests {
		got, listed := r.status(tt.serial)
		if got != tt.want || listed != tt.listed {
			t.Errorf("status(%v) = %q, %v; want %q, %v", tt.serial, got, listed, tt.want, tt.listed)
		}
	}
	if r.Len() != 2 {
		t.Errorf("Len = %d, want 2", r.Len())
	}
}

func TestRevocationsNil(t *testing.T) {
	// Verifies: SEC-002.
	var r *Revocations
	if _, listed := r.status(big.NewInt(1)); listed || r.Len() != 0 {
		t.Error("nil list reports entries")
	}
}

func TestParseRevocationsMalformed(t *testing.T) {
	// Verifies: SEC-002.
	cases := map[string]string{
		"not JSON":         `{`,
		"no entries":       `{}`,
		"entries is array": `{"entries":[]}`,
		"serial not hex":   `{"entries":{"xyz":{"status":"REVOKED"}}}`,
		"empty serial":     `{"entries":{"":{"status":"REVOKED"}}}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := ParseRevocations([]byte(in))
			if r != nil {
				t.Errorf("list = %+v", r)
			}
			wantCode(t, err, plxerr.AttestationFailed)
		})
	}
}
