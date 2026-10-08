// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/seccfg"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// tokenKeysTTL is how long a replica keeps the public token keys it read.
// A key added by a rotation reaches every replica within this time; the
// tokens it signs are refused until then, and the device asks again.
const tokenKeysTTL = 30 * time.Second

// DeviceTrust is what the api role authenticates device calls with
// (SEC-020–SEC-025). It is nil where the process does not run the api
// role, which never holds a token signer.
type DeviceTrust struct {
	// Tokens verifies access tokens.
	Tokens *devtoken.Verifier
	// Proofs verifies DPoP proofs, nonce included.
	Proofs *dpop.Verifier
	// Nonces issues the DPoP nonces.
	Nonces *dpop.Nonces
	// Replay detects reused proofs.
	Replay *dpop.Replay
	// Window and FallbackWindow are the accepted distances of a proof's
	// iat from the clock, normally and while the replay cache is
	// degraded.
	Window, FallbackWindow time.Duration
	// Windows returns the windows of one environment, which an operator
	// may have tightened (SEC-182); nil where the installation's apply.
	// The nonce rotation and the replay policy stay the installation's.
	Windows func(ctx context.Context, appID, environmentID string) (window, fallback time.Duration, err error)
}

// tokenSignerFor narrows the signing backend to token signing for the api
// role, and returns nil for the others: only the api role issues access
// tokens, and it cannot reach anything else of the backend (L-3).
func tokenSignerFor(cfg *config.Config, backend signing.Crypter) (signing.TokenSigner, error) {
	if !cfg.Has(config.RoleAPI) {
		return nil, nil //nolint:nilnil // the role issues no tokens
	}
	t, ok := backend.(signing.TokenSigner)
	if !ok {
		return nil, errors.New("server: the signing backend cannot sign device access tokens")
	}
	return signing.TokenOnly(t), nil
}

// tokenKeys caches the public token keys, which a backend may have to
// fetch from a remote service.
type tokenKeys struct {
	signer signing.TokenSigner
	now    func() time.Time

	mu    sync.Mutex
	keys  []signing.TokenKey
	until time.Time
}

// Keys returns the public token keys, read at most once per tokenKeysTTL.
func (c *tokenKeys) Keys(ctx context.Context) ([]signing.TokenKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now := c.now(); c.keys != nil && now.Before(c.until) {
		return c.keys, nil
	}
	keys, err := c.signer.TokenKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("server: token keys: %w", err)
	}
	c.keys, c.until = keys, c.now().Add(tokenKeysTTL)
	return keys, nil
}

// installationSetting is the default of a security setting under the
// standard profile, which stands for the installation's own profile until
// the remote security configuration arrives.
func installationSetting(k settings.Key) (settings.Value, error) {
	s, ok := settings.Lookup(k)
	if !ok {
		return settings.Value{}, fmt.Errorf("server: the setting %s is not in the registry", k)
	}
	v, ok := s.Defaults.For(settings.Standard)
	if !ok {
		return settings.Value{}, fmt.Errorf("server: the setting %s has no standard default", k)
	}
	return v, nil
}

// seconds reads a setting in seconds.
func seconds(k settings.Key) (time.Duration, error) {
	v, err := installationSetting(k)
	if err != nil {
		return 0, err
	}
	return time.Duration(v.Int()) * time.Second, nil
}

// issuerFor builds the issuer of device access tokens: the public base
// URL is both issuer and audience, and the installation's default of the
// accessTokenLifetime setting is the lifetime.
func issuerFor(cfg *config.Config, signer signing.TokenSigner) *devtoken.Issuer {
	base := strings.TrimSuffix(cfg.Server.PublicBaseURL, "/")
	return &devtoken.Issuer{Signer: signer, Issuer: base, Audience: base, Lifetime: cfg.Auth.Device.AccessTokenTTL.Duration()}
}

