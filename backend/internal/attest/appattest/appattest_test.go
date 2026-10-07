// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package appattest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const testAppID = "TEAM123456.com.example.plux"

var (
	testNow      = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	testNotAfter = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
)

// cborBytes is a small CBOR encoder for the test vectors: unsigned
// integers, byte and text strings, arrays and maps with string keys.
func cborBytes(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n < 256:
			return []byte{major<<5 | 24, byte(n)}
		case n < 65536:
			return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
		default:
			return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
		}
	}
	switch x := v.(type) {
	case int:
		return head(0, uint64(x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case []any:
		out := head(4, uint64(len(x)))
		for _, e := range x {
			out = append(out, cborBytes(e)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out := head(5, uint64(len(x)))
		for _, k := range keys {
			out = append(out, cborBytes(k)...)
			out = append(out, cborBytes(x[k])...)
		}
		return out
	}
	panic("unsupported test value")
}

// pki is a root, an intermediate and a device key, all generated for the
// test.
type pki struct {
	root   *x509.Certificate
	rootK  *ecdsa.PrivateKey
	inter  *x509.Certificate
	interK *ecdsa.PrivateKey
	roots  *x509.CertPool
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newPKI(t *testing.T, notAfter time.Time) pki {
	t.Helper()
	rootK, interK := newKey(t), newKey(t)
	rootT := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test App Attestation Root"},
		NotBefore: testNow.Add(-24 * time.Hour), NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootT, rootT, &rootK.PublicKey, rootK)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	interT := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test App Attestation CA 1"},
		NotBefore: testNow.Add(-24 * time.Hour), NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interT, root, &interK.PublicKey, rootK)
	if err != nil {
		t.Fatal(err)
	}
	inter, err := x509.ParseCertificate(interDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return pki{root: root, rootK: rootK, inter: inter, interK: interK, roots: pool}
}

// attestation describes one synthetic attestation; the zero value is a
// valid development attestation.
type attestation struct {
	env          Environment
	appID        string
	counter      uint32
	aaguid       []byte
	credID       []byte
	keyID        []byte
	nonceIn      []byte
	fmtName      string
	dropField    string
	badType      string
	keyHashFlip  bool
	notAfterCert time.Time
}

type vector struct {
	object []byte
	keyID  []byte
	hash   [32]byte
	key    *ecdsa.PrivateKey
}

func (a attestation) build(t *testing.T, p pki) vector {
	t.Helper()
	key := newKey(t)
	ecdhKey, err := key.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(ecdhKey.Bytes())
	keyID := sum[:]
	if a.keyHashFlip {
		keyID = bytes.Clone(keyID)
		keyID[0] ^= 1
	}
	hash := ClientDataHash([]byte("challenge"), "jkt-thumbprint")
	appID := testAppID
	if a.appID != "" {
		appID = a.appID
	}
	aaguid := a.aaguid
	if aaguid == nil {
		g, _ := a.env.aaguid()
		aaguid = g[:]
	}
	credID := a.credID
	if credID == nil {
		credID = keyID
	}
	rp := sha256.Sum256([]byte(appID))
	authData := append([]byte{}, rp[:]...)
	authData = append(authData, 0x40)
	authData = binary.BigEndian.AppendUint32(authData, a.counter)
	authData = append(authData, aaguid...)
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(credID)))
	authData = append(authData, credID...)
	authData = append(authData, 0xa0)

	nonce := a.nonceIn
	if nonce == nil {
		n := nonceOf(authData, hash)
		nonce = n[:]
	}
	extVal, err := asn1.Marshal(nonceExtension{Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	notAfter := a.notAfterCert
	if notAfter.IsZero() {
		notAfter = testNotAfter
	}
	credT := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "credential"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: notAfter,
		ExtraExtensions: []pkix.Extension{{Id: nonceOID, Value: extVal}},
	}
	credDER, err := x509.CreateCertificate(rand.Reader, credT, p.inter, &key.PublicKey, p.interK)
	if err != nil {
		t.Fatal(err)
	}
	fmtName := a.fmtName
	if fmtName == "" {
		fmtName = "apple-appattest"
	}
	obj := map[string]any{
		"fmt":      fmtName,
		"attStmt":  map[string]any{"x5c": []any{credDER, p.inter.Raw}, "receipt": []byte("receipt-bytes")},
		"authData": authData,
	}
	switch a.badType {
	case "":
	case "authData":
		obj["authData"] = "text"
	case "attStmt":
		obj["attStmt"] = []any{}
	case "x5c":
		obj["attStmt"] = map[string]any{"x5c": []any{1, 2}, "receipt": []byte("r")}
	case "x5cOne":
		obj["attStmt"] = map[string]any{"x5c": []any{credDER}, "receipt": []byte("r")}
	case "x5cGarbage":
		obj["attStmt"] = map[string]any{"x5c": []any{[]byte("junk"), p.inter.Raw}, "receipt": []byte("r")}
	case "receipt":
		obj["attStmt"] = map[string]any{"x5c": []any{credDER, p.inter.Raw}, "receipt": 7}
	}
	if a.dropField != "" {
		if stmt, ok := obj["attStmt"].(map[string]any); ok && (a.dropField == "x5c" || a.dropField == "receipt") {
			delete(stmt, a.dropField)
		} else {
			delete(obj, a.dropField)
		}
	}
	return vector{object: cborBytes(obj), keyID: keyID, hash: hash, key: key}
}

func (p pki) verifier() *Verifier {
	return &Verifier{Roots: p.roots, Now: func() time.Time { return testNow }}
}

func isAttestationFailed(err error) bool {
	var e *plxerr.Error
	return errors.As(err, &e) && e.Code == plxerr.AttestationFailed
}

// Verifies: SEC-004.
// A valid attestation verifies in both environments and yields the
// attested key, the receipt and a zero counter.
func TestVerifyAttestationValid(t *testing.T) {
	t.Parallel()
	p := newPKI(t, testNotAfter)
	for name, env := range map[string]Environment{"development": Development, "production": Production} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := attestation{env: env}.build(t, p)
			got, err := p.verifier().VerifyAttestation(v.object, v.keyID, v.hash, testAppID, env)
			if err != nil {
				t.Fatalf("VerifyAttestation: %v", err)
			}
			if !got.PublicKey.Equal(&v.key.PublicKey) || string(got.Receipt) != "receipt-bytes" || got.Counter != 0 {
				t.Errorf("attestation = %+v", got)
			}
		})
	}
}

