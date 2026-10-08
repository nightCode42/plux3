// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/asn1"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
)

// fakeKey is a key on the fake token: Ed25519 or P-256, or two keys the
// selector cannot tell apart.
type fakeKey struct {
	ed        ed25519.PrivateKey
	ec        *ecdsa.PrivateKey
	ambiguous bool
	// bare returns the public point without its OCTET STRING wrapper.
	bare bool
}

// fakeToken is a Token over software keys, selected by label.
type fakeToken struct {
	mu   sync.Mutex
	keys map[string]fakeKey
	// signErr, when set, fails every signature.
	signErr error
	// shortSignature makes Sign return a truncated signature.
	shortSignature bool
	// gate, when set, makes every call wait until it is closed.
	gate chan struct{}
}

// newFakeToken returns a token holding an Ed25519 key "release" and a
// P-256 key "token".
func newFakeToken(t *testing.T) *fakeToken {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeToken{keys: map[string]fakeKey{
		"release": {ed: priv}, "token": {ec: ec}, "twin": {ed: priv, ambiguous: true},
	}}
}

func (f *fakeToken) lookup(sel Selector) (fakeKey, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.keys[sel.Label]
	switch {
	case !ok:
		return fakeKey{}, ErrKeyNotFound
	case key.ambiguous:
		return fakeKey{}, ErrAmbiguousKey
	}
	return key, nil
}

func (f *fakeToken) PublicKey(sel Selector) (PublicAttributes, error) {
	key, err := f.lookup(sel)
	if err != nil {
		return PublicAttributes{}, err
	}
	params, point := paramsP256, []byte(nil)
	if key.ed != nil {
		params, point = paramsEd25519OID, key.ed.Public().(ed25519.PublicKey)
	} else {
		point, _ = key.ec.PublicKey.Bytes()
	}
	if !key.bare {
		point, _ = asn1.Marshal(point)
	}
	return PublicAttributes{ECParams: params, ECPoint: point}, nil
}

func (f *fakeToken) Sign(sel Selector, alg pkcs11pb.Algorithm, data []byte) ([]byte, error) {
	key, err := f.lookup(sel)
	if err != nil {
		return nil, err
	}
	if f.signErr != nil {
		return nil, f.signErr
	}
	var sig []byte
	switch alg {
	case pkcs11pb.Algorithm_ALGORITHM_ED25519:
		sig = ed25519.Sign(key.ed, data)
	case pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256:
		r, s, err := ecdsa.Sign(rand.Reader, key.ec, data)
		if err != nil {
			return nil, err
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	case pkcs11pb.Algorithm_ALGORITHM_UNSPECIFIED:
	}
	if f.shortSignature {
		return sig[:10], nil
	}
	return sig, nil
}

// serveFake starts a server on a socket in a short temporary directory
// and returns the socket path.
func serveFake(t *testing.T, token Token, opts Options) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "p11")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	l, err := Listen(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer(token, opts).Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path
}

// roundTrip sends raw bytes on a new connection, half-closes it and reads
// the one response.
func roundTrip(t *testing.T, path string, raw []byte) *pkcs11pb.Response {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var resp pkcs11pb.Response
	if err := pkcs11wire.ReadMessage(conn, &resp); err != nil {
		t.Fatalf("read the response: %v", err)
	}
	return &resp
}

// signRequest builds a sign request.
func signRequest(ref string, alg pkcs11pb.Algorithm, digest []byte) *pkcs11pb.Request {
	return &pkcs11pb.Request{Request: &pkcs11pb.Request_Sign{Sign: &pkcs11pb.SignRequest{KeyRef: ref, Digest: digest, Algorithm: alg}}}
}

// errorCode returns the code of an error response, or UNSPECIFIED.
func errorCode(resp *pkcs11pb.Response) pkcs11pb.ErrorCode {
	if e := resp.GetError(); e != nil {
		return e.GetCode()
	}
	return pkcs11pb.ErrorCode_ERROR_CODE_UNSPECIFIED
}

// isClosed reports whether err is the error of using a closed
// connection.
func isClosed(err error) bool { return errors.Is(err, net.ErrClosed) }