// buildDeviceTrust assembles the verification side of device
// authentication: tokens, proofs, nonces and the replay cache.
func buildDeviceTrust(ctx context.Context, cfg *config.Config, db *storage.DB, shared cache.Cache, set limits.Set, d deviceDeps) (*DeviceTrust, error) {
	crypter, signer, log := d.crypter, d.signer, d.log
	if signer == nil {
		return nil, nil //nolint:nilnil // no api role, no device authentication
	}
	rotation, err := seconds(settings.DPOPNonceRotation)
	if err != nil {
		return nil, err
	}
	window, err := seconds(settings.DPOPIatWindow)
	if err != nil {
		return nil, err
	}
	fallback, err := seconds(settings.DPOPIatWindowFallback)
	if err != nil {
		return nil, err
	}
	policy, err := installationSetting(settings.ReplayCacheFallback)
	if err != nil {
		return nil, err
	}
	key, err := signing.InstallationKey(ctx, db, crypter, "dpop-nonce")
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	nonces, err := dpop.NewNonces(key, rotation, time.Now)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	fail := dpop.PerReplica
	if policy.Text() == "failClosed" {
		fail = dpop.FailClosed
	}
	base := strings.TrimSuffix(cfg.Server.PublicBaseURL, "/")
	return &DeviceTrust{
		Tokens: &devtoken.Verifier{Keys: (&tokenKeys{signer: signer, now: time.Now}).Keys, Issuer: base, Audience: base},
		Proofs: &dpop.Verifier{Nonces: nonces},
		Nonces: nonces,
		Replay: dpop.NewReplay(shared, fail, int(min(set.Get(limits.DPOPReplayCacheEntries), 1<<30)), time.Now, func(err error) { //nolint:gosec // G115: bounded by min
			log.WarnContext(ctx, "the DPoP replay cache is unavailable; this replica remembers proofs itself and applies the narrower issued-at window (SEC-023)",
				slog.Any("error", err))
		}, dpop.WithHealth(replayHealth(d.metrics))),
		Window: window, FallbackWindow: fallback, Windows: windowsOf(d.configs),
	}, nil
}

// windowsOf reads the DPoP issued-at windows of an environment from its
// security configuration; nil when there is no store to read.
func windowsOf(configs *seccfg.Service) func(context.Context, string, string) (time.Duration, time.Duration, error) {
	if configs == nil {
		return nil
	}
	return func(ctx context.Context, appID, environmentID string) (time.Duration, time.Duration, error) {
		v, _, err := configs.Effective(ctx, appID, environmentID)
		if err != nil {
			return 0, 0, fmt.Errorf("server: the security configuration: %w", err)
		}
		return time.Duration(v.Get(settings.DPOPIatWindow).Int()) * time.Second,
			time.Duration(v.Get(settings.DPOPIatWindowFallback).Int()) * time.Second, nil
	}
}

// replayHealth feeds the replay cache's health into the metrics of
// SEC-023; without metrics it hears nothing.
func replayHealth(m *observability.Metrics) dpop.Health {
	if m == nil {
		return dpop.Health{}
	}
	return dpop.Health{Fallback: m.ReplayCacheFallback, Recovered: m.ReplayCacheRecovered}
}

// deviceDeps are the collaborators of the device side of the services.
type deviceDeps struct {
	crypter signing.Crypter
	signer  signing.TokenSigner
	ids     interface{ New() (string, error) }
	audit   *audit.Log
	log     *slog.Logger
	// configs holds the environments' security configurations.
	configs *seccfg.Service
	// metrics receives the replay cache's health; nil records nothing.
	metrics *observability.Metrics
}

// buildDeviceSide assembles the device service and the trust that
// authenticates device calls.
func buildDeviceSide(ctx context.Context, cfg *config.Config, db *storage.DB, shared cache.Cache, set limits.Set, d deviceDeps) (*device.Service, *DeviceTrust, error) {
	if d.log == nil {
		d.log = slog.Default()
	}
	devices, err := buildDevices(db, d.ids, shared, d.audit, cfg, d.signer, d.configs)
	if err != nil {
		return nil, nil, err
	}
	trust, err := buildDeviceTrust(ctx, cfg, db, shared, set, d)
	if err != nil {
		return nil, nil, err
	}
	return devices, trust, nil
}

