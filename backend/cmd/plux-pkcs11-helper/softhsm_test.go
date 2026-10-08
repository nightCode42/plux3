// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/pkcs11"

	"github.com/nightCode42/plux3/backend/internal/pkcs11helper"
	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// softHSMEnv names the SoftHSM2 library the integration test runs against;
// the test is skipped when it is unset.
const softHSMEnv = "PLUX_TEST_SOFTHSM_MODULE"

const (
	testLabel   = "plux-test"
	testSOPIN   = "so-pin-5678"
	testUserPIN = "user-pin-1234"
	// ckmECEdwardsKeyPairGen is CKM_EC_EDWARDS_KEY_PAIR_GEN of PKCS#11 3.0.
	ckmECEdwardsKeyPairGen = 0x00001055
)

// DER curve identifiers for key generation.
var (
	oidEd25519 = []byte{0x06, 0x03, 0x2b, 0x65, 0x70}
	oidP256    = []byte{0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07}
)

// provision initialises a SoftHSM token in a temporary directory and
// generates the keys the test signs with. The library is unloaded again,
// so that the helper's own module can load it.
func provision(t *testing.T, library string) {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "softhsm2.conf")
	tokens := filepath.Join(dir, "tokens")
	if err := os.Mkdir(tokens, 0o700); err != nil {
		t.Fatal(err)
	}
	config := "directories.tokendir = " + tokens + "\nobjectstore.backend = file\nlog.level = ERROR\n"
	if err := os.WriteFile(conf, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOFTHSM2_CONF", conf)
	ctx := pkcs11.New(library)
	if ctx == nil {
		t.Fatalf("cannot load %s", library)
	}
	defer ctx.Destroy()
	if err := ctx.Initialize(); err != nil { //nolint:misspell // the binding's name
		t.Fatal(err)
	}
	defer func() { _ = ctx.Finalize() }() //nolint:misspell // the binding's name
	slots, err := ctx.GetSlotList(false)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots: %v, %v", slots, err)
	}
	if err := ctx.InitToken(slots[0], testSOPIN, testLabel); err != nil {
		t.Fatal(err)
	}
	slot := tokenSlot(t, ctx)
	session, err := ctx.OpenSession(slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return ctx.Login(session, pkcs11.CKU_SO, testSOPIN) },
		func() error { return ctx.InitPIN(session, testUserPIN) },
		func() error { return ctx.Logout(session) },
		func() error { return ctx.Login(session, pkcs11.CKU_USER, testUserPIN) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	generate(t, ctx, session, ckmECEdwardsKeyPairGen, oidEd25519, "targets-test", []byte{1})
	generate(t, ctx, session, pkcs11.CKM_EC_KEY_PAIR_GEN, oidP256, "plux-tokens-development", []byte{2})
	generateAES(t, ctx, session, "plux-secrets")
	generateAES(t, ctx, session, "other-secrets")
}

// tokenSlot finds the slot of the initialised token.
func tokenSlot(t *testing.T, ctx *pkcs11.Ctx) uint {
	t.Helper()
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range slots {
		if info, err := ctx.GetTokenInfo(slot); err == nil && info.Label == testLabel {
			return slot
		}
	}
	t.Fatal("the initialised token is not listed")
	return 0
}

// generateAES creates an AES-256 key that stays on the token.
func generateAES(t *testing.T, ctx *pkcs11.Ctx, session pkcs11.SessionHandle, label string) {
	t.Helper()
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_SECRET_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_AES),
		pkcs11.NewAttribute(pkcs11.CKA_VALUE_LEN, 32),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),
		pkcs11.NewAttribute(pkcs11.CKA_ENCRYPT, true),
		pkcs11.NewAttribute(pkcs11.CKA_DECRYPT, true),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
	}
	if _, err := ctx.GenerateKey(session, []*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_AES_KEY_GEN, nil)}, template); err != nil {
		t.Fatalf("generate %s: %v", label, err)
	}
}

// generate creates a key pair that stays on the token.
func generate(t *testing.T, ctx *pkcs11.Ctx, session pkcs11.SessionHandle, mechanism uint, params []byte, label string, id []byte) {
	t.Helper()
	public := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, params),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
		pkcs11.NewAttribute(pkcs11.CKA_ID, id),
	}
	private := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
		pkcs11.NewAttribute(pkcs11.CKA_ID, id),
	}
	if _, _, err := ctx.GenerateKeyPair(session, []*pkcs11.Mechanism{pkcs11.NewMechanism(mechanism, nil)}, public, private); err != nil {
		t.Fatalf("generate %s: %v", label, err)
	}
}

