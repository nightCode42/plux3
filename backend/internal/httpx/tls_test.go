// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package httpx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// authority is a throw-away certificate authority.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// newAuthority creates a CA that is valid for an hour.
func newAuthority(t *testing.T, name string) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue writes a certificate for 127.0.0.1 signed by the authority, usable
// as a server and a client certificate, and returns the file paths.
func (a authority) issue(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	write(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certFile, keyFile
}

// write stores a test file.
func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// serveTLS serves a handler with the TLS configuration and returns its
// address.
func serveTLS(t *testing.T, cfg *tls.Config, h http.Handler) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// handshake dials the address with the client configuration and reports
// the negotiated state.
func handshake(addr string, client *tls.Config) (tls.ConnectionState, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: client}
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return tls.ConnectionState{}, err
	}
	defer func() { _ = conn.Close() }()
	return conn.(*tls.Conn).ConnectionState(), nil
}

// Verifies: SEC-040.
func TestServerTLSHandshakes(t *testing.T) {
	t.Parallel()
	ca := newAuthority(t, "ca")
	dir := t.TempDir()
	cert, key := ca.issue(t, dir, "server")
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	client := func(min, max uint16, suites ...uint16) *tls.Config {
		return &tls.Config{RootCAs: roots, MinVersion: min, MaxVersion: max, CipherSuites: suites}
	}
	cases := []struct {
		name       string
		allow12    bool
		client     *tls.Config
		wantErr    bool
		wantSuite  uint16
		wantMaxVer uint16
	}{
		{"1.3 by default", false, client(tls.VersionTLS12, tls.VersionTLS13), false, 0, tls.VersionTLS13},
		{"1.2 refused by default", false, client(tls.VersionTLS12, tls.VersionTLS12), true, 0, 0},
		{"1.3 when 1.2 is allowed", true, client(tls.VersionTLS12, tls.VersionTLS13), false, 0, tls.VersionTLS13},
		{
			"1.2 with GCM when allowed", true, client(tls.VersionTLS12, tls.VersionTLS12, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256),
			false, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.VersionTLS12,
		},
		{
			"1.2 with ChaCha20 when allowed", true, client(tls.VersionTLS12, tls.VersionTLS12, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256),
			false, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.VersionTLS12,
		},
		{
			"1.2 with CBC refused even when allowed", true, client(tls.VersionTLS12, tls.VersionTLS12, tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA),
			true, 0, 0,
		},
		{
			"1.2 with AES-256 CBC refused even when allowed", true, client(tls.VersionTLS12, tls.VersionTLS12, tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA),
			true, 0, 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := ServerTLS(ServerTLSOptions{CertFile: cert, KeyFile: key, AllowTLS12: tc.allow12})
			if err != nil {
				t.Fatal(err)
			}
			addr := serveTLS(t, cfg, http.NotFoundHandler())
			tc.client.ServerName = "127.0.0.1"
			state, err := handshake(addr, tc.client)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("handshake succeeded with %s", tls.VersionName(state.Version))
				}
				return
			}
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			if state.Version != tc.wantMaxVer || (tc.wantSuite != 0 && state.CipherSuite != tc.wantSuite) {
				t.Errorf("negotiated %s %s", tls.VersionName(state.Version), tls.CipherSuiteName(state.CipherSuite))
			}
		})
	}
}

