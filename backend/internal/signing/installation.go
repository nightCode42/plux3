// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// InstallationKey returns a named 32-byte key the installation holds,
// such as the one that authenticates page tokens (SRV-004), creating it
// on first use. It is stored sealed by the backend (SEC-120), and every
// replica reads the same one: the first to create it wins and the others
// read what it wrote.
func InstallationKey(ctx context.Context, db *storage.DB, c Crypter, name string) ([]byte, error) {
	binding := []byte("installation-secret:" + name)
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	sealed, err := Seal(ctx, c, fresh, binding)
	if err != nil {
		return nil, err
	}
	var stored []byte
	err = db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.CreateInstallationSecret(ctx, dbgen.CreateInstallationSecretParams{Name: name, Sealed: sealed.Encode()}); err != nil {
			return fmt.Errorf("signing: store %s: %w", name, err)
		}
		stored, err = q.GetInstallationSecret(ctx, name)
		if err != nil {
			return fmt.Errorf("signing: read %s: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped above or by InTx
	}
	decoded, err := DecodeSealed(stored)
	if err != nil {
		return nil, err
	}
	key, err := Open(ctx, c, decoded, binding)
	if err != nil {
		return nil, fmt.Errorf("signing: open %s: %w", name, err)
	}
	if len(key) != 32 {
		return nil, errors.New("signing: " + name + " is not a 32-byte key")
	}
	return key, nil
}
