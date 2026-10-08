// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package pkcs11helper

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies: SEC-120.
func TestListenCreatesAnOwnerOnlySocket(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s")
	l, err := Listen(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	// A stale socket, left by a process that was killed, is replaced.
	if uc, ok := l.(interface{ SetUnlinkOnClose(bool) }); ok {
		uc.SetUnlinkOnClose(false)
	}
	_ = l.Close()
	l2, err := Listen(t.Context(), path)
	if err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	}
	_ = l2.Close()
}

// Verifies: SEC-120.
func TestListenNeverRemovesAFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(t.Context(), path); err == nil {
		t.Fatal("listened over a regular file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Errorf("the file was touched: %q, %v", data, err)
	}
}
