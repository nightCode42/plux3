// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// File keeps keys in a directory, for development and tests. It is
// refused for production environments (SEC-056).
//
// Each signing key is a file <ref>.ed25519 holding the 64-byte private
// key; the wrapping key is master.key, 32 bytes. Missing keys are
// created on first use, so a developer needs no ceremony.
type File struct {
	dir string

	mu    sync.Mutex
	cache map[string]ed25519.PrivateKey
	// master wraps data keys; it is read once.
	master []byte
}

// keyRef matches a key reference, so that one can never escape the
// directory.
var keyRef = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// NewFile returns a file backend rooted at dir, creating it if needed.
func NewFile(dir string) (*File, error) {
	if dir == "" {
		return nil, errors.New("signing: the file backend needs a directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("signing: create %s: %w", abs, err)
	}
	return &File{dir: abs, cache: map[string]ed25519.PrivateKey{}}, nil
}

// Name identifies the backend.
func (*File) Name() string { return "file" }

// AllowedInProduction is false: a production environment must use a
// hardware or cloud backend (SEC-056, SEC-120).
func (*File) AllowedInProduction() bool { return false }

// Sign signs a message with the named key.
func (f *File) Sign(_ context.Context, ref string, message []byte) ([]byte, string, error) {
	key, err := f.private(ref)
	if err != nil {
		return nil, "", err
	}
	return ed25519.Sign(key, message), KeyID(key.Public().(ed25519.PublicKey)), nil
}

// PublicKey returns the public half of a named key.
func (f *File) PublicKey(_ context.Context, ref string) (ed25519.PublicKey, string, error) {
	key, err := f.private(ref)
	if err != nil {
		return nil, "", err
	}
	pub, _ := key.Public().(ed25519.PublicKey)
	return pub, KeyID(pub), nil
}

// KeyID is the stable identifier of a public key: the first sixteen
// bytes of its SHA-256, in hexadecimal. It changes when the key does,
// so a stored signature always names the key that made it.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:16])
}

// private returns the named key, generating it on first use.
func (f *File) private(ref string) (ed25519.PrivateKey, error) {
	if !keyRef.MatchString(ref) {
		return nil, fmt.Errorf("signing: %q is not a valid key reference", ref)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if key, ok := f.cache[ref]; ok {
		return key, nil
	}
	path := filepath.Join(f.dir, ref+".ed25519")
	data, err := os.ReadFile(path) //nolint:gosec // ref is checked against keyRef
	switch {
	case err == nil:
		if len(data) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("signing: %s is not an Ed25519 private key", ref)
		}
	case errors.Is(err, fs.ErrNotExist):
		_, generated, genErr := ed25519.GenerateKey(rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("signing: generate %s: %w", ref, genErr)
		}
		if writeErr := os.WriteFile(path, generated, 0o600); writeErr != nil {
			return nil, fmt.Errorf("signing: write %s: %w", ref, writeErr)
		}
		data = generated
	default:
		return nil, fmt.Errorf("signing: read %s: %w", ref, err)
	}
	key := ed25519.PrivateKey(data)
	f.cache[ref] = key
	return key, nil
}

// masterKey returns the wrapping key, generating it on first use.
func (f *File) masterKey() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.master != nil {
		return f.master, nil
	}
	path := filepath.Join(f.dir, "master.key")
	data, err := os.ReadFile(path) //nolint:gosec // a fixed name under the configured directory
	switch {
	case err == nil:
		if len(data) != 32 {
			return nil, errors.New("signing: master.key must be 32 bytes")
		}
	case errors.Is(err, fs.ErrNotExist):
		data = make([]byte, 32)
		if _, readErr := rand.Read(data); readErr != nil {
			return nil, fmt.Errorf("signing: generate the master key: %w", readErr)
		}
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			return nil, fmt.Errorf("signing: write the master key: %w", writeErr)
		}
	default:
		return nil, fmt.Errorf("signing: read the master key: %w", err)
	}
	f.master = data
	return data, nil
}

// Wrap encrypts a data key with AES-256-GCM under the master key.
func (f *File) Wrap(_ context.Context, dataKey []byte) ([]byte, string, error) {
	master, err := f.masterKey()
	if err != nil {
		return nil, "", err
	}
	aead, err := newAEAD(master)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("signing: %w", err)
	}
	return aead.Seal(nonce, nonce, dataKey, nil), masterKeyID(master), nil
}

// Unwrap decrypts a data key. A wrapped key from another master key
// fails authentication rather than returning nonsense.
func (f *File) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	master, err := f.masterKey()
	if err != nil {
		return nil, err
	}
	if id := masterKeyID(master); keyID != "" && keyID != id {
		return nil, fmt.Errorf("signing: the secret was wrapped with key %s, this backend holds %s", keyID, id)
	}
	aead, err := newAEAD(master)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < aead.NonceSize() {
		return nil, errors.New("signing: the wrapped key is truncated")
	}
	nonce, body := wrapped[:aead.NonceSize()], wrapped[aead.NonceSize():]
	out, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("signing: unwrap: %w", err)
	}
	return out, nil
}

// newAEAD builds AES-256-GCM for a 32-byte key.
func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	return aead, nil
}

// masterKeyID identifies the wrapping key without revealing it.
func masterKeyID(master []byte) string {
	sum := sha256.Sum256(master)
	return "file:" + hex.EncodeToString(sum[:8])
}
