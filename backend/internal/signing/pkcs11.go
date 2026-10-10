// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
)

// Sizes of the wrapped data key: the 96-bit IV before the ciphertext and
// the 128-bit tag after it.
const (
	pkcs11IVSize  = 12
	pkcs11TagSize = 16
)

// pkcs11Timeout is the default time allowed for one helper call.
const pkcs11Timeout = 15 * time.Second

// PKCS11 reaches a hardware security module through the PKCS#11 helper
// (SEC-120, ADR-0060). The helper is a separate process that loads the
// vendor module and holds the token session; this backend is pure Go,
// speaks the pkcs11wire protocol over the helper's Unix socket, and
// never sees a PIN or a private key.
//
// Keys are provisioned on the token by the operator and found by label:
// the release key of a reference is the key labelled with it, and the
// device access token keys are labelled "<prefix>-production" and
// "<prefix>-development". Nothing is created on first use, so a missing
// key is ErrNoKey. A signing key may be Ed25519 (release signing) or
// ECDSA P-256 (device access tokens); the helper refuses a key of the
// other kind. Data keys (SEC-106) are wrapped by an AES key on the token,
// labelled "plux-secrets" unless configured, with AES-GCM: the wrapped
// key is the 96-bit IV, the ciphertext and the 128-bit tag.
//
// The helper is trusted to hold the key, not to sign correctly: every
// signature is verified against the public key before it is returned.
type PKCS11 struct {
	socket   string
	tokenKey string
	wrapKey  string
	timeout  time.Duration

	mu sync.Mutex
	// public caches each key's public half by label. A key replaced on
	// the token shows as a signature that does not verify, which clears
	// the entry.
	public map[string]pkcs11Public
}

// pkcs11Public is a public key read from the helper.
type pkcs11Public struct {
	ed    ed25519.PublicKey
	ec    *ecdsa.PublicKey
	keyID string
}

// PKCS11Options configures NewPKCS11.
type PKCS11Options struct {
	// Socket is the path of the helper's Unix socket.
	Socket string
	// TokenKey is the prefix of the labels of the ECDSA P-256 keys that
	// sign device access tokens, one per environment class; "" uses
	// "plux-tokens".
	TokenKey string
	// WrapKey is the label of the AES key that wraps data keys (SEC-106);
	// "" uses "plux-secrets".
	WrapKey string
	// Timeout bounds one call to the helper; 0 uses 15 seconds.
	Timeout time.Duration
}

// NewPKCS11 returns a PKCS#11 backend. It does not connect: the helper
// may start after the server, and Ping reports whether it answers.
func NewPKCS11(opts PKCS11Options) (*PKCS11, error) {
	if opts.Socket == "" {
		return nil, errors.New("signing: the pkcs11 backend needs the helper's socket path")
	}
	prefix := opts.TokenKey
	if prefix == "" {
		prefix = "plux-tokens"
	}
	if !keyRef.MatchString(prefix + "-development") {
		return nil, fmt.Errorf("signing: %q is not a valid key reference", prefix)
	}
	wrap := opts.WrapKey
	if wrap == "" {
		wrap = "plux-secrets"
	}
	if !keyRef.MatchString(wrap) {
		return nil, fmt.Errorf("signing: %q is not a valid key reference", wrap)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = pkcs11Timeout
	}
	return &PKCS11{socket: opts.Socket, tokenKey: prefix, wrapKey: wrap, timeout: timeout, public: map[string]pkcs11Public{}}, nil
}

// Name identifies the backend.
func (*PKCS11) Name() string { return "pkcs11" }

// AllowedInProduction is true: the keys live on the token, not on disk.
func (*PKCS11) AllowedInProduction() bool { return true }

