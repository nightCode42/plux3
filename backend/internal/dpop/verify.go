// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

const (
	// proofType is the required typ header value (RFC 9449 §4.2).
	proofType = "dpop+jwt"
	// maxIAT bounds the iat claim so its conversion cannot overflow.
	maxIAT = 1e12
)

// errUnsupportedURL reports a URL that is not absolute http or https.
var errUnsupportedURL = errors.New("dpop: url is not absolute http or https")

// Expect describes what a proof must be bound to.
type Expect struct {
	// Method is the HTTP method of the request, compared exactly.
	Method string
	// URL is the request URL; query and fragment are ignored.
	URL string
	// AccessToken is the access token the proof must be bound to through
	// its ath claim. Empty means no token is presented (the token
	// endpoint) and ath must be absent.
	AccessToken string
	// JKT is the thumbprint the access token is bound to (its cnf.jkt).
	// When set, the proof's key must have it, and that is checked as soon
	// as the header parses, before the signature, the claims and the
	// nonce (ADR-0012). Empty means no binding is expected.
	JKT string
	// Window is the largest accepted distance between the proof's iat and
	// the verifier's clock, in either direction.
	Window time.Duration
}

// Proof is a verified DPoP proof.
type Proof struct {
	// JKT is the thumbprint of the proof's key.
	JKT string
	// JTI is the proof's unique identifier, for the replay check.
	JTI string
	// IssuedAt is the proof's iat claim.
	IssuedAt time.Time
	// Key is the proof's public key.
	Key *ecdsa.PublicKey
}

// Verifier verifies DPoP proofs. It holds no mutable state and is safe for
// concurrent use.
type Verifier struct {
	// Nonces, when set, makes a valid server nonce mandatory in every
	// proof. Nil means nonces are not required.
	Nonces *Nonces
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// MaxProofBytes is the longest proof accepted; zero means the registry
	// default, dpop.proofBytes (LIM-001).
	MaxProofBytes int64
	// MaxJTIBytes is the longest proof identifier accepted; zero means the
	// registry default, dpop.jtiBytes (LIM-001).
	MaxJTIBytes int64
}

// bound resolves a configured bound: zero or less means the registry default.
func bound(configured int64, k limits.Key) int64 {
	if configured > 0 {
		return configured
	}
	def, _ := limits.Lookup(k)
	return def.Default
}

// header is the part of a proof's protected header that is checked. Fields
// that must be absent are raw messages so that any value is noticed.
type header struct {
	Typ string          `json:"typ"`
	Alg string          `json:"alg"`
	JWK json.RawMessage `json:"jwk"`
	Kid json.RawMessage `json:"kid"`
	X5c json.RawMessage `json:"x5c"`
	Jku json.RawMessage `json:"jku"`
}

// claims are the proof's checked claims. Optional claims are pointers so
// that absence differs from an empty string.
type claims struct {
	HTM   string   `json:"htm"`
	HTU   string   `json:"htu"`
	IAT   *float64 `json:"iat"`
	JTI   string   `json:"jti"`
	ATH   *string  `json:"ath"`
	Nonce *string  `json:"nonce"`
}

// invalid returns the error for a failed check. The name identifies the
// check and never carries proof content (SEC-092).
func invalid(check string) error {
	return plxerr.New(plxerr.DPoPProofInvalid, "dpop proof failed check %q", check)
}

// Verify checks a proof against what the request expects, in this order:
// size, structure, header, key binding, signature, claims, access-token
// hash and nonce. Every failure is DPoPProofInvalid except a key that is
// not the one the token is bound to, which is TokenBindingMismatch, and a
// missing or stale nonce, which is DPoPNonceRequired. Verify does not check for replay; the caller does,
// with Replay.Check.
func (v *Verifier) Verify(proof string, e Expect) (Proof, error) {
	if int64(len(proof)) > bound(v.MaxProofBytes, limits.DPOPProofBytes) {
		return Proof{}, invalid("size")
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Proof{}, invalid("format")
	}
	key, err := parseHeader(parts[0])
	if err != nil {
		return Proof{}, err
	}
	jkt, err := Thumbprint(key)
	if err != nil {
		return Proof{}, invalid("jwk")
	}
	if e.JKT != "" {
		if err := CheckBinding(e.JKT, jkt); err != nil {
			return Proof{}, err
		}
	}
	payload, err := verifySignature(proof, key)
	if err != nil {
		return Proof{}, err
	}
	c, err := v.checkClaims(payload, e)
	if err != nil {
		return Proof{}, err
	}
	if v.Nonces != nil && (c.Nonce == nil || !v.Nonces.Valid(*c.Nonce)) {
		return Proof{}, plxerr.New(plxerr.DPoPNonceRequired, "dpop proof needs a current server nonce")
	}
	return Proof{JKT: jkt, JTI: c.JTI, IssuedAt: unixSeconds(*c.IAT), Key: key}, nil
}

