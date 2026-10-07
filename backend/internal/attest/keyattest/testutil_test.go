// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	testPackage = "com.example.app"
	serialRoot  = 1
	serialInt   = 2
	serialLeaf  = 3
)

var (
	testChallenge = []byte("0123456789abcdef-server-challenge")
	testNow       = time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
)

func testDigest() []byte {
	d := sha256.Sum256([]byte("signing certificate"))
	return d[:]
}

func otherDigest() []byte {
	d := sha256.Sum256([]byte("another signing certificate"))
	return d[:]
}

func testPolicy() Policy {
	return Policy{
		PackageNames:        []string{testPackage},
		SigningCertDigests:  [][]byte{testDigest()},
		RequireHardware:     true,
		RequireVerifiedBoot: true,
	}
}

// rotSpec describes a RootOfTrust sequence.
type rotSpec struct {
	locked   bool
	state    int64
	withHash bool
}

// kdSpec describes a KeyDescription to encode.
type kdSpec struct {
	attVersion   int64
	attLevel     int64
	kmLevel      int64
	challenge    []byte
	root         *rotSpec
	patch        int64
	packages     []string
	digests      [][]byte
	noAppID      bool
	appInHW      bool
	extraSoftTLV []byte
}

// validSpec is a TEE-backed, locked, verified device running the test app.
func validSpec() kdSpec {
	return kdSpec{
		attVersion: 300,
		attLevel:   1,
		kmLevel:    1,
		challenge:  testChallenge,
		root:       &rotSpec{locked: true, state: 0, withHash: true},
		patch:      20260905,
		packages:   []string{testPackage},
		digests:    [][]byte{testDigest()},
	}
}

// explicitTLV encodes one DER element with an explicit high-tag-number
// context-specific constructed identifier.
func explicitTLV(number uint32, content []byte) []byte {
	out := []byte{0xbf}
	var groups []byte
	for n := number; ; n >>= 7 {
		groups = append([]byte{byte(n & 0x7f)}, groups...)
		if n < 0x80 {
			break
		}
	}
	for i := range len(groups) - 1 {
		groups[i] |= 0x80
	}
	out = append(out, groups...)
	return append(append(out, derLength(len(content))...), content...)
}

func derLength(n int) []byte {
	switch {
	case n < 0x80:
		return []byte{byte(n)}
	case n < 0x100:
		return []byte{0x81, byte(n)}
	default:
		return []byte{0x82, byte(n >> 8), byte(n)}
	}
}

func build(f func(b *cryptobyte.Builder)) []byte {
	var b cryptobyte.Builder
	f(&b)
	out, err := b.Bytes()
	if err != nil {
		panic(err)
	}
	return out
}

func encodeRoot(r rotSpec) []byte {
	return build(func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1OctetString([]byte("verified boot key"))
			b.AddASN1Boolean(r.locked)
			b.AddASN1Enum(r.state)
			if r.withHash {
				b.AddASN1OctetString([]byte("verified boot hash"))
			}
		})
	})
}

func encodeAppID(packages []string, digests [][]byte) []byte {
	return build(func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) {
				for _, p := range packages {
					b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
						b.AddASN1OctetString([]byte(p))
						b.AddASN1Int64(7)
					})
				}
			})
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) {
				for _, d := range digests {
					b.AddASN1OctetString(d)
				}
			})
		})
	})
}

func tagged(number uint32, f func(b *cryptobyte.Builder)) []byte {
	return explicitTLV(number, build(f))
}

