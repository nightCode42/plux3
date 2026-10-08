// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// The PKCS#11 backend is a Backend and signs release metadata and tokens.
var (
	_ signing.Backend     = (*signing.PKCS11)(nil)
	_ signing.TokenSigner = (*signing.PKCS11)(nil)
)

// fakeHelper is an in-process helper: it speaks the wire protocol over a
// Unix socket and signs with software keys labelled like the token's.
type fakeHelper struct {
	path string

	mu    sync.Mutex
	keys  map[string]any // ed25519.PrivateKey or *ecdsa.PrivateKey, by label
	calls []*pkcs11pb.Request
	// override, when set, answers instead of the honest logic; returning
	// nil falls back to it.
	override func(*pkcs11pb.Request) *pkcs11pb.Response
	// raw, when set, is written to the client instead of a response.
	raw []byte
	// stall holds the connection open without answering.
	stall chan struct{}
}

// newFakeHelper starts a helper holding Ed25519 keys "targets-env" and
// "other" and P-256 keys "plux-tokens-development" and
// "plux-tokens-production".
func newFakeHelper(t *testing.T) *fakeHelper {
	t.Helper()
	dir, err := os.MkdirTemp("", "p11")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeHelper{path: filepath.Join(dir, "h.sock"), keys: map[string]any{}}
	for _, label := range []string{"targets-env", "other"} {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[label] = priv
	}
	for _, label := range []string{"plux-tokens-development", "plux-tokens-production"} {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[label] = priv
	}
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

// serve answers the one request of a connection.
func (f *fakeHelper) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var req pkcs11pb.Request
	if err := pkcs11wire.ReadMessage(conn, &req); err != nil {
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, &req)
	override, raw, stall := f.override, f.raw, f.stall
	f.mu.Unlock()
	if stall != nil {
		<-stall
		return
	}
	if raw != nil {
		_, _ = conn.Write(raw)
		return
	}
	var resp *pkcs11pb.Response
	if override != nil {
		resp = override(&req)
	}
	if resp == nil {
		resp = f.answer(&req)
	}
	_ = pkcs11wire.WriteMessage(conn, resp)
}

// answer is the honest helper.
func (f *fakeHelper) answer(req *pkcs11pb.Request) *pkcs11pb.Response {
	switch r := req.GetRequest().(type) {
	case *pkcs11pb.Request_PublicKey:
		return f.publicKey(r.PublicKey.GetKeyRef())
	case *pkcs11pb.Request_Sign:
		return f.sign(r.Sign)
	}
	return helperError(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST)
}

func helperError(code pkcs11pb.ErrorCode) *pkcs11pb.Response {
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Error{Error: &pkcs11pb.Error{Code: code, Message: "refused"}}}
}

