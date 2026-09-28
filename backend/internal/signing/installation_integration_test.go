// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: SEC-106, SEC-120.
// An installation key is created once, shared by every reader, stored
// sealed, and unreadable with another backend's keys.
func TestInstallationKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	b := backend(t)
	first, err := signing.InstallationKey(ctx, db, b, "page-token")
	if err != nil || len(first) != 32 {
		t.Fatalf("InstallationKey = %x, %v", first, err)
	}
	second, err := signing.InstallationKey(ctx, db, b, "page-token")
	if err != nil || !bytes.Equal(first, second) {
		t.Errorf("a second read returned another key: %v", err)
	}
	other, err := signing.InstallationKey(ctx, db, b, "other")
	if err != nil || bytes.Equal(first, other) {
		t.Errorf("two names share a key: %v", err)
	}
	var stored []byte
	if err := db.Pool().QueryRow(ctx, `SELECT sealed FROM installation_secrets WHERE name = 'page-token'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, first) {
		t.Error("an installation key is stored in the clear")
	}
	if _, err := signing.InstallationKey(ctx, db, backend(t), "page-token"); err == nil {
		t.Error("another backend opened the key")
	}
}
