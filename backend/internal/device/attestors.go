// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"time"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// KeyAttestor verifies an Android Key Attestation chain; a
// *keyattest.Verifier is one.
type KeyAttestor interface {
	Verify(chain [][]byte, challenge []byte, p keyattest.Policy) (keyattest.Result, error)
}

// IntegrityChecker verifies a Play Integrity token with the keys of one
// app; PlayIntegrityChecker is the production one.
type IntegrityChecker interface {
	Verify(keys playintegrity.Keys, token string, e playintegrity.Expect) (playintegrity.Verdict, error)
}

// AppAttestor verifies an App Attest attestation object; a
// *appattest.Verifier is one.
type AppAttestor interface {
	VerifyAttestation(object, keyID []byte, clientDataHash [32]byte, appID string, env appattest.Environment) (appattest.Attestation, error)
}

// Attestors bundles the verifiers a registration uses. A nil verifier
// makes its platform unavailable: registrations that need it fail with
// PLX-6009 rather than skipping the check.
type Attestors struct {
	KeyAttestation KeyAttestor
	PlayIntegrity  IntegrityChecker
	AppAttest      AppAttestor
}

// PlayIntegrityChecker opens Play Integrity tokens with the keys it is
// given, at the time Now returns.
type PlayIntegrityChecker struct {
	// Now is the clock; it must not be nil.
	Now func() time.Time
}

// Verify opens and checks a token with the keys of one app.
func (c PlayIntegrityChecker) Verify(keys playintegrity.Keys, token string, e playintegrity.Expect) (playintegrity.Verdict, error) {
	v := playintegrity.Verifier{Keys: keys, Now: c.Now}
	return v.Verify(token, e) //nolint:wrapcheck // a domain error
}

// TrustConfig is what the server trusts about one app's builds. Its
// storage arrives with the security configuration; registration reads it
// through an AppTrust.
type TrustConfig struct {
	// AndroidPackages are the application identifiers the app ships as.
	AndroidPackages []string
	// AndroidCertDigests are the SHA-256 digests of its signing
	// certificates; empty skips the certificate check.
	AndroidCertDigests [][]byte
	// PlayIntegrity are the app's Play Console keys; nil means Play
	// Integrity is not configured.
	PlayIntegrity *playintegrity.Keys
	// IOSAppID is the team and bundle identifier, "TEAMID.com.example.app";
	// empty means App Attest is not configured.
	IOSAppID string
	// AppAttestProduction selects App Attest's production environment
	// rather than its sandbox.
	AppAttestProduction bool
}

// AppTrust returns the trust configuration of an app.
type AppTrust func(ctx context.Context, appID string) (TrustConfig, error)

// Profiles returns the security profile of an environment. A nil Profiles
// means every environment is standard.
type Profiles func(ctx context.Context, appID, envID string) (settings.Profile, error)
