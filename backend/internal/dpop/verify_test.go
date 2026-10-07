// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	testMethod = "POST"
	testURL    = "https://api.example.com/v1/token"
	testWindow = 60 * time.Second
)

// testNow is the fixed verifier clock.
var testNow = time.Unix(1_800_000_000, 0).UTC()

func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// jwkOf returns the public JWK members of a P-256 key.
func jwkOf(pub *ecdsa.PublicKey) map[string]any {
	point, _ := pub.Bytes()
	return map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   b64(point[1:33]),
		"y":   b64(point[33:]),
	}
}

func goodHeader(key *ecdsa.PrivateKey) map[string]any {
	return map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": jwkOf(&key.PublicKey)}
}

func goodClaims() map[string]any {
	return map[string]any{
		"htm": testMethod,
		"htu": testURL,
		"iat": testNow.Unix(),
		"jti": "jti-0001",
	}
}

// signRaw builds a compact ES256 JWS from arbitrary header and claims, so
// that tests can produce proofs go-jose itself would refuse to build.
func signRaw(t testing.TB, key *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + b64(sig)
}

func testVerifier() *Verifier {
	return &Verifier{Now: func() time.Time { return testNow }}
}

func testExpect() Expect {
	return Expect{Method: testMethod, URL: testURL, Window: testWindow}
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code plxerr.Code) {
	t.Helper()
	got, ok := plxerr.CodeOf(err)
	if !ok || got != code {
		t.Fatalf("error = %v, want code %v", err, code)
	}
}

func athOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return b64(sum[:])
}

// Verifies: SEC-021, SEC-022.
func TestVerifyAccepts(t *testing.T) {
	key := newKey(t)
	wantJKT, err := Thumbprint(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("hand-built proof", func(t *testing.T) {
		p, err := testVerifier().Verify(signRaw(t, key, goodHeader(key), goodClaims()), testExpect())
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if p.JKT != wantJKT || p.JTI != "jti-0001" || !p.IssuedAt.Equal(testNow) || !p.Key.Equal(&key.PublicKey) {
			t.Fatalf("Proof = %+v", p)
		}
	})

	t.Run("go-jose proof with access token", func(t *testing.T) {
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.ES256, Key: key},
			(&jose.SignerOptions{EmbedJWK: true}).WithType("dpop+jwt"),
		)
		if err != nil {
			t.Fatal(err)
		}
		c := goodClaims()
		c["ath"] = athOf("access-token")
		payload, _ := json.Marshal(c)
		jws, err := signer.Sign(payload)
		if err != nil {
			t.Fatal(err)
		}
		compact, err := jws.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		e := testExpect()
		e.AccessToken = "access-token"
		p, err := testVerifier().Verify(compact, e)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if p.JKT != wantJKT {
			t.Fatalf("JKT = %q, want %q", p.JKT, wantJKT)
		}
	})

	t.Run("window edges are inclusive", func(t *testing.T) {
		for _, off := range []time.Duration{-testWindow, testWindow} {
			c := goodClaims()
			c["iat"] = testNow.Add(off).Unix()
			if _, err := testVerifier().Verify(signRaw(t, key, goodHeader(key), c), testExpect()); err != nil {
				t.Fatalf("offset %v: %v", off, err)
			}
		}
	})

	t.Run("jti of 64 characters", func(t *testing.T) {
		c := goodClaims()
		c["jti"] = strings.Repeat("j", 64)
		if _, err := testVerifier().Verify(signRaw(t, key, goodHeader(key), c), testExpect()); err != nil {
			t.Fatal(err)
		}
	})
}

