// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Vault reaches HashiCorp Vault's Transit secrets engine over its HTTP
// API (SEC-120, ADR-0004). Keys never leave Vault: signing sends the
// message and receives a signature, and envelope encryption sends only
// a data key.
//
// Signing keys are Ed25519 Transit keys named by their reference; one
// that does not exist is created, not exportable, on first use, so each
// environment's key appears when the environment first signs. Data keys
// are wrapped with one AES-256-GCM Transit key. Device access tokens
// are signed by two ecdsa-p256 Transit keys, one per environment class,
// created the same way.
type Vault struct {
	address   string
	mount     string
	namespace string
	token     string
	wrapKey   string
	tokenKey  string
	client    *http.Client

	mu sync.Mutex
	// public caches each key's latest public half, by reference; a
	// public key never changes for a version, and a rotation shows as a
	// new version in the signature, which clears the entry.
	public map[string]vaultPublic
	// tokenPub caches each token key's public versions, by Transit key
	// name; a version Vault has not described yet triggers a new read.
	tokenPub map[string]vaultTokenKeys
}

// vaultPublic is a cached public key with its Transit version.
type vaultPublic struct {
	key     ed25519.PublicKey
	version int
}

// VaultOptions configures NewVault.
type VaultOptions struct {
	// Address is Vault's base URL, such as "https://vault:8200".
	Address string
	// Token authenticates to Vault; it needs the Transit policy for the
	// mount and nothing else.
	Token string
	// Mount is the Transit engine's mount path; "" uses "transit".
	Mount string
	// Namespace is the Vault Enterprise namespace, or "".
	Namespace string
	// WrapKey names the AES-256-GCM Transit key that wraps data keys;
	// "" uses "plux-secrets".
	WrapKey string
	// TokenKey is the prefix of the ecdsa-p256 Transit keys that sign
	// device access tokens, one per environment class, such as
	// "plux-tokens-production"; "" uses "plux-tokens".
	TokenKey string
	// Client is the HTTP client; nil uses one with a 15-second timeout.
	// Vault is operator configuration on a private network, so the
	// SSRF-safe client, which refuses private addresses, is not used.
	Client *http.Client
}

