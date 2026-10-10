// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// frameOf encodes a message as one frame.
func frameOf(t *testing.T, m proto.Message) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pkcs11wire.WriteMessage(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Verifies: SEC-120.
func TestServerSignsEd25519(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	path := serveFake(t, token, Options{})
	message := []byte("the targets metadata")
	resp := roundTrip(t, path, frameOf(t, signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, message)))
	got := resp.GetSign()
	if got == nil {
		t.Fatalf("response = %v", resp)
	}
	pub := token.keys["release"].ed.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, message, got.GetSignature()) {
		t.Error("the signature does not verify")
	}
	if got.GetKeyId() != signing.KeyID(pub) {
		t.Errorf("key ID = %q, want %q", got.GetKeyId(), signing.KeyID(pub))
	}
}

// Verifies: SEC-120.
func TestServerSignsECDSAAsRS(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	path := serveFake(t, token, Options{})
	digest := sha256.Sum256([]byte("header.payload"))
	resp := roundTrip(t, path, frameOf(t, signRequest("pkcs11:object=token", pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, digest[:])))
	sig := resp.GetSign().GetSignature()
	if len(sig) != 64 {
		t.Fatalf("response = %v", resp)
	}
	pub := &token.keys["token"].ec.PublicKey
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the signature does not verify")
	}
	if want, _ := signing.TokenKeyID(pub); resp.GetSign().GetKeyId() != want {
		t.Errorf("key ID = %q, want %q", resp.GetSign().GetKeyId(), want)
	}
}

// Verifies: SEC-120.
func TestServerReturnsPublicKeys(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	token.keys["bare"] = fakeKey{ec: token.keys["token"].ec, bare: true}
	path := serveFake(t, token, Options{})
	for ref, alg := range map[string]pkcs11pb.Algorithm{
		"release": pkcs11pb.Algorithm_ALGORITHM_ED25519,
		"token":   pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256,
		"bare":    pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256,
	} {
		req := &pkcs11pb.Request{Request: &pkcs11pb.Request_PublicKey{PublicKey: &pkcs11pb.PublicKeyRequest{KeyRef: "pkcs11:object=" + ref}}}
		got := roundTrip(t, path, frameOf(t, req)).GetPublicKey()
		if got == nil || got.GetAlgorithm() != alg || got.GetKeyId() == "" {
			t.Errorf("%s: response = %v", ref, got)
			continue
		}
		if _, err := x509.ParsePKIXPublicKey(got.GetPublicKeyDer()); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

// Verifies: SEC-120.
func TestServerRefusesBadRequests(t *testing.T) {
	t.Parallel()
	digest := make([]byte, 32)
	tests := []struct {
		name string
		req  *pkcs11pb.Request
		want pkcs11pb.ErrorCode
	}{
		{"unspecified algorithm", signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_UNSPECIFIED, digest), pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM},
		{"unknown algorithm", signRequest("pkcs11:object=release", 99, digest), pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM},
		{"ECDSA with an Ed25519 key", signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, digest), pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM},
		{"Ed25519 with a P-256 key", signRequest("pkcs11:object=token", pkcs11pb.Algorithm_ALGORITHM_ED25519, digest), pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM},
		{"short digest", signRequest("pkcs11:object=token", pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, digest[:31]), pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"long digest", signRequest("pkcs11:object=token", pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, append(digest, 0)), pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"bad key reference", signRequest("object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, digest), pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"no such key", signRequest("pkcs11:object=absent", pkcs11pb.Algorithm_ALGORITHM_ED25519, digest), pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND},
		{"ambiguous key", signRequest("pkcs11:object=twin", pkcs11pb.Algorithm_ALGORITHM_ED25519, digest), pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"no operation", &pkcs11pb.Request{}, pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"public key of nothing", &pkcs11pb.Request{Request: &pkcs11pb.Request_PublicKey{PublicKey: &pkcs11pb.PublicKeyRequest{KeyRef: "pkcs11:object=absent"}}}, pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND},
	}
	path := serveFake(t, newFakeToken(t), Options{})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errorCode(roundTrip(t, path, frameOf(t, tc.req))); got != tc.want {
				t.Errorf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

// Verifies: SEC-120.
func TestServerKeepsTokenDetailToItself(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	token.signErr = errors.New("CKR_PIN_INCORRECT for pin 1234")
	var logged bytes.Buffer
	srv := NewServer(token, Options{Log: slog.New(slog.NewTextHandler(&logged, nil))})
	resp := srv.Respond(context.Background(), signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, []byte("m")))
	if errorCode(resp) != pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("response = %v", resp)
	}
	if strings.Contains(resp.GetError().GetMessage(), "1234") || strings.Contains(resp.GetError().GetMessage(), "CKR") {
		t.Errorf("the client was told %q", resp.GetError().GetMessage())
	}
	if !strings.Contains(logged.String(), "CKR_PIN_INCORRECT") {
		t.Errorf("the failure was not logged: %q", logged.String())
	}
}

// Verifies: SEC-120.
func TestServerRefusesAMalformedSignature(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	token.shortSignature = true
	resp := NewServer(token, Options{}).Respond(context.Background(), signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, []byte("m")))
	if errorCode(resp) != pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Errorf("response = %v", resp)
	}
}

