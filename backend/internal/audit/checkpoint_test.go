// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit_test

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// signedCheckpoint returns a checkpoint signed with a fresh key, and the
// keys that verify it.
func signedCheckpoint(t *testing.T) (audit.Checkpoint, audit.PublicKeys) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	cp := audit.Checkpoint{
		OrganizationID: "0198f6a2-0000-7000-8000-000000000001", Sequence: 7, EntryHash: strings.Repeat("ab", 32),
		CreatedAt: time.Date(2026, 3, 1, 12, 0, 0, 123456000, time.UTC), KeyID: signing.KeyID(pub), Algorithm: signing.Algorithm,
	}
	payload, err := cp.Payload()
	if err != nil {
		t.Fatal(err)
	}
	cp.Signature = ed25519.Sign(priv, payload)
	return cp, audit.PublicKeys{cp.KeyID: pub}
}

// Verifies: SEC-141.
// The signed bytes are the canonical encoding of every field but the
// signature: sorted keys, no whitespace, the sequence as a string.
func TestCheckpointPayloadIsCanonical(t *testing.T) {
	t.Parallel()
	cp, _ := signedCheckpoint(t)
	got, err := cp.Payload()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"algorithm":"ed25519","created_at":"2026-03-01T12:00:00.123456Z",` +
		`"entry_hash":"` + cp.EntryHash + `","key_id":"` + cp.KeyID + `",` +
		`"organization_id":"0198f6a2-0000-7000-8000-000000000001","sequence":"7","type":"plux.audit.checkpoint.v1"}`
	if string(got) != want {
		t.Errorf("payload = %s\nwant      %s", got, want)
	}
}

// Verifies: SEC-141.
// A signature verifies, and fails once any signed field, the signature,
// the key or the algorithm is changed.
func TestCheckpointSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	cp, keys := signedCheckpoint(t)
	if err := cp.Verify(keys); err != nil {
		t.Fatalf("Verify = %v", err)
	}
	other, _, _ := ed25519.GenerateKey(nil)
	tests := []struct {
		name   string
		change func(*audit.Checkpoint)
		keys   audit.PublicKeys
	}{
		{"sequence", func(c *audit.Checkpoint) { c.Sequence++ }, keys},
		{"entry hash", func(c *audit.Checkpoint) { c.EntryHash = strings.Repeat("cd", 32) }, keys},
		{"organisation", func(c *audit.Checkpoint) { c.OrganizationID = "0198f6a2-0000-7000-8000-000000000002" }, keys},
		{"time", func(c *audit.Checkpoint) { c.CreatedAt = c.CreatedAt.Add(time.Microsecond) }, keys},
		{"signature", func(c *audit.Checkpoint) { c.Signature = append([]byte{0}, c.Signature[1:]...) }, keys},
		{"algorithm", func(c *audit.Checkpoint) { c.Algorithm = "rsa" }, keys},
		{"unknown key", func(*audit.Checkpoint) {}, audit.PublicKeys{cp.KeyID: other}},
		{"no key", func(*audit.Checkpoint) {}, audit.PublicKeys{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := cp
			tt.change(&c)
			if err := c.Verify(tt.keys); err == nil {
				t.Error("Verify accepted a changed checkpoint")
			}
		})
	}
}