// Verifies: SEC-021, SEC-022.
func TestVerifyRejects(t *testing.T) {
	key := newKey(t)
	other := newKey(t)
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validJWK := jwkOf(&key.PublicKey)

	tests := []struct {
		name   string
		check  string
		edit   func(h, c map[string]any)
		expect func(e *Expect)
	}{
		{name: "typ missing", check: "typ", edit: func(h, _ map[string]any) { delete(h, "typ") }},
		{name: "typ wrong", check: "typ", edit: func(h, _ map[string]any) { h["typ"] = "JWT" }},
		{name: "typ case", check: "typ", edit: func(h, _ map[string]any) { h["typ"] = "DPoP+JWT" }},
		{name: "alg none", check: "alg", edit: func(h, _ map[string]any) { h["alg"] = "none" }},
		{name: "alg HS256", check: "alg", edit: func(h, _ map[string]any) { h["alg"] = "HS256" }},
		{name: "alg RS256", check: "alg", edit: func(h, _ map[string]any) { h["alg"] = "RS256" }},
		{name: "alg ES384", check: "alg", edit: func(h, _ map[string]any) { h["alg"] = "ES384" }},
		{name: "alg missing", check: "alg", edit: func(h, _ map[string]any) { delete(h, "alg") }},
		{name: "jwk missing", check: "jwk", edit: func(h, _ map[string]any) { delete(h, "jwk") }},
		{name: "jwk null", check: "jwk", edit: func(h, _ map[string]any) { h["jwk"] = nil }},
		{name: "jwk private", check: "jwk", edit: func(h, _ map[string]any) {
			j := maps.Clone(validJWK)
			d, _ := key.Bytes()
			j["d"] = b64(d)
			h["jwk"] = j
		}},
		{name: "jwk P-384", check: "jwk", edit: func(h, _ map[string]any) {
			h["jwk"] = map[string]any{
				"kty": "EC", "crv": "P-384",
				"x": b64(p384.X.FillBytes(make([]byte, 48))), "y": b64(p384.Y.FillBytes(make([]byte, 48))),
			}
		}},
		{name: "jwk RSA", check: "jwk", edit: func(h, _ map[string]any) {
			h["jwk"] = map[string]any{"kty": "RSA", "n": "AQAB", "e": "AQAB"}
		}},
		{name: "jwk off curve", check: "jwk", edit: func(h, _ map[string]any) {
			j := maps.Clone(validJWK)
			j["y"] = b64(make([]byte, 32))
			h["jwk"] = j
		}},
		{name: "jwk oct", check: "jwk", edit: func(h, _ map[string]any) {
			h["jwk"] = map[string]any{"kty": "oct", "k": "AAAA"}
		}},
		{name: "kid present", check: "header parameters", edit: func(h, _ map[string]any) { h["kid"] = "k1" }},
		{name: "x5c present", check: "header parameters", edit: func(h, _ map[string]any) { h["x5c"] = []string{"AAAA"} }},
		{name: "jku present", check: "header parameters", edit: func(h, _ map[string]any) { h["jku"] = "https://evil.example/jwks" }},
		{name: "kid null", check: "header parameters", edit: func(h, _ map[string]any) { h["kid"] = nil }},
		{name: "signature by another key", check: "signature", edit: nil},
		{name: "claims not an object", check: "claims", edit: func(_, c map[string]any) { c["htm"] = 7 }},
		{name: "htm case", check: "htm", edit: func(_, c map[string]any) { c["htm"] = "post" }},
		{name: "htm other method", check: "htm", edit: func(_, c map[string]any) { c["htm"] = "GET" }},
		{name: "htm missing", check: "htm", edit: func(_, c map[string]any) { delete(c, "htm") }},
		{name: "htu other path", check: "htu", edit: func(_, c map[string]any) { c["htu"] = "https://api.example.com/v1/other" }},
		{name: "htu other host", check: "htu", edit: func(_, c map[string]any) { c["htu"] = "https://evil.example.com/v1/token" }},
		{name: "htu missing", check: "htu", edit: func(_, c map[string]any) { delete(c, "htu") }},
		{name: "htu relative", check: "htu", edit: func(_, c map[string]any) { c["htu"] = "/v1/token" }},
		{name: "expected url unusable", check: "htu", expect: func(e *Expect) { e.URL = "::" }},
		{name: "empty expected method", check: "htm", expect: func(e *Expect) { e.Method = "" }, edit: func(_, c map[string]any) { c["htm"] = "" }},
		{name: "iat too old", check: "iat", edit: func(_, c map[string]any) { c["iat"] = testNow.Add(-testWindow - time.Second).Unix() }},
		{name: "iat in the future", check: "iat", edit: func(_, c map[string]any) { c["iat"] = testNow.Add(testWindow + time.Second).Unix() }},
		{name: "iat missing", check: "iat", edit: func(_, c map[string]any) { delete(c, "iat") }},
		{name: "iat huge", check: "iat", edit: func(_, c map[string]any) { c["iat"] = 1e300 }},
		{name: "jti missing", check: "jti", edit: func(_, c map[string]any) { delete(c, "jti") }},
		{name: "jti empty", check: "jti", edit: func(_, c map[string]any) { c["jti"] = "" }},
		{name: "jti too long", check: "jti", edit: func(_, c map[string]any) { c["jti"] = strings.Repeat("j", 65) }},
		{name: "ath missing", check: "ath", expect: func(e *Expect) { e.AccessToken = "tok" }},
		{name: "ath wrong", check: "ath", expect: func(e *Expect) { e.AccessToken = "tok" }, edit: func(_, c map[string]any) { c["ath"] = athOf("other") }},
		{name: "ath unexpected", check: "ath", edit: func(_, c map[string]any) { c["ath"] = athOf("tok") }},
		{name: "ath empty and unexpected", check: "ath", edit: func(_, c map[string]any) { c["ath"] = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, c := goodHeader(key), goodClaims()
			if tc.edit != nil {
				tc.edit(h, c)
			}
			signer := key
			if tc.name == "signature by another key" {
				signer = other
			}
			e := testExpect()
			if tc.expect != nil {
				tc.expect(&e)
			}
			_, err := testVerifier().Verify(signRaw(t, signer, h, c), e)
			wantCode(t, err, plxerr.DPoPProofInvalid)
			if !strings.Contains(err.Error(), `"`+tc.check+`"`) {
				t.Fatalf("error = %v, want check %q", err, tc.check)
			}
		})
	}
}

