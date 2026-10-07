// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"crypto/x509"
	"embed"
	"encoding/pem"
	"fmt"
	"io/fs"
)

// rootFiles holds Google's Android Key Attestation roots as published at
// https://android.googleapis.com/attestation/root.
//
//go:embed roots/*.pem
var rootFiles embed.FS

// DefaultRoots returns a new pool holding the embedded Google Android Key
// Attestation roots. Each call returns an independent pool.
func DefaultRoots() (*x509.CertPool, error) {
	entries, err := fs.ReadDir(rootFiles, "roots")
	if err != nil {
		return nil, fmt.Errorf("keyattest: read embedded roots: %w", err)
	}
	pool := x509.NewCertPool()
	for _, e := range entries {
		data, err := rootFiles.ReadFile("roots/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("keyattest: read embedded root %s: %w", e.Name(), err)
		}
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("keyattest: embedded root %s is not a certificate", e.Name())
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("keyattest: parse embedded root %s: %w", e.Name(), err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}
