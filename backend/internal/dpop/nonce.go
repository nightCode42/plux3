// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

const (
	// minNonceKey is the shortest HMAC key accepted, in bytes.
	minNonceKey = 32
	// maxNonceRotation is the longest epoch length (SEC-024).
	maxNonceRotation = 5 * time.Minute
	// nonceMACLen is the number of MAC bytes kept in a nonce.
	nonceMACLen = 16
	// nonceLen is the decoded length of a nonce: epoch plus MAC.
	nonceLen = 8 + nonceMACLen
)

// Nonces issues and validates server-provided DPoP nonces (SEC-024). A
// nonce is the epoch number followed by a truncated HMAC of it, so it is
// stateless: any replica holding the key accepts what another issued. A
// nonce is valid in its own epoch and the next one. A Nonces is immutable
// after construction and safe for concurrent use.
type Nonces struct {
	key      []byte
	rotation time.Duration
	now      func() time.Time
}

// NewNonces returns a nonce issuer. The key must be at least 32 bytes and
// is copied; the rotation must be positive and at most five minutes; now
// supplies the clock.
func NewNonces(key []byte, rotation time.Duration, now func() time.Time) (*Nonces, error) {
	switch {
	case len(key) < minNonceKey:
		return nil, errors.New("dpop: nonce key must be at least 32 bytes")
	case rotation <= 0 || rotation > maxNonceRotation:
		return nil, errors.New("dpop: nonce rotation must be positive and at most 5 minutes")
	case now == nil:
		return nil, errors.New("dpop: nonce clock is required")
	}
	return &Nonces{key: append([]byte(nil), key...), rotation: rotation, now: now}, nil
}

// epoch returns the current epoch number, clamped at zero.
func (n *Nonces) epoch() uint64 {
	ns := n.now().UnixNano()
	if ns < 0 {
		return 0
	}
	return uint64(ns / int64(n.rotation))
}

// mac returns the truncated MAC of an epoch.
func (n *Nonces) mac(epoch uint64) []byte {
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], epoch)
	h := hmac.New(sha256.New, n.key)
	h.Write(e[:])
	return h.Sum(nil)[:nonceMACLen]
}

// encode returns the nonce of an epoch.
func (n *Nonces) encode(epoch uint64) string {
	raw := make([]byte, 8, nonceLen)
	binary.BigEndian.PutUint64(raw, epoch)
	raw = append(raw, n.mac(epoch)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Current returns the nonce for the current epoch, to send in a DPoP-Nonce
// header.
func (n *Nonces) Current() string { return n.encode(n.epoch()) }

// Valid reports whether nonce was issued by this key in the current or the
// previous epoch.
func (n *Nonces) Valid(nonce string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(raw) != nonceLen {
		return false
	}
	epoch := binary.BigEndian.Uint64(raw[:8])
	cur := n.epoch()
	if epoch != cur && (cur == 0 || epoch != cur-1) {
		return false
	}
	return hmac.Equal(raw[8:], n.mac(epoch))
}