// Verifies: SEC-021.
func TestVerifyRejectsMalformed(t *testing.T) {
	key := newKey(t)
	good := signRaw(t, key, goodHeader(key), goodClaims())
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + b64([]byte(`{"htm":"POST","htu":"`+testURL+`","iat":1800000000,"jti":"x"}`)) + "." + parts[2]
	shortSig := parts[0] + "." + parts[1] + "." + b64([]byte("short"))
	jsonForm, _ := json.Marshal(map[string]any{"protected": parts[0], "payload": parts[1], "signature": parts[2]})

	tests := []struct {
		name, proof, check string
	}{
		{"empty", "", "format"},
		{"one segment", parts[0], "format"},
		{"two segments", parts[0] + "." + parts[1], "format"},
		{"four segments", good + ".AAAA", "format"},
		{"empty signature", parts[0] + "." + parts[1] + ".", "format"},
		{"empty payload", parts[0] + ".." + parts[2], "format"},
		{"header not base64", "!!!." + parts[1] + "." + parts[2], "format"},
		{"header not json", b64([]byte("nope")) + "." + parts[1] + "." + parts[2], "header"},
		{"json serialisation", string(jsonForm), "format"},
		{"payload tampered", tampered, "signature"},
		{"signature truncated", shortSig, "signature"},
		{"size", strings.Repeat("a", maxProofSize+1), "size"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testVerifier().Verify(tc.proof, testExpect())
			wantCode(t, err, plxerr.DPoPProofInvalid)
			if !strings.Contains(err.Error(), `"`+tc.check+`"`) {
				t.Fatalf("error = %v, want check %q", err, tc.check)
			}
		})
	}

	t.Run("exactly the size limit passes the size check", func(t *testing.T) {
		_, err := testVerifier().Verify(strings.Repeat("a", maxProofSize), testExpect())
		if err == nil || strings.Contains(err.Error(), `"size"`) {
			t.Fatalf("error = %v, want a failure other than size", err)
		}
	})
}

// Verifies: SEC-022.
func TestVerifyChecksInOrder(t *testing.T) {
	key := newKey(t)
	c := goodClaims()
	c["htm"] = "GET"
	c["iat"] = 1
	c["jti"] = ""
	_, err := testVerifier().Verify(signRaw(t, key, goodHeader(key), c), testExpect())
	if err == nil || !strings.Contains(err.Error(), `"htm"`) {
		t.Fatalf("error = %v, want the htm check first", err)
	}
}

