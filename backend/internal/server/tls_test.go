// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/observability"
)

// identity writes a self-signed certificate for 127.0.0.1 that serves
// as server certificate, client certificate and CA alike, and returns
// its files.
func identity(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "plux-test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

// newServer assembles a server from a configuration body.
func newServer(t *testing.T, body string) *Server {
	t.Helper()
	s, err := New(Deps{Config: testConfig(t, body), Log: discard(), Metrics: observability.NewMetrics()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// Verifies: SEC-040.
// Every response of the api role carries HSTS, errors and the router's
// own 404 included.
func TestEveryResponseCarriesHSTS(t *testing.T) {
	t.Parallel()
	s := newServer(t, base)
	s.Register("/boom", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	for path, status := range map[string]int{"/livez": 200, "/boom": 500, "/nowhere": 404} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		const want = "max-age=63072000; includeSubDomains"
		if rec.Code != status || rec.Header().Get("Strict-Transport-Security") != want {
			t.Errorf("%s: status %d, HSTS %q", path, rec.Code, rec.Header().Get("Strict-Transport-Security"))
		}
	}
}

// Verifies: SEC-040.
func TestHSTSFollowsTheConfiguration(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ extra, want string }{
		"preload":      {"  hsts:\n    preload: true\n", "max-age=63072000; includeSubDomains; preload"},
		"no subdomain": {"  hsts:\n    includeSubDomains: false\n    maxAge: 24h\n", "max-age=86400"},
		"disabled":     {"  hsts:\n    maxAge: 0s\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newServer(t, "server:\n  publicBaseURL: \"https://plux.example\"\n"+tc.extra+"database:\n  url: \"postgres://plux@db/plux\"\n")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil))
			if got := rec.Header().Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("HSTS = %q, want %q", got, tc.want)
			}
		})
	}
}

// Verifies: SEC-040.
// A configured certificate makes the api role speak TLS 1.3 and HTTP/2,
// and refuse TLS 1.2 unless the installation allows it.
func TestAPIRoleTerminatesTLS(t *testing.T) {
	t.Parallel()
	certFile, keyFile, pool := identity(t)
	tlsBody := func(extra string) string {
		return "server:\n  publicBaseURL: \"https://plux.example\"\n  tls:\n    certFile: " + certFile +
			"\n    keyFile: " + keyFile + "\n" + extra + "database:\n  url: \"postgres://plux@db/plux\"\n"
	}
	for name, tc := range map[string]struct {
		extra     string
		maxClient uint16
		wantErr   bool
	}{
		"1.3":                  {"", tls.VersionTLS13, false},
		"1.2 refused":          {"", tls.VersionTLS12, true},
		"1.2 when allowed":     {"    allowTLS12: true\n", tls.VersionTLS12, false},
		"1.3 when 1.2 allowed": {"    allowTLS12: true\n", tls.VersionTLS13, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newServer(t, tlsBody(tc.extra))
			addr := serve(t, s.http)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
				ForceAttemptHTTP2: true,
				TLSClientConfig:   &tls.Config{RootCAs: pool, MaxVersion: tc.maxClient},
			}}
			resp, err := get(t, client, "https://"+addr+"/livez")
			if tc.wantErr {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("TLS 1.2 was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.TLS.Version != tc.maxClient || resp.Header.Get("Strict-Transport-Security") == "" {
				t.Errorf("negotiated %s, HSTS %q", tls.VersionName(resp.TLS.Version), resp.Header.Get("Strict-Transport-Security"))
			}
			if tc.maxClient == tls.VersionTLS13 && resp.ProtoMajor != 2 {
				t.Errorf("protocol HTTP/%d, want HTTP/2", resp.ProtoMajor)
			}
		})
	}
}

// Verifies: SEC-040.
func TestAPIRoleSpeaksPlainHTTPWithoutACertificate(t *testing.T) {
	t.Parallel()
	s := newServer(t, base)
	if s.http.TLSConfig != nil || s.internal != nil {
		t.Errorf("TLS %v, internal listener %v without being configured", s.http.TLSConfig, s.internal)
	}
}

// Verifies: SEC-040.
func TestNewRefusesAnUnreadableCertificate(t *testing.T) {
	t.Parallel()
	body := "server:\n  publicBaseURL: \"https://plux.example\"\n  tls:\n    certFile: /nonexistent.crt\n    keyFile: /nonexistent.key\n" +
		"database:\n  url: \"postgres://plux@db/plux\"\n"
	if _, err := New(Deps{Config: testConfig(t, body), Log: discard()}); err == nil {
		t.Error("New accepted a missing certificate")
	}
}

// Verifies: SEC-043.
// The internal listener serves only a caller that presents a certificate
// of the internal CA.
func TestInternalListenerRequiresMutualTLS(t *testing.T) {
	t.Parallel()
	certFile, keyFile, pool := identity(t)
	s := newServer(t, "server:\n  publicBaseURL: \"https://plux.example\"\n  internalListen: \"127.0.0.1:0\"\n"+
		"  internalTLS:\n    caFile: "+certFile+"\n    certFile: "+certFile+"\n    keyFile: "+keyFile+"\n"+
		"database:\n  url: \"postgres://plux@db/plux\"\n")
	s.RegisterInternal("GET /internal/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	addr := serve(t, s.internal)

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	get := func(c *tls.Config) (*http.Response, error) {
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: c}}
		return get(t, client, "https://"+addr+"/internal/ping")
	}
	resp, err := get(&tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("a caller with a certificate was refused: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("status %d over %s", resp.StatusCode, tls.VersionName(resp.TLS.Version))
	}
	if resp, err := get(&tls.Config{RootCAs: pool}); err == nil {
		_ = resp.Body.Close()
		t.Error("a caller without a certificate was served")
	}
	if s.http.TLSConfig != nil {
		t.Error("the internal certificate leaked onto the public listener")
	}
}

// serve runs the server on a free loopback port and returns the address.
func serve(t *testing.T, srv *http.Server) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if srv.TLSConfig != nil {
			_ = srv.ServeTLS(ln, "", "")
			return
		}
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() { _ = srv.Close() })
	return fmt.Sprint(ln.Addr())
}
