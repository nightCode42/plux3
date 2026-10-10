// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// tokenKeySize is the length of a P-256 private scalar.
const tokenKeySize = 32

// SignToken signs a token's signing input with the development token
// key, creating the key on first use. The production class is refused,
// as the whole backend is (SEC-056).
func (f *File) SignToken(_ context.Context, class TokenClass, signingInput []byte) ([]byte, string, error) {
	key, err := f.tokenKey(class)
	if err != nil {
		return nil, "", err
	}
	id, err := TokenKeyID(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(signingInput)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return nil, "", fmt.Errorf("signing: sign a token: %w", err)
	}
	sig := make([]byte, TokenSignatureSize)
	r.FillBytes(sig[:tokenKeySize])
	s.FillBytes(sig[tokenKeySize:])
	return sig, id, nil
}

// TokenKeys returns the public key of the development class. The file
// backend holds no production key, so none is listed.
func (f *File) TokenKeys(_ context.Context) ([]TokenKey, error) {
	key, err := f.tokenKey(TokenDevelopment)
	if err != nil {
		return nil, err
	}
	id, err := TokenKeyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return []TokenKey{{ID: id, Class: TokenDevelopment, Public: &key.PublicKey}}, nil
}

// tokenKey returns the key of a class, generating it on first use. It is
// stored as <dir>/token-<class>.p256, the 32-byte private scalar.
func (f *File) tokenKey(class TokenClass) (*ecdsa.PrivateKey, error) {
	if class == TokenProduction {
		return nil, ErrProductionToken
	}
	if !class.valid() {
		return nil, fmt.Errorf("signing: %q is not a token class", string(class))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if key, ok := f.tokens[class]; ok {
		return key, nil
	}
	path := filepath.Join(f.dir, "token-"+string(class)+".p256")
	data, err := os.ReadFile(path) //nolint:gosec // the class is checked above
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		generated, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("signing: generate the %s token key: %w", class, genErr)
		}
		raw, rawErr := generated.Bytes()
		if rawErr != nil {
			return nil, fmt.Errorf("signing: encode the %s token key: %w", class, rawErr)
		}
		if writeErr := os.WriteFile(path, raw, 0o600); writeErr != nil {
			return nil, fmt.Errorf("signing: write the %s token key: %w", class, writeErr)
		}
		f.tokens[class] = generated
		return generated, nil
	default:
		return nil, fmt.Errorf("signing: read the %s token key: %w", class, err)
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), data)
	if err != nil {
		return nil, fmt.Errorf("signing: %s token key is not a P-256 private key", class)
	}
	f.tokens[class] = key
	return key, nil
}
