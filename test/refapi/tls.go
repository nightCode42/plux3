// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

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
	"time"
)

// authority is the certificate authority the reference API generates at
// start. Its key lives in memory only: the apps trust its certificate, which
// is public, and nothing can mint certificates under it once the process
// ends.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// The certificates bracket the clock by a day before and a week after, so
// an emulator whose clock drifts still accepts them.
const (
	validBefore = 24 * time.Hour
	validAfter  = 7 * 24 * time.Hour
)

// newAuthority generates an ECDSA P-256 certificate authority valid around
// now.
func newAuthority(now time.Time) (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Plux reference API test CA", Organization: []string{"Plux contributors"}},
		NotBefore:             now.Add(-validBefore),
		NotAfter:              now.Add(validAfter),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("signing the CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing the CA certificate: %w", err)
	}
	return &authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// certificatePEM is the authority's certificate, which clients trust.
func (a *authority) certificatePEM() []byte { return a.pem }

// issue signs a server certificate for localhost, 127.0.0.1 and ::1 and
// returns the TLS configuration that serves it over HTTP/1.1 only: the
// WebSocket handshake needs a connection it can hijack.
func (a *authority) issue(now time.Time) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the server key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             now.Add(-validBefore),
		NotAfter:              now.Add(validAfter),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("signing the server certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
		Certificates: []tls.Certificate{{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key}},
	}, nil
}

// newSerial returns a random 127-bit certificate serial number.
func newSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("generating a serial number: %w", err)
	}
	return serial, nil
}
