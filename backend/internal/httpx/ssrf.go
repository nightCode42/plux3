// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package httpx holds the outbound HTTP client the server uses for
// anything a user can name, and the guards that bound what it reads
// (SEC-104, SEC-105).
//
// Every server-side fetch of a user-supplied URL — an OpenAPI import, an
// AI provider, a function's HTTP call, a webhook — goes through Client.
// It refuses private, loopback, link-local and cloud-metadata addresses
// unless an allowlist names them, and it checks the address again after
// DNS resolution and after every redirect, so a name that resolves
// differently the second time cannot reach an internal service.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is returned when a request would reach an address the
// policy refuses.
var ErrBlocked = errors.New("httpx: the address is not allowed")

// blockedPrefixes are the ranges refused unless explicitly allowed. They
// cover RFC 1918 and its IPv6 equivalents, loopback, link-local — which
// includes the cloud metadata address 169.254.169.254 — carrier-grade
// NAT, and the ranges reserved for documentation and benchmarking, which
// no legitimate integration uses.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// Policy decides which addresses an outbound request may reach.
type Policy struct {
	// Allow lists prefixes that are permitted even when a blocked range
	// covers them. An installation that runs its own services on a
	// private network names them here, deliberately.
	Allow []netip.Prefix
	// AllowLoopback permits 127.0.0.0/8 and ::1. It exists for tests and
	// for a single-node installation that talks to a sidecar; it is
	// never on by default.
	AllowLoopback bool
}

// Check reports whether an address may be reached.
func (p Policy) Check(addr netip.Addr) error {
	addr = addr.Unmap()
	for _, allowed := range p.Allow {
		if allowed.Contains(addr) {
			return nil
		}
	}
	if p.AllowLoopback && addr.IsLoopback() {
		return nil
	}
	for _, blocked := range blockedPrefixes {
		if blocked.Contains(addr) {
			return fmt.Errorf("%w: %s is in the reserved range %s", ErrBlocked, addr, blocked)
		}
	}
	return nil
}

// Options configures NewClient.
type Options struct {
	// Policy decides which addresses may be reached.
	Policy Policy
	// Timeout bounds a whole request, redirects included.
	Timeout time.Duration
	// MaxRedirects bounds a redirect chain; zero means ten.
	MaxRedirects int
}

// NewClient returns an HTTP client that enforces the policy. It is safe
// for concurrent use and should be built once and shared.
func NewClient(opts Options) *http.Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = 10
	}
	policy := opts.Policy
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs after the name has been resolved, with the
		// address the connection will actually use, so a name that
		// resolves to a blocked address — including one that changed
		// between the check and the connection — is refused here.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrBlocked, address)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("%w: %s is not an IP address", ErrBlocked, host)
			}
			return policy.Check(addr)
		},
	}
	return &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			// A proxy would resolve the name itself, which would defeat
			// the check above.
			Proxy: nil,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= opts.MaxRedirects {
				return fmt.Errorf("httpx: more than %d redirects", opts.MaxRedirects)
			}
			return CheckURL(req.URL.Scheme, req.URL.Hostname())
		},
	}
}

// CheckURL refuses a scheme or a host that must never be fetched. It is
// the cheap check made before a request is built; the dialer makes the
// authoritative one against the resolved address.
func CheckURL(scheme, host string) error {
	switch strings.ToLower(scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: the scheme %q is not allowed", ErrBlocked, scheme)
	}
	if host == "" {
		return fmt.Errorf("%w: the URL has no host", ErrBlocked)
	}
	return nil
}

// Get fetches a URL with the client, refusing anything the policy
// blocks. It exists so that call sites cannot forget to check the URL
// before building the request.
func Get(ctx context.Context, c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	if err := CheckURL(req.URL.Scheme, req.URL.Hostname()); err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	return resp, nil
}
