// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// RefreshRequest is what a device presents to renew its access token. The
// DPoP proof of the request has been verified already; the device is
// authenticated by that proof alone (SEC-025).
type RefreshRequest struct {
	// DeviceID names the device.
	DeviceID string
	// ProofJKT is the thumbprint of the key that signed the proof.
	ProofJKT string
	// Proof is the proof as sent: an iOS assertion signs its SHA-256, and
	// an Android integrity token is bound to it.
	Proof string
	// AppAttestAssertion is the iOS device's assertion over the proof.
	AppAttestAssertion []byte
	// PlayIntegrityToken is an Android device's Play Integrity token.
	PlayIntegrityToken string
}

// AccessToken is a device access token with its expiry.
type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

// Refresh issues a new access token to a device that proves possession of
// its DPoP key (SEC-020, SEC-025). The proof's key must be the one the
// device registered; a revoked device is refused (SEC-006), and so is one
// whose attestation is older than the re-attestation interval, which must
// attest again first. On iOS the refresh also needs an App Attest
// assertion over the proof, whose counter is stored with a
// compare-and-set; on Android it needs a Play Integrity token where
// androidRefreshRequiresIntegrity is set.
func (s *Service) Refresh(ctx context.Context, r RefreshRequest) (AccessToken, error) {
	if s.tokens == nil {
		return AccessToken{}, errors.New("device: refreshing needs a token issuer")
	}
	row, err := s.deviceForKey(ctx, r.DeviceID, r.ProofJKT)
	if err != nil {
		return AccessToken{}, err
	}
	appID, envID := storage.ID(row.AppID), storage.ID(row.EnvironmentID)
	conf, err := s.settingsFor(ctx, appID, envID)
	if err != nil {
		return AccessToken{}, err
	}
	if !row.AttestedAt.Valid || s.now().Sub(storage.Time(row.AttestedAt)) > conf.ReattestationInterval {
		return AccessToken{}, plxerr.New(plxerr.ReattestationRequired, "the device must attest again before it can refresh")
	}
	counter, err := s.proveRefresh(ctx, row, conf, r)
	if err != nil {
		return AccessToken{}, err
	}
	if err := s.recordRefresh(ctx, row, counter); err != nil {
		return AccessToken{}, err
	}
	production, err := s.EnvironmentIsProduction(ctx, envID)
	if err != nil {
		return AccessToken{}, err
	}
	value, expires, err := s.tokens.Issue(ctx, devtoken.Claims{
		DeviceID: storage.ID(row.ID), AppID: appID, Environment: envID, OrganizationID: storage.ID(row.OrganizationID),
		HostBuild: row.HostBuild, Production: production, Assurance: row.AssuranceLevel, JKT: r.ProofJKT,
		Lifetime: conf.AccessTokenLifetime,
	})
	if err != nil {
		return AccessToken{}, fmt.Errorf("device: %w", err)
	}
	return AccessToken{Value: value, ExpiresAt: expires}, nil
}

// deviceForKey reads the device a proof speaks for. An unknown device and
// a key that is not the device's are refused alike, so the call does not
// tell whether a device exists; only the holder of the key learns that
// the device was revoked.
func (s *Service) deviceForKey(ctx context.Context, deviceID, jkt string) (dbgen.Device, error) {
	mismatch := plxerr.New(plxerr.TokenBindingMismatch, "the DPoP proof is not from the key of this device")
	id, err := storage.UUID(deviceID)
	if err != nil {
		return dbgen.Device{}, plxerr.New(plxerr.InvalidFormat, "the device identifier is not valid")
	}
	var row dbgen.Device
	err = s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = dbgen.New(tx).GetDevice(ctx, id)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Device{}, mismatch
	}
	if err != nil {
		return dbgen.Device{}, fmt.Errorf("device: %w", err)
	}
	if row.DpopJkt == nil || dpop.CheckBinding(*row.DpopJkt, jkt) != nil {
		return dbgen.Device{}, mismatch
	}
	if row.RevokedAt.Valid {
		return dbgen.Device{}, plxerr.New(plxerr.DeviceRevoked, "the device was revoked")
	}
	return row, nil
}

// proveRefresh checks the platform proof a refresh carries. It returns
// the new App Attest counter, or nil when the refresh has none.
func (s *Service) proveRefresh(ctx context.Context, row dbgen.Device, conf Settings, r RefreshRequest) (*uint32, error) {
	switch {
	case len(row.AppAttestPublicKey) > 0:
		return s.verifyAssertion(ctx, row, r)
	case row.Platform == "android" && conf.AndroidRefreshRequiresIntegrity:
		return nil, s.verifyRefreshIntegrity(ctx, row, r)
	default:
		return nil, nil
	}
}

