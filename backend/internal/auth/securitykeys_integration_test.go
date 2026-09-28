// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// cbor encodes the values a software authenticator writes: unsigned and
// negative integers, byte and text strings, and maps with integer or
// string keys in the order given.
type cborMap [][2]any

func cbor(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n < 1<<8:
			return []byte{major<<5 | 24, byte(n)}
		case n < 1<<16:
			return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
		default:
			return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n)) //nolint:gosec // test values are small
		}
	}
	switch x := v.(type) {
	case int:
		if x < 0 {
			return head(1, uint64(-1-x))
		}
		return head(0, uint64(x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case cborMap:
		out := head(5, uint64(len(x)))
		for _, kv := range x {
			out = append(out, cbor(kv[0])...)
			out = append(out, cbor(kv[1])...)
		}
		return out
	}
	panic("unsupported")
}

// authenticator is a software security key.
type authenticator struct {
	rpID    string
	origin  string
	id      []byte
	ec      *ecdsa.PrivateKey
	ed      ed25519.PrivateKey
	counter uint32
}

func newAuthenticator(t *testing.T, ed bool) *authenticator {
	t.Helper()
	a := &authenticator{rpID: "plux.example", origin: "https://plux.example", id: make([]byte, 16)}
	_, _ = rand.Read(a.id)
	var err error
	if ed {
		_, a.ed, err = ed25519.GenerateKey(rand.Reader)
	} else {
		a.ec, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (a *authenticator) coseKey() []byte {
	if a.ed != nil {
		return cbor(cborMap{{1, 1}, {3, -8}, {-1, 6}, {-2, []byte(a.ed.Public().(ed25519.PublicKey))}})
	}
	pub, _ := a.ec.PublicKey.Bytes()
	return cbor(cborMap{{1, 2}, {3, -7}, {-1, 1}, {-2, pub[1:33]}, {-3, pub[33:]}})
}

func (a *authenticator) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	flags := byte(0x01)
	if attested {
		flags |= 0x40
	}
	out := append(rp[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.counter)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.id))) //nolint:gosec // a 16-byte identifier
		out = append(out, a.id...)
		out = append(out, a.coseKey()...)
	}
	return out
}

func (a *authenticator) clientData(t *testing.T, ceremony string, options []byte) []byte {
	t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(options, &o); err != nil || o.Challenge == "" {
		t.Fatalf("options %s: %v", options, err)
	}
	b, _ := json.Marshal(map[string]any{"type": ceremony, "challenge": o.Challenge, "origin": a.origin})
	return b
}

// register answers creation options.
func (a *authenticator) register(t *testing.T, options []byte) ([]byte, []byte) {
	t.Helper()
	cd := a.clientData(t, "webauthn.create", options)
	obj := cbor(cborMap{{"fmt", "none"}, {"attStmt", cborMap{}}, {"authData", a.authData(true)}})
	return cd, obj
}

// assert answers request options.
func (a *authenticator) assert(t *testing.T, options []byte) auth.WebAuthnAssertion {
	t.Helper()
	a.counter++
	cd := a.clientData(t, "webauthn.get", options)
	data := a.authData(false)
	sum := sha256.Sum256(cd)
	signed := append(append([]byte{}, data...), sum[:]...)
	var sig []byte
	if a.ed != nil {
		sig = ed25519.Sign(a.ed, signed)
	} else {
		digest := sha256.Sum256(signed)
		var err error
		if sig, err = a.ec.Sign(rand.Reader, digest[:], crypto.SHA256); err != nil {
			t.Fatal(err)
		}
	}
	return auth.WebAuthnAssertion{CredentialID: a.id, ClientDataJSON: cd, AuthenticatorData: data, Signature: sig}
}

func withWebAuthn(o *auth.Options) {
	o.WebAuthn = auth.WebAuthnConfig{RPID: "plux.example", RPName: "Plux", Origins: []string{"https://plux.example"}}
}