// parseHeader decodes the protected header and returns the proof's key.
func parseHeader(segment string) (*ecdsa.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, invalid("format")
	}
	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, invalid("header")
	}
	switch {
	case h.Typ != proofType:
		return nil, invalid("typ")
	case h.Alg != string(jose.ES256):
		return nil, invalid("alg")
	case len(h.JWK) == 0 || string(h.JWK) == "null":
		return nil, invalid("jwk")
	case h.Kid != nil || h.X5c != nil || h.Jku != nil:
		return nil, invalid("header parameters")
	}
	return parseKey(h.JWK)
}

// parseKey decodes a JWK and requires a public P-256 key on the curve.
func parseKey(raw json.RawMessage) (*ecdsa.PublicKey, error) {
	var jwk jose.JSONWebKey
	if err := json.Unmarshal(raw, &jwk); err != nil || !jwk.IsPublic() || !jwk.Valid() {
		return nil, invalid("jwk")
	}
	pub, ok := jwk.Key.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, invalid("jwk")
	}
	if _, err := pub.ECDH(); err != nil {
		return nil, invalid("jwk")
	}
	return pub, nil
}

// verifySignature checks the signature under the proof's own key and
// returns the payload.
func verifySignature(proof string, key *ecdsa.PublicKey) ([]byte, error) {
	jws, err := jose.ParseSignedCompact(proof, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return nil, invalid("format")
	}
	payload, err := jws.Verify(key)
	if err != nil {
		return nil, invalid("signature")
	}
	return payload, nil
}

// checkClaims decodes the payload and checks method, URL, time, identifier
// and access-token hash.
func (v *Verifier) checkClaims(payload []byte, e Expect) (claims, error) {
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return claims{}, invalid("claims")
	}
	if c.HTM != e.Method || e.Method == "" {
		return claims{}, invalid("htm")
	}
	want, err := normaliseURL(e.URL)
	if err != nil {
		return claims{}, invalid("htu")
	}
	got, err := normaliseURL(c.HTU)
	if err != nil || got != want {
		return claims{}, invalid("htu")
	}
	if c.IAT == nil || math.Abs(*c.IAT) > maxIAT || !v.within(*c.IAT, e.Window) {
		return claims{}, invalid("iat")
	}
	if c.JTI == "" || int64(len(c.JTI)) > bound(v.MaxJTIBytes, limits.DPOPJtiBytes) {
		return claims{}, invalid("jti")
	}
	if !athMatches(c.ATH, e.AccessToken) {
		return claims{}, invalid("ath")
	}
	return c, nil
}

// within reports whether iat lies within window of the clock.
func (v *Verifier) within(iat float64, window time.Duration) bool {
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	d := now().Sub(unixSeconds(iat))
	if d < 0 {
		d = -d
	}
	return d <= window
}

// unixSeconds converts a NumericDate to a time.
func unixSeconds(s float64) time.Time {
	sec := math.Floor(s)
	return time.Unix(int64(sec), int64((s-sec)*1e9)).UTC()
}

// athMatches reports whether the ath claim fits the access token: equal to
// its hash when a token is expected, absent when none is.
func athMatches(ath *string, token string) bool {
	if token == "" {
		return ath == nil
	}
	if ath == nil {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(*ath), []byte(want)) == 1
}

// normaliseURL reduces a URL to the form htu is compared in (RFC 9449
// §4.3): lower-case scheme and host, default port removed, path kept,
// query, fragment and user information dropped.
func normaliseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("dpop: url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", errUnsupportedURL
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port == "" || (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	} else {
		port = ":" + port
	}
	return scheme + "://" + host + port + u.EscapedPath(), nil
}