// Verifies: SEC-120.
func TestServerTimesOutAStuckToken(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	token.gate = make(chan struct{})
	t.Cleanup(func() { close(token.gate) })
	path := serveFake(t, token, Options{Timeout: 100 * time.Millisecond})
	start := time.Now()
	resp := roundTrip(t, path, frameOf(t, signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, []byte("m"))))
	if errorCode(resp) != pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Errorf("response = %v", resp)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("answered after %v", elapsed)
	}
}

// Verifies: SEC-120.
func TestServerFramingErrors(t *testing.T) {
	t.Parallel()
	valid := frameOf(t, signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, []byte("m")))
	// A response sent where a request belongs: its error variant is a
	// field the request has no meaning for.
	wrongVariant := frameOf(t, &pkcs11pb.Response{Response: &pkcs11pb.Response_Error{Error: &pkcs11pb.Error{Message: "x"}}})
	tests := map[string][]byte{
		"oversized":         binary.BigEndian.AppendUint32(nil, pkcs11wire.MaxMessageSize+1),
		"truncated length":  {0, 0},
		"truncated payload": valid[:len(valid)-2],
		"not protobuf":      append(binary.BigEndian.AppendUint32(nil, 3), 0xff, 0xff, 0xff),
		"wrong variant":     wrongVariant,
	}
	path := serveFake(t, newFakeToken(t), Options{})
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := errorCode(roundTrip(t, path, raw)); got != pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST {
				t.Errorf("code = %v, want INVALID_REQUEST", got)
			}
		})
	}
}

// Verifies: SEC-120.
func TestServerSurvivesAProbe(t *testing.T) {
	t.Parallel()
	path := serveFake(t, newFakeToken(t), Options{})
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	resp := roundTrip(t, path, frameOf(t, signRequest("pkcs11:object=release", pkcs11pb.Algorithm_ALGORITHM_ED25519, []byte("m"))))
	if resp.GetSign() == nil {
		t.Errorf("response after a probe = %v", resp)
	}
}

// Verifies: SEC-120.
func TestServeStopsWhenTheContextIsDone(t *testing.T) {
	t.Parallel()
	l, err := Listen(t.Context(), t.TempDir()+"/s")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer(newFakeToken(t), Options{}).Serve(ctx, l) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if _, err := l.Accept(); !isClosed(err) {
		t.Errorf("the listener is still open: %v", err)
	}
}

// wrapRoundTrip wraps a data key through the socket and returns the
// wrapped key.
func wrapOnce(t *testing.T, path, ref string, dataKey []byte) *pkcs11pb.Response {
	t.Helper()
	req := &pkcs11pb.Request{Request: &pkcs11pb.Request_Wrap{Wrap: &pkcs11pb.WrapRequest{KeyRef: "pkcs11:object=" + ref, Plaintext: dataKey}}}
	return roundTrip(t, path, frameOf(t, req))
}

// unwrapOnce unwraps through the socket.
func unwrapOnce(t *testing.T, path, ref string, wrapped []byte) *pkcs11pb.Response {
	t.Helper()
	req := &pkcs11pb.Request{Request: &pkcs11pb.Request_Unwrap{Unwrap: &pkcs11pb.UnwrapRequest{KeyRef: "pkcs11:object=" + ref, Wrapped: wrapped}}}
	return roundTrip(t, path, frameOf(t, req))
}

