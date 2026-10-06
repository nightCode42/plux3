// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// started is a running refapi.
type started struct {
	url  string
	ca   []byte
	done chan error
	stop context.CancelFunc
}

// startRun runs the command as the e2e driver does and waits for the line
// it announces itself with.
func startRun(t *testing.T) *started {
	t.Helper()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := run(ctx, []string{"-ca-out", caFile}, pw)
		_ = pw.Close()
		done <- err
	}()
	t.Cleanup(func() { cancel(); _ = pr.Close() })
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		cancel()
		t.Fatalf("no announcement: %v (run: %v)", err, <-done)
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	return &started{url: strings.TrimSpace(strings.TrimPrefix(line, "refapi listening on ")), ca: ca, done: done, stop: cancel}
}

// get sends a GET request with the test's context.
func get(t *testing.T, client *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client.Do(req)
}

// trusting is a client that trusts the pem certificates and nothing else.
func trusting(t *testing.T, pem []byte) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("the CA file holds no certificate")
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
}

func TestRunAnnouncesItselfAndServesTLSToAClientTrustingOnlyItsCA(t *testing.T) {
	s := startRun(t)
	if !regexp.MustCompile(`^https://127\.0\.0\.1:[0-9]+$`).MatchString(s.url) {
		t.Fatalf("announced %q", s.url)
	}
	client := trusting(t, s.ca)
	res, err := get(t, client, s.url+"/express/v1/products/p-001")
	if err != nil {
		t.Fatalf("a client trusting the written CA cannot connect: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || res.TLS == nil || res.ProtoMajor != 1 {
		t.Fatalf("answered %d over %v as HTTP/%d", res.StatusCode, res.TLS != nil, res.ProtoMajor)
	}

	// The same address through localhost and ::1's name is valid too.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.url, "https://"))
	res, err = get(t, client, "https://localhost:"+port+"/express/v1/products/p-001")
	if err == nil {
		_ = res.Body.Close()
	}
	// localhost may resolve to ::1 only, where nothing listens; a failure
	// then is a refused connection, never a certificate error.
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &unknown) || errors.As(err, &hostname) {
		t.Fatalf("localhost is not covered by the certificate: %v", err)
	}
}

func TestRunRefusesClientsThatDoNotTrustItsCA(t *testing.T) {
	s := startRun(t)
	if res, err := get(t, &http.Client{Timeout: 10 * time.Second}, s.url+"/express/v1/products"); err == nil {
		_ = res.Body.Close()
		t.Fatal("a client with the system roots connected")
	}
	other, err := newAuthority(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res, err := get(t, trusting(t, other.certificatePEM()), s.url+"/express/v1/products"); err == nil {
		_ = res.Body.Close()
		t.Fatal("a client trusting another CA connected")
	}
	// Cleartext is not served at all.
	if res, err := get(t, http.DefaultClient, strings.Replace(s.url, "https://", "http://", 1)+"/express/v1/products"); err == nil {
		defer res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Fatal("the API answered over cleartext HTTP")
		}
	}
}

func TestRunWritesOnlyTheCertificate(t *testing.T) {
	s := startRun(t)
	if !strings.HasPrefix(string(s.ca), "-----BEGIN CERTIFICATE-----") || strings.Contains(string(s.ca), "PRIVATE KEY") {
		t.Fatalf("the CA file holds:\n%s", s.ca)
	}
}

func TestRunStopsWhenItsContextEnds(t *testing.T) {
	s := startRun(t)
	s.stop()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop")
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no CA output", nil},
		{"an unknown flag", []string{"-ca-out", "x.pem", "-nope"}},
		{"an address that cannot be bound", []string{"-ca-out", filepath.Join(t.TempDir(), "ca.pem"), "-addr", "256.0.0.1:1"}},
		{"a CA path that cannot be written", []string{"-ca-out", filepath.Join(t.TempDir(), "missing", "ca.pem")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := run(context.Background(), tc.args, io.Discard); err == nil {
				t.Fatal("run succeeded")
			}
		})
	}
}

func TestAuthorityIssuesCertificatesForLoopback(t *testing.T) {
	now := time.Now()
	ca, err := newAuthority(now)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ca.issue(now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
		opts := x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now}
		if _, err := leaf.Verify(opts); err != nil {
			t.Errorf("the certificate is not valid for %s: %v", name, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "example.com", CurrentTime: now}); err == nil {
		t.Error("the certificate is valid for example.com")
	}
	if got := ca.cert.PublicKeyAlgorithm; got != x509.ECDSA {
		t.Errorf("CA key algorithm %v, want ECDSA", got)
	}
	if !ca.cert.IsCA || ca.cert.MaxPathLen != 0 {
		t.Error("the CA is not a CA that signs leaves only")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost", CurrentTime: now.Add(30 * 24 * time.Hour)}); err == nil {
		t.Error("the certificate does not expire")
	}
}