// Ping reports whether the helper accepts connections, for readiness
// (SRV-007). It connects and closes, which asks the token for nothing.
func (p *PKCS11) Ping(ctx context.Context) error {
	conn, err := p.dial(ctx)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// Wrap encrypts a data key with the wrapping key on the token. The key
// identifier names the key, so that Unwrap refuses a secret wrapped
// under another.
func (p *PKCS11) Wrap(ctx context.Context, dataKey []byte) ([]byte, string, error) {
	resp, err := p.call(ctx, &pkcs11pb.Request{Request: &pkcs11pb.Request_Wrap{
		Wrap: &pkcs11pb.WrapRequest{KeyRef: pkcs11URI(p.wrapKey), Plaintext: dataKey},
	}})
	if err != nil {
		return nil, "", err
	}
	reply, ok := resp.GetResponse().(*pkcs11pb.Response_Wrap)
	if !ok {
		return nil, "", errors.New("signing: the helper answered a wrap request with another kind of response")
	}
	if len(reply.Wrap.GetWrapped()) != pkcs11IVSize+len(dataKey)+pkcs11TagSize {
		return nil, "", errors.New("signing: the helper returned a wrapped key of the wrong length")
	}
	return reply.Wrap.GetWrapped(), p.wrapKeyID(), nil
}

// Unwrap decrypts a data key that Wrap produced. A wrapped key that was
// altered, or wrapped under another key, fails.
func (p *PKCS11) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != p.wrapKeyID() {
		return nil, fmt.Errorf("signing: the secret was wrapped with key %s, this backend uses %s", keyID, p.wrapKeyID())
	}
	resp, err := p.call(ctx, &pkcs11pb.Request{Request: &pkcs11pb.Request_Unwrap{
		Unwrap: &pkcs11pb.UnwrapRequest{KeyRef: pkcs11URI(p.wrapKey), Wrapped: wrapped},
	}})
	if err != nil {
		return nil, err
	}
	reply, ok := resp.GetResponse().(*pkcs11pb.Response_Unwrap)
	if !ok {
		return nil, errors.New("signing: the helper answered an unwrap request with another kind of response")
	}
	if len(reply.Unwrap.GetPlaintext()) != len(wrapped)-pkcs11IVSize-pkcs11TagSize {
		return nil, errors.New("signing: the helper returned a data key of the wrong length")
	}
	return reply.Unwrap.GetPlaintext(), nil
}

// wrapKeyID is the identifier Wrap stores with a secret.
func (p *PKCS11) wrapKeyID() string { return "pkcs11:" + p.wrapKey }

