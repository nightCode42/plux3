// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// backend returns a file backend in a temporary directory.
func backend(t *testing.T) *signing.File {
	t.Helper()
	b, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	return b
}

// Verifies: SEC-120, SRV-052.
func TestSignAndVerify(t *testing.T) {
	t.Parallel()
	b := backend(t)
	ctx := context.Background()
	message := []byte("bundle hash")
	sig, keyID, err := b.Sign(ctx, "targets", message)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, sameID, err := b.PublicKey(ctx, "targets")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if keyID != sameID || keyID == "" {
		t.Errorf("key ids %q and %q", keyID, sameID)
	}
	if !ed25519.Verify(pub, message, sig) {
		t.Error("the signature does not verify")
	}
	if ed25519.Verify(pub, []byte("another hash"), sig) {
		t.Error("the signature verifies a different message")
	}
	// A second key is a different key.
	_, otherID, err := b.PublicKey(ctx, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if otherID == keyID {
		t.Error("two references returned the same key")
	}
	// The same reference is stable across backends on the same directory.
	again, _, err := b.Sign(ctx, "targets", message)
	if err != nil || !bytes.Equal(sig, again) {
		t.Errorf("Ed25519 signatures must be deterministic: %v", err)
	}
}

// Verifies: SEC-056, SEC-120.
func TestFileBackendIsRefusedInProduction(t *testing.T) {
	t.Parallel()
	b := backend(t)
	if b.AllowedInProduction() {
		t.Error("the file backend must not be allowed for a production environment")
	}
	if b.Name() != "file" {
		t.Errorf("Name = %q", b.Name())
	}
}

// Verifies: SEC-120.
func TestKeyReferencesCannotEscape(t *testing.T) {
	t.Parallel()
	b := backend(t)
	for _, ref := range []string{"../escape", "a/b", "", "Targets", strings.Repeat("x", 100)} {
		if _, _, err := b.Sign(context.Background(), ref, nil); err == nil {
			t.Errorf("Sign with reference %q was accepted", ref)
		}
	}
	if _, err := signing.NewFile(""); err == nil {
		t.Error("an empty directory was accepted")
	}
}

// bindA is the binding of the secrets these tests seal.
var bindA = []byte("environment-secret:a:KEY")

// Verifies: SEC-106.
func TestSecretsAreSealedWithEnvelopeEncryption(t *testing.T) {
	t.Parallel()
	b := backend(t)
	ctx := context.Background()
	value := []byte("demo-value-abcdef0123456789")
	sealed, err := signing.Seal(ctx, b, value, bindA)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed.Ciphertext, value) || bytes.Contains(sealed.WrappedKey, value) {
		t.Error("the secret appears in what is stored")
	}
	if sealed.KeyID == "" {
		t.Error("the wrapping key was not identified")
	}
	if sealed.Hint != "…6789" {
		t.Errorf("hint = %q", sealed.Hint)
	}
	got, err := signing.Open(ctx, b, sealed, bindA)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	// Each secret gets its own data key, so two seals of one value
	// differ.
	other, err := signing.Seal(ctx, b, value, bindA)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed.Ciphertext, other.Ciphertext) {
		t.Error("two seals produced the same ciphertext")
	}
	// A short value gets no hint, so it cannot be guessed from it.
	short, err := signing.Seal(ctx, b, []byte("abc"), bindA)
	if err != nil {
		t.Fatal(err)
	}
	if short.Hint != "" {
		t.Errorf("a short secret was hinted at: %q", short.Hint)
	}
}

// Verifies: SEC-106.
func TestSealedSecretsAreRefusedElsewhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first, second := backend(t), backend(t)
	sealed, err := signing.Seal(ctx, first, []byte("value"), bindA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signing.Open(ctx, second, sealed, bindA); err == nil {
		t.Error("a secret sealed by one installation opened in another")
	}
	// A ciphertext moved to another row does not open there.
	if _, err := signing.Open(ctx, first, sealed, []byte("environment-secret:other:KEY")); err == nil {
		t.Error("a secret opened under another binding")
	}
	// Tampering with the ciphertext fails authentication.
	damaged := sealed
	damaged.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	damaged.Ciphertext[len(damaged.Ciphertext)-1] ^= 0xFF
	if _, err := signing.Open(ctx, first, damaged, bindA); err == nil {
		t.Error("a tampered secret was opened")
	}
	damaged = sealed
	damaged.Ciphertext = sealed.Ciphertext[:4]
	if _, err := signing.Open(ctx, first, damaged, bindA); err == nil {
		t.Error("a truncated secret was opened")
	}
	damaged = sealed
	damaged.WrappedKey = sealed.WrappedKey[:4]
	if _, err := signing.Open(ctx, first, damaged, bindA); err == nil {
		t.Error("a truncated wrapped key was opened")
	}
}

// Verifies: SEC-120.
func TestKeysArePrivateOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Sign(context.Background(), "targets", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := signing.Seal(context.Background(), b, []byte("x"), bindA); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"targets.ed25519", "master.key"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o; want 600", name, perm)
		}
	}
	// A file that is not a key is reported, not used.
	if err := os.WriteFile(filepath.Join(dir, "broken.ed25519"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Sign(context.Background(), "broken", nil); err == nil {
		t.Error("a malformed key file was used")
	}
}

// Verifies: SEC-106.
func TestSealedValuesSurviveOneColumn(t *testing.T) {
	t.Parallel()
	s := signing.Sealed{KeyID: "file:abcd", WrappedKey: []byte{1, 2, 3}, Ciphertext: []byte("ciphertext")}
	got, err := signing.DecodeSealed(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != s.KeyID || !bytes.Equal(got.WrappedKey, s.WrappedKey) || !bytes.Equal(got.Ciphertext, s.Ciphertext) {
		t.Errorf("round trip = %+v", got)
	}
	encoded := s.Encode()
	for _, bad := range [][]byte{nil, {2, 0, 0, 0, 0, 0, 0, 0, 0}, encoded[:6], encoded[:14]} {
		if _, err := signing.DecodeSealed(bad); err == nil {
			t.Errorf("DecodeSealed(%x) was accepted", bad)
		}
	}
}

// Verifies: SEC-120.
// The narrowed crypter wraps and unwraps and can do nothing else.
func TestCrypterOnlyCannotSign(t *testing.T) {
	t.Parallel()
	c := signing.CrypterOnly(backend(t))
	if _, ok := c.(signing.Signer); ok {
		t.Fatal("the narrowed crypter is a Signer")
	}
	sealed, err := signing.Seal(context.Background(), c, []byte("value"), bindA)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := signing.Open(context.Background(), c, sealed, bindA); err != nil || string(got) != "value" {
		t.Errorf("Open = %q, %v", got, err)
	}
}
