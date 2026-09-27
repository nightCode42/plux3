// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package auth decides who is calling and what they may do
// (SEC-100, SEC-101, SEC-102, SRV-064).
//
// Nothing here trusts a stored value to be a secret: passwords are
// Argon2id hashes, and sessions and tokens are stored as the SHA-256 of
// a secret that exists only in the caller's hands, so a copy of the
// database cannot be replayed.
//
// Authorisation is deny-by-default: a call names the permission it
// needs, and a principal that does not hold it is refused before any
// work is done. Row-level security is the second barrier beneath this
// one, never the first (SEC-102).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. They follow OWASP's guidance for a server that
// also serves requests: 19 MiB of memory, two passes, one lane.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// Password length bounds. The lower follows NIST SP 800-63B's advice
// for a password that is the only factor; the upper stops a request
// from making the server hash megabytes.
const (
	minPasswordLength = 12
	maxPasswordLength = 1024
)

// ErrWrongPassword is returned when a password does not match.
var ErrWrongPassword = errors.New("auth: wrong password")

// CheckPassword reports why a new password is not acceptable.
func CheckPassword(password string) error {
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordLength:
		return fmt.Errorf("auth: a password must be at least %d characters", minPasswordLength)
	case len(password) > maxPasswordLength:
		return fmt.Errorf("auth: a password may be at most %d bytes", maxPasswordLength)
	}
	return nil
}

// HashPassword returns a PHC string for a password (SEC-100). The
// parameters are stored with the hash, so they can be raised later
// without invalidating existing passwords.
func HashPassword(password string) (string, error) {
	if err := CheckPassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC string. It compares in
// constant time and reports nothing about why a check failed.
func VerifyPassword(encoded, password string) error {
	if len(password) > maxPasswordLength {
		return ErrWrongPassword
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrWrongPassword
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrWrongPassword
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrWrongPassword
	}
	// Parameters come from the database; bounding them keeps a damaged
	// row from making one sign-in cost gigabytes or minutes.
	if memory == 0 || memory > 1<<20 || time == 0 || time > 16 || threads == 0 || threads > 16 {
		return ErrWrongPassword
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrWrongPassword
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrWrongPassword
	}
	if len(want) < 16 || len(want) > 64 {
		return ErrWrongPassword
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want))) //nolint:gosec // bounded above
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrWrongPassword
	}
	return nil
}

// decoyHash is verified against when an account does not exist or has
// no password, so that a refusal takes as long either way and the
// timing does not reveal which addresses have accounts. It is the hash
// of a random value nobody knows.
const decoyHash = "$argon2id$v=19$m=19456,t=2,p=1$" +
	"c2FsdHNhbHRzYWx0c2FsdA$" +
	"4r3Tq3pP6lC0Jd0d3wJ3h2YJ6yJ0c2Y8vT1m3u7xT9E"

// passwordMatches reports whether a password matches a stored hash.
func passwordMatches(encoded, password string) bool { return VerifyPassword(encoded, password) == nil }

// burn spends the time a password check takes, for an account that
// cannot sign in.
func burn(password string) { _ = VerifyPassword(decoyHash, password) }