// encodeKD encodes the KeyDescription extension value.
func encodeKD(s kdSpec) []byte {
	// Tags the verifier must skip: [1] purpose, [701] creationDateTime.
	var soft []byte
	soft = append(soft, build(func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.Tag(1).ContextSpecific().Constructed(), func(b *cryptobyte.Builder) {
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) { b.AddASN1Int64(2) })
		})
	})...)
	soft = append(soft, tagged(701, func(b *cryptobyte.Builder) { b.AddASN1Int64(1_700_000_000_000) })...)
	if !s.noAppID && !s.appInHW {
		soft = append(soft, tagged(709, func(b *cryptobyte.Builder) {
			b.AddASN1OctetString(encodeAppID(s.packages, s.digests))
		})...)
	}
	soft = append(soft, s.extraSoftTLV...)

	var hw []byte
	hw = append(hw, tagged(702, func(b *cryptobyte.Builder) { b.AddASN1Int64(0) })...)
	if s.root != nil {
		hw = append(hw, explicitTLV(704, encodeRoot(*s.root))...)
	}
	if s.patch != 0 {
		hw = append(hw, tagged(706, func(b *cryptobyte.Builder) { b.AddASN1Int64(s.patch) })...)
	}
	if s.appInHW && !s.noAppID {
		hw = append(hw, tagged(709, func(b *cryptobyte.Builder) {
			b.AddASN1OctetString(encodeAppID(s.packages, s.digests))
		})...)
	}

	return build(func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1Int64(s.attVersion)
			b.AddASN1Enum(s.attLevel)
			b.AddASN1Int64(300)
			b.AddASN1Enum(s.kmLevel)
			b.AddASN1OctetString(s.challenge)
			b.AddASN1OctetString(nil)
			b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { b.AddBytes(soft) })
			b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { b.AddBytes(hw) })
		})
	})
}

// testPKI is a synthetic root, intermediate and leaf factory.
type testPKI struct {
	t       *testing.T
	roots   *x509.CertPool
	rootDER []byte
	intDER  []byte
	rootKey *ecdsa.PrivateKey
	intKey  *ecdsa.PrivateKey
	rootC   *x509.Certificate
	intC    *x509.Certificate
}

func newKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func create(t *testing.T, tmpl, parent *x509.Certificate, pub crypto.PublicKey, signer crypto.Signer) ([]byte, *x509.Certificate) {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return der, c
}

func caTemplate(serial int64, cn string, ext []pkix.Extension) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             testNow.Add(-24 * time.Hour),
		NotAfter:              testNow.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		ExtraExtensions:       ext,
	}
}

// newPKI builds a root and an intermediate. intExt is added to the
// intermediate's extensions.
func newPKI(t *testing.T, intExt []pkix.Extension) *testPKI {
	t.Helper()
	p := &testPKI{t: t, rootKey: newKey(t, elliptic.P256()), intKey: newKey(t, elliptic.P256())}
	rootTmpl := caTemplate(serialRoot, "Test Root", nil)
	p.rootDER, p.rootC = create(t, rootTmpl, rootTmpl, &p.rootKey.PublicKey, p.rootKey)
	p.intDER, p.intC = create(t, caTemplate(serialInt, "Test Intermediate", intExt), p.rootC, &p.intKey.PublicKey, p.rootKey)
	p.roots = x509.NewCertPool()
	p.roots.AddCert(p.rootC)
	return p
}

// leaf issues a leaf certificate for a fresh key of the given curve carrying
// the extension value ext (omitted when nil), and returns its DER.
func (p *testPKI) leaf(curve elliptic.Curve, ext []byte) []byte {
	p.t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serialLeaf),
		Subject:      pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore:    testNow.Add(-24 * time.Hour),
		NotAfter:     testNow.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if ext != nil {
		tmpl.ExtraExtensions = []pkix.Extension{{Id: attestationOID, Value: ext}}
	}
	der, _ := create(p.t, tmpl, p.intC, &newKey(p.t, curve).PublicKey, p.intKey)
	return der
}

// chain returns leaf, intermediate and root.
func (p *testPKI) chain(leaf []byte) [][]byte {
	return [][]byte{leaf, p.intDER, p.rootDER}
}

func (p *testPKI) verifier(rev *Revocations) *Verifier {
	return &Verifier{Roots: p.roots, Revocations: rev, Now: func() time.Time { return testNow }}
}

// wantCode asserts that err is a plxerr error with the given code.
func wantCode(t *testing.T, err error, code plxerr.Code) {
	t.Helper()
	var pe *plxerr.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want a plxerr error with code %d", err, code)
	}
	if pe.Code != code {
		t.Fatalf("code = %d (%v), want %d", pe.Code, err, code)
	}
}