// Verifies: SEC-004.
// Every failed check rejects the attestation with AttestationFailed.
func TestVerifyAttestationRefuses(t *testing.T) {
	t.Parallel()
	p := newPKI(t, testNotAfter)
	other := newPKI(t, testNotAfter)
	prodAAGUID := append([]byte("appattest"), make([]byte, 7)...)
	tests := []struct {
		name  string
		a     attestation
		env   Environment
		appID string
		pki   *pki
		hash  *[32]byte
		check string
	}{
		{name: "production aaguid in development", a: attestation{aaguid: prodAAGUID}, env: Development, check: "AAGUID"},
		{name: "development aaguid in production", a: attestation{env: Development}, env: Production, check: "AAGUID"},
		{name: "unknown environment", a: attestation{}, env: Environment(9), check: "environment"},
		{name: "wrong app identifier", a: attestation{}, appID: "TEAM123456.com.example.other", check: "application identifier"},
		{name: "nonzero counter", a: attestation{counter: 1}, check: "counter"},
		{name: "key hash mismatch", a: attestation{keyHashFlip: true}, check: "key identifier"},
		{name: "credential id mismatch", a: attestation{credID: bytes.Repeat([]byte{7}, 32)}, check: "credential identifier"},
		{name: "nonce mismatch", a: attestation{nonceIn: bytes.Repeat([]byte{1}, 32)}, check: "nonce"},
		{name: "other client data hash", a: attestation{}, hash: &[32]byte{1}, check: "nonce"},
		{name: "unknown root", a: attestation{}, pki: &other, check: "chain"},
		{name: "expired certificate", a: attestation{notAfterCert: testNow.Add(-time.Minute)}, check: "chain"},
		{name: "wrong format", a: attestation{fmtName: "packed"}, check: "format"},
		{name: "missing fmt", a: attestation{dropField: "fmt"}, check: "format"},
		{name: "missing authData", a: attestation{dropField: "authData"}, check: "authData"},
		{name: "missing attStmt", a: attestation{dropField: "attStmt"}, check: "attStmt"},
		{name: "missing x5c", a: attestation{dropField: "x5c"}, check: "x5c"},
		{name: "missing receipt", a: attestation{dropField: "receipt"}, check: "receipt"},
		{name: "authData is text", a: attestation{badType: "authData"}, check: "authData"},
		{name: "attStmt is an array", a: attestation{badType: "attStmt"}, check: "attStmt"},
		{name: "x5c holds integers", a: attestation{badType: "x5c"}, check: "x5c"},
		{name: "x5c holds one certificate", a: attestation{badType: "x5cOne"}, check: "x5c"},
		{name: "x5c holds garbage", a: attestation{badType: "x5cGarbage"}, check: "does not parse"},
		{name: "receipt is an integer", a: attestation{badType: "receipt"}, check: "receipt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := tc.a.build(t, p)
			appID := testAppID
			if tc.appID != "" {
				appID = tc.appID
			}
			hash := v.hash
			if tc.hash != nil {
				hash = *tc.hash
			}
			ver := p.verifier()
			if tc.pki != nil {
				ver = tc.pki.verifier()
			}
			_, err := ver.VerifyAttestation(v.object, v.keyID, hash, appID, tc.env)
			if !isAttestationFailed(err) || !strings.Contains(err.Error(), tc.check) {
				t.Fatalf("err = %v, want AttestationFailed naming %q", err, tc.check)
			}
		})
	}
}

