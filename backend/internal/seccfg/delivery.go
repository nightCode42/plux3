// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Scope names an environment and the organisation and app it belongs to.
type Scope struct {
	OrganizationID, AppID, EnvironmentID string
}

// Delivery is what a manifest response carries about the configuration of
// a device that holds one version when the manifest pins another
// (SEC-182).
type Delivery struct {
	// Patch is an RFC 7396 merge patch, as JSON, from the device document
	// the device holds to the one the manifest pins. It is empty when the
	// versions are the same, and also when the pinned version is no longer
	// kept, where FullRequired is set.
	Patch []byte
	// FullRequired is set when no patch from the device's version could be
	// given: the patch then runs from the empty document, which is the
	// whole device document.
	FullRequired bool
}

// Deliver computes the delivery for a device holding version from when the
// manifest pins version to. Nothing is sent when the versions are equal or
// when nothing is pinned. A device whose version is 0, or is no longer
// kept, gets the patch from the empty document. A patch beyond
// securityConfig.patchBytes is replaced by that one and marks the delivery
// as FullRequired.
func (s *Service) Deliver(ctx context.Context, sc Scope, from, to int64) (Delivery, error) {
	if from == to || to == 0 {
		return Delivery{}, nil
	}
	env, err := storage.UUID(sc.EnvironmentID)
	if err != nil {
		return Delivery{}, errors.New("seccfg: the environment identifier is not valid")
	}
	var out Delivery
	err = s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: sc.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		target, found, err := version(ctx, q, env, to)
		if err != nil || !found {
			out = Delivery{FullRequired: !found}
			return err
		}
		// A version that has left the history reads as the empty document.
		base, _, err := version(ctx, q, env, from)
		if err != nil {
			return err
		}
		lim, err := s.limitsFor(ctx, tx, sc.OrganizationID, sc.AppID)
		if err != nil {
			return err
		}
		out, err = deliver(base.tree(), target.tree(), lim.Get(limits.SecurityConfigPatchBytes))
		return err
	})
	if err != nil {
		return Delivery{}, err //nolint:wrapcheck // InTx wraps its own failures
	}
	return out, nil
}

// version reads one stored version; found is false when it was never
// stored or has left the history.
func version(ctx context.Context, q *dbgen.Queries, environmentID pgtype.UUID, v int64) (Config, bool, error) {
	row, err := q.GetSecurityConfig(ctx, dbgen.GetSecurityConfigParams{EnvironmentID: environmentID, Version: v})
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, failure(err, "security configuration")
	}
	c, err := configOf(row)
	return c, err == nil, err
}

// deliver diffs two device documents and applies the size limit.
func deliver(from, to map[string]any, maxPatch int64) (Delivery, error) {
	patch, _ := diff(from, to)
	raw, err := marshal(patch)
	if err != nil {
		return Delivery{}, err
	}
	if int64(len(raw)) <= maxPatch {
		return Delivery{Patch: raw}, nil
	}
	patch, _ = diff(emptyDocument(), to)
	if raw, err = marshal(patch); err != nil {
		return Delivery{}, err
	}
	return Delivery{Patch: raw, FullRequired: true}, nil
}
