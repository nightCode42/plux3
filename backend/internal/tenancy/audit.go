// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package tenancy

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// AuditEntries lists an organisation's audit log in order, after a
// sequence number (SEC-140).
func (s *Service) AuditEntries(ctx context.Context, p auth.Principal, after int64, size int32) ([]audit.Entry, error) {
	if err := authorize(p, auth.AuditRead); err != nil {
		return nil, err
	}
	var out []audit.Entry
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.o.Audit.List(ctx, tx, p.OrganizationID, after, size)
		return err //nolint:wrapcheck // the audit package wraps its own failures
	})
	return out, err
}

// InstallationAuditEntries lists the installation's own chain — sign-ins,
// invitations, second factors — for an installation administrator.
func (s *Service) InstallationAuditEntries(ctx context.Context, id auth.Identity, after int64, size int32) ([]audit.Entry, error) {
	if id.Kind != auth.KindUser || !id.InstallationAdmin {
		return nil, plxerr.New(plxerr.PermissionDenied, "only an installation administrator may read the installation's audit log")
	}
	var out []audit.Entry
	err := s.o.DB.InTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.o.Audit.List(ctx, tx, "", after, size)
		return err //nolint:wrapcheck // the audit package wraps its own failures
	})
	return out, err //nolint:wrapcheck // InTx wraps its own failures
}