// Verifies: SEC-004.
// A root outside its validity period refuses the chain, and malformed or
// oversized objects are refused before any certificate is read.
func TestVerifyAttestationInput(t *testing.T) {
	t.Parallel()
	p := newPKI(t, testNotAfter)
	good := attestation{}.build(t, p)
	expiredRoot := newPKI(t, testNow.Add(-time.Hour))
	ex := attestation{}.build(t, expiredRoot)
	tests := []struct {
		name   string
		ver    *Verifier
		object []byte
		keyID  []byte
	}{
		{name: "expired root", ver: expiredRoot.verifier(), object: ex.object, keyID: ex.keyID},
		{name: "empty", ver: p.verifier(), object: nil, keyID: good.keyID},
		{name: "oversized", ver: p.verifier(), object: make([]byte, maxInput+1), keyID: good.keyID},
		{name: "not CBOR", ver: p.verifier(), object: []byte{0xff}, keyID: good.keyID},
		{name: "not a map", ver: p.verifier(), object: cborBytes([]any{1}), keyID: good.keyID},
		{name: "trailing bytes", ver: p.verifier(), object: append(bytes.Clone(good.object), 0), keyID: good.keyID},
		{name: "truncated", ver: p.verifier(), object: good.object[:len(good.object)-5], keyID: good.keyID},
		{name: "no roots", ver: &Verifier{Now: func() time.Time { return testNow }}, object: good.object, keyID: good.keyID},
		{name: "no clock", ver: &Verifier{Roots: p.roots}, object: good.object, keyID: good.keyID},
		{name: "short key id", ver: p.verifier(), object: good.object, keyID: good.keyID[:16]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.ver.VerifyAttestation(tc.object, tc.keyID, good.hash, testAppID, Development)
			if !isAttestationFailed(err) {
				t.Fatalf("err = %v, want AttestationFailed", err)
			}
		})
	}
}

