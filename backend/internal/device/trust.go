// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// maxReasonLength bounds the reason recorded with a revocation.
const maxReasonLength = 256

// Reattest replaces a device's attestation with a fresh one for the same
// DPoP key, bound to a new challenge, and recomputes its assurance level
// (SEC-006). A revoked device cannot re-attest.
func (s *Service) Reattest(ctx context.Context, deviceID string, challenge []byte, e Evidence) (Device, error) {
	id, err := storage.UUID(deviceID)
	if err != nil {
		return Device{}, plxerr.New(plxerr.InvalidFormat, "the device identifier is not valid")
	}
	row, err := s.deviceForReattestation(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if err := e.matches(row.Platform); err != nil {
		return Device{}, err
	}
	key, _, jkt, err := parseKey(row.DpopPublicKey)
	if err != nil {
		return Device{}, plxerr.New(plxerr.AttestationFailed, "the device has no DPoP key to attest")
	}
	appID, envID := storage.ID(row.AppID), storage.ID(row.EnvironmentID)
	production, err := s.environmentIsProduction(ctx, row.EnvironmentID)
	if err != nil {
		return Device{}, err
	}
	if err := s.consumeChallenge(ctx, challenge, appID, envID); err != nil {
		return Device{}, err
	}
	conf, err := s.settingsFor(ctx, appID, envID)
	if err != nil {
		return Device{}, err
	}
	v, err := s.verify(ctx, subject{
		appID: appID, envID: envID, production: production, challenge: challenge,
		key: key, jkt: jkt, claim: KeyStorage(row.KeyStorage), settings: conf,
	}, e)
	if err != nil {
		return Device{}, err
	}
	provider := string(v.proof.Provider)
	params := dbgen.UpdateDeviceAttestationParams{
		ID: row.ID, AssuranceLevel: string(Assess(v.proof, conf)), KeyStorage: string(v.proof.KeyStorage),
		AttestationProvider: &provider, AttestationVerdicts: v.verdicts, AttestationRiskMetric: v.risk,
		AttestedAt: storage.Timestamp(s.now()),
	}
	if a := v.appAttest; a != nil {
		params.AppAttestKeyID, params.AppAttestPublicKey, params.AppAttestReceipt, params.AppAttestCounter = a.keyID, a.publicKey, a.receipt, a.counter
	}
	var d Device
	err = s.db.InTx(ctx, storage.Tenant{OrganizationID: storage.ID(row.OrganizationID)}, func(ctx context.Context, tx pgx.Tx) error {
		updated, err := dbgen.New(tx).UpdateDeviceAttestation(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return plxerr.New(plxerr.DeviceRevoked, "the device was revoked")
		}
		if err != nil {
			return fmt.Errorf("device: re-attest: %w", err)
		}
		d = deviceOf(updated)
		return nil
	})
	return d, err //nolint:wrapcheck // InTx wraps its own failures
}

// deviceForReattestation reads a device, in the authentication scope: a
// re-attesting device has no principal. Unknown and revoked devices are
// told apart, since only the device itself can ask.
func (s *Service) deviceForReattestation(ctx context.Context, id pgtype.UUID) (dbgen.Device, error) {
	var row dbgen.Device
	err := s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = dbgen.New(tx).GetDevice(ctx, id)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, plxerr.New(plxerr.ResourceNotFound, "no such device")
	}
	if err != nil {
		return row, fmt.Errorf("device: %w", err)
	}
	if row.RevokedAt.Valid {
		return row, plxerr.New(plxerr.DeviceRevoked, "the device was revoked")
	}
	return row, nil
}

// environmentIsProduction says whether an environment is a production
// one, in the registration scope.
func (s *Service) environmentIsProduction(ctx context.Context, id pgtype.UUID) (bool, error) {
	var production bool
	err := s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeRegistration}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		production, err = dbgen.New(tx).FindEnvironmentForReattestation(ctx, id)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, plxerr.New(plxerr.ResourceNotFound, "no such environment")
	}
	if err != nil {
		return false, fmt.Errorf("device: %w", err)
	}
	return production, nil
}

// Revoke withdraws a device's trust: its tokens are deleted, no new one
// is issued and every replica refuses the ones already out as soon as the
// revocation reaches the shared cache (SEC-006). Revoking a revoked
// device changes nothing and keeps the first reason, and publishes the
// revocation again, so that a failed publication is mended by repeating
// the call. The revocation is audited in the same transaction.
func (s *Service) Revoke(ctx context.Context, p auth.Principal, deviceID, reason string) (Device, error) {
	if s.audit == nil {
		return Device{}, errors.New("device: revoking needs an audit log")
	}
	id, err := storage.UUID(deviceID)
	if err != nil {
		return Device{}, plxerr.New(plxerr.InvalidFormat, "the device identifier is not valid")
	}
	if len(reason) > maxReasonLength {
		return Device{}, plxerr.New(plxerr.InvalidFormat, "the reason is longer than %d characters", maxReasonLength)
	}
	var d Device
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		d, err = s.revokeIn(ctx, tx, p, id, reason)
		return err
	})
	if err != nil {
		return Device{}, err
	}
	if err := s.publishRevocation(ctx, d.DPoPJKT); err != nil {
		return Device{}, err
	}
	return d, nil
}

// revokeIn revokes a device inside the transaction of its organisation.
func (s *Service) revokeIn(ctx context.Context, tx pgx.Tx, p auth.Principal, id pgtype.UUID, reason string) (Device, error) {
	q := dbgen.New(tx)
	row, err := q.GetDevice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.AuthorizeApp(auth.AppRead, storage.ID(row.AppID)) != nil) {
		return Device{}, plxerr.New(plxerr.ResourceNotFound, "no such device")
	}
	if err != nil {
		return Device{}, fmt.Errorf("device: %w", err)
	}
	if err := p.AuthorizeApp(auth.AppManage, storage.ID(row.AppID)); err != nil {
		return Device{}, fmt.Errorf("device: %w", err)
	}
	changed, err := q.RevokeDevice(ctx, dbgen.RevokeDeviceParams{ID: id, RevokedAt: storage.Timestamp(s.now()), RevokedReason: reason})
	if err != nil {
		return Device{}, fmt.Errorf("device: revoke: %w", err)
	}
	if changed == 0 {
		return deviceOf(row), nil
	}
	if _, err := q.DeleteDeviceTokens(ctx, id); err != nil {
		return Device{}, fmt.Errorf("device: revoke: %w", err)
	}
	if _, err := s.audit.Append(ctx, tx, audit.Entry{
		OrganizationID: p.OrganizationID, Actor: p.Actor(), Action: audit.DeviceRevoked,
		TargetKind: "device", TargetID: storage.ID(id), Detail: reason,
	}); err != nil {
		return Device{}, fmt.Errorf("device: %w", err)
	}
	if row, err = q.GetDevice(ctx, id); err != nil {
		return Device{}, fmt.Errorf("device: %w", err)
	}
	return deviceOf(row), nil
}