// NewVault returns a Vault Transit backend.
func NewVault(opts VaultOptions) (*Vault, error) {
	u, err := url.Parse(opts.Address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("signing: the Vault address %q is not an http(s) URL", opts.Address)
	}
	if opts.Token == "" {
		return nil, errors.New("signing: the Vault backend needs a token")
	}
	mount := strings.Trim(opts.Mount, "/")
	if mount == "" {
		mount = "transit"
	}
	wrap := opts.WrapKey
	if wrap == "" {
		wrap = "plux-secrets"
	}
	if !keyRef.MatchString(wrap) {
		return nil, fmt.Errorf("signing: %q is not a valid key reference", wrap)
	}
	tokenKey := opts.TokenKey
	if tokenKey == "" {
		tokenKey = "plux-tokens"
	}
	if !keyRef.MatchString(tokenKey + "-development") {
		return nil, fmt.Errorf("signing: %q is not a valid key reference", tokenKey)
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Vault{
		address: strings.TrimSuffix(opts.Address, "/"), mount: mount,
		namespace: opts.Namespace, token: opts.Token, wrapKey: wrap,
		tokenKey: tokenKey, client: client, public: map[string]vaultPublic{},
		tokenPub: map[string]vaultTokenKeys{},
	}, nil
}

// Name identifies the backend.
func (*Vault) Name() string { return "vault" }

// AllowedInProduction is true: the keys live in Vault, not on disk.
func (*Vault) AllowedInProduction() bool { return true }

// Ping reports whether Vault answers and accepts the token, for
// readiness (SRV-007). It reads the wrapping key's description, which
// Wrap already needs and which holds no key material; a key not created
// yet is a healthy answer, since the first Wrap creates it.
func (v *Vault) Ping(ctx context.Context) error {
	var info vaultKey
	err := v.call(ctx, http.MethodGet, "keys/"+v.wrapKey, nil, &info)
	var status statusError
	if err == nil || (errors.As(err, &status) && status.code == http.StatusNotFound) {
		return nil
	}
	return fmt.Errorf("signing: Vault: %w", err)
}

// Sign signs a message with the named Ed25519 key.
func (v *Vault) Sign(ctx context.Context, ref string, message []byte) ([]byte, string, error) {
	if err := v.ensure(ctx, ref, "ed25519"); err != nil {
		return nil, "", err
	}
	var out struct {
		Data struct {
			Signature string `json:"signature"`
		} `json:"data"`
	}
	body := map[string]any{"input": base64.StdEncoding.EncodeToString(message)}
	if err := v.call(ctx, http.MethodPost, "sign/"+ref, body, &out); err != nil {
		return nil, "", err
	}
	version, sig, err := parseVaultCiphertext(out.Data.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, "", fmt.Errorf("signing: Vault returned a %d-byte signature for %s", len(sig), ref)
	}
	pub, err := v.publicKey(ctx, ref, version)
	if err != nil {
		return nil, "", err
	}
	// Vault is trusted to hold the key, not to sign correctly: a
	// signature that does not verify is never handed out.
	if !ed25519.Verify(pub, message, sig) {
		return nil, "", fmt.Errorf("signing: Vault's signature under %s does not verify", ref)
	}
	return sig, KeyID(pub), nil
}

// PublicKey returns the public half of the named key's latest version.
func (v *Vault) PublicKey(ctx context.Context, ref string) (ed25519.PublicKey, string, error) {
	if err := v.ensure(ctx, ref, "ed25519"); err != nil {
		return nil, "", err
	}
	pub, err := v.publicKey(ctx, ref, 0)
	if err != nil {
		return nil, "", err
	}
	return pub, KeyID(pub), nil
}

// Wrap encrypts a data key with the wrapping key.
func (v *Vault) Wrap(ctx context.Context, dataKey []byte) ([]byte, string, error) {
	if err := v.ensure(ctx, v.wrapKey, "aes256-gcm96"); err != nil {
		return nil, "", err
	}
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	body := map[string]any{"plaintext": base64.StdEncoding.EncodeToString(dataKey)}
	if err := v.call(ctx, http.MethodPost, "encrypt/"+v.wrapKey, body, &out); err != nil {
		return nil, "", err
	}
	if _, _, err := parseVaultCiphertext(out.Data.Ciphertext); err != nil {
		return nil, "", err
	}
	return []byte(out.Data.Ciphertext), "vault:" + v.wrapKey, nil
}

// Unwrap decrypts a data key that Wrap produced. Vault keeps every
// version of the wrapping key it has not been told to retire, so a
// rotation does not strand older secrets.
func (v *Vault) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != "vault:"+v.wrapKey {
		return nil, fmt.Errorf("signing: the secret was wrapped with key %s, this backend uses vault:%s", keyID, v.wrapKey)
	}
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	body := map[string]any{"ciphertext": string(wrapped)}
	if err := v.call(ctx, http.MethodPost, "decrypt/"+v.wrapKey, body, &out); err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(out.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("signing: Vault returned a data key that is not base64: %w", err)
	}
	return key, nil
}

// ensure creates a Transit key of a type when it does not exist yet.
// Creating one that exists is a no-op in Vault, so the call is safe to
// race; the cache avoids it after the first success.
func (v *Vault) ensure(ctx context.Context, ref, kind string) error {
	if !keyRef.MatchString(ref) {
		return fmt.Errorf("signing: %q is not a valid key reference", ref)
	}
	v.mu.Lock()
	_, known := v.public[ref]
	v.mu.Unlock()
	if known {
		return nil
	}
	var info vaultKey
	err := v.call(ctx, http.MethodGet, "keys/"+ref, nil, &info)
	var status statusError
	switch {
	case err == nil:
	case errors.As(err, &status) && status.code == http.StatusNotFound:
		create := map[string]any{"type": kind, "exportable": false, "allow_plaintext_backup": false}
		if err := v.call(ctx, http.MethodPost, "keys/"+ref, create, nil); err != nil {
			return err
		}
		if err := v.call(ctx, http.MethodGet, "keys/"+ref, nil, &info); err != nil {
			return err
		}
	default:
		return err
	}
	if info.Data.Type != kind {
		return fmt.Errorf("signing: Vault key %s is %s, not %s", ref, info.Data.Type, kind)
	}
	if info.Data.Exportable {
		return fmt.Errorf("signing: Vault key %s is exportable; only a key that cannot leave Vault may sign", ref)
	}
	if kind != "ed25519" {
		v.mu.Lock()
		v.public[ref] = vaultPublic{}
		v.mu.Unlock()
		return nil
	}
	return v.remember(ref, info)
}

