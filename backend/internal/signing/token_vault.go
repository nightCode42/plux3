// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// vaultTokenKind is the Transit key type of a token key.
const vaultTokenKind = "ecdsa-p256"

// vaultTokenKeys is a token key's public versions as last read.
type vaultTokenKeys struct {
	versions map[int]*ecdsa.PublicKey
	latest   int
}

// tokenKeyName is the Transit key of a class: the token key prefix and
// the class, such as "plux-tokens-production".
func (v *Vault) tokenKeyName(class TokenClass) (string, error) {
	if !class.valid() {
		return "", fmt.Errorf("signing: %q is not a token class", string(class))
	}
	return v.tokenKey + "-" + string(class), nil
}

// SignToken signs a token's signing input with the class's Transit key,
// which is created, not exportable, on first use. Transit hashes the
// input with SHA-256 and answers in JWS form, r followed by s.
func (v *Vault) SignToken(ctx context.Context, class TokenClass, signingInput []byte) ([]byte, string, error) {
	name, err := v.tokenKeyName(class)
	if err != nil {
		return nil, "", err
	}
	if err := v.ensure(ctx, name, vaultTokenKind); err != nil {
		return nil, "", err
	}
	var out struct {
		Data struct {
			Signature string `json:"signature"`
		} `json:"data"`
	}
	body := map[string]any{
		"input":                base64.StdEncoding.EncodeToString(signingInput),
		"hash_algorithm":       "sha2-256",
		"marshaling_algorithm": "jws", //nolint:misspell // Vault Transit's parameter name
	}
	if err := v.call(ctx, http.MethodPost, "sign/"+name, body, &out); err != nil {
		return nil, "", err
	}
	version, sig, err := parseVaultJWSSignature(out.Data.Signature)
	if err != nil {
		return nil, "", err
	}
	pub, err := v.tokenPublic(ctx, name, version)
	if err != nil {
		return nil, "", err
	}
	// Vault is trusted to hold the key, not to sign correctly: a
	// signature that does not verify is never handed out.
	digest := sha256.Sum256(signingInput)
	r := new(big.Int).SetBytes(sig[:tokenKeySize])
	s := new(big.Int).SetBytes(sig[tokenKeySize:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return nil, "", fmt.Errorf("signing: Vault's signature under %s does not verify", name)
	}
	id, err := TokenKeyID(pub)
	if err != nil {
		return nil, "", err
	}
	return sig, id, nil
}

// TokenKeys returns every version of both classes' keys, each class
// oldest first.
func (v *Vault) TokenKeys(ctx context.Context) ([]TokenKey, error) {
	var keys []TokenKey
	for _, class := range []TokenClass{TokenProduction, TokenDevelopment} {
		name, err := v.tokenKeyName(class)
		if err != nil {
			return nil, err
		}
		if err := v.ensure(ctx, name, vaultTokenKind); err != nil {
			return nil, err
		}
		described, err := v.describeToken(ctx, name)
		if err != nil {
			return nil, err
		}
		numbers := make([]int, 0, len(described.versions))
		for n := range described.versions {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		for _, n := range numbers {
			pub := described.versions[n]
			id, err := TokenKeyID(pub)
			if err != nil {
				return nil, err
			}
			keys = append(keys, TokenKey{ID: id, Class: class, Public: pub})
		}
	}
	return keys, nil
}

// tokenPublic returns the public key of a version of a token key,
// reading Vault again when the version is newer than the last read.
func (v *Vault) tokenPublic(ctx context.Context, name string, version int) (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	cached, ok := v.tokenPub[name]
	v.mu.Unlock()
	if ok {
		if pub, found := cached.versions[version]; found {
			return pub, nil
		}
	}
	described, err := v.describeToken(ctx, name)
	if err != nil {
		return nil, err
	}
	pub, found := described.versions[version]
	if !found {
		return nil, fmt.Errorf("signing: Vault signed %s with version %d, which it does not describe", name, version)
	}
	return pub, nil
}

// describeToken reads a token key's versions from Vault and remembers
// them.
func (v *Vault) describeToken(ctx context.Context, name string) (vaultTokenKeys, error) {
	var info vaultKey
	if err := v.call(ctx, http.MethodGet, "keys/"+name, nil, &info); err != nil {
		return vaultTokenKeys{}, err
	}
	if info.Data.Type != vaultTokenKind {
		return vaultTokenKeys{}, fmt.Errorf("signing: Vault key %s is %s, not %s", name, info.Data.Type, vaultTokenKind)
	}
	described := vaultTokenKeys{versions: map[int]*ecdsa.PublicKey{}, latest: info.Data.LatestVersion}
	for label, raw := range info.Data.Keys {
		n, err := strconv.Atoi(label)
		if err != nil || n < 1 {
			return vaultTokenKeys{}, fmt.Errorf("signing: Vault describes %s with a version %q", name, label)
		}
		pub, err := parseVaultECDSAKey(raw)
		if err != nil {
			return vaultTokenKeys{}, fmt.Errorf("signing: Vault's version %d of %s: %w", n, name, err)
		}
		described.versions[n] = pub
	}
	if _, ok := described.versions[described.latest]; !ok {
		return vaultTokenKeys{}, fmt.Errorf("signing: Vault describes no version %d of %s", described.latest, name)
	}
	v.mu.Lock()
	v.tokenPub[name] = described
	v.mu.Unlock()
	return described, nil
}

// parseVaultECDSAKey reads a version's PEM-encoded P-256 public key.
func parseVaultECDSAKey(raw json.RawMessage) (*ecdsa.PublicKey, error) {
	var entry struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil || entry.PublicKey == "" {
		return nil, errors.New("there is no public key")
	}
	block, _ := pem.Decode([]byte(entry.PublicKey))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("the public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("the public key is not PKIX")
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("the public key is not a P-256 key")
	}
	return pub, nil
}

// parseVaultJWSSignature splits Transit's "vault:v<N>:<base64url>" form
// for a JWS-marshalled signature and checks its length.
func parseVaultJWSSignature(s string) (int, []byte, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] != "vault" || !strings.HasPrefix(parts[1], "v") {
		return 0, nil, errors.New("signing: Vault returned a signature that is not in its vault:v<N>: form")
	}
	version, err := strconv.Atoi(parts[1][1:])
	if err != nil || version < 1 {
		return 0, nil, errors.New("signing: Vault returned a signature with no key version")
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[2], "="))
	if err != nil {
		return 0, nil, errors.New("signing: Vault returned a signature that is not base64url")
	}
	if len(sig) != TokenSignatureSize {
		return 0, nil, fmt.Errorf("signing: Vault returned a %d-byte token signature, want %d", len(sig), TokenSignatureSize)
	}
	return version, sig, nil
}