// key finds a key by the label in a key reference.
func (f *fakeHelper) key(ref string) (any, bool) {
	label, ok := strings.CutPrefix(ref, "pkcs11:object=")
	if !ok {
		return nil, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k, found := f.keys[label]
	return k, found
}

func (f *fakeHelper) publicKey(ref string) *pkcs11pb.Response {
	key, ok := f.key(ref)
	if !ok {
		return helperError(pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND)
	}
	var pub any
	var alg pkcs11pb.Algorithm
	var id string
	switch k := key.(type) {
	case ed25519.PrivateKey:
		p := k.Public().(ed25519.PublicKey)
		pub, alg, id = p, pkcs11pb.Algorithm_ALGORITHM_ED25519, signing.KeyID(p)
	case *ecdsa.PrivateKey:
		pub, alg = &k.PublicKey, pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256
		id, _ = signing.TokenKeyID(&k.PublicKey)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_PublicKey{PublicKey: &pkcs11pb.PublicKeyResponse{PublicKeyDer: der, Algorithm: alg, KeyId: id}}}
}

func (f *fakeHelper) sign(req *pkcs11pb.SignRequest) *pkcs11pb.Response {
	key, ok := f.key(req.GetKeyRef())
	if !ok {
		return helperError(pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND)
	}
	var sig []byte
	var id string
	switch k := key.(type) {
	case ed25519.PrivateKey:
		sig, id = ed25519.Sign(k, req.GetDigest()), signing.KeyID(k.Public().(ed25519.PublicKey))
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, k, req.GetDigest())
		if err != nil {
			return helperError(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		id, _ = signing.TokenKeyID(&k.PublicKey)
	}
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Sign{Sign: &pkcs11pb.SignResponse{Signature: sig, KeyId: id}}}
}

// callCount is the number of requests the helper received.
func (f *fakeHelper) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newPKCS11 returns a backend talking to the helper.
func newPKCS11(t *testing.T, f *fakeHelper) *signing.PKCS11 {
	t.Helper()
	b, err := signing.NewPKCS11(signing.PKCS11Options{Socket: f.path, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Verifies: SEC-120.
func TestPKCS11SignsReleaseMetadata(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	ctx := context.Background()
	message := []byte("targets metadata")
	sig, id, err := b.Sign(ctx, "targets-env", message)
	if err != nil {
		t.Fatal(err)
	}
	pub, pubID, err := b.PublicKey(ctx, "targets-env")
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, message, sig) || id != pubID || id != signing.KeyID(pub) {
		t.Errorf("signature verifies %v, key ID %q, public key ID %q", ed25519.Verify(pub, message, sig), id, pubID)
	}
	other, otherID, err := b.PublicKey(ctx, "other")
	if err != nil || other.Equal(pub) || otherID == id {
		t.Errorf("a second key is not distinct: %v", err)
	}
	if b.Name() != "pkcs11" || !b.AllowedInProduction() {
		t.Errorf("Name = %q, AllowedInProduction = %v", b.Name(), b.AllowedInProduction())
	}
}

// Verifies: SEC-120.
func TestPKCS11AsksForAPublicKeyOnce(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	for range 3 {
		if _, _, err := b.Sign(context.Background(), "targets-env", []byte("m")); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.callCount(); got != 4 {
		t.Errorf("%d requests for three signatures, want 1 public key and 3 signatures", got)
	}
}

// Verifies: SEC-120.
func TestPKCS11SignsTokensPerClass(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	ctx := context.Background()
	input := []byte("eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJkIn0")
	keys, err := b.TokenKeys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("TokenKeys = %v, %v", keys, err)
	}
	for _, class := range []signing.TokenClass{signing.TokenProduction, signing.TokenDevelopment} {
		sig, id, err := b.SignToken(ctx, class, input)
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		if len(sig) != signing.TokenSignatureSize {
			t.Errorf("%s: %d-byte signature", class, len(sig))
		}
		var key *signing.TokenKey
		for i := range keys {
			if keys[i].Class == class {
				key = &keys[i]
			}
		}
		if key == nil || key.ID != id || !verifyES256(key.Public, input, sig) {
			t.Errorf("%s: the signature does not verify under the listed key", class)
		}
	}
	if _, _, err := b.SignToken(ctx, "staging", input); err == nil {
		t.Error("signed for a class that does not exist")
	}
}

// Verifies: SEC-120.
func TestPKCS11MissingKeys(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	ctx := context.Background()
	if _, _, err := b.Sign(ctx, "absent", []byte("m")); !errors.Is(err, signing.ErrNoKey) {
		t.Errorf("Sign with a missing key: %v, want ErrNoKey", err)
	}
	delete(f.keys, "plux-tokens-production")
	keys, err := b.TokenKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Class != signing.TokenDevelopment {
		t.Errorf("TokenKeys = %v, %v; want the development key only", keys, err)
	}
	if _, _, err := b.SignToken(ctx, signing.TokenProduction, []byte("x")); !errors.Is(err, signing.ErrNoKey) {
		t.Errorf("SignToken without the production key: %v, want ErrNoKey", err)
	}
}

// Verifies: SEC-120.
func TestPKCS11RefusesBadKeyReferencesAndKeyKinds(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	ctx := context.Background()
	for _, ref := range []string{"", "Targets", "../x", "a;object=b", "a b", strings.Repeat("a", 65)} {
		if _, _, err := b.Sign(ctx, ref, []byte("m")); err == nil {
			t.Errorf("Sign accepted the reference %q", ref)
		}
	}
	if f.callCount() != 0 {
		t.Errorf("a refused reference reached the helper (%d calls)", f.callCount())
	}
	// A P-256 key cannot release-sign, and an Ed25519 key cannot sign a token.
	f.keys["plux-tokens-wrong"] = f.keys["targets-env"]
	f.keys["ecdsa-release"] = f.keys["plux-tokens-development"]
	if _, _, err := b.Sign(ctx, "ecdsa-release", []byte("m")); err == nil {
		t.Error("Sign used a P-256 key")
	}
	wrong, err := signing.NewPKCS11(signing.PKCS11Options{Socket: f.path, TokenKey: "plux-tokens-wrong"}) //nolint:gosec // a label, not a credential
	if err != nil {
		t.Fatal(err)
	}
	f.keys["plux-tokens-wrong-development"] = f.keys["targets-env"]
	if _, _, err := wrong.SignToken(ctx, signing.TokenDevelopment, []byte("m")); err == nil {
		t.Error("SignToken used an Ed25519 key")
	}
}

// Verifies: SEC-120.
func TestPKCS11RefusesWhatItCannotTrust(t *testing.T) {
	t.Parallel()
	_, impostor, _ := ed25519.GenerateKey(rand.Reader)
	short := &pkcs11pb.Response{Response: &pkcs11pb.Response_Sign{Sign: &pkcs11pb.SignResponse{Signature: []byte{1, 2, 3}}}}
	tests := []struct {
		name     string
		override func(f *fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response
		want     string
	}{
		{"a signature of another key", func(f *fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			id := signing.KeyID(f.keys["targets-env"].(ed25519.PrivateKey).Public().(ed25519.PublicKey))
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if s := r.GetSign(); s != nil {
					return &pkcs11pb.Response{Response: &pkcs11pb.Response_Sign{Sign: &pkcs11pb.SignResponse{Signature: ed25519.Sign(impostor, s.GetDigest()), KeyId: id}}}
				}
				return nil
			}
		}, "does not verify"},
		{"a signature that names another key", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if s := r.GetSign(); s != nil {
					return &pkcs11pb.Response{Response: &pkcs11pb.Response_Sign{Sign: &pkcs11pb.SignResponse{Signature: ed25519.Sign(impostor, s.GetDigest()), KeyId: "ffff"}}}
				}
				return nil
			}
		}, "other than the one it described"},
		{"a short signature", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetSign() != nil {
					return short
				}
				return nil
			}
		}, "3-byte signature"},
		{"a sign answer to a key request", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetPublicKey() != nil {
					return short
				}
				return nil
			}
		}, "another kind of response"},
		{"a key answer to a sign request", func(f *fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetSign() != nil {
					return f.publicKey("pkcs11:object=targets-env")
				}
				return nil
			}
		}, "another kind of response"},
		{"a key identifier that does not match", func(f *fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetPublicKey() != nil {
					resp := f.publicKey("pkcs11:object=targets-env")
					resp.GetPublicKey().KeyId = "ffff"
					return resp
				}
				return nil
			}
		}, "identifier does not match"},
		{"an algorithm that does not match", func(f *fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetPublicKey() != nil {
					resp := f.publicKey("pkcs11:object=targets-env")
					resp.GetPublicKey().Algorithm = pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256
					return resp
				}
				return nil
			}
		}, "does not match the key"},
		{"a key that is not PKIX", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(r *pkcs11pb.Request) *pkcs11pb.Response {
				if r.GetPublicKey() != nil {
					return &pkcs11pb.Response{Response: &pkcs11pb.Response_PublicKey{PublicKey: &pkcs11pb.PublicKeyResponse{PublicKeyDer: []byte("nope")}}}
				}
				return nil
			}
		}, "not PKIX"},
		{"an error", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(*pkcs11pb.Request) *pkcs11pb.Response { return helperError(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL) }
		}, "ERROR_CODE_INTERNAL"},
		{"an empty response", func(*fakeHelper) func(*pkcs11pb.Request) *pkcs11pb.Response {
			return func(*pkcs11pb.Request) *pkcs11pb.Response { return &pkcs11pb.Response{} }
		}, "another kind of response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeHelper(t)
			f.override = tc.override(f)
			_, _, err := newPKCS11(t, f).Sign(context.Background(), "targets-env", []byte("m"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Verifies: SEC-120.
func TestPKCS11RefusesBrokenFraming(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"an oversized frame": {0, 1, 0, 1},
		"a truncated frame":  {0, 0, 0, 9, 1},
		"no answer":          {},
		"garbage":            {0, 0, 0, 3, 0xff, 0xff, 0xff},
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFakeHelper(t)
			f.raw = raw
			if _, _, err := newPKCS11(t, f).Sign(context.Background(), "targets-env", []byte("m")); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// Verifies: SEC-120.
func TestPKCS11RefusesAnOversizedMessage(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	_, _, err := b.Sign(context.Background(), "targets-env", make([]byte, pkcs11wire.MaxMessageSize))
	if !errors.Is(err, pkcs11wire.ErrMessageTooLarge) {
		t.Errorf("err = %v, want ErrMessageTooLarge", err)
	}
}

// Verifies: SEC-120.
func TestPKCS11GivesUpOnAStuckHelper(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	f.stall = make(chan struct{})
	t.Cleanup(func() { close(f.stall) })
	b, err := signing.NewPKCS11(signing.PKCS11Options{Socket: f.path, Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err := b.Sign(context.Background(), "targets-env", []byte("m")); err == nil {
		t.Fatal("signed")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("gave up after %v", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := b.Sign(ctx, "targets-env", []byte("m")); err == nil {
		t.Error("signed with a cancelled context")
	}
}

// Verifies: SEC-120.
func TestPKCS11Ping(t *testing.T) {
	t.Parallel()
	f := newFakeHelper(t)
	b := newPKCS11(t, f)
	if err := b.Ping(context.Background()); err != nil {
		t.Errorf("Ping = %v", err)
	}
	gone, err := signing.NewPKCS11(signing.PKCS11Options{Socket: filepath.Join(t.TempDir(), "none")})
	if err != nil {
		t.Fatal(err)
	}
	if err := gone.Ping(context.Background()); err == nil {
		t.Error("Ping succeeded without a helper")
	}
	if _, _, err := gone.Sign(context.Background(), "targets-env", []byte("m")); err == nil {
		t.Error("Sign succeeded without a helper")
	}
}

// Verifies: SEC-120, SEC-106.
func TestPKCS11DoesNotWrapDataKeys(t *testing.T) {
	t.Parallel()
	b := newPKCS11(t, newFakeHelper(t))
	if _, _, err := b.Wrap(context.Background(), make([]byte, 32)); !errors.Is(err, signing.ErrNoWrapKey) {
		t.Errorf("Wrap = %v, want ErrNoWrapKey", err)
	}
	if _, err := b.Unwrap(context.Background(), []byte("x"), "id"); !errors.Is(err, signing.ErrNoWrapKey) {
		t.Errorf("Unwrap = %v, want ErrNoWrapKey", err)
	}
}

// Verifies: SEC-120.
func TestNewPKCS11ChecksItsOptions(t *testing.T) {
	t.Parallel()
	if _, err := signing.NewPKCS11(signing.PKCS11Options{}); err == nil {
		t.Error("accepted no socket")
	}
	if _, err := signing.NewPKCS11(signing.PKCS11Options{Socket: "/s", TokenKey: "Bad Key"}); err == nil {
		t.Error("accepted a bad token key prefix")
	}
}

// Verifies: SEC-120.
func TestPKCS11TokenOnlyHidesTheBackend(t *testing.T) {
	t.Parallel()
	narrowed := signing.TokenOnly(newPKCS11(t, newFakeHelper(t)))
	if _, ok := narrowed.(signing.Signer); ok {
		t.Error("a token-only view can sign releases")
	}
}