// vaultKey is the part of Transit's key description this backend reads.
type vaultKey struct {
	Data struct {
		Type          string `json:"type"`
		Exportable    bool   `json:"exportable"`
		LatestVersion int    `json:"latest_version"`
		// Keys maps each version to its public key for an asymmetric key,
		// and to its creation time for a symmetric one.
		Keys map[string]json.RawMessage `json:"keys"`
	} `json:"data"`
}

// remember caches the latest public key of a described key.
func (v *Vault) remember(ref string, info vaultKey) error {
	latest := info.Data.LatestVersion
	described, ok := info.Data.Keys[strconv.Itoa(latest)]
	if !ok {
		return fmt.Errorf("signing: Vault describes no version %d of %s", latest, ref)
	}
	var entry struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(described, &entry); err != nil {
		return fmt.Errorf("signing: Vault's description of %s has no public key", ref)
	}
	raw, err := base64.StdEncoding.DecodeString(entry.PublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("signing: Vault's public key of %s is not an Ed25519 key", ref)
	}
	v.mu.Lock()
	v.public[ref] = vaultPublic{key: ed25519.PublicKey(raw), version: latest}
	v.mu.Unlock()
	return nil
}

// publicKey returns the public key of a version; zero is the latest
// known. A version newer than the cached one is fetched.
func (v *Vault) publicKey(ctx context.Context, ref string, version int) (ed25519.PublicKey, error) {
	v.mu.Lock()
	cached := v.public[ref]
	v.mu.Unlock()
	if cached.key != nil && (version == 0 || version == cached.version) {
		return cached.key, nil
	}
	var info vaultKey
	if err := v.call(ctx, http.MethodGet, "keys/"+ref, nil, &info); err != nil {
		return nil, err
	}
	if err := v.remember(ref, info); err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if version != 0 && v.public[ref].version != version {
		return nil, fmt.Errorf("signing: Vault signed %s with version %d, but its latest is %d", ref, version, v.public[ref].version)
	}
	return v.public[ref].key, nil
}

// statusError is a refusal from Vault.
type statusError struct {
	code int
	path string
}

// Error names the status and the path, never the token or the body,
// which may echo what was sent.
func (e statusError) Error() string {
	return fmt.Sprintf("signing: Vault answered %d to %s", e.code, e.path)
}

// call sends one request to the Transit mount.
func (v *Vault) call(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("signing: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.address+"/v1/"+v.mount+"/"+path, reader)
	if err != nil {
		return fmt.Errorf("signing: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("X-Vault-Request", "true")
	if v.namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("signing: reach Vault: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return statusError{code: resp.StatusCode, path: path}
	}
	if into == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil {
		return fmt.Errorf("signing: Vault's answer to %s is not JSON: %w", path, err)
	}
	return nil
}

// parseVaultCiphertext splits Transit's "vault:v<N>:<base64>" form.
func parseVaultCiphertext(s string) (int, []byte, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] != "vault" || !strings.HasPrefix(parts[1], "v") {
		return 0, nil, errors.New("signing: Vault returned a value that is not in its vault:v<N>: form")
	}
	version, err := strconv.Atoi(parts[1][1:])
	if err != nil || version < 1 {
		return 0, nil, errors.New("signing: Vault returned a value with no key version")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, nil, errors.New("signing: Vault returned a value that is not base64")
	}
	return version, raw, nil
}
