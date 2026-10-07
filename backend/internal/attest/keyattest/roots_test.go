// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io/fs"
	"testing"
	"time"
)

func TestDefaultRoots(t *testing.T) {
	// Verifies: SEC-002.
	want := map[string]time.Time{
		"cedb1cb6dc896ae5ec797348bce9286753c2b38ee71ce0fbe34a9a1248800dfc": time.Date(2042, time.March, 15, 18, 7, 48, 0, time.UTC),
		"6d9db4ce6c5c0b293166d08986e05774a8776ceb525d9e4329520de12ba4bcc0": time.Date(2035, time.July, 15, 22, 32, 18, 0, time.UTC),
	}
	files, err := fs.Glob(rootFiles, "roots/*.pem")
	if err != nil || len(files) != len(want) {
		t.Fatalf("embedded roots = %v, %v; want %d files", files, err, len(want))
	}
	for _, name := range files {
		data, err := rootFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(data)
		if block == nil {
			t.Fatalf("%s: no PEM block", name)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sum := sha256.Sum256(cert.Raw)
		fp := hex.EncodeToString(sum[:])
		notAfter, ok := want[fp]
		if !ok {
			t.Errorf("%s: unexpected fingerprint %s", name, fp)
			continue
		}
		if !cert.NotAfter.Equal(notAfter) {
			t.Errorf("%s: NotAfter = %s, want %s", name, cert.NotAfter, notAfter)
		}
		delete(want, fp)
	}
	if len(want) != 0 {
		t.Errorf("missing roots: %v", want)
	}
	pool, err := DefaultRoots()
	if err != nil {
		t.Fatalf("DefaultRoots: %v", err)
	}
	if got := len(pool.Subjects()); got != 2 { //nolint:staticcheck // counts the pool for the test only
		t.Errorf("pool holds %d roots, want 2", got)
	}
}