// Verifies: SEC-040.
func TestServerTLSOffersOnlyAEADECDHESuites(t *testing.T) {
	t.Parallel()
	ca := newAuthority(t, "ca")
	cert, key := ca.issue(t, t.TempDir(), "server")
	cfg, err := ServerTLS(ServerTLSOptions{CertFile: cert, KeyFile: key, AllowTLS12: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 || len(cfg.CipherSuites) != 6 {
		t.Fatalf("min %x, suites %d", cfg.MinVersion, len(cfg.CipherSuites))
	}
	for _, id := range cfg.CipherSuites {
		name := tls.CipherSuiteName(id)
		if !strings.HasPrefix(name, "TLS_ECDHE_") || (!strings.Contains(name, "_GCM_") && !strings.Contains(name, "CHACHA20_POLY1305")) {
			t.Errorf("suite %s is not an AEAD ECDHE suite", name)
		}
	}
	strict, err := ServerTLS(ServerTLSOptions{CertFile: cert, KeyFile: key})
	if err != nil || strict.MinVersion != tls.VersionTLS13 {
		t.Errorf("default minimum = %x, %v", strict.MinVersion, err)
	}
}

// Verifies: SEC-040.
func TestServerTLSNeedsAKeyPair(t *testing.T) {
	t.Parallel()
	if _, err := ServerTLS(ServerTLSOptions{CertFile: "/nonexistent.crt", KeyFile: "/nonexistent.key"}); err == nil {
		t.Error("a missing certificate was accepted")
	}
}

// Verifies: SEC-043.
func TestInternalTLSRequiresAClientCertificateOfTheCA(t *testing.T) {
	t.Parallel()
	ca, other := newAuthority(t, "internal"), newAuthority(t, "other")
	dir := t.TempDir()
	srvCert, srvKey := ca.issue(t, dir, "api")
	cfg, err := InternalServerTLS(InternalTLSOptions{CAFile: writeCA(t, dir, "ca.pem", ca), CertFile: srvCert, KeyFile: srvKey})
	if err != nil {
		t.Fatal(err)
	}
	addr := serveTLS(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Peer", r.TLS.PeerCertificates[0].Subject.CommonName)
	}))

	workerCert, workerKey := ca.issue(t, dir, "worker")
	rogueCert, rogueKey := other.issue(t, dir, "rogue")
	good := InternalTLSOptions{CAFile: writeCA(t, dir, "ca.pem", ca), CertFile: workerCert, KeyFile: workerKey}

	client, err := NewInternalClient(good, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fetch(t, client, "https://"+addr+"/")
	if err != nil {
		t.Fatalf("a role with a certificate of the CA was refused: %v", err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("X-Peer") != "worker" || resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("peer %q over %s", resp.Header.Get("X-Peer"), tls.VersionName(resp.TLS.Version))
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	rogue, err := tls.LoadX509KeyPair(rogueCert, rogueKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*tls.Config{
		"no certificate":              {RootCAs: roots},
		"a certificate of another CA": {RootCAs: roots, Certificates: []tls.Certificate{rogue}},
	} {
		c.ServerName = "127.0.0.1"
		// With TLS 1.3 the client finishes its handshake before the
		// server judges its certificate, so the refusal arrives with the
		// first read.
		if err := roundTrip(addr, c); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Verifies: SEC-043.
func TestInternalClientRefusesAServerOfAnotherCA(t *testing.T) {
	t.Parallel()
	ca, other := newAuthority(t, "internal"), newAuthority(t, "other")
	dir := t.TempDir()
	impostorCert, impostorKey := other.issue(t, dir, "impostor")
	cfg, err := InternalServerTLS(InternalTLSOptions{CAFile: writeCA(t, dir, "other.pem", other), CertFile: impostorCert, KeyFile: impostorKey})
	if err != nil {
		t.Fatal(err)
	}
	addr := serveTLS(t, cfg, http.NotFoundHandler())
	cert, key := ca.issue(t, dir, "worker")
	client, err := NewInternalClient(InternalTLSOptions{CAFile: writeCA(t, dir, "ca.pem", ca), CertFile: cert, KeyFile: key}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := fetch(t, client, "https://"+addr+"/"); err == nil {
		_ = resp.Body.Close()
		t.Error("a server of another CA was trusted")
	}
}

// Verifies: SEC-043.
func TestInternalTLSNeedsItsFiles(t *testing.T) {
	t.Parallel()
	ca := newAuthority(t, "internal")
	dir := t.TempDir()
	cert, key := ca.issue(t, dir, "role")
	write(t, filepath.Join(dir, "empty.pem"), []byte("not a certificate"))
	for name, o := range map[string]InternalTLSOptions{
		"missing CA":  {CAFile: filepath.Join(dir, "none.pem"), CertFile: cert, KeyFile: key},
		"junk CA":     {CAFile: filepath.Join(dir, "empty.pem"), CertFile: cert, KeyFile: key},
		"missing key": {CAFile: writeCA(t, dir, "ca.pem", ca), CertFile: cert, KeyFile: filepath.Join(dir, "none.key")},
	} {
		if _, err := NewInternalClient(o, time.Second); err == nil {
			t.Errorf("%s: client accepted", name)
		}
		if _, err := InternalServerTLS(o); err == nil {
			t.Errorf("%s: listener accepted", name)
		}
	}
}

// writeCA stores the authority's certificate and returns its path.
func writeCA(t *testing.T, dir, name string, a authority) string {
	t.Helper()
	path := filepath.Join(dir, name)
	write(t, path, a.pem)
	return path
}

// roundTrip completes a handshake and one read, returning the first
// error either reports.
func roundTrip(addr string, c *tls.Config) error {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: c}
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	return err
}

// Verifies: SEC-040.
func TestHSTSValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		maxAge  time.Duration
		sub     bool
		preload bool
		want    string
	}{
		{2 * 365 * 24 * time.Hour, true, false, "max-age=63072000; includeSubDomains"},
		{2 * 365 * 24 * time.Hour, true, true, "max-age=63072000; includeSubDomains; preload"},
		{time.Hour, false, false, "max-age=3600"},
		{0, true, true, ""},
	} {
		if got := HSTSValue(tc.maxAge, tc.sub, tc.preload); got != tc.want {
			t.Errorf("HSTSValue(%v, %v, %v) = %q, want %q", tc.maxAge, tc.sub, tc.preload, got, tc.want)
		}
	}
}

// Verifies: SEC-040.
func TestHSTSCoversSuccessAndErrorResponses(t *testing.T) {
	t.Parallel()
	const want = "max-age=63072000; includeSubDomains"
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/boom", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	h := HSTS(mux, want)
	for path, status := range map[string]int{"/ok": 200, "/boom": 500, "/missing": 404} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rec.Code != status || rec.Header().Get("Strict-Transport-Security") != want {
			t.Errorf("%s: status %d, header %q", path, rec.Code, rec.Header().Get("Strict-Transport-Security"))
		}
	}
	rec := httptest.NewRecorder()
	HSTS(mux, "").ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ok", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("a disabled HSTS set %q", got)
	}
}

// fetch makes a GET request with the test's context.
func fetch(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req) //nolint:wrapcheck // a test helper
}