// buildDevices assembles the device service, whose registrations verify
// attestation and whose refreshes issue access tokens.
func buildDevices(db *storage.DB, gen interface{ New() (string, error) }, shared cache.Cache, auditLog *audit.Log, cfg *config.Config, signer signing.TokenSigner, configs *seccfg.Service) (*device.Service, error) {
	attestors, err := buildAttestors()
	if err != nil {
		return nil, err
	}
	trust, err := appTrust(cfg)
	if err != nil {
		return nil, err
	}
	opts := device.Options{
		DB: db, IDs: gen, Cache: shared, Audit: auditLog, Attestors: attestors, AppTrust: trust,
		AccessTokenLifetime: cfg.Auth.Device.AccessTokenTTL.Duration(),
		DevelopmentProvider: cfg.Attestation.DevelopmentProvider,
	}
	if configs != nil {
		opts.Config = func(ctx context.Context, appID, envID string) (device.Values, error) {
			v, _, err := configs.Effective(ctx, appID, envID)
			return v, err //nolint:wrapcheck // a domain error
		}
	}
	if signer != nil {
		opts.Tokens = issuerFor(cfg, signer)
	}
	svc, err := device.NewService(opts)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return svc, nil
}

// buildAttestors builds the production verifiers of platform evidence,
// anchored in the roots Google and Apple publish.
func buildAttestors() (device.Attestors, error) {
	androidRoots, err := keyattest.DefaultRoots()
	if err != nil {
		return device.Attestors{}, fmt.Errorf("server: %w", err)
	}
	key, err := keyattest.NewVerifier(androidRoots, nil, time.Now)
	if err != nil {
		return device.Attestors{}, fmt.Errorf("server: %w", err)
	}
	appleRoots, err := appattest.DefaultRoots()
	if err != nil {
		return device.Attestors{}, fmt.Errorf("server: %w", err)
	}
	return device.Attestors{
		KeyAttestation: key,
		PlayIntegrity:  device.PlayIntegrityChecker{Now: time.Now},
		AppAttest:      &appattest.Verifier{Roots: appleRoots, Now: time.Now},
		AppAssertions:  device.AppAssertionChecker{},
	}, nil
}

// appTrust builds the per-app attestation trust from the configuration.
// An app the configuration does not list has none: Android and iOS
// evidence is then refused as unavailable. This is an interim source until
// the remote security configuration supplies it.
func appTrust(cfg *config.Config) (device.AppTrust, error) {
	trusts := make(map[string]device.TrustConfig, len(cfg.Attestation.Apps))
	for _, id := range slices.Sorted(maps.Keys(cfg.Attestation.Apps)) {
		t, err := trustOf(cfg.Attestation.Apps[id])
		if err != nil {
			return nil, fmt.Errorf("server: attestation.apps[%s]: %w", id, err)
		}
		trusts[id] = t
	}
	return func(_ context.Context, appID string) (device.TrustConfig, error) { return trusts[appID], nil }, nil
}

// trustOf converts one app's configuration.
func trustOf(a config.AppAttestation) (device.TrustConfig, error) {
	t := device.TrustConfig{
		AndroidPackages: slices.Clone(a.AndroidPackages), IOSAppID: a.IOSAppID, AppAttestProduction: a.AppAttestProduction,
	}
	for _, d := range a.AndroidCertDigests {
		raw, err := hex.DecodeString(d)
		if err != nil {
			return device.TrustConfig{}, errors.New("a certificate digest is not hexadecimal")
		}
		t.AndroidCertDigests = append(t.AndroidCertDigests, raw)
	}
	if a.PlayIntegrityDecryptionKey != "" {
		keys, err := playintegrity.ParseKeys(a.PlayIntegrityDecryptionKey.Value(), a.PlayIntegrityVerificationKey.Value())
		if err != nil {
			return device.TrustConfig{}, err //nolint:wrapcheck // a domain error that never echoes the keys
		}
		t.PlayIntegrity = &keys
	}
	return t, nil
}