// Sign signs a message with the Ed25519 key labelled ref.
func (p *PKCS11) Sign(ctx context.Context, ref string, message []byte) ([]byte, string, error) {
	pub, err := p.edKey(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	sig, err := p.sign(ctx, ref, pkcs11pb.Algorithm_ALGORITHM_ED25519, message)
	if err != nil {
		return nil, "", err
	}
	if !ed25519.Verify(pub.ed, message, sig) {
		p.forget(ref)
		return nil, "", fmt.Errorf("signing: the helper's signature under %s does not verify", ref)
	}
	return sig, pub.keyID, nil
}

// PublicKey returns the public half of the Ed25519 key labelled ref.
func (p *PKCS11) PublicKey(ctx context.Context, ref string) (ed25519.PublicKey, string, error) {
	pub, err := p.edKey(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	return pub.ed, pub.keyID, nil
}

// SignToken signs a token's signing input with the class's ECDSA P-256
// key, and returns the signature in JOSE ES256 form.
func (p *PKCS11) SignToken(ctx context.Context, class TokenClass, signingInput []byte) ([]byte, string, error) {
	label, err := p.tokenLabel(class)
	if err != nil {
		return nil, "", err
	}
	pub, err := p.ecKey(ctx, label)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(signingInput)
	sig, err := p.sign(ctx, label, pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, digest[:])
	if err != nil {
		return nil, "", err
	}
	r := new(big.Int).SetBytes(sig[:tokenKeySize])
	s := new(big.Int).SetBytes(sig[tokenKeySize:])
	if !ecdsa.Verify(pub.ec, digest[:], r, s) {
		p.forget(label)
		return nil, "", fmt.Errorf("signing: the helper's signature under %s does not verify", label)
	}
	return sig, pub.keyID, nil
}

// TokenKeys returns the token keys of both classes. A class whose key is
// not provisioned on the token is left out, as the file backend leaves
// out the production class; SignToken for it fails with ErrNoKey.
func (p *PKCS11) TokenKeys(ctx context.Context) ([]TokenKey, error) {
	var keys []TokenKey
	for _, class := range []TokenClass{TokenProduction, TokenDevelopment} {
		label, err := p.tokenLabel(class)
		if err != nil {
			return nil, err
		}
		pub, err := p.ecKey(ctx, label)
		if errors.Is(err, ErrNoKey) {
			continue
		}
		if err != nil {
			return nil, err
		}
		keys = append(keys, TokenKey{ID: pub.keyID, Class: class, Public: pub.ec})
	}
	return keys, nil
}

// tokenLabel is the label of a class's key.
func (p *PKCS11) tokenLabel(class TokenClass) (string, error) {
	if !class.valid() {
		return "", fmt.Errorf("signing: %q is not a token class", string(class))
	}
	return p.tokenKey + "-" + string(class), nil
}

// edKey returns the Ed25519 key labelled ref.
func (p *PKCS11) edKey(ctx context.Context, ref string) (pkcs11Public, error) {
	pub, err := p.publicOf(ctx, ref)
	if err != nil {
		return pkcs11Public{}, err
	}
	if pub.ed == nil {
		return pkcs11Public{}, fmt.Errorf("signing: the token key %s is not an Ed25519 key", ref)
	}
	return pub, nil
}

// ecKey returns the ECDSA P-256 key labelled ref.
func (p *PKCS11) ecKey(ctx context.Context, ref string) (pkcs11Public, error) {
	pub, err := p.publicOf(ctx, ref)
	if err != nil {
		return pkcs11Public{}, err
	}
	if pub.ec == nil {
		return pkcs11Public{}, fmt.Errorf("signing: the token key %s is not an ECDSA P-256 key", ref)
	}
	return pub, nil
}

// forget drops a cached public key.
func (p *PKCS11) forget(ref string) {
	p.mu.Lock()
	delete(p.public, ref)
	p.mu.Unlock()
}

// publicOf returns the public key labelled ref, reading it from the
// helper the first time and checking the identifier the helper reports
// against one computed here.
func (p *PKCS11) publicOf(ctx context.Context, ref string) (pkcs11Public, error) {
	if !keyRef.MatchString(ref) {
		return pkcs11Public{}, fmt.Errorf("signing: %q is not a valid key reference", ref)
	}
	p.mu.Lock()
	cached, ok := p.public[ref]
	p.mu.Unlock()
	if ok {
		return cached, nil
	}
	resp, err := p.call(ctx, &pkcs11pb.Request{Request: &pkcs11pb.Request_PublicKey{
		PublicKey: &pkcs11pb.PublicKeyRequest{KeyRef: pkcs11URI(ref)},
	}})
	if err != nil {
		return pkcs11Public{}, err
	}
	reply, ok := resp.GetResponse().(*pkcs11pb.Response_PublicKey)
	if !ok {
		return pkcs11Public{}, errors.New("signing: the helper answered a public key request with another kind of response")
	}
	pub, err := parsePKCS11Public(reply.PublicKey)
	if err != nil {
		return pkcs11Public{}, fmt.Errorf("signing: the helper's key %s: %w", ref, err)
	}
	p.mu.Lock()
	p.public[ref] = pub
	p.mu.Unlock()
	return pub, nil
}

// parsePKCS11Public decodes a public key response and recomputes its
// identifier.
func parsePKCS11Public(r *pkcs11pb.PublicKeyResponse) (pkcs11Public, error) {
	parsed, err := x509.ParsePKIXPublicKey(r.GetPublicKeyDer())
	if err != nil {
		return pkcs11Public{}, errors.New("the public key is not PKIX")
	}
	var pub pkcs11Public
	switch key := parsed.(type) {
	case ed25519.PublicKey:
		if r.GetAlgorithm() != pkcs11pb.Algorithm_ALGORITHM_ED25519 {
			return pkcs11Public{}, errors.New("the algorithm does not match the key")
		}
		pub = pkcs11Public{ed: key, keyID: KeyID(key)}
	case *ecdsa.PublicKey:
		if r.GetAlgorithm() != pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256 || key.Curve != elliptic.P256() {
			return pkcs11Public{}, errors.New("the key is not an ECDSA P-256 key")
		}
		id, err := TokenKeyID(key)
		if err != nil {
			return pkcs11Public{}, err
		}
		pub = pkcs11Public{ec: key, keyID: id}
	default:
		return pkcs11Public{}, errors.New("the key is neither Ed25519 nor ECDSA P-256")
	}
	if r.GetKeyId() != pub.keyID {
		return pkcs11Public{}, errors.New("the key identifier does not match the key")
	}
	return pub, nil
}

// sign asks the helper for a signature of a digest and checks its length
// and the identifier of the key that made it.
func (p *PKCS11) sign(ctx context.Context, ref string, alg pkcs11pb.Algorithm, digest []byte) ([]byte, error) {
	resp, err := p.call(ctx, &pkcs11pb.Request{Request: &pkcs11pb.Request_Sign{
		Sign: &pkcs11pb.SignRequest{KeyRef: pkcs11URI(ref), Digest: digest, Algorithm: alg},
	}})
	if err != nil {
		return nil, err
	}
	reply, ok := resp.GetResponse().(*pkcs11pb.Response_Sign)
	if !ok {
		return nil, errors.New("signing: the helper answered a sign request with another kind of response")
	}
	if len(reply.Sign.GetSignature()) != TokenSignatureSize {
		return nil, fmt.Errorf("signing: the helper returned a %d-byte signature for %s", len(reply.Sign.GetSignature()), ref)
	}
	p.mu.Lock()
	known, cached := p.public[ref]
	p.mu.Unlock()
	if cached && reply.Sign.GetKeyId() != known.keyID {
		p.forget(ref)
		return nil, fmt.Errorf("signing: the helper signed %s with a key other than the one it described", ref)
	}
	return reply.Sign.GetSignature(), nil
}

// pkcs11URI is the helper's key reference for a label. A reference that
// passed keyRef holds only characters a URI carries unencoded.
func pkcs11URI(label string) string { return "pkcs11:object=" + label }

// dial connects to the helper's socket.
func (p *PKCS11) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: p.timeout}
	conn, err := d.DialContext(ctx, "unix", p.socket)
	if err != nil {
		return nil, fmt.Errorf("signing: reach the pkcs11 helper: %w", err)
	}
	return conn, nil
}

// call sends one request on its own connection and returns the answer,
// or the error the helper reported.
func (p *PKCS11) call(ctx context.Context, req *pkcs11pb.Request) (*pkcs11pb.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := p.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("signing: the pkcs11 helper connection: %w", err)
		}
	}
	// A cancelled context interrupts a blocked read or write.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := pkcs11wire.WriteMessage(conn, req); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	var resp pkcs11pb.Response
	if err := pkcs11wire.ReadMessage(conn, &resp); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	if failed, ok := resp.GetResponse().(*pkcs11pb.Response_Error); ok {
		return nil, pkcs11Error(failed.Error)
	}
	return &resp, nil
}

// pkcs11Error maps the helper's error to the package's errors: a key
// that does not exist is ErrNoKey.
func pkcs11Error(e *pkcs11pb.Error) error {
	if e.GetCode() == pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND {
		return ErrNoKey
	}
	return fmt.Errorf("signing: the pkcs11 helper refused (%s): %s", e.GetCode(), e.GetMessage())
}
