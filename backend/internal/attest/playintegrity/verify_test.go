// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package playintegrity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const testPackage = "com.example.plux"

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// fixture holds the keys of one test run and builds tokens with them.
type fixture struct {
	t       testing.TB
	aes     []byte
	signer  *ecdsa.PrivateKey
	keys    Keys
	hash    string
	certDig []byte
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	aes := make([]byte, aesKeyLen)
	if _, err := rand.Read(aes); err != nil {
		t.Fatal(err)
	}
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dig := sha256.Sum256([]byte("signing certificate"))
	return &fixture{
		t: t, aes: aes, signer: signer,
		keys:    Keys{Decryption: aes, Verification: &signer.PublicKey},
		hash:    RequestHash([]byte("challenge"), "jkt-thumbprint"),
		certDig: dig[:],
	}
}

// verdict returns a valid verdict payload as a generic map.
func (f *fixture) verdict() map[string]any {
	return map[string]any{
		"requestDetails": map[string]any{
			"requestPackageName": testPackage,
			"requestHash":        f.hash,
			"timestampMillis":    strconv.FormatInt(testNow.Add(-5*time.Second).UnixMilli(), 10),
		},
		"appIntegrity": map[string]any{
			"appRecognitionVerdict":   "PLAY_RECOGNIZED", //nolint:misspell // Google's verdict value
			"packageName":             testPackage,
			"certificateSha256Digest": []string{base64.RawURLEncoding.EncodeToString(f.certDig)},
			"versionCode":             "42",
		},
		"deviceIntegrity": map[string]any{
			"deviceRecognitionVerdict": []string{"MEETS_DEVICE_INTEGRITY", "MEETS_BASIC_INTEGRITY"},
		},
		"accountDetails": map[string]any{"appLicensingVerdict": "LICENSED"},
	}
}

func (f *fixture) sign(payload any, alg jose.SignatureAlgorithm, key any) string {
	f.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	obj, err := s.Sign(b)
	if err != nil {
		f.t.Fatal(err)
	}
	out, err := obj.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) encrypt(plain string, alg jose.KeyAlgorithm, key any) string {
	f.t.Helper()
	enc, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: alg, Key: key}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	obj, err := enc.Encrypt([]byte(plain))
	if err != nil {
		f.t.Fatal(err)
	}
	out, err := obj.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) token(payload any) string {
	return f.encrypt(f.sign(payload, jose.ES256, f.signer), jose.A256KW, f.aes)
}

func (f *fixture) expect() Expect {
	return Expect{
		PackageName: testPackage,
		RequestHash: f.hash,
		CertDigests: [][]byte{f.certDig},
		MaxAge:      time.Minute,
	}
}

func (f *fixture) verifier() *Verifier {
	return &Verifier{Keys: f.keys, Now: func() time.Time { return testNow }}
}

func set(m map[string]any, section, key string, value any) {
	m[section].(map[string]any)[key] = value
}

func requireAttestationFailed(t *testing.T, err error) {
	t.Helper()
	var pe *plxerr.Error
	if !errors.As(err, &pe) || pe.Code != plxerr.AttestationFailed {
		t.Fatalf("want AttestationFailed, got %v", err)
	}
}

// Verifies: SEC-003.
func TestVerifyValid(t *testing.T) {
	f := newFixture(t)
	v, err := f.verifier().Verify(f.token(f.verdict()), f.expect())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !v.AppRecognised || v.Licensing != "LICENSED" || v.VersionCode != 42 {
		t.Errorf("unexpected verdict %+v", v)
	}
	if want := testNow.Add(-5 * time.Second); !v.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", v.Timestamp, want)
	}
	if v.Strongest() != LabelDevice || len(v.Device) != 2 {
		t.Errorf("Device = %v, Strongest = %q", v.Device, v.Strongest())
	}
}

func TestVerifyNumericFieldsAndNoCertCheck(t *testing.T) {
	f := newFixture(t)
	p := f.verdict()
	set(p, "requestDetails", "timestampMillis", testNow.UnixMilli())
	set(p, "appIntegrity", "versionCode", 7)
	e := f.expect()
	e.CertDigests = nil
	v, err := f.verifier().Verify(f.token(p), e)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if v.VersionCode != 7 || !v.Timestamp.Equal(testNow) {
		t.Errorf("unexpected verdict %+v", v)
	}
}

