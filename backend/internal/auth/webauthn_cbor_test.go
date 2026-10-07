// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"errors"
	"testing"
)

// Verifies: SEC-100.
// A malformed COSE key or authenticator data is refused, never trusted.
func TestParseCOSEKeyRefuses(t *testing.T) {
	t.Parallel()
	for name, in := range map[string][]byte{
		"not CBOR":          {0xff},
		"not a map":         {0x01},
		"an unknown kty":    {0xa2, 0x01, 0x05, 0x03, 0x26},
		"EC without y":      {0xa4, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x40},
		"RSA below 2048":    {0xa4, 0x01, 0x03, 0x03, 0x39, 0x01, 0x00, 0x20, 0x41, 0x01, 0x21, 0x41, 0x03},
		"Ed25519 too short": {0xa4, 0x01, 0x01, 0x03, 0x27, 0x20, 0x06, 0x21, 0x41, 0x00},
	} {
		if _, _, err := parseCOSEKey(in); !errors.Is(err, errWebAuthn) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, in := range map[string][]byte{
		"short":                  make([]byte, 36),
		"attested but short":     append(make([]byte, 32), 0x41, 0, 0, 0, 0),
		"a zero-length id":       append(append(make([]byte, 32), 0x41, 0, 0, 0, 0), make([]byte, 18)...),
		"a key that is not CBOR": append(append(append(make([]byte, 32), 0x41, 0, 0, 0, 0), make([]byte, 16)...), 0, 1, 7, 0xff),
	} {
		if _, err := parseAuthenticatorData(in); !errors.Is(err, errWebAuthn) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzParseWebAuthn checks that no input makes the COSE key or
// authenticator data parser panic.
func FuzzParseWebAuthn(f *testing.F) {
	for _, seed := range [][]byte{{0xa1, 0x01, 0x02}, make([]byte, 37), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _, _ = parseCOSEKey(data)
		_, _ = parseAuthenticatorData(data)
	})
}
