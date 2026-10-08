// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// OrganizationAudit is the outcome of verifying one organisation's
// chain and checkpoints.
type OrganizationAudit struct {
	OrganizationID string `json:"organization_id"`
	// Entries and Checkpoints count what was verified before any break.
	Entries     int64 `json:"entries"`
	Checkpoints int   `json:"checkpoints"`
	// Break is the first thing wrong; nil when the chain holds.
	Break *audit.Break `json:"break,omitempty"`
}

// AuditReport is the outcome of `plux-server audit verify`.
type AuditReport struct {
	Organizations []OrganizationAudit `json:"organizations"`
}

// OK reports whether every chain held together.
func (r AuditReport) OK() bool {
	for _, o := range r.Organizations {
		if o.Break != nil {
			return false
		}
	}
	return true
}

// VerifyAudit verifies the audit chain and checkpoints of one
// organisation, or of every organisation when only is empty (SEC-140,
// SEC-141). A broken chain is reported, not returned as an error; the
// error is for a database or key that could not be read.
func VerifyAudit(ctx context.Context, svc *Services, db *storage.DB, keys audit.PublicKeys, only string) (AuditReport, error) {
	var report AuditReport
	visit := func(org string) error {
		result, err := verifyOrganization(ctx, svc.Audit, db, keys, org)
		if err != nil {
			return err
		}
		report.Organizations = append(report.Organizations, result)
		return nil
	}
	if only != "" {
		return report, visit(only)
	}
	err := svc.Tenancy.ForEachOrganization(ctx, visit)
	return report, err //nolint:wrapcheck // a domain error
}

// verifyOrganization verifies one organisation's chain.
func verifyOrganization(ctx context.Context, log *audit.Log, db *storage.DB, keys audit.PublicKeys, org string) (OrganizationAudit, error) {
	result := OrganizationAudit{OrganizationID: org}
	err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		sum, err := log.VerifyCheckpointed(ctx, tx, org, keys)
		result.Entries, result.Checkpoints = sum.Entries, sum.Checkpoints
		return err //nolint:wrapcheck // classified below
	})
	var broken *audit.Break
	switch {
	case err == nil:
	case errors.As(err, &broken):
		result.Break = broken
	default:
		return result, fmt.Errorf("server: verify the audit chain of %s: %w", org, err)
	}
	return result, nil
}
