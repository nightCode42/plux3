// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/pkcs11"
)

// The token the rehearsal provisions.
const (
	softHSMEnv   = "PLUX_TEST_SOFTHSM_MODULE"
	rehearsalTok = "plux-root"
	rehearsalSO  = "so-pin-5678"
	rehearsalPIN = "user-pin-1234"
	// ckmECEdwardsKeyPairGen is CKM_EC_EDWARDS_KEY_PAIR_GEN of PKCS#11 3.0.
	ckmECEdwardsKeyPairGen = 0x00001055
)

// oidEd25519 is the DER curve identifier of Ed25519.
var oidEd25519 = []byte{0x06, 0x03, 0x2b, 0x65, 0x70}

// Verifies: SEC-121, SEC-051.
// The rehearsal of the root key ceremony on a SoftHSM token: three Ed25519
// root keys are generated on the token, the helper serves it, and the
// holders export, make, sign, verify and upload root 1 with two of three
// signatures (one is refused) and then a rotation to three other keys that
// both the old and the new threshold sign.
func TestRootCeremonyOnSoftHSM_SEC_121(t *testing.T) {
	library := os.Getenv(softHSMEnv)
	if library == "" {
		t.Skipf("%s is not set; no SoftHSM2 module to run against", softHSMEnv)
	}
	dir := t.TempDir()
	labels := []string{"root-a", "root-b", "root-c", "root-d", "root-e", "root-f"}
	provisionRootToken(t, library, dir, labels)
	socket := startHelper(t, library, dir)

	refs := make([]string, len(labels))
	for i, label := range labels {
		refs[i] = "pkcs11:object=" + label
	}
	runCeremony(t, dir, []string{"-pkcs11-socket", socket}, refs)
}

// provisionRootToken initialises a SoftHSM token in dir and generates one
// Ed25519 key pair per label on it. The module is unloaded again so that
// the helper process can load it.
func provisionRootToken(t *testing.T, library, dir string, labels []string) {
	t.Helper()
	tokens := filepath.Join(dir, "tokens")
	if err := os.Mkdir(tokens, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "softhsm2.conf")
	if err := os.WriteFile(conf, []byte("directories.tokendir = "+tokens+"\nobjectstore.backend = file\nlog.level = ERROR\n"), 0o600); err != nil {
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
	if err := ctx.InitToken(slots[0], rehearsalSO, rehearsalTok); err != nil {
		t.Fatal(err)
	}
	slot := initialisedSlot(t, ctx)
	session, err := ctx.OpenSession(slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return ctx.Login(session, pkcs11.CKU_SO, rehearsalSO) },
		func() error { return ctx.InitPIN(session, rehearsalPIN) },
		func() error { return ctx.Logout(session) },
		func() error { return ctx.Login(session, pkcs11.CKU_USER, rehearsalPIN) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	for i, label := range labels {
		generateEd25519(t, ctx, session, label, byte(i+1))
	}
}

// initialisedSlot finds the slot of the token InitToken made.
func initialisedSlot(t *testing.T, ctx *pkcs11.Ctx) uint {
	t.Helper()
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range slots {
		if info, err := ctx.GetTokenInfo(slot); err == nil && info.Label == rehearsalTok {
			return slot
		}
	}
	t.Fatal("the initialised token is not listed")
	return 0
}

// generateEd25519 creates a key pair that cannot leave the token.
func generateEd25519(t *testing.T, ctx *pkcs11.Ctx, session pkcs11.SessionHandle, label string, id byte) {
	t.Helper()
	public := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, oidEd25519),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
		pkcs11.NewAttribute(pkcs11.CKA_ID, []byte{id}),
	}
	private := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
		pkcs11.NewAttribute(pkcs11.CKA_ID, []byte{id}),
	}
	if _, _, err := ctx.GenerateKeyPair(session, []*pkcs11.Mechanism{pkcs11.NewMechanism(ckmECEdwardsKeyPairGen, nil)}, public, private); err != nil {
		t.Fatalf("generate %s: %v", label, err)
	}
}

// startHelper builds and starts plux-pkcs11-helper on the provisioned token
// and returns its socket once it accepts connections. The PIN reaches it
// through the environment, as in a ceremony, and is never printed.
func startHelper(t *testing.T, library, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "plux-pkcs11-helper")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "../plux-pkcs11-helper") //nolint:gosec // fixed arguments
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the helper: %v\n%s", err, out)
	}
	// A short path: a Unix socket's name is limited to about 100 bytes.
	sockDir, err := os.MkdirTemp("", "root")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "h.sock")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "--module", library, "--token-label", rehearsalTok, "--socket", socket) //nolint:gosec // the helper just built
	cmd.Env = append(os.Environ(), "PLUX_PKCS11_PIN="+rehearsalPIN)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return socket
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not open its socket")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
