// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/httpx"
)

// This file verifies the identity tokens a CI provider mints, so that a
// pipeline can exchange one for a scoped Plux token and needs no
// long-lived secret (SRV-064). It verifies; it never issues. Only the
// signature algorithms an OpenID provider is required to support are
// accepted, and only the keys the provider publishes.

// ErrInvalidToken is returned when an identity token is not acceptable.
var ErrInvalidToken = errors.New("auth: the identity token is not valid")

// Claims are the parts of an identity token this server reads.
type Claims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  any    `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	// Nonce binds an OpenID Connect ID token to the sign-in that asked
	// for it.
	Nonce string `json:"nonce"`
	// EmailVerified is a boolean, or the string "true" some providers
	// send.
	EmailVerified any `json:"email_verified"`
}

// emailVerified reports whether the provider vouches for the address.
func (c Claims) emailVerified() bool {
	switch v := c.EmailVerified.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// audiences returns the token's audiences, which may be one string or a
// list.
func (c Claims) audiences() []string {
	switch v := c.Audience.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// Verifier checks identity tokens of one issuer against the keys that
// issuer publishes.
type Verifier struct {
	issuer   string
	client   *http.Client
	now      func() time.Time
	maxSkew  time.Duration
	cacheTTL time.Duration

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	jwksURI   string
}

// NewVerifier returns a verifier for an issuer. The HTTP client must be
// the SSRF-safe one: the issuer is configuration, but its discovery
// document names a URL (SEC-105).
func NewVerifier(issuer string, client *http.Client, now func() time.Time) (*Verifier, error) {
	if issuer == "" {
		return nil, errors.New("auth: a verifier needs an issuer")
	}
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		issuer: strings.TrimSuffix(issuer, "/"), client: client, now: now,
		maxSkew: 2 * time.Minute, cacheTTL: time.Hour, keys: map[string]*rsa.PublicKey{},
	}, nil
}

// Verify checks a token's signature, issuer, audience and validity
// window and returns its claims.
func (v *Verifier) Verify(ctx context.Context, token, audience string) (Claims, error) {
	header, claims, signed, signature, err := split(token)
	if err != nil {
		return Claims{}, err
	}
	if header.Algorithm != "RS256" && header.Algorithm != "RS384" && header.Algorithm != "RS512" {
		return Claims{}, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidToken, header.Algorithm)
	}
	key, err := v.key(ctx, header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	hash, digest := digestFor(header.Algorithm, signed)
	if err := rsa.VerifyPKCS1v15(key, hash, digest, signature); err != nil {
		return Claims{}, fmt.Errorf("%w: the signature does not verify", ErrInvalidToken)
	}
	if claims.Issuer != v.issuer {
		return Claims{}, fmt.Errorf("%w: issued by %q, not %q", ErrInvalidToken, claims.Issuer, v.issuer)
	}
	if audience != "" && !contains(claims.audiences(), audience) {
		return Claims{}, fmt.Errorf("%w: the audience does not include %q", ErrInvalidToken, audience)
	}
	now := v.now()
	if claims.ExpiresAt == 0 || now.After(time.Unix(claims.ExpiresAt, 0).Add(v.maxSkew)) {
		return Claims{}, fmt.Errorf("%w: it has expired", ErrInvalidToken)
	}
	if claims.NotBefore != 0 && now.Add(v.maxSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return Claims{}, fmt.Errorf("%w: it is not valid yet", ErrInvalidToken)
	}
	if claims.Subject == "" {
		return Claims{}, fmt.Errorf("%w: it names no subject", ErrInvalidToken)
	}
	return claims, nil
}

// header is a token's header.
type header struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// split decodes a compact token into its parts.
func split(token string) (header, Claims, []byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header{}, Claims{}, nil, nil, fmt.Errorf("%w: it is not a compact JWS", ErrInvalidToken)
	}
	var h header
	if err := decodeSegment(parts[0], &h); err != nil {
		return header{}, Claims{}, nil, nil, err
	}
	var c Claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return header{}, Claims{}, nil, nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return header{}, Claims{}, nil, nil, fmt.Errorf("%w: the signature is not base64url", ErrInvalidToken)
	}
	return h, c, []byte(parts[0] + "." + parts[1]), signature, nil
}

// decodeSegment decodes one base64url JSON segment.
func decodeSegment(segment string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return fmt.Errorf("%w: a segment is not base64url", ErrInvalidToken)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: a segment is not JSON", ErrInvalidToken)
	}
	return nil
}

// digestFor hashes the signing input with the algorithm's hash.
func digestFor(algorithm string, signed []byte) (crypto.Hash, []byte) {
	switch algorithm {
	case "RS384":
		sum := sha512.Sum384(signed)
		return crypto.SHA384, sum[:]
	case "RS512":
		sum := sha512.Sum512(signed)
		return crypto.SHA512, sum[:]
	default:
		sum := sha256.Sum256(signed)
		return crypto.SHA256, sum[:]
	}
}

// contains reports whether a list holds a value.
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// key returns the issuer's key with an identifier, fetching the key set
// when it is unknown or stale.
func (v *Verifier) key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	key, ok := v.keys[keyID]
	fresh := v.now().Sub(v.fetchedAt) < v.cacheTTL
	v.mu.Unlock()
	if ok && fresh {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if key, ok := v.keys[keyID]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("%w: the issuer publishes no key %q", ErrInvalidToken, keyID)
}

// refresh fetches the discovery document and the key set.
func (v *Verifier) refresh(ctx context.Context) error {
	uri := v.jwksURI
	if uri == "" {
		discovered, err := v.discover(ctx)
		if err != nil {
			return err
		}
		uri = discovered
	}
	keys, err := v.fetchKeys(ctx, uri)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys, v.fetchedAt, v.jwksURI = keys, v.now(), uri
	return nil
}

// discover reads the issuer's OpenID configuration and returns its
// jwks_uri, checking that it belongs to the issuer.
func (v *Verifier) discover(ctx context.Context) (string, error) {
	url := v.issuer + "/.well-known/openid-configuration"
	var document struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.getJSON(ctx, url, &document); err != nil {
		return "", err
	}
	if strings.TrimSuffix(document.Issuer, "/") != v.issuer {
		return "", fmt.Errorf("%w: the discovery document names issuer %q", ErrInvalidToken, document.Issuer)
	}
	if !strings.HasPrefix(document.JWKSURI, v.issuer+"/") && document.JWKSURI != v.issuer {
		return "", fmt.Errorf("%w: jwks_uri %q is outside the issuer", ErrInvalidToken, document.JWKSURI)
	}
	return document.JWKSURI, nil
}

// fetchKeys reads a JWK Set and returns the RSA keys in it.
func (v *Verifier) fetchKeys(ctx context.Context, uri string) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := v.getJSON(ctx, uri, &set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		var exponent [4]byte
		copy(exponent[4-len(e):], e)
		out[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(binary.BigEndian.Uint32(exponent[:])),
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the issuer publishes no usable key", ErrInvalidToken)
	}
	return out, nil
}

// getJSON fetches a small JSON document through the SSRF-safe client.
func (v *Verifier) getJSON(ctx context.Context, url string, into any) error {
	resp, err := httpx.Get(ctx, v.client, url)
	if err != nil {
		return fmt.Errorf("auth: fetch %s: %w", path.Base(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: fetch %s: status %d", path.Base(url), resp.StatusCode)
	}
	body, err := httpx.ReadAll(resp.Body, 1<<20)
	if err != nil {
		return fmt.Errorf("auth: read %s: %w", path.Base(url), err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("auth: %s is not JSON: %w", path.Base(url), err)
	}
	return nil
}

// unverifiedIssuer reads a token's issuer without verifying it, only to
// choose the verifier that will; nothing else is read from an
// unverified token.
func unverifiedIssuer(token string) (string, error) {
	_, claims, _, _, err := split(token)
	if err != nil {
		return "", err
	}
	if claims.Issuer == "" {
		return "", fmt.Errorf("%w: it names no issuer", ErrInvalidToken)
	}
	return strings.TrimSuffix(claims.Issuer, "/"), nil
}