// verifyAssertion checks the App Attest assertion of an iOS refresh: it
// signs the SHA-256 of the DPoP proof, so it cannot be reused with
// another request.
func (s *Service) verifyAssertion(ctx context.Context, row dbgen.Device, r RefreshRequest) (*uint32, error) {
	if len(r.AppAttestAssertion) == 0 {
		return nil, plxerr.New(plxerr.AttestationFailed, "an iOS refresh needs an App Attest assertion")
	}
	trust, err := s.trustOf(ctx, storage.ID(row.AppID))
	if err != nil {
		return nil, err
	}
	if s.attestors.AppAssertions == nil || trust.IOSAppID == "" {
		return nil, plxerr.New(plxerr.AttestationUnavailable, "iOS attestation is not configured for this app")
	}
	parsed, err := x509.ParsePKIXPublicKey(row.AppAttestPublicKey)
	pub, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok {
		return nil, plxerr.New(plxerr.AttestationFailed, "the device has no usable App Attest key")
	}
	if row.AppAttestCounter < 0 || row.AppAttestCounter > int64(^uint32(0)) {
		return nil, plxerr.New(plxerr.AttestationFailed, "the stored App Attest counter is out of range")
	}
	counter, err := s.attestors.AppAssertions.VerifyAssertion(r.AppAttestAssertion, pub, sha256.Sum256([]byte(r.Proof)), trust.IOSAppID, uint32(row.AppAttestCounter))
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return &counter, nil
}

// verifyRefreshIntegrity checks the Play Integrity token of an Android
// refresh, bound to the DPoP proof of the request. A refresh cannot ask
// for a fresh challenge first, so the proof, itself single-use and
// nonce-bound, is what the token is bound to.
func (s *Service) verifyRefreshIntegrity(ctx context.Context, row dbgen.Device, r RefreshRequest) error {
	if r.PlayIntegrityToken == "" {
		return plxerr.New(plxerr.AttestationFailed, "this environment needs a Play Integrity token with every Android refresh")
	}
	trust, err := s.trustOf(ctx, storage.ID(row.AppID))
	if err != nil {
		return err
	}
	if s.attestors.PlayIntegrity == nil || trust.PlayIntegrity == nil || len(trust.AndroidPackages) == 0 {
		return plxerr.New(plxerr.AttestationUnavailable, "Android attestation is not configured for this app")
	}
	sum := sha256.Sum256([]byte(r.Proof))
	hash := base64.RawURLEncoding.EncodeToString(sum[:])
	var last error
	for _, pkg := range trust.AndroidPackages {
		_, last = s.attestors.PlayIntegrity.Verify(*trust.PlayIntegrity, r.PlayIntegrityToken, playintegrity.Expect{
			PackageName: pkg, RequestHash: hash, CertDigests: trust.AndroidCertDigests, MaxAge: integrityMaxAge,
		})
		if last == nil {
			return nil
		}
	}
	return last //nolint:wrapcheck // a domain error
}

// recordRefresh notes that the device was seen. With an App Attest
// counter it also stores it, only while the stored counter is still the
// one the assertion was checked against: of two refreshes that carry the
// same assertion, one loses (SEC-025).
func (s *Service) recordRefresh(ctx context.Context, row dbgen.Device, counter *uint32) error {
	return s.db.InTx(ctx, storage.Tenant{OrganizationID: storage.ID(row.OrganizationID)}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck // InTx wraps its own failures
		q := dbgen.New(tx)
		if counter == nil {
			if err := q.TouchDevice(ctx, row.ID); err != nil {
				return fmt.Errorf("device: %w", err)
			}
			return nil
		}
		changed, err := q.AdvanceAppAttestCounter(ctx, dbgen.AdvanceAppAttestCounterParams{
			ID: row.ID, Previous: row.AppAttestCounter, Counter: int64(*counter),
		})
		if err != nil {
			return fmt.Errorf("device: %w", err)
		}
		if changed == 0 {
			return plxerr.New(plxerr.AttestationFailed, "the App Attest assertion was already used")
		}
		return nil
	})
}

// VerifyKey checks that a DPoP proof's key is the one a device
// registered, for calls that authenticate by proof alone. It tells the
// caller of an unknown device and of a key that is not the device's
// nothing apart (PLX-6013), and refuses a revoked device (PLX-6006).
func (s *Service) VerifyKey(ctx context.Context, deviceID, jkt string) error {
	_, err := s.deviceForKey(ctx, deviceID, jkt)
	return err
}
