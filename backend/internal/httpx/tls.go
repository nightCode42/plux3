// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"
)

// ServerTLSOptions configures the TLS the api role terminates itself
// (SEC-040).
type ServerTLSOptions struct {
	// CertFile and KeyFile are the PEM certificate chain and private key.
	CertFile, KeyFile string
	// AllowTLS12 lets a client negotiate TLS 1.2 with an AEAD ECDHE
	// suite; without it only TLS 1.3 is accepted. It is the installation
	// default of the tls12Allowed setting.
	AllowTLS12 bool
}

// tls12Suites are the only TLS 1.2 cipher suites ever offered: ephemeral
// key exchange for forward secrecy and an AEAD cipher, never CBC or RSA
// key transport. TLS 1.3 suites are not configurable in Go and are all
// AEAD.
var tls12Suites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// ServerTLS returns the TLS configuration of a public listener: TLS 1.3
// only, or TLS 1.2 with the AEAD ECDHE suites when the option allows it
// (SEC-040). The certificate is read once; renewing it takes a restart.
func ServerTLS(o ServerTLSOptions) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("httpx: load the server certificate: %w", err)
	}
	c := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if o.AllowTLS12 {
		c.MinVersion = tls.VersionTLS12
		c.CipherSuites = slices.Clone(tls12Suites)
	}
	return c, nil
}

// InternalTLSOptions are the files of a role's identity for the traffic
// between roles (SEC-043).
type InternalTLSOptions struct {
	// CAFile is the PEM certificate authority that signs every role's
	// certificate.
	CAFile string
	// CertFile and KeyFile are this role's certificate chain and key.
	CertFile, KeyFile string
}

// InternalServerTLS returns the TLS configuration of an internal
// listener: TLS 1.3, and a client certificate signed by the CA is
// required and verified before any request is read (SEC-043).
func InternalServerTLS(o InternalTLSOptions) (*tls.Config, error) {
	pool, cert, err := internalIdentity(o)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

// NewInternalClient returns an HTTP client for calling another role: it
// presents this role's certificate and trusts only the internal CA
// (SEC-043). Unlike NewClient it may reach private addresses, because
// the roles of an installation live on them; the server's certificate
// is what is trusted, not the address. It is safe for concurrent use.
func NewInternalClient(o InternalTLSOptions, timeout time.Duration) (*http.Client, error) {
	pool, cert, err := internalIdentity(o)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      pool,
				MinVersion:   tls.VersionTLS13,
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		// An internal call never follows a redirect off the role it
		// addressed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// internalIdentity loads the CA pool and the role's key pair.
func internalIdentity(o InternalTLSOptions) (*x509.CertPool, tls.Certificate, error) {
	pem, err := os.ReadFile(o.CAFile)
	if err != nil {
		return nil, tls.Certificate{}, fmt.Errorf("httpx: read the internal CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, tls.Certificate{}, errors.New("httpx: the internal CA file holds no PEM certificate")
	}
	cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, tls.Certificate{}, fmt.Errorf("httpx: load the role certificate: %w", err)
	}
	return pool, cert, nil
}

// HSTSValue renders a Strict-Transport-Security header value (SEC-040).
// A maxAge of zero or less renders "", which disables the header.
func HSTSValue(maxAge time.Duration, includeSubDomains, preload bool) string {
	if maxAge <= 0 {
		return ""
	}
	v := "max-age=" + strconv.FormatInt(int64(maxAge/time.Second), 10)
	if includeSubDomains {
		v += "; includeSubDomains"
	}
	if preload {
		v += "; preload"
	}
	return v
}

// HSTS sets Strict-Transport-Security on every response, error responses
// included, before the handler runs (SEC-040). An empty value returns the
// handler unchanged.
func HSTS(next http.Handler, value string) http.Handler {
	if value == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", value)
		next.ServeHTTP(w, r)
	})
}