// Verifies: SEC-120.
func TestSoftHSMSignsThroughTheHelper(t *testing.T) {
	library := os.Getenv(softHSMEnv)
	if library == "" {
		t.Skipf("%s is not set; no SoftHSM2 module to run against", softHSMEnv)
	}
	provision(t, library)
	mod, err := openModule(library, testLabel, testUserPIN)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.close()
	if _, err := openModule(library, "no-such-token", testUserPIN); err == nil {
		t.Error("opened a token that does not exist")
	}

	dir, err := os.MkdirTemp("", "p11")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	socket := filepath.Join(dir, "h.sock")
	l, err := pkcs11helper.Listen(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pkcs11helper.NewServer(mod, pkcs11helper.Options{}).Serve(ctx, l) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()

	backend, err := signing.NewPKCS11(signing.PKCS11Options{Socket: socket, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	checkRelease(t, backend)
	checkTokens(t, backend)
	checkSelectors(t, socket)
	checkWrap(t, backend, socket)

	// An HSM ends an idle session; the next call logs in again.
	mod.mu.Lock()
	_ = mod.ctx.CloseSession(mod.session)
	mod.mu.Unlock()
	if _, _, err := backend.Sign(context.Background(), "targets-test", []byte("after the session closed")); err != nil {
		t.Errorf("Sign after the session closed: %v", err)
	}
}

// checkRelease signs and reads the Ed25519 key through the backend.
func checkRelease(t *testing.T, backend *signing.PKCS11) {
	t.Helper()
	bg := context.Background()
	message := []byte("targets metadata")
	sig, id, err := backend.Sign(bg, "targets-test", message)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, pubID, err := backend.PublicKey(bg, "targets-test")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if !ed25519.Verify(pub, message, sig) || id != pubID {
		t.Errorf("the signature does not verify under %s (%s)", pubID, id)
	}
	if _, _, err := backend.Sign(bg, "absent", message); !errors.Is(err, signing.ErrNoKey) {
		t.Errorf("Sign with a missing key: %v, want ErrNoKey", err)
	}
}

// checkTokens signs a token with the P-256 key through the backend.
func checkTokens(t *testing.T, backend *signing.PKCS11) {
	t.Helper()
	bg := context.Background()
	input := []byte("eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJkIn0")
	sig, id, err := backend.SignToken(bg, signing.TokenDevelopment, input)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	keys, err := backend.TokenKeys(bg)
	if err != nil || len(keys) != 1 || keys[0].ID != id {
		t.Fatalf("TokenKeys = %v, %v; want the development key %s", keys, err, id)
	}
	digest := sha256.Sum256(input)
	if len(sig) != signing.TokenSignatureSize || !verifyRS(keys[0], digest[:], sig) {
		t.Errorf("the ES256 signature does not verify")
	}
	if _, _, err := backend.SignToken(bg, signing.TokenProduction, input); !errors.Is(err, signing.ErrNoKey) {
		t.Errorf("SignToken without the production key: %v, want ErrNoKey", err)
	}
}

// checkWrap wraps and unwraps a data key with the AES key on the token.
func checkWrap(t *testing.T, backend *signing.PKCS11, socket string) {
	t.Helper()
	bg := context.Background()
	dataKey := bytes.Repeat([]byte{7}, 32)
	wrapped, id, err := backend.Wrap(bg, dataKey)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if len(wrapped) != 12+len(dataKey)+16 || bytes.Contains(wrapped, dataKey) {
		t.Errorf("wrapped key of %d bytes", len(wrapped))
	}
	got, err := backend.Unwrap(bg, wrapped, id)
	if err != nil || !bytes.Equal(got, dataKey) {
		t.Errorf("Unwrap = %x, %v", got, err)
	}
	again, _, err := backend.Wrap(bg, dataKey)
	if err != nil || bytes.Equal(again[:12], wrapped[:12]) {
		t.Errorf("a second wrap reuses the IV (%v)", err)
	}
	for _, i := range []int{0, 12, len(wrapped) - 1} {
		tampered := bytes.Clone(wrapped)
		tampered[i] ^= 1
		if _, err := backend.Unwrap(bg, tampered, id); err == nil || !strings.Contains(err.Error(), "DECRYPT_FAILED") {
			t.Errorf("a wrapped key altered at byte %d: %v", i, err)
		}
	}
	other, err := signing.NewPKCS11(signing.PKCS11Options{Socket: socket, WrapKey: "other-secrets", Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Unwrap(bg, wrapped, "pkcs11:other-secrets"); err == nil || !strings.Contains(err.Error(), "DECRYPT_FAILED") {
		t.Errorf("the wrong key: %v", err)
	}
	absent, err := signing.NewPKCS11(signing.PKCS11Options{Socket: socket, WrapKey: "absent", Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := absent.Wrap(bg, dataKey); !errors.Is(err, signing.ErrNoKey) {
		t.Errorf("Wrap with a missing key: %v, want ErrNoKey", err)
	}
}

// checkSelectors reaches a key by its CKA_ID and refuses an unknown one,
// speaking the protocol directly.
func checkSelectors(t *testing.T, socket string) {
	t.Helper()
	ask := func(ref string) *pkcs11pb.Response {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		req := &pkcs11pb.Request{Request: &pkcs11pb.Request_PublicKey{PublicKey: &pkcs11pb.PublicKeyRequest{KeyRef: ref}}}
		if err := pkcs11wire.WriteMessage(conn, req); err != nil {
			t.Fatal(err)
		}
		var resp pkcs11pb.Response
		if err := pkcs11wire.ReadMessage(conn, &resp); err != nil {
			t.Fatal(err)
		}
		return &resp
	}
	byID := ask("pkcs11:id=%01").GetPublicKey()
	if byID == nil || byID.GetAlgorithm() != pkcs11pb.Algorithm_ALGORITHM_ED25519 {
		t.Fatalf("by id = %v", byID)
	}
	if _, err := x509.ParsePKIXPublicKey(byID.GetPublicKeyDer()); err != nil {
		t.Error(err)
	}
	if got := ask("pkcs11:id=%63").GetError().GetCode(); got != pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND {
		t.Errorf("unknown id: %v", got)
	}
	both := ask("pkcs11:object=targets-test;id=%02").GetError().GetCode()
	if both != pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND {
		t.Errorf("a label and an id of different keys: %v, want KEY_NOT_FOUND", both)
	}
}

// verifyRS checks an ES256 signature, r then s, of a digest.
func verifyRS(key signing.TokenKey, digest, sig []byte) bool {
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(key.Public, digest, r, s)
}