// Verifies: SEC-100.
// A security key or passkey is registered as a second factor and then
// answers the sign-in challenge; a response from another origin, for
// another site, with a forged signature, or replayed with an old
// counter is refused.
func TestSecurityKeys(t *testing.T) {
	t.Parallel()
	f := newFixtureWith(t, nil, failedSignIns, withWebAuthn)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	id := f.signIn(t, "ada@example.com")
	key := newAuthenticator(t, false)

	factor, options, err := f.svc.BeginWebAuthnRegistration(ctx, id, "yubikey")
	if err != nil || factor.Kind != "webauthn" || factor.Confirmed {
		t.Fatalf("BeginWebAuthnRegistration = %+v, %v", factor, err)
	}
	var creation struct {
		RP               struct{ ID string } `json:"rp"`
		Attestation      string              `json:"attestation"`
		PubKeyCredParams []struct{ Alg int } `json:"pubKeyCredParams"`
	}
	if err := json.Unmarshal(options, &creation); err != nil || creation.RP.ID != "plux.example" || creation.Attestation != "none" || len(creation.PubKeyCredParams) != 3 {
		t.Errorf("creation options = %s", options)
	}
	// Another origin is refused.
	wrong := *key
	wrong.origin = "https://evil.example"
	cd, obj := wrong.register(t, options)
	if _, err := f.svc.FinishWebAuthnRegistration(ctx, id, factor.ID, cd, obj); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("another origin: %v", err)
	}
	// A credential for another site is refused.
	other := *key
	other.rpID = "evil.example"
	cd, obj = other.register(t, options)
	if _, err := f.svc.FinishWebAuthnRegistration(ctx, id, factor.ID, cd, obj); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("another site: %v", err)
	}
	cd, obj = key.register(t, options)
	confirmed, err := f.svc.FinishWebAuthnRegistration(ctx, id, factor.ID, cd, obj)
	if err != nil || !confirmed.Confirmed {
		t.Fatalf("FinishWebAuthnRegistration = %+v, %v", confirmed, err)
	}
	if !f.reload(t, id).SecondFactor {
		t.Error("registering a key did not count as presenting a second factor")
	}
	if _, err := f.svc.FinishWebAuthnRegistration(ctx, id, factor.ID, cd, obj); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a second registration of the factor: %v", err)
	}
	// A second key, Ed25519.
	ed := newAuthenticator(t, true)
	f2, options2, err := f.svc.BeginWebAuthnRegistration(ctx, id, "passkey")
	if err != nil {
		t.Fatal(err)
	}
	cd, obj = ed.register(t, options2)
	if _, err := f.svc.FinishWebAuthnRegistration(ctx, id, f2.ID, cd, obj); err != nil {
		t.Fatalf("an Ed25519 key: %v", err)
	}

	// Sign-in now asks for the key.
	_, challenge, err := f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	if err != nil || !slices.Contains(challenge.Kinds, "webauthn") {
		t.Fatalf("StartPasswordLogin = %+v, %v", challenge, err)
	}
	if _, err := f.svc.CompleteWebAuthnLogin(ctx, challenge.Secret, key.assert(t, options)); code(err) != plxerr.PreconditionFailed {
		t.Errorf("an answer before asking for options: %v", err)
	}
	request, err := f.svc.BeginWebAuthnLogin(ctx, challenge.Secret)
	if err != nil {
		t.Fatal(err)
	}
	forged := key.assert(t, request)
	forged.Signature[len(forged.Signature)-1] ^= 1
	if _, err := f.svc.CompleteWebAuthnLogin(ctx, challenge.Secret, forged); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a forged signature: %v", err)
	}
	// The challenge is spent by the wrong answer; ask again.
	request, err = f.svc.BeginWebAuthnLogin(ctx, challenge.Secret)
	if err != nil {
		t.Fatal(err)
	}
	answer := key.assert(t, request)
	session, err := f.svc.CompleteWebAuthnLogin(ctx, challenge.Secret, answer)
	if err != nil || session.Secret == "" {
		t.Fatalf("CompleteWebAuthnLogin = %+v, %v", session, err)
	}
	signedIn, err := f.svc.AuthenticateSession(ctx, session.Secret)
	if err != nil || !signedIn.SecondFactor {
		t.Errorf("the session = %+v, %v", signedIn, err)
	}
	// A replayed answer on a new sign-in: its counter has not advanced.
	_, challenge, _ = f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	request, err = f.svc.BeginWebAuthnLogin(ctx, challenge.Secret)
	if err != nil {
		t.Fatal(err)
	}
	key.counter--
	if _, err := f.svc.CompleteWebAuthnLogin(ctx, challenge.Secret, key.assert(t, request)); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a counter that did not advance: %v", err)
	}
	request, _ = f.svc.BeginWebAuthnLogin(ctx, challenge.Secret)
	if _, err := f.svc.CompleteWebAuthnLogin(ctx, challenge.Secret, ed.assert(t, request)); err != nil {
		t.Errorf("the Ed25519 key: %v", err)
	}
	// Codes are not accepted for security keys.
	_, challenge, _ = f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	if _, err := f.svc.CompleteMFA(ctx, challenge.Secret, "123456"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a code for an account with only keys: %v", err)
	}
	factors, err := f.svc.ListFactors(ctx, id)
	if err != nil || len(factors) != 2 {
		t.Errorf("ListFactors = %+v, %v", factors, err)
	}
}

// Verifies: SEC-100.
// Without a relying party configured, security keys are refused.
func TestSecurityKeysNeedConfiguration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	id := f.signIn(t, "ada@example.com")
	if _, _, err := f.svc.BeginWebAuthnRegistration(ctx, id, "key"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("registration without a relying party: %v", err)
	}
	if _, err := f.svc.BeginWebAuthnLogin(ctx, "plux_mfa_x"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("sign-in without a relying party: %v", err)
	}
	_ = base64.RawURLEncoding
}
