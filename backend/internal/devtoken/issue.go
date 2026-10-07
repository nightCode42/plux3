// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package devtoken

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// Issuer mints device access tokens. It is safe for concurrent use and
// must not be copied after first use.
type Issuer struct {
	// Signer signs the tokens; the api role passes signing.TokenOnly.
	Signer signing.TokenSigner
	// Issuer is the iss claim.
	Issuer string
	// Audience is the aud claim.
	Audience string
	// Lifetime is how long a token lives: more than zero and at most
	// MaxLifetime, in whole seconds.
	Lifetime time.Duration
	// Now is the clock; nil uses the wall clock.
	Now func() time.Time
	// Random supplies the token identifiers; nil uses the system source.
	Random io.Reader

	mu sync.Mutex
	// kids remembers the key identifier that signs for each class. The
	// signature's own key identifier is checked against it, so a
	// rotation shows up as a mismatch that clears the entry.
	kids map[signing.TokenClass]string
}

// Issue mints a token for the claims and returns it with its expiry.
// The signing key is the newest of the class (production if c.Production,
// development otherwise); its identifier is written into the header
// before signing, because the header is part of what is signed, and the
// identifier the signer reports is checked against it afterwards. When a
// rotation makes them differ the keys are read again and signing is
// retried once; a token is never returned with a kid that is not its
// signer's.
func (i *Issuer) Issue(ctx context.Context, c Claims) (string, time.Time, error) {
	if err := i.validate(c); err != nil {
		return "", time.Time{}, err
	}
	class := signing.TokenDevelopment
	if c.Production {
		class = signing.TokenProduction
	}
	jti, err := i.newID()
	if err != nil {
		return "", time.Time{}, err
	}
	now := i.now()
	iat := now.Unix()
	exp := iat + int64(i.Lifetime/time.Second)
	payload, err := json.Marshal(wire{
		Iss: i.Issuer, Aud: i.Audience, Sub: c.DeviceID, ClientID: c.AppID,
		Env: c.Environment, AL: c.Assurance, Cnf: wireCnf{JKT: c.JKT},
		IAT: iat, EXP: exp, JTI: jti,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("devtoken: encode the claims: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, ok, err := i.sign(ctx, class, payload)
		if err != nil {
			return "", time.Time{}, err
		}
		if ok {
			return token, time.Unix(exp, 0).UTC(), nil
		}
	}
	return "", time.Time{}, errors.New("devtoken: the signing key changed twice while signing a token")
}

// sign builds and signs one token. It reports false, with the cached
// key identifier cleared, when the signer used another key than the one
// named in the header.
func (i *Issuer) sign(ctx context.Context, class signing.TokenClass, payload []byte) (string, bool, error) {
	kid, err := i.keyID(ctx, class)
	if err != nil {
		return "", false, err
	}
	header, err := json.Marshal(struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}{Alg: "ES256", Typ: tokenType, Kid: kid})
	if err != nil {
		return "", false, fmt.Errorf("devtoken: encode the header: %w", err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sig, signedBy, err := i.Signer.SignToken(ctx, class, []byte(input))
	if err != nil {
		return "", false, fmt.Errorf("devtoken: sign: %w", err)
	}
	if signedBy != kid {
		i.mu.Lock()
		delete(i.kids, class)
		i.mu.Unlock()
		return "", false, nil
	}
	if len(sig) != signing.TokenSignatureSize {
		return "", false, fmt.Errorf("devtoken: the signer returned a %d-byte signature", len(sig))
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), true, nil
}

// keyID returns the identifier of the newest key of a class, reading the
// signer's keys when none is remembered.
func (i *Issuer) keyID(ctx context.Context, class signing.TokenClass) (string, error) {
	i.mu.Lock()
	kid, ok := i.kids[class]
	i.mu.Unlock()
	if ok {
		return kid, nil
	}
	keys, err := i.Signer.TokenKeys(ctx)
	if err != nil {
		return "", fmt.Errorf("devtoken: list the token keys: %w", err)
	}
	for _, k := range keys {
		if k.Class == class {
			kid = k.ID
		}
	}
	if kid == "" {
		return "", fmt.Errorf("devtoken: the signer has no %s token key", class)
	}
	i.mu.Lock()
	if i.kids == nil {
		i.kids = map[signing.TokenClass]string{}
	}
	i.kids[class] = kid
	i.mu.Unlock()
	return kid, nil
}

// validate checks the issuer's configuration and the claims.
func (i *Issuer) validate(c Claims) error {
	switch {
	case i.Signer == nil:
		return errors.New("devtoken: the issuer needs a signer")
	case i.Issuer == "" || i.Audience == "":
		return errors.New("devtoken: the issuer needs an issuer and an audience")
	case i.Lifetime < time.Second || i.Lifetime > MaxLifetime:
		return fmt.Errorf("devtoken: the lifetime must be between one second and %s", MaxLifetime)
	case c.DeviceID == "" || c.AppID == "" || c.Environment == "" || c.JKT == "":
		return errors.New("devtoken: the claims need a device, an app, an environment and a key thumbprint")
	case !validAssurance(c.Assurance):
		return errors.New("devtoken: the assurance level is not AL0 to AL3")
	}
	return nil
}

// now reads the clock.
func (i *Issuer) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

// newID returns a fresh token identifier: sixteen random bytes in
// unpadded base64url.
func (i *Issuer) newID() (string, error) {
	src := i.Random
	if src == nil {
		src = rand.Reader
	}
	buf := make([]byte, jtiBytes)
	if _, err := io.ReadFull(src, buf); err != nil {
		return "", fmt.Errorf("devtoken: draw a token identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
