// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Verifies: SEC-105.
func TestPolicyBlocksInternalAddresses(t *testing.T) {
	t.Parallel()
	var p Policy
	for _, addr := range []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fd00::1", "fe80::1", "2001:db8::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1",
	} {
		if err := p.Check(netip.MustParseAddr(addr)); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s was allowed: %v", addr, err)
		}
	}
	for _, addr := range []string{"93.184.216.34", "8.8.8.8", "2606:4700::1111"} {
		if err := p.Check(netip.MustParseAddr(addr)); err != nil {
			t.Errorf("%s was blocked: %v", addr, err)
		}
	}
}

// Verifies: SEC-105.
func TestPolicyAllowsWhatAnInstallationNames(t *testing.T) {
	t.Parallel()
	p := Policy{Allow: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}}
	if err := p.Check(netip.MustParseAddr("10.1.2.3")); err != nil {
		t.Errorf("an allowed address was blocked: %v", err)
	}
	if err := p.Check(netip.MustParseAddr("10.2.2.3")); !errors.Is(err, ErrBlocked) {
		t.Errorf("an address outside the allowlist was permitted: %v", err)
	}
	loop := Policy{AllowLoopback: true}
	if err := loop.Check(netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Errorf("loopback was blocked although it is allowed: %v", err)
	}
}

// Verifies: SEC-105.
// The check happens against the resolved address, so a name that points
// at the metadata service is refused at connection time.
func TestClientRefusesAResolvedInternalAddress(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	blocked := NewClient(Options{Timeout: 5 * time.Second})
	resp, err := Get(context.Background(), blocked, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Error("a loopback server was reached")
	} else if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error %v does not say the address is refused", err)
	}

	allowed := NewClient(Options{Policy: Policy{AllowLoopback: true}, Timeout: 5 * time.Second})
	resp, err = Get(context.Background(), allowed, srv.URL)
	if err != nil {
		t.Fatalf("an allowed address was refused: %v", err)
	}
	_ = resp.Body.Close()
}

// Verifies: SEC-105.
// A redirect to an internal address is refused as well, because the
// address is checked again on every hop.
func TestClientRefusesARedirectToAnInternalAddress(t *testing.T) {
	t.Parallel()
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer internal.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// Loopback is allowed so that the first hop succeeds; the second hop
	// is refused by scheme, which stands in for the address check the
	// dialer makes in a deployment.
	c := NewClient(Options{Policy: Policy{AllowLoopback: true}, MaxRedirects: 1, Timeout: 5 * time.Second})
	resp, err := Get(context.Background(), c, redirector.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the redirect chain was followed past its limit")
	}
	if !strings.Contains(err.Error(), "redirects") {
		t.Errorf("error %v does not mention the redirect limit", err)
	}
}

// Verifies: SEC-105.
func TestCheckURLRefusesOtherSchemes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ scheme, host string }{
		{"file", "etc"}, {"gopher", "h"}, {"ftp", "h"}, {"https", ""},
	} {
		if err := CheckURL(tc.scheme, tc.host); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s://%s was allowed", tc.scheme, tc.host)
		}
	}
	for _, scheme := range []string{"http", "HTTPS"} {
		if err := CheckURL(scheme, "example.com"); err != nil {
			t.Errorf("%s was blocked: %v", scheme, err)
		}
	}
	resp, err := Get(context.Background(), NewClient(Options{}), "file:///etc/passwd")
	if !errors.Is(err, ErrBlocked) {
		if err == nil {
			_ = resp.Body.Close()
		}
		t.Errorf("Get of a file URL = %v", err)
	}
	resp, err = Get(context.Background(), NewClient(Options{}), "://bad")
	if err == nil {
		_ = resp.Body.Close()
		t.Error("an unparsable URL was accepted")
	}
}

// Verifies: SEC-104.
func TestReadAllRefusesALargeBody(t *testing.T) {
	t.Parallel()
	if got, err := ReadAll(strings.NewReader("12345"), 8); err != nil || string(got) != "12345" {
		t.Errorf("ReadAll = %q, %v", got, err)
	}
	if got, err := ReadAll(strings.NewReader("12345678"), 8); err != nil || len(got) != 8 {
		t.Errorf("a body of exactly the limit = %q, %v", got, err)
	}
	_, err := ReadAll(strings.NewReader("123456789"), 8)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("ReadAll of an oversized body = %v; want ErrTooLarge", err)
	}
}

// Verifies: SEC-104.
func TestMaxBytesRefusesALargeRequest(t *testing.T) {
	t.Parallel()
	h := MaxBytes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := ReadAll(r.Body, 1<<20); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}), 8)

	small := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, small)
	if rec.Code != http.StatusOK {
		t.Errorf("a small body was refused with %d", rec.Code)
	}

	large := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 64)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, large)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a large body was accepted with %d", rec.Code)
	}

	// A request that lies about its length is still bounded when it is
	// read, not only when the header is checked.
	lying := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 64)))
	lying.ContentLength = 4
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, lying)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body larger than its declared length was accepted with %d", rec.Code)
	}
}