// Verifies: SEC-004.
// A truncated authenticator data is refused, not read past its end.
func TestCheckAuthDataShort(t *testing.T) {
	t.Parallel()
	g, _ := Development.aaguid()
	rp := sha256.Sum256([]byte(testAppID))
	full := append(append(append(rp[:len(rp):len(rp)], 0x40, 0, 0, 0, 0), g[:]...), 0, 40)
	for name, in := range map[string][]byte{
		"empty":         nil,
		"before aaguid": full[:30],
		"credential id": append(bytes.Clone(full), make([]byte, 10)...),
		"no credential": full,
	} {
		if _, err := checkAuthData(in, make([]byte, 32), testAppID, g); !isAttestationFailed(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Verifies: SEC-004.
// ClientDataHash is SHA-256 over the challenge followed by the
// thumbprint.
func TestClientDataHash(t *testing.T) {
	t.Parallel()
	want := sha256.Sum256([]byte("abcjkt"))
	if got := ClientDataHash([]byte("abc"), "jkt"); got != want {
		t.Errorf("ClientDataHash = %x, want %x", got, want)
	}
}

// Verifies: SEC-004.
// The embedded root parses and is the Apple App Attestation Root CA.
func TestDefaultRoots(t *testing.T) {
	t.Parallel()
	pool, err := DefaultRoots()
	if err != nil {
		t.Fatalf("DefaultRoots: %v", err)
	}
	if pool == nil {
		t.Fatal("DefaultRoots returned no pool")
	}
	sum := sha256.Sum256(firstCertDER(t))
	const want = "1cb9823ba28ba6ad2d33a006941de2ae4f513ef1d4e831b9f7e0fa7b6242c932"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("fingerprint = %s, want %s", got, want)
	}
	cert, err := x509.ParseCertificate(firstCertDER(t))
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "Apple App Attestation Root CA" || cert.NotAfter.Year() != 2045 {
		t.Errorf("subject %q, expiry %v", cert.Subject.CommonName, cert.NotAfter)
	}
}

// firstCertDER returns the DER of the embedded root.
func firstCertDER(t *testing.T) []byte {
	t.Helper()
	block, _ := pem.Decode(appleRootPEM)
	if block == nil {
		t.Fatal("the embedded root is not PEM")
	}
	return block.Bytes
}

// assertion is one synthetic assertion.
type assertion struct {
	appID      string
	counter    uint32
	signWith   *ecdsa.PrivateKey
	hashSigned *[32]byte
}

func (a assertion) build(t *testing.T, key *ecdsa.PrivateKey, hash [32]byte) []byte {
	t.Helper()
	appID := testAppID
	if a.appID != "" {
		appID = a.appID
	}
	rp := sha256.Sum256([]byte(appID))
	authData := append([]byte{}, rp[:]...)
	authData = append(authData, 0x00)
	authData = binary.BigEndian.AppendUint32(authData, a.counter)
	signed := hash
	if a.hashSigned != nil {
		signed = *a.hashSigned
	}
	nonce := nonceOf(authData, signed)
	digest := sha256.Sum256(nonce[:])
	k := key
	if a.signWith != nil {
		k = a.signWith
	}
	sig, err := ecdsa.SignASN1(rand.Reader, k, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return cborBytes(map[string]any{"signature": sig, "authenticatorData": authData})
}

// Verifies: SEC-004.
// An assertion verifies when signed by the attested key for the same
// client data and application, with an increased counter.
func TestVerifyAssertion(t *testing.T) {
	t.Parallel()
	key := newKey(t)
	hash := ClientDataHash([]byte("challenge"), "jkt")
	otherHash := [32]byte{9}
	tests := []struct {
		name string
		in   func() []byte
		last uint32
		want uint32
		ok   bool
	}{
		{name: "valid", in: func() []byte { return assertion{counter: 5}.build(t, key, hash) }, last: 4, want: 5, ok: true},
		{name: "valid from zero", in: func() []byte { return assertion{counter: 1}.build(t, key, hash) }, want: 1, ok: true},
		{name: "bad signature", in: func() []byte { return assertion{counter: 5, signWith: newKey(t)}.build(t, key, hash) }, last: 4},
		{name: "other client data", in: func() []byte { return assertion{counter: 5, hashSigned: &otherHash}.build(t, key, hash) }, last: 4},
		{name: "wrong app", in: func() []byte { return assertion{counter: 5, appID: "TEAM123456.com.example.other"}.build(t, key, hash) }, last: 4},
		{name: "counter equal", in: func() []byte { return assertion{counter: 5}.build(t, key, hash) }, last: 5},
		{name: "counter lower", in: func() []byte { return assertion{counter: 3}.build(t, key, hash) }, last: 5},
		{name: "empty", in: func() []byte { return nil }},
		{name: "oversized", in: func() []byte { return make([]byte, maxInput+1) }},
		{name: "not CBOR", in: func() []byte { return []byte{0xff} }},
		{name: "not a map", in: func() []byte { return cborBytes([]any{}) }},
		{name: "no signature", in: func() []byte { return cborBytes(map[string]any{"authenticatorData": make([]byte, 37)}) }},
		{name: "signature is text", in: func() []byte {
			return cborBytes(map[string]any{"signature": "x", "authenticatorData": make([]byte, 37)})
		}},
		{name: "short authenticator data", in: func() []byte {
			return cborBytes(map[string]any{"signature": []byte{1}, "authenticatorData": make([]byte, 36)})
		}},
		{name: "trailing bytes", in: func() []byte { return append(assertion{counter: 5}.build(t, key, hash), 0) }, last: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := VerifyAssertion(tc.in(), &key.PublicKey, hash, testAppID, tc.last)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("VerifyAssertion = %d, %v", got, err)
				}
				return
			}
			if !isAttestationFailed(err) {
				t.Fatalf("err = %v, want AttestationFailed", err)
			}
		})
	}
	if _, err := VerifyAssertion(assertion{counter: 5}.build(t, key, hash), nil, hash, testAppID, 0); !isAttestationFailed(err) {
		t.Errorf("nil key: %v", err)
	}
}

// FuzzVerifyAttestation checks that no input makes attestation
// verification panic.
func FuzzVerifyAttestation(f *testing.F) {
	pool, err := DefaultRoots()
	if err != nil {
		f.Fatal(err)
	}
	ver := &Verifier{Roots: pool, Now: func() time.Time { return testNow }}
	f.Add([]byte{0xa0}, make([]byte, 32))
	f.Add(cborBytes(map[string]any{"fmt": "apple-appattest", "attStmt": map[string]any{"x5c": []any{[]byte{1}, []byte{2}}, "receipt": []byte{3}}, "authData": make([]byte, 60)}), make([]byte, 32))
	f.Fuzz(func(_ *testing.T, object, keyID []byte) {
		_, _ = ver.VerifyAttestation(object, keyID, [32]byte{}, testAppID, Development)
	})
}

// FuzzVerifyAssertion checks that no input makes assertion verification
// panic.
func FuzzVerifyAssertion(f *testing.F) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte{0xa0})
	f.Add(cborBytes(map[string]any{"signature": []byte{1, 2}, "authenticatorData": make([]byte, 40)}))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = VerifyAssertion(data, &key.PublicKey, [32]byte{}, testAppID, 0)
	})
}
