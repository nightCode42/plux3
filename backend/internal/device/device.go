// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package device registers the installations of an app, issues their
// short-lived access tokens and records what each one holds (GOV-010).
//
// A device registers with a hardware-bound DPoP key and platform
// attestation (SEC-001, SEC-003), proves possession of the key on every
// request (SEC-021) and refreshes its token with a fresh proof
// (SEC-025). The secret-based registration of P2 is refused (PLX-6008);
// its tables stay until a contracting migration removes them (DEP-030).
package device

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// TokenTTL is the longest a device access token lives (SEC-020). A
// revocation is remembered for this long, which is how long a token
// issued before it can still be presented.
const TokenTTL = 15 * time.Minute

// maxBundles bounds the bundles one report may list: one per plugin
// plus the app bundle.
const maxBundles = 1024

// Platforms a device may register as.
var platforms = map[string]bool{"android": true, "ios": true, "web": true, "macos": true, "windows": true, "linux": true}

// IDs generates identifiers.
type IDs interface {
	New() (string, error)
}

// Options configures a Service.
type Options struct {
	DB  *storage.DB
	IDs IDs
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Random is the source of registration challenges; nil uses
	// crypto/rand.
	Random io.Reader
	// Cache holds registration challenges (SEC-005). Without it, no
	// challenge can be issued or accepted.
	Cache cache.Cache
	// Audit records revocations; Revoke needs it.
	Audit *audit.Log
	// Attestors verify attestation evidence (SEC-003). A platform
	// without its verifier cannot register.
	Attestors Attestors
	// AppTrust says what each app's builds look like; nil means no app
	// has attestation configured.
	AppTrust AppTrust
	// Profiles returns an environment's security profile; nil means
	// every environment is standard.
	Profiles Profiles
	// Config returns an environment's security configuration, overrides
	// included; when set, it replaces Profiles (SEC-182).
	Config Config
	// Tokens issues access tokens (SEC-020). Without it, no token can be
	// refreshed.
	Tokens *devtoken.Issuer
}

// Service is the domain logic of devices.
type Service struct {
	db        *storage.DB
	ids       IDs
	now       func() time.Time
	random    io.Reader
	cache     cache.Cache
	audit     *audit.Log
	attestors Attestors
	appTrust  AppTrust
	profiles  Profiles
	config    Config
	tokens    *devtoken.Issuer
	// production remembers which environments are production ones.
	production *productionCache
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	if o.DB == nil || o.IDs == nil {
		return nil, errors.New("device: a database and identifiers are required")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	random := o.Random
	if random == nil {
		random = rand.Reader
	}
	return &Service{
		db: o.DB, ids: o.IDs, now: now, random: random, cache: o.Cache, audit: o.Audit,
		attestors: o.Attestors, appTrust: o.AppTrust, profiles: o.Profiles, config: o.Config, tokens: o.Tokens, production: &productionCache{},
	}, nil
}

// Device is a registered installation.
type Device struct {
	ID, AppID, EnvironmentID                   string
	Platform, OSVersion, RuntimeVersion, Build string
	AssuranceLevel                             string
	InstalledSequence                          int64
	RegisteredAt, LastSeenAt                   time.Time
	// KeyStorage is where the attestation proved the DPoP key lives.
	KeyStorage KeyStorage
	// DPoPJKT is the thumbprint of the device's DPoP key; empty for a
	// device that registered with a secret.
	DPoPJKT string
	// Provider is the attestation behind the device; empty for a device
	// that registered with a secret.
	Provider Provider
	// AttestedAt is when the server last verified the device's
	// attestation; zero when it never did.
	AttestedAt time.Time
	// Verdicts are the provider verdicts the server accepted.
	Verdicts []string
	// RiskMetric is the provider's risk figure; zero when it has none.
	RiskMetric int32
	// RevokedAt is when the device was revoked; zero while it is
	// trusted.
	RevokedAt time.Time
}

// Identity is an authenticated device.
type Identity struct {
	DeviceID, OrganizationID, AppID, EnvironmentID string
	// HostBuild is the host app build the device registered or last
	// reported, which chooses its manifest (REL-080).
	HostBuild string
}

// Installed is one bundle a device holds; Key is "" for the app bundle.
type Installed struct {
	Key    string
	SHA256 []byte
}

// ReportInstalled records the release a device runs and, when given, the
// bundles it holds (REL-022, REL-080).
func (s *Service) ReportInstalled(ctx context.Context, d Identity, sequence int64, bundles []Installed) error {
	if sequence < 0 {
		return plxerr.New(plxerr.InvalidFormat, "the release sequence is negative")
	}
	if len(bundles) > maxBundles {
		return plxerr.New(plxerr.InvalidFormat, "more than %d bundles", maxBundles)
	}
	id := storage.MustUUID(d.DeviceID)
	org := storage.MustUUID(d.OrganizationID)
	return s.db.InTx(ctx, storage.Tenant{OrganizationID: d.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck // InTx wraps its own failures
		q := dbgen.New(tx)
		if err := q.SetDeviceSequence(ctx, dbgen.SetDeviceSequenceParams{ID: id, InstalledSequence: sequence}); err != nil {
			return fmt.Errorf("device: %w", err)
		}
		if bundles == nil {
			return nil
		}
		keys := make([]string, 0, len(bundles))
		for _, b := range bundles {
			if len(b.SHA256) != 32 {
				return plxerr.New(plxerr.InvalidFormat, "a bundle hash is not a SHA-256")
			}
			keys = append(keys, b.Key)
			if err := q.UpsertDeviceBundle(ctx, dbgen.UpsertDeviceBundleParams{
				DeviceID: id, OrganizationID: org, PluginKey: b.Key, BundleSha256: b.SHA256,
			}); err != nil {
				return fmt.Errorf("device: %w", err)
			}
		}
		if err := q.DeleteDeviceBundles(ctx, dbgen.DeleteDeviceBundlesParams{DeviceID: id, Keep: keys}); err != nil {
			return fmt.Errorf("device: %w", err)
		}
		return nil
	})
}

// Get returns one device.
func (s *Service) Get(ctx context.Context, p auth.Principal, deviceID string) (Device, error) {
	id, err := storage.UUID(deviceID)
	if err != nil {
		return Device{}, plxerr.New(plxerr.InvalidFormat, "the device identifier is not valid")
	}
	var d Device
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := dbgen.New(tx).GetDevice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return plxerr.New(plxerr.ResourceNotFound, "no such device")
		}
		if err != nil {
			return fmt.Errorf("device: %w", err)
		}
		if p.AuthorizeApp(auth.AppRead, storage.ID(row.AppID)) != nil {
			return plxerr.New(plxerr.ResourceNotFound, "no such device")
		}
		d = deviceOf(row)
		return nil
	})
	return d, err
}

