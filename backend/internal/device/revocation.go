// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// revokedPrefix starts the cache key of a revoked DPoP key. The key is
// the public thumbprint, never a token or a proof (SEC-092).
const revokedPrefix = "device:revoked:"

// revokedKey is the cache key under which a revoked key is remembered.
func revokedKey(jkt string) string { return revokedPrefix + jkt }

// publishRevocation tells every replica that tokens bound to a DPoP key
// are no longer accepted, for as long as one issued before the revocation
// can live (SEC-006). A device without a DPoP key has none to publish.
func (s *Service) publishRevocation(ctx context.Context, jkt string) error {
	if s.cache == nil || jkt == "" {
		return nil
	}
	if err := s.cache.Set(ctx, revokedKey(jkt), []byte{1}, TokenTTL); err != nil {
		return fmt.Errorf("device: publish the revocation: %w", err)
	}
	return nil
}

// IsRevoked reports whether the device that holds a DPoP key was revoked.
// The shared cache answers; only when it cannot, the database does, since
// refusing a revoked device must not depend on the cache being up.
func (s *Service) IsRevoked(ctx context.Context, jkt string) (bool, error) {
	if s.cache != nil {
		if _, found, err := s.cache.Get(ctx, revokedKey(jkt)); err == nil {
			return found, nil
		}
	}
	var row dbgen.Device
	err := s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = dbgen.New(tx).GetDeviceByJKT(ctx, &jkt)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("device: %w", err)
	}
	return row.RevokedAt.Valid, nil
}