// Verifies: SEC-092.
func TestVerifyErrorsHideContent(t *testing.T) {
	key := newKey(t)
	c := goodClaims()
	c["jti"] = "secret-jti-value"
	c["htu"] = "https://secret.example.net/path"
	proof := signRaw(t, key, goodHeader(key), c)
	_, err := testVerifier().Verify(proof, testExpect())
	if err == nil {
		t.Fatal("want an error")
	}
	for _, leak := range []string{"secret-jti-value", "secret.example.net", proof} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error %q leaks %q", err, leak)
		}
	}
}

// Verifies: SEC-024.
func TestVerifyNonce(t *testing.T) {
	key := newKey(t)
	clock := testNow
	nonces, err := NewNonces(make([]byte, 32), time.Minute, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{Nonces: nonces, Now: func() time.Time { return clock }}
	withNonce := func(n any) string {
		c := goodClaims()
		c["iat"] = clock.Unix()
		if n != nil {
			c["nonce"] = n
		}
		return signRaw(t, key, goodHeader(key), c)
	}

	if _, err := v.Verify(withNonce(nonces.Current()), testExpect()); err != nil {
		t.Fatalf("current nonce: %v", err)
	}
	old := nonces.Current()
	clock = clock.Add(time.Minute)
	if _, err := v.Verify(withNonce(old), testExpect()); err != nil {
		t.Fatalf("previous nonce: %v", err)
	}
	clock = clock.Add(time.Minute)
	for name, n := range map[string]any{
		"missing":  nil,
		"stale":    old,
		"forged":   b64(make([]byte, nonceLen)),
		"empty":    "",
		"not text": 12,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(withNonce(n), testExpect())
			if name == "not text" {
				wantCode(t, err, plxerr.DPoPProofInvalid)
				return
			}
			wantCode(t, err, plxerr.DPoPNonceRequired)
		})
	}

	t.Run("nonce not required when unset", func(t *testing.T) {
		plain := &Verifier{Now: func() time.Time { return clock }}
		if _, err := plain.Verify(withNonce(nil), testExpect()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("other failures win over the nonce", func(t *testing.T) {
		c := goodClaims()
		c["iat"] = clock.Unix()
		c["htm"] = "GET"
		_, err := v.Verify(signRaw(t, key, goodHeader(key), c), testExpect())
		wantCode(t, err, plxerr.DPoPProofInvalid)
	})
}

// Verifies: SEC-022.
func TestNormaliseURL(t *testing.T) {
	tests := []struct {
		in, want string
		bad      bool
	}{
		{in: "https://api.example.com/v1/token", want: "https://api.example.com/v1/token"},
		{in: "HTTPS://API.Example.COM/v1/token", want: "https://api.example.com/v1/token"},
		{in: "https://api.example.com:443/v1/token", want: "https://api.example.com/v1/token"},
		{in: "http://api.example.com:80/v1", want: "http://api.example.com/v1"},
		{in: "https://api.example.com:80/v1", want: "https://api.example.com:80/v1"},
		{in: "http://api.example.com:443/v1", want: "http://api.example.com:443/v1"},
		{in: "https://api.example.com:8443/v1", want: "https://api.example.com:8443/v1"},
		{in: "https://api.example.com/v1/token?a=b&c=d", want: "https://api.example.com/v1/token"},
		{in: "https://api.example.com/v1/token#frag", want: "https://api.example.com/v1/token"},
		{in: "https://api.example.com/v1/token?a=b#frag", want: "https://api.example.com/v1/token"},
		{in: "https://api.example.com/v1/token/", want: "https://api.example.com/v1/token/"},
		{in: "https://api.example.com/V1/Token", want: "https://api.example.com/V1/Token"},
		{in: "https://api.example.com", want: "https://api.example.com"},
		{in: "https://api.example.com/", want: "https://api.example.com/"},
		{in: "https://api.example.com/a%2Fb", want: "https://api.example.com/a%2Fb"},
		{in: "https://user:pw@api.example.com/v1", want: "https://api.example.com/v1"}, //nolint:gosec // G101: a test URL
		{in: "https://[::1]:443/v1", want: "https://[::1]/v1"},
		{in: "https://[::1]:8080/v1", want: "https://[::1]:8080/v1"},
		{in: "/v1/token", bad: true},
		{in: "api.example.com/v1", bad: true},
		{in: "ftp://api.example.com/v1", bad: true},
		{in: "https:///v1", bad: true},
		{in: "", bad: true},
		{in: "https://api.example.com/%zz", bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normaliseURL(tc.in)
			if tc.bad {
				if err == nil {
					t.Fatalf("normaliseURL(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("normaliseURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// Verifies: SEC-022.
func TestVerifyHTUNormalisation(t *testing.T) {
	key := newKey(t)
	tests := []struct {
		name, htu, expected string
		ok                  bool
	}{
		{"case and default port", "HTTPS://Api.Example.com:443/v1/token?x=1#f", testURL, true},
		{"expected side normalised too", testURL, "https://API.example.com:443/v1/token?q=1", true},
		{"trailing slash differs", "https://api.example.com/v1/token/", testURL, false},
		{"other port differs", "https://api.example.com:8443/v1/token", testURL, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := goodClaims()
			c["htu"] = tc.htu
			e := testExpect()
			e.URL = tc.expected
			_, err := testVerifier().Verify(signRaw(t, key, goodHeader(key), c), e)
			if tc.ok != (err == nil) {
				t.Fatalf("Verify = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestVerifierDefaultClock(t *testing.T) {
	key := newKey(t)
	c := goodClaims()
	c["iat"] = time.Now().Unix()
	if _, err := (&Verifier{}).Verify(signRaw(t, key, goodHeader(key), c), testExpect()); err != nil {
		t.Fatalf("Verify with the default clock: %v", err)
	}
}

// Verifies: SEC-021.
func TestThumbprint(t *testing.T) {
	key := newKey(t)
	// RFC 7638 §3: the SHA-256 hash of the member-sorted, whitespace-free
	// JSON of the required members.
	canonical := `{"crv":"P-256","kty":"EC","x":"` + b64(key.X.FillBytes(make([]byte, 32))) +
		`","y":"` + b64(key.Y.FillBytes(make([]byte, 32))) + `"}`
	sum := sha256.Sum256([]byte(canonical))
	want := b64(sum[:])

	got, err := Thumbprint(&key.PublicKey)
	if err != nil || got != want {
		t.Fatalf("Thumbprint = %q, %v; want %q", got, err, want)
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, pub := range map[string]*ecdsa.PublicKey{"nil": nil, "P-384": &p384.PublicKey} {
		t.Run(name, func(t *testing.T) {
			_, err := Thumbprint(pub)
			wantCode(t, err, plxerr.DPoPProofInvalid)
		})
	}
}

// Verifies: SEC-020.
func TestCheckBinding(t *testing.T) {
	tests := []struct {
		name, token, proof string
		ok                 bool
	}{
		{"equal", "abc", "abc", true},
		{"different", "abc", "abd", false},
		{"prefix", "abc", "abcd", false},
		{"empty both", "", "", false},
		{"empty token", "", "abc", false},
		{"empty proof", "abc", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBinding(tc.token, tc.proof)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			wantCode(t, err, plxerr.TokenBindingMismatch)
		})
	}
}

func FuzzVerify(f *testing.F) {
	key := newKey(f)
	good := signRaw(f, key, goodHeader(key), goodClaims())
	f.Add(good)
	f.Add("")
	f.Add("a.b.c")
	f.Add("..")
	f.Add(`{"payload":"","signatures":[]}`)
	parts := strings.Split(good, ".")
	f.Add(parts[0] + "." + parts[1] + ".")
	f.Add(strings.Repeat("a.", 5000))
	v := testVerifier()
	f.Fuzz(func(_ *testing.T, proof string) {
		_, _ = v.Verify(proof, testExpect())
	})
}