// List lists an app's devices, newest first, optionally of one
// environment.
func (s *Service) List(ctx context.Context, p auth.Principal, appID, environmentID string, before storage.Cursor, size int32) ([]Device, error) {
	if err := p.AuthorizeApp(auth.AppRead, appID); err != nil {
		return nil, fmt.Errorf("device: %w", err)
	}
	app, err := storage.UUID(appID)
	if err != nil {
		return nil, plxerr.New(plxerr.InvalidFormat, "the app identifier is not valid")
	}
	var env pgtype.UUID
	if environmentID != "" {
		if env, err = storage.UUID(environmentID); err != nil {
			return nil, plxerr.New(plxerr.InvalidFormat, "the environment identifier is not valid")
		}
	}
	t := before.Time
	if t.IsZero() {
		t = s.now().Add(time.Hour)
	}
	var out []Device
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListDevices(ctx, dbgen.ListDevicesParams{
			AppID: app, EnvironmentID: env, BeforeTime: storage.Timestamp(t), BeforeID: maxUUID(before), PageSize: size,
		})
		if err != nil {
			return fmt.Errorf("device: %w", err)
		}
		for _, r := range rows {
			out = append(out, deviceOf(r))
		}
		return nil
	})
	return out, err
}

// Incompatible counts the devices of an app whose runtime is older than
// minRuntime, or that report one of the host builds that cannot run the
// release (REL-080). Devices report the features their runtime supports
// from P3; until then the runtime version decides.
func (*Service) Incompatible(ctx context.Context, tx pgx.Tx, appID, minRuntime string, hostBuilds []string) (int64, error) {
	if minRuntime == "" && len(hostBuilds) == 0 {
		return 0, nil
	}
	if hostBuilds == nil {
		hostBuilds = []string{}
	}
	n, err := dbgen.New(tx).CountIncompatibleDevices(ctx, dbgen.CountIncompatibleDevicesParams{
		AppID: storage.MustUUID(appID), MinRuntime: minRuntime, HostBuilds: hostBuilds,
	})
	if err != nil {
		return 0, fmt.Errorf("device: %w", err)
	}
	return n, nil
}

// ExpireTokens deletes expired access tokens.
func (s *Service) ExpireTokens(ctx context.Context, org string) (int64, error) {
	var n int64
	err := s.db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = dbgen.New(tx).ExpireDeviceTokens(ctx, storage.Timestamp(s.now()))
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("device: %w", err)
	}
	return n, nil
}

func (s *Service) inOrg(ctx context.Context, p auth.Principal, f func(context.Context, pgx.Tx) error) error {
	return s.db.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, f) //nolint:wrapcheck // InTx wraps its own failures
}

func (s *Service) newID() (string, error) {
	id, err := s.ids.New()
	if err != nil {
		return "", fmt.Errorf("device: %w", err)
	}
	return id, nil
}

func deviceOf(r dbgen.Device) Device {
	return Device{
		ID: storage.ID(r.ID), AppID: storage.ID(r.AppID), EnvironmentID: storage.ID(r.EnvironmentID),
		Platform: r.Platform, OSVersion: r.OsVersion, RuntimeVersion: r.RuntimeVersion, Build: r.HostBuild,
		AssuranceLevel: r.AssuranceLevel, InstalledSequence: r.InstalledSequence,
		RegisteredAt: storage.Time(r.RegisteredAt), LastSeenAt: storage.Time(r.LastSeenAt),
		KeyStorage: KeyStorage(r.KeyStorage), DPoPJKT: deref(r.DpopJkt), Provider: Provider(deref(r.AttestationProvider)),
		AttestedAt: storage.Time(r.AttestedAt), Verdicts: r.AttestationVerdicts, RiskMetric: r.AttestationRiskMetric,
		RevokedAt: storage.Time(r.RevokedAt),
	}
}

// deref reads an optional text column; NULL becomes "".
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// maxUUID is the cursor's identifier, or the largest one for a first
// page ordered descending.
func maxUUID(c storage.Cursor) pgtype.UUID {
	if c.ID == "" {
		u := pgtype.UUID{Valid: true}
		for i := range u.Bytes {
			u.Bytes[i] = 0xff
		}
		return u
	}
	return storage.MustUUID(c.ID)
}
