// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Sealed is a secret at rest: the value encrypted with a fresh data
// key, that data key wrapped by the backend, and the identifier of the
// key that wrapped it (SEC-106).
type Sealed struct {
	Ciphertext []byte
	WrappedKey []byte
	KeyID      string
	// Hint is the last few characters of the value, so that a person can
	// recognise which secret is stored without it being returned.
	Hint string
}

// Seal encrypts a secret. The value never reaches the backend: only the
// data key does.
//
// binding ties the ciphertext to where it is stored, such as
// "environment-secret:<environment>:<key>": it is authenticated but not
// stored, so a ciphertext copied to another row fails to open rather
// than being read as that row's secret.
func Seal(ctx context.Context, c Crypter, value, binding []byte) (Sealed, error) {
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return Sealed{}, fmt.Errorf("signing: generate a data key: %w", err)
	}
	aead, err := newAEAD(dataKey)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, fmt.Errorf("signing: %w", err)
	}
	ciphertext := aead.Seal(nonce, nonce, value, binding)
	wrapped, keyID, err := c.Wrap(ctx, dataKey)
	if err != nil {
		return Sealed{}, err
	}
	return Sealed{Ciphertext: ciphertext, WrappedKey: wrapped, KeyID: keyID, Hint: hint(value)}, nil
}

// Open decrypts a sealed secret with the binding it was sealed with.
func Open(ctx context.Context, c Crypter, s Sealed, binding []byte) ([]byte, error) {
	dataKey, err := c.Unwrap(ctx, s.WrappedKey, s.KeyID)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	aead, err := newAEAD(dataKey)
	if err != nil {
		return nil, err
	}
	if len(s.Ciphertext) < aead.NonceSize() {
		return nil, errors.New("signing: the secret is truncated")
	}
	nonce, body := s.Ciphertext[:aead.NonceSize()], s.Ciphertext[aead.NonceSize():]
	out, err := aead.Open(nil, nonce, body, binding)
	if err != nil {
		return nil, fmt.Errorf("signing: open a secret: %w", err)
	}
	return out, nil
}

// hint returns the last four characters of a value, or "" when the
// value is too short to hint at without revealing it.
func hint(value []byte) string {
	if !utf8.Valid(value) {
		return ""
	}
	runes := []rune(string(value))
	if len(runes) < 8 {
		return ""
	}
	return "…" + string(runes[len(runes)-4:])
}

// A sealed value kept in one column is packed with explicit lengths:
// version, key ID, wrapped key, ciphertext. The hint is not packed; a
// caller that shows one stores it beside the value.
const sealedVersion byte = 1

// Encode packs a sealed value for a single column.
func (s Sealed) Encode() []byte {
	out := make([]byte, 0, 1+4+len(s.KeyID)+4+len(s.WrappedKey)+len(s.Ciphertext))
	out = append(out, sealedVersion)
	out = binary.BigEndian.AppendUint32(out, uint32(len(s.KeyID))) //nolint:gosec // lengths of in-process values
	out = append(out, s.KeyID...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(s.WrappedKey))) //nolint:gosec // lengths of in-process values
	out = append(out, s.WrappedKey...)
	return append(out, s.Ciphertext...)
}

// DecodeSealed unpacks what Encode wrote.
func DecodeSealed(b []byte) (Sealed, error) {
	malformed := errors.New("signing: the stored secret is malformed")
	if len(b) < 9 || b[0] != sealedVersion {
		return Sealed{}, malformed
	}
	rest := b[1:]
	keyLen := uint64(binary.BigEndian.Uint32(rest[:4]))
	rest = rest[4:]
	if uint64(len(rest)) < keyLen+4 {
		return Sealed{}, malformed
	}
	keyID := string(rest[:keyLen])
	rest = rest[keyLen:]
	wrappedLen := uint64(binary.BigEndian.Uint32(rest[:4]))
	rest = rest[4:]
	if uint64(len(rest)) < wrappedLen {
		return Sealed{}, malformed
	}
	return Sealed{KeyID: keyID, WrappedKey: rest[:wrappedLen], Ciphertext: rest[wrappedLen:]}, nil
}
