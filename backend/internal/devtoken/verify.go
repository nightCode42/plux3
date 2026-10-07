// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package devtoken

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// Verifier checks device access tokens. It holds no state and is safe
// for concurrent use; the caller may cache what Keys returns.
type Verifier struct {
	// Keys lists the public token keys, as signing.TokenSigner.TokenKeys
	// does.
	Keys func(ctx context.Context) ([]signing.TokenKey, error)
	// Issuer and Audience are the iss and aud a token must carry.
	Issuer, Audience string
	// Now is the clock; nil uses the wall clock.
	Now func() time.Time
	// Leeway is the clock skew tolerated on iat and exp.
	Leeway time.Duration
}

// Verify checks a token for an environment of the given class and
// returns its claims, in this order: size, structure and algorithm,
// header, key and class, signature, claims. Every failed check is
// AccessTokenInvalid and names the check. A failure to obtain the keys is
// not a verdict on the token and is returned as a plain error, so that a
// caller answers with an outage rather than a refusal.
func (v *Verifier) Verify(ctx context.Context, token string, production bool) (Claims, error) {
	if v.Keys == nil || v.Issuer == "" || v.Audience == "" {
		return Claims{}, errors.New("devtoken: the verifier needs keys, an issuer and an audience")
	}
	if len(token) > MaxTokenSize {
		return Claims{}, invalid("size")
	}
	jws, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return Claims{}, invalid("format")
	}
	kid, err := checkHeader(jws.Signatures[0].Header)
	if err != nil {
		return Claims{}, err
	}
	key, err := v.key(ctx, kid, production)
	if err != nil {
		return Claims{}, err
	}
	payload, err := jws.Verify(key)
	if err != nil {
		return Claims{}, invalid("signature")
	}
	return v.checkClaims(payload, production)
}

// VerifyFor checks a token whose environment is not known beforehand. The
// environment it names, read before its signature is checked, selects the
// key class through production; a token that lies about its environment
// fails because the key of the class production names did not sign it.
// Nothing else of the unverified payload is used. A failure of production
// is returned as it is.
func (v *Verifier) VerifyFor(ctx context.Context, token string, production func(ctx context.Context, environment string) (bool, error)) (Claims, error) {
	if len(token) > MaxTokenSize {
		return Claims{}, invalid("size")
	}
	jws, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return Claims{}, invalid("format")
	}
	var peek struct {
		Env string `json:"env"`
	}
	if err := json.Unmarshal(jws.UnsafePayloadWithoutVerification(), &peek); err != nil || peek.Env == "" {
		return Claims{}, invalid("env")
	}
	class, err := production(ctx, peek.Env)
	if err != nil {
		return Claims{}, err
	}
	return v.Verify(ctx, token, class)
}

// checkHeader requires the access-token type and a key identifier, and
// refuses a header that carries a key of its own (jwk).
func checkHeader(h jose.Header) (string, error) {
	typ, _ := h.ExtraHeaders[jose.HeaderType].(string)
	switch {
	case typ != tokenType:
		return "", invalid("typ")
	case h.KeyID == "":
		return "", invalid("kid")
	case h.JSONWebKey != nil:
		return "", invalid("header parameters")
	}
	return h.KeyID, nil
}

// key finds the named key and checks that its class suits the
// environment (SEC-056).
func (v *Verifier) key(ctx context.Context, kid string, production bool) (*ecdsa.PublicKey, error) {
	keys, err := v.Keys(ctx)
	if err != nil {
		return nil, fmt.Errorf("devtoken: load the token keys: %w", err)
	}
	for _, k := range keys {
		if k.ID != kid {
			continue
		}
		known := k.Class == signing.TokenProduction || k.Class == signing.TokenDevelopment
		if !known || (k.Class == signing.TokenProduction) != production {
			return nil, invalid("key class")
		}
		if k.Public == nil || k.Public.Curve != elliptic.P256() {
			return nil, invalid("kid")
		}
		return k.Public, nil
	}
	return nil, invalid("kid")
}

// checkClaims decodes the payload and checks issuer, audience, time and
// the claims a token must carry.
func (v *Verifier) checkClaims(payload []byte, production bool) (Claims, error) {
	var w struct {
		Iss      string   `json:"iss"`
		Aud      string   `json:"aud"`
		Sub      string   `json:"sub"`
		ClientID string   `json:"client_id"`
		Env      string   `json:"env"`
		Org      string   `json:"org"`
		HB       string   `json:"hb"`
		AL       string   `json:"al"`
		Cnf      wireCnf  `json:"cnf"`
		IAT      *float64 `json:"iat"`
		EXP      *float64 `json:"exp"`
		JTI      string   `json:"jti"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		return Claims{}, invalid("claims")
	}
	switch {
	case w.Iss != v.Issuer:
		return Claims{}, invalid("iss")
	case w.Aud != v.Audience:
		return Claims{}, invalid("aud")
	case w.Sub == "":
		return Claims{}, invalid("sub")
	case w.ClientID == "":
		return Claims{}, invalid("client_id")
	case w.Env == "":
		return Claims{}, invalid("env")
	case w.Org == "":
		return Claims{}, invalid("org")
	case !validAssurance(w.AL):
		return Claims{}, invalid("al")
	case w.Cnf.JKT == "":
		return Claims{}, invalid("cnf")
	case w.JTI == "":
		return Claims{}, invalid("jti")
	}
	iat, exp, err := v.checkTimes(w.IAT, w.EXP)
	if err != nil {
		return Claims{}, err
	}
	return Claims{
		DeviceID: w.Sub, AppID: w.ClientID, Environment: w.Env, OrganizationID: w.Org, HostBuild: w.HB, Production: production,
		Assurance: w.AL, JKT: w.Cnf.JKT, IssuedAt: iat, ExpiresAt: exp, ID: w.JTI,
	}, nil
}

// checkTimes checks iat and exp against the clock, the leeway and the
// maximum lifetime.
func (v *Verifier) checkTimes(iatClaim, expClaim *float64) (iat, exp time.Time, err error) {
	if iatClaim == nil || math.Abs(*iatClaim) > maxNumericDate {
		return iat, exp, invalid("iat")
	}
	if expClaim == nil || math.Abs(*expClaim) > maxNumericDate {
		return iat, exp, invalid("exp")
	}
	iat, exp = numericDate(*iatClaim), numericDate(*expClaim)
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	leeway := max(v.Leeway, 0)
	switch {
	case iat.After(now.Add(leeway)):
		return iat, exp, invalid("iat")
	case !exp.After(now.Add(-leeway)):
		return iat, exp, invalid("exp")
	case !exp.After(iat) || exp.Sub(iat) > MaxLifetime:
		return iat, exp, invalid("lifetime")
	}
	return iat, exp, nil
}

// numericDate converts a NumericDate to a time.
func numericDate(s float64) time.Time {
	sec := math.Floor(s)
	return time.Unix(int64(sec), int64((s-sec)*1e9)).UTC()
}
