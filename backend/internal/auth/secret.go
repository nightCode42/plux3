// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// secretAlphabet is Crockford-free base32 without padding: it is
// case-insensitive on paper and has no characters that are easy to
// confuse when a person types a code.
var secretAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// Secret is a credential in the caller's hands. Only its hash is
// stored, so a copy of the database cannot be replayed (SEC-101).
type Secret struct {
	// Value is the whole secret, returned exactly once.
	Value string
	// Prefix is the visible part, for recognising a token in a list.
	Prefix string
	// Hash is what is stored.
	Hash []byte
}

// NewSecret returns a secret of n random bytes, rendered with the given
// prefix, for example "plux_pat".
func NewSecret(kind string, n int) (Secret, error) {
	if n < 16 {
		return Secret{}, fmt.Errorf("auth: a secret needs at least 16 bytes, not %d", n)
	}
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return Secret{}, fmt.Errorf("auth: %w", err)
	}
	body := strings.ToLower(secretAlphabet.EncodeToString(raw))
	value := kind + "_" + body
	return Secret{Value: value, Prefix: kind + "_" + body[:6], Hash: HashSecret(value)}, nil
}

// HashSecret returns what is stored for a secret. SHA-256 is right here
// and Argon2id is not: the secret is 128 random bits or more, so there
// is nothing to guess, and the hash is computed on every request.
func HashSecret(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// Kinds of secret.
const (
	// PrefixSession is a browser session cookie's value (SEC-101).
	PrefixSession = "plux_ses"
	// PrefixToken is a personal access token or a CI token (SRV-064).
	PrefixToken = "plux_pat"
	// PrefixDeviceCode is the device authorization grant's device code
	// (CLI-002).
	PrefixDeviceCode = "plux_dev"
	// PrefixCSRF is the token a state-changing Studio call must echo.
	PrefixCSRF = "plux_csrf"
	// PrefixChallenge identifies a sign-in waiting for its second factor.
	PrefixChallenge = "plux_mfa"
	// PrefixInvitation lets an invited person set their password.
	PrefixInvitation = "plux_inv"
)

// userCodeAlphabet has no vowels, so that a code never spells a word,
// and no characters that are easy to confuse (RFC 8628 §6.1).
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// NewUserCode returns the code a person types to approve `plux login`,
// in the form "BCDF-GHJK": about 34.6 bits, enough for a code that
// lives fifteen minutes and whose guesses are rate limited.
func NewUserCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	var b strings.Builder
	for i, v := range raw {
		if i == 4 {
			b.WriteByte('-')
		}
		// 256 is not a multiple of 20, so the first 240 values are used
		// evenly and the bias of the rest is under one part in 40.
		b.WriteByte(userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return b.String(), nil
}

// NormaliseUserCode accepts a user code typed in lower case or without
// its hyphen.
func NormaliseUserCode(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if len(s) != 8 {
		return s
	}
	return s[:4] + "-" + s[4:]
}