// Verifies: SEC-120, SEC-106.
func TestServerWrapsAndUnwrapsDataKeys(t *testing.T) {
	t.Parallel()
	path := serveFake(t, newFakeToken(t), Options{})
	dataKey := bytes.Repeat([]byte{7}, 32)
	wrapped := wrapOnce(t, path, "wrap", dataKey).GetWrap().GetWrapped()
	if len(wrapped) != ivSize+len(dataKey)+tagSize {
		t.Fatalf("wrapped key is %d bytes", len(wrapped))
	}
	if bytes.Contains(wrapped, dataKey) {
		t.Error("the data key appears in the wrapped key")
	}
	if got := unwrapOnce(t, path, "wrap", wrapped).GetUnwrap().GetPlaintext(); !bytes.Equal(got, dataKey) {
		t.Errorf("unwrapped = %x", got)
	}
	// A fresh IV every time.
	again := wrapOnce(t, path, "wrap", dataKey).GetWrap().GetWrapped()
	if bytes.Equal(wrapped[:ivSize], again[:ivSize]) {
		t.Error("two wraps share an IV")
	}
}

// Verifies: SEC-120, SEC-106.
func TestServerRefusesWhatDoesNotUnwrap(t *testing.T) {
	t.Parallel()
	path := serveFake(t, newFakeToken(t), Options{})
	wrapped := wrapOnce(t, path, "wrap", bytes.Repeat([]byte{7}, 32)).GetWrap().GetWrapped()
	flip := func(i int) []byte {
		out := bytes.Clone(wrapped)
		out[i] ^= 1
		return out
	}
	decrypt := pkcs11pb.ErrorCode_ERROR_CODE_DECRYPT_FAILED
	invalid := pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST
	for name, tc := range map[string]struct {
		ref     string
		wrapped []byte
		want    pkcs11pb.ErrorCode
	}{
		"another key":       {"wrap2", wrapped, decrypt},
		"a tampered tag":    {"wrap", flip(len(wrapped) - 1), decrypt},
		"a tampered cipher": {"wrap", flip(ivSize), decrypt},
		"a tampered IV":     {"wrap", flip(0), decrypt},
		"too short":         {"wrap", wrapped[:ivSize+tagSize], invalid},
		"no such key":       {"absent", wrapped, pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND},
	} {
		if got := errorCode(unwrapOnce(t, path, tc.ref, tc.wrapped)); got != tc.want {
			t.Errorf("%s: code = %v, want %v", name, got, tc.want)
		}
	}
	if got := errorCode(wrapOnce(t, path, "wrap", nil)); got != invalid {
		t.Errorf("an empty data key: %v", got)
	}
	if got := errorCode(wrapOnce(t, path, "absent", []byte("k"))); got != pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND {
		t.Errorf("wrap with a missing key: %v", got)
	}
}

// Verifies: SEC-120, SEC-106.
func TestServerUsesTheIVTheTokenChose(t *testing.T) {
	t.Parallel()
	token := newFakeToken(t)
	token.forceIV = bytes.Repeat([]byte{9}, ivSize)
	path := serveFake(t, token, Options{})
	wrapped := wrapOnce(t, path, "wrap", []byte("a data key")).GetWrap().GetWrapped()
	if !bytes.HasPrefix(wrapped, token.forceIV) {
		t.Errorf("the wrapped key does not start with the token's IV: %x", wrapped)
	}
	if got := unwrapOnce(t, path, "wrap", wrapped).GetUnwrap().GetPlaintext(); string(got) != "a data key" {
		t.Errorf("unwrapped = %q", got)
	}
}

// Verifies: SEC-120, SEC-106.
func TestServerRefusesAMalformedTokenAnswer(t *testing.T) {
	t.Parallel()
	good := serveFake(t, newFakeToken(t), Options{})
	wrapped := wrapOnce(t, good, "wrap", []byte("a data key")).GetWrap().GetWrapped()
	token := newFakeToken(t)
	token.badLength = true
	path := serveFake(t, token, Options{})
	if got := errorCode(wrapOnce(t, path, "wrap", []byte("a data key"))); got != pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Errorf("wrap: %v", got)
	}
	if got := errorCode(unwrapOnce(t, path, "wrap", wrapped)); got != pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Errorf("unwrap: %v", got)
	}
}
