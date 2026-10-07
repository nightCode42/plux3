// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package playintegrity

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

const (
	// maxClockSkew is how far in the future a verdict timestamp may lie.
	maxClockSkew = 60 * time.Second
	// recognised is the only acceptable app recognition verdict.
	recognised = "PLAY_RECOGNIZED" //nolint:misspell // Google's verdict value
)

// Expect states what a verdict must match to be accepted.
type Expect struct {
	// PackageName is the Android application ID the token must be issued for.
	PackageName string
	// RequestHash is the expected requestHash, see RequestHash.
	RequestHash string
	// CertDigests are the SHA-256 digests of the accepted signing
	// certificates. When empty the certificate is not checked; when set,
	// the verdict must name at least one digest and all of them must be here.
	CertDigests [][]byte
	// MaxAge is the oldest acceptable verdict age. It must be positive.
	MaxAge time.Duration
}

// Verifier opens and checks Play Integrity standard-request tokens. It holds
// no mutable state and is safe for concurrent use.
type Verifier struct {
	// Keys are the Play Console decryption and verification keys.
	Keys Keys
	// MaxTokenBytes bounds the token size accepted before any parsing; zero
	// means the registry default, attest.playIntegrityTokenBytes (LIM-001).
	MaxTokenBytes int64
	// Now returns the current time; it is injected for deterministic tests.
	Now func() time.Time
}

// maxTokenBytes returns the effective token size bound.
func (v *Verifier) maxTokenBytes() int64 {
	if v.MaxTokenBytes > 0 {
		return v.MaxTokenBytes
	}
	def, _ := limits.Lookup(limits.AttestPlayIntegrityTokenBytes)
	return def.Default
}

// Verify decrypts the token, verifies its signature and checks the verdict
// against e. Every failure is plxerr.AttestationFailed and names the check
// that failed, never the token or its content.
func (v *Verifier) Verify(token string, e Expect) (Verdict, error) {
	if v == nil || v.Now == nil || !v.Keys.valid() {
		return Verdict{}, fail("verifier is not configured")
	}
	if e.PackageName == "" || e.RequestHash == "" || e.MaxAge <= 0 {
		return Verdict{}, fail("expectation is incomplete")
	}
	if int64(len(token)) > v.maxTokenBytes() {
		return Verdict{}, fail("token is too large")
	}
	payload, err := v.open(token)
	if err != nil {
		return Verdict{}, err
	}
	var raw rawVerdict
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Verdict{}, fail("verdict is not valid")
	}
	return check(&raw, e, v.Now())
}

// open decrypts the JWE and verifies the inner JWS, returning its payload.
func (v *Verifier) open(token string) ([]byte, error) {
	jwe, err := jose.ParseEncryptedCompact(token,
		[]jose.KeyAlgorithm{jose.A256KW}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return nil, fail("token is not an accepted encrypted token")
	}
	inner, err := jwe.Decrypt(v.Keys.Decryption)
	if err != nil {
		return nil, fail("token decryption failed")
	}
	jws, err := jose.ParseSignedCompact(string(inner), []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return nil, fail("token is not an accepted signed token")
	}
	payload, err := jws.Verify(v.Keys.Verification)
	if err != nil {
		return nil, fail("token signature is invalid")
	}
	return payload, nil
}

// check applies every verdict check and builds the Verdict.
func check(raw *rawVerdict, e Expect, now time.Time) (Verdict, error) {
	if !equal(raw.RequestDetails.PackageName, e.PackageName) {
		return Verdict{}, fail("request package name mismatch")
	}
	if !equal(raw.AppIntegrity.PackageName, e.PackageName) {
		return Verdict{}, fail("app package name mismatch")
	}
	if !equal(raw.RequestDetails.RequestHash, e.RequestHash) {
		return Verdict{}, fail("request hash mismatch")
	}
	ts, err := checkTimestamp(int64(raw.RequestDetails.TimestampMillis), now, e.MaxAge)
	if err != nil {
		return Verdict{}, err
	}
	if raw.AppIntegrity.Recognition != recognised {
		return Verdict{}, fail("app is not recognised by Play")
	}
	if err := checkCerts(raw.AppIntegrity.CertDigests, e.CertDigests); err != nil {
		return Verdict{}, err
	}
	return Verdict{
		Device:        knownLabels(raw.DeviceIntegrity.Labels),
		AppRecognised: true,
		Licensing:     raw.AccountDetails.Licensing,
		VersionCode:   int64(raw.AppIntegrity.VersionCode),
		Timestamp:     ts,
	}, nil
}

func checkTimestamp(millis int64, now time.Time, maxAge time.Duration) (time.Time, error) {
	if millis <= 0 {
		return time.Time{}, fail("verdict timestamp is missing")
	}
	ts := time.UnixMilli(millis)
	if now.Sub(ts) > maxAge {
		return time.Time{}, fail("verdict is too old")
	}
	if ts.Sub(now) > maxClockSkew {
		return time.Time{}, fail("verdict timestamp is in the future")
	}
	return ts.UTC(), nil
}

// checkCerts verifies that the verdict's certificate digests are all accepted.
func checkCerts(got []string, accepted [][]byte) error {
	if len(accepted) == 0 {
		return nil
	}
	if len(got) == 0 {
		return fail("certificate digest is missing")
	}
	for _, s := range got {
		d, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil || !slices.ContainsFunc(accepted, func(a []byte) bool {
			return subtle.ConstantTimeCompare(a, d) == 1
		}) {
			return fail("certificate digest mismatch")
		}
	}
	return nil
}

func knownLabels(in []string) []DeviceLabel {
	var out []DeviceLabel
	for _, s := range in {
		if l := DeviceLabel(s); knownLabel(l) {
			out = append(out, l)
		}
	}
	return out
}

// equal compares two strings in constant time.
func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func fail(msg string) error {
	return plxerr.New(plxerr.AttestationFailed, "play integrity: %s", msg)
}