func TestVerifyRejects(t *testing.T) {
	other := newFixture(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ms := func(d time.Duration) string {
		return strconv.FormatInt(testNow.Add(d).UnixMilli(), 10)
	}
	otherDig := sha256.Sum256([]byte("another certificate"))
	tests := []struct {
		name  string
		token func(f *fixture) string
		exp   func(e *Expect)
	}{
		{name: "wrong decryption key", token: func(f *fixture) string {
			return f.encrypt(f.sign(f.verdict(), jose.ES256, f.signer), jose.A256KW, other.aes)
		}},
		{name: "wrong verification key", token: func(f *fixture) string {
			return f.encrypt(f.sign(f.verdict(), jose.ES256, other.signer), jose.A256KW, f.aes)
		}},
		{name: "HS256 inner signature", token: func(f *fixture) string {
			return f.encrypt(f.sign(f.verdict(), jose.HS256, make([]byte, 32)), jose.A256KW, f.aes)
		}},
		{name: "none inner signature", token: func(f *fixture) string {
			b, _ := json.Marshal(f.verdict())
			hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
			return f.encrypt(hdr+"."+base64.RawURLEncoding.EncodeToString(b)+".", jose.A256KW, f.aes)
		}},
		{name: "RSA-OAEP key wrap", token: func(f *fixture) string {
			return f.encrypt(f.sign(f.verdict(), jose.ES256, f.signer), jose.RSA_OAEP_256, &rsaKey.PublicKey)
		}},
		{name: "none key wrap", token: func(*fixture) string {
			return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","enc":"A256GCM"}`)) + "..AAAA.AAAA.AAAA"
		}},
		{name: "unsigned plaintext", token: func(f *fixture) string {
			return f.encrypt(`{"requestDetails":{}}`, jose.A256KW, f.aes)
		}},
		{name: "tampered ciphertext", token: func(f *fixture) string {
			parts := strings.Split(f.token(f.verdict()), ".")
			ct := []byte(parts[3])
			if ct[0] == 'A' {
				ct[0] = 'B'
			} else {
				ct[0] = 'A'
			}
			parts[3] = string(ct)
			return strings.Join(parts, ".")
		}},
		{name: "JSON serialisation", token: func(*fixture) string { return `{"protected":"e30"}` }},
		{name: "empty token", token: func(*fixture) string { return "" }},
		{name: "oversized token", token: func(*fixture) string { return strings.Repeat("A", maxTokenBytes+1) }},
		{name: "request package", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "requestDetails", "requestPackageName", "com.evil")
			return f.token(p)
		}},
		{name: "app package", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "appIntegrity", "packageName", "com.evil")
			return f.token(p)
		}},
		{name: "request hash", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "requestDetails", "requestHash", RequestHash([]byte("other"), "jkt-thumbprint"))
			return f.token(p)
		}},
		{name: "stale timestamp", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "requestDetails", "timestampMillis", ms(-2*time.Minute))
			return f.token(p)
		}},
		{name: "future timestamp", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "requestDetails", "timestampMillis", ms(61*time.Second))
			return f.token(p)
		}},
		{name: "missing timestamp", token: func(f *fixture) string {
			p := f.verdict()
			delete(p["requestDetails"].(map[string]any), "timestampMillis")
			return f.token(p)
		}},
		{name: "malformed timestamp", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "requestDetails", "timestampMillis", "soon")
			return f.token(p)
		}},
		{name: "unrecognised app", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "appIntegrity", "appRecognitionVerdict", "UNRECOGNIZED_VERSION") //nolint:misspell // Google's verdict value
			return f.token(p)
		}},
		{name: "certificate digest mismatch", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "appIntegrity", "certificateSha256Digest",
				[]string{base64.RawURLEncoding.EncodeToString(otherDig[:])})
			return f.token(p)
		}},
		{name: "extra certificate digest", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "appIntegrity", "certificateSha256Digest", []string{
				base64.RawURLEncoding.EncodeToString(f.certDig),
				base64.RawURLEncoding.EncodeToString(otherDig[:]),
			})
			return f.token(p)
		}},
		{name: "missing certificate digest", token: func(f *fixture) string {
			p := f.verdict()
			delete(p["appIntegrity"].(map[string]any), "certificateSha256Digest")
			return f.token(p)
		}},
		{name: "undecodable certificate digest", token: func(f *fixture) string {
			p := f.verdict()
			set(p, "appIntegrity", "certificateSha256Digest", []string{"!!!"})
			return f.token(p)
		}},
		{
			name: "zero max age", token: func(f *fixture) string { return f.token(f.verdict()) },
			exp: func(e *Expect) { e.MaxAge = 0 },
		},
		{
			name: "empty package expectation", token: func(f *fixture) string { return f.token(f.verdict()) },
			exp: func(e *Expect) { e.PackageName = "" },
		},
		{
			name: "empty hash expectation", token: func(f *fixture) string { return f.token(f.verdict()) },
			exp: func(e *Expect) { e.RequestHash = "" },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			e := f.expect()
			if tc.exp != nil {
				tc.exp(&e)
			}
			tok := tc.token(f)
			// The shared fixture of this closure owns the keys the verifier uses.
			_, err := f.verifier().Verify(tok, e)
			requireAttestationFailed(t, err)
			if strings.Contains(err.Error(), tok) && tok != "" {
				t.Error("error message echoes the token")
			}
		})
	}
}

func TestVerifierMisconfigured(t *testing.T) {
	f := newFixture(t)
	tok := f.token(f.verdict())
	tests := []struct {
		name string
		v    *Verifier
	}{
		{"nil verifier", nil},
		{"no clock", &Verifier{Keys: f.keys}},
		{"no keys", &Verifier{Now: func() time.Time { return testNow }}},
		{"short decryption key", &Verifier{
			Keys: Keys{Decryption: f.aes[:16], Verification: f.keys.Verification},
			Now:  func() time.Time { return testNow },
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.v.Verify(tok, f.expect())
			requireAttestationFailed(t, err)
		})
	}
}

func TestVerifyTimestampBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		offset time.Duration
		ok     bool
	}{
		{"exactly max age", -time.Minute, true},
		{"just over max age", -time.Minute - time.Millisecond, false},
		{"exactly allowed skew", 60 * time.Second, true},
		{"just over allowed skew", 60*time.Second + time.Millisecond, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := f.verdict()
			set(p, "requestDetails", "timestampMillis", testNow.Add(tc.offset).UnixMilli())
			_, err := f.verifier().Verify(f.token(p), f.expect())
			if tc.ok != (err == nil) {
				t.Errorf("ok = %v, err = %v", tc.ok, err)
			}
		})
	}
}

func TestVerifyDeviceLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   []DeviceLabel
		best   DeviceLabel
	}{
		{
			"unknown ignored",
			[]string{"MEETS_FUTURE_INTEGRITY", "MEETS_BASIC_INTEGRITY"},
			[]DeviceLabel{LabelBasic},
			LabelBasic,
		},
		{"only unknown", []string{"SOMETHING_ELSE"}, nil, LabelNone},
		{"none", nil, nil, LabelNone},
		{"virtual only", []string{"MEETS_VIRTUAL_INTEGRITY"}, []DeviceLabel{LabelVirtual}, LabelNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := f.verdict()
			set(p, "deviceIntegrity", "deviceRecognitionVerdict", tc.labels)
			v, err := f.verifier().Verify(f.token(p), f.expect())
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if len(v.Device) != len(tc.want) {
				t.Fatalf("Device = %v, want %v", v.Device, tc.want)
			}
			for i := range tc.want {
				if v.Device[i] != tc.want[i] {
					t.Errorf("Device[%d] = %q, want %q", i, v.Device[i], tc.want[i])
				}
			}
			if v.Strongest() != tc.best {
				t.Errorf("Strongest = %q, want %q", v.Strongest(), tc.best)
			}
		})
	}
}

func TestStrongest(t *testing.T) {
	tests := []struct {
		name   string
		labels []DeviceLabel
		want   DeviceLabel
	}{
		{"empty", nil, LabelNone},
		{"virtual counts as none", []DeviceLabel{LabelVirtual}, LabelNone},
		{"basic", []DeviceLabel{LabelBasic}, LabelBasic},
		{"device over basic", []DeviceLabel{LabelBasic, LabelDevice}, LabelDevice},
		{"strong first", []DeviceLabel{LabelStrong, LabelDevice, LabelBasic}, LabelStrong},
		{"strong last", []DeviceLabel{LabelVirtual, LabelBasic, LabelDevice, LabelStrong}, LabelStrong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Verdict{Device: tc.labels}).Strongest(); got != tc.want {
				t.Errorf("Strongest = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestHash(t *testing.T) {
	sum := sha256.Sum256([]byte("chal" + "jkt"))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := RequestHash([]byte("chal"), "jkt"); got != want {
		t.Errorf("RequestHash = %q, want %q", got, want)
	}
	if RequestHash([]byte("chal"), "jkt") == RequestHash([]byte("chal"), "jkt2") {
		t.Error("thumbprint does not influence the hash")
	}
}

func TestParseKeys(t *testing.T) {
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	spki := func(pub any) string {
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(der)
	}
	goodAES := base64.StdEncoding.EncodeToString(make([]byte, 32))
	goodPub := spki(&signer.PublicKey)
	tests := []struct {
		name     string
		dec, pub string
		ok       bool
	}{
		{"valid", goodAES, goodPub, true},
		{"decryption not base64", "***secret***", goodPub, false},
		{"decryption wrong length", base64.StdEncoding.EncodeToString(make([]byte, 16)), goodPub, false},
		{"verification not base64", goodAES, "***secret***", false},
		{"verification not DER", goodAES, base64.StdEncoding.EncodeToString([]byte("junk")), false},
		{"verification wrong curve", goodAES, spki(&p384.PublicKey), false},
		{"verification RSA", goodAES, spki(&rsaKey.PublicKey), false},
		{"both empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := ParseKeys(tc.dec, tc.pub)
			if tc.ok {
				if err != nil || !k.valid() || !k.Verification.Equal(&signer.PublicKey) {
					t.Fatalf("ParseKeys: %v", err)
				}
				return
			}
			requireAttestationFailed(t, err)
			if strings.Contains(err.Error(), "secret") {
				t.Error("error echoes its input")
			}
		})
	}
}

func FuzzVerify(f *testing.F) {
	fx := newFixture(f)
	good := fx.token(fx.verdict())
	f.Add(good)
	f.Add("")
	f.Add("a.b.c.d.e")
	f.Add(`{"protected":"e30","ciphertext":"AA"}`)
	f.Add(good[:len(good)/2])
	v := fx.verifier()
	e := fx.expect()
	f.Fuzz(func(_ *testing.T, token string) {
		_, _ = v.Verify(token, e)
	})
}
