// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies HMAC-SHA1 for TOTP
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters. They are RFC 6238's defaults, which is what every
// authenticator application implements.
const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew is how many periods on either side are accepted, for a
	// device whose clock is slightly off.
	totpSkew = 1
	// totpSecretLen is the length of a generated secret in bytes.
	totpSecretLen = 20
)

// ErrWrongCode is returned when a one-time code does not match.
var ErrWrongCode = errors.New("auth: wrong code")

// totpAlphabet is base32 without padding, as authenticator
// applications expect.
var totpAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh secret in the base32 form a user enters
// or scans (SEC-100).
func NewTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	return totpAlphabet.EncodeToString(raw), nil
}

// TOTPURL returns the otpauth:// URL an authenticator application
// scans. The account is the user's own identifier; the issuer is the
// installation, so several installations do not collide in one
// application.
func TOTPURL(issuer, account, secret string) string {
	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   "/" + issuer + ":" + account,
	}
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(int(totpPeriod.Seconds())))
	u.RawQuery = q.Encode()
	return u.String()
}

// TOTPCode returns the code of a secret at a moment, which is what a
// test and the verifier both need.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := totpAlphabet.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("auth: the TOTP secret is not base32: %w", err)
	}
	unix := at.UTC().Unix()
	if unix < 0 {
		return "", errors.New("auth: a TOTP time before 1970")
	}
	counter := uint64(unix) / uint64(totpPeriod.Seconds())
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0F
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7FFFFFFF
	mod := uint32(1)
	for range totpDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// matchCode is MatchTOTP as a decision: the step and whether it matched.
func matchCode(secret, code string, at time.Time) (int64, bool) {
	step, err := MatchTOTP(secret, code, at)
	return step, err == nil
}

// MatchTOTP checks a code against a secret at a moment, accepting one
// period on either side, and returns the time step the code belongs to.
// It compares in constant time. The caller refuses a step it has
// already accepted, so that a code works only once (RFC 6238 §5.2).
func MatchTOTP(secret, code string, at time.Time) (int64, error) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, ErrWrongCode
	}
	step := at.UTC().Unix() / int64(totpPeriod.Seconds())
	if step < 1 {
		return 0, ErrWrongCode
	}
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		want, err := TOTPCode(secret, at.Add(time.Duration(skew)*totpPeriod))
		if err != nil {
			return 0, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step + int64(skew), nil
		}
	}
	return 0, ErrWrongCode
}
