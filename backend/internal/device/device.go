// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package device registers the installations of an app, issues their
// short-lived access tokens and records what each one holds (GOV-010).
//
// In P2 a device proves itself with the secret it received when it
// registered; only the secret's hash is stored. From P6 the secret is
// replaced by a hardware-bound key with attestation (SEC-020, SEC-025),
// and the assurance level recorded here rises above "none".
package device

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// TokenTTL is how long a device access token lives. There is no refresh
// token: a device asks again with its credential.
const TokenTTL = 15 * time.Minute

// secretBytes is the entropy of a device secret and a token.
const secretBytes = 32

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
}

// Service is the domain logic of devices.
type Service struct {
	db     *storage.DB
	ids    IDs
	now    func() time.Time
	tokens *tokenCache
}

// tokenCacheTTL bounds how long a replica trusts a token it has looked
// up without asking the database again. Device tokens cannot be revoked
// before they expire in P2, so the cache never serves a token past its
// own expiry either; it spares the database one read per device call
// (NFR-020).
const tokenCacheTTL = 30 * time.Second

// maxCachedTokens bounds the cache; past it, the cache starts over.
const maxCachedTokens = 100000

// tokenCache maps a token's hash to its device.
type tokenCache struct {
	mu      sync.Mutex
	entries map[string]cachedToken
}

type cachedToken struct {
	id    Identity
	until time.Time
}

func (c *tokenCache) get(hash string, now time.Time) (Identity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[hash]
	if !ok || !now.Before(e.until) {
		return Identity{}, false
	}
	return e.id, true
}

func (c *tokenCache) put(hash string, id Identity, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxCachedTokens {
		c.entries = map[string]cachedToken{}
	}
	c.entries[hash] = cachedToken{id: id, until: until}
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
	return &Service{db: o.DB, ids: o.IDs, now: now, tokens: &tokenCache{}}, nil
}

// Device is a registered installation.
type Device struct {
	ID, AppID, EnvironmentID                   string
	Platform, OSVersion, RuntimeVersion, Build string
	AssuranceLevel                             string
	InstalledSequence                          int64
	RegisteredAt, LastSeenAt                   time.Time
}

// Identity is an authenticated device.
type Identity struct {
	DeviceID, OrganizationID, AppID, EnvironmentID string
	// HostBuild is the host app build the device registered or last
	// reported, which chooses its manifest (REL-080).
	HostBuild string
}

// Registration is what a device says about itself.
type Registration struct {
	AppID, Environment                         string
	Platform, OSVersion, RuntimeVersion, Build string
}

// Register records a new device and returns it with its secret, which is
// shown exactly once.
func (s *Service) Register(ctx context.Context, r Registration) (Device, string, error) {
	if !platforms[r.Platform] {
		return Device{}, "", plxerr.New(plxerr.InvalidFormat, "platform %q is not one of android, ios, web, macos, windows, linux", r.Platform)
	}
	for _, f := range []string{r.OSVersion, r.RuntimeVersion, r.Build} {
		if len(f) > 64 {
			return Device{}, "", plxerr.New(plxerr.InvalidFormat, "a version or build field is longer than 64 characters")
		}
	}
	app, err := storage.UUID(r.AppID)
	if err != nil {
		return Device{}, "", plxerr.New(plxerr.InvalidFormat, "the app identifier is not valid")
	}
	var found dbgen.FindAppForRegistrationRow
	err = s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeRegistration}, func(ctx context.Context, tx pgx.Tx) error {
		found, err = dbgen.New(tx).FindAppForRegistration(ctx, dbgen.FindAppForRegistrationParams{ID: app, Key: r.Environment})
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, "", plxerr.New(plxerr.ResourceNotFound, "no such app or environment")
	}
	if err != nil {
		return Device{}, "", fmt.Errorf("device: %w", err)
	}
	secret, err := auth.NewSecret(auth.PrefixDeviceSecret, secretBytes)
	if err != nil {
		return Device{}, "", fmt.Errorf("device: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return Device{}, "", err
	}
	var d Device
	err = s.db.InTx(ctx, storage.Tenant{OrganizationID: storage.ID(found.OrganizationID)}, func(ctx context.Context, tx pgx.Tx) error {
		row, err := dbgen.New(tx).InsertDevice(ctx, dbgen.InsertDeviceParams{
			ID: storage.MustUUID(id), OrganizationID: found.OrganizationID, AppID: app, EnvironmentID: found.EnvironmentID,
			Platform: r.Platform, OsVersion: r.OSVersion, RuntimeVersion: r.RuntimeVersion, HostBuild: r.Build,
			SecretHash: secret.Hash,
		})
		if err != nil {
			return fmt.Errorf("device: register: %w", err)
		}
		d = deviceOf(row)
		return nil
	})
	if err != nil {
		return Device{}, "", fmt.Errorf("device: %w", err)
	}
	return d, secret.Value, nil
}

// Token is a device access token, shown exactly once.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// IssueToken exchanges a device's credential for an access token.
// Unknown devices and wrong secrets are refused alike.
func (s *Service) IssueToken(ctx context.Context, deviceID, secret string) (Token, error) {
	id, err := storage.UUID(deviceID)
	if err != nil || !strings.HasPrefix(secret, auth.PrefixDeviceSecret+"_") {
		return Token{}, refused()
	}
	var row dbgen.FindDeviceSecretRow
	err = s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		row, err = dbgen.New(tx).FindDeviceSecret(ctx, id)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, refused()
	}
	if err != nil {
		return Token{}, fmt.Errorf("device: %w", err)
	}
	if subtle.ConstantTimeCompare(row.SecretHash, auth.HashSecret(secret)) != 1 {
		return Token{}, refused()
	}
	tok, err := auth.NewSecret(auth.PrefixDeviceToken, secretBytes)
	if err != nil {
		return Token{}, fmt.Errorf("device: %w", err)
	}
	tid, err := s.newID()
	if err != nil {
		return Token{}, err
	}
	expires := s.now().Add(TokenTTL).UTC().Truncate(time.Second)
	err = s.db.InTx(ctx, storage.Tenant{OrganizationID: storage.ID(row.OrganizationID)}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.InsertDeviceToken(ctx, dbgen.InsertDeviceTokenParams{
			ID: storage.MustUUID(tid), OrganizationID: row.OrganizationID, DeviceID: id, SecretHash: tok.Hash,
			ExpiresAt: storage.Timestamp(expires),
		}); err != nil {
			return fmt.Errorf("device: issue a token: %w", err)
		}
		return q.TouchDevice(ctx, id) //nolint:wrapcheck // one statement
	})
	if err != nil {
		return Token{}, fmt.Errorf("device: %w", err)
	}
	return Token{Value: tok.Value, ExpiresAt: expires}, nil
}

// IsToken reports whether a bearer credential is a device token, so the
// edge knows which service authenticates it.
func IsToken(bearer string) bool { return strings.HasPrefix(bearer, auth.PrefixDeviceToken+"_") }

// Authenticate resolves a device access token.
func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if !IsToken(token) {
		return Identity{}, refused()
	}
	hash := auth.HashSecret(token)
	now := s.now()
	if id, ok := s.tokens.get(string(hash), now); ok {
		return id, nil
	}
	var row dbgen.FindDeviceTokenRow
	err := s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = dbgen.New(tx).FindDeviceToken(ctx, hash)
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, refused()
	}
	if err != nil {
		return Identity{}, fmt.Errorf("device: %w", err)
	}
	expires := storage.Time(row.ExpiresAt)
	if !expires.After(now) {
		return Identity{}, refused()
	}
	id := Identity{
		DeviceID: storage.ID(row.DeviceID), OrganizationID: storage.ID(row.OrganizationID),
		AppID: storage.ID(row.AppID), EnvironmentID: storage.ID(row.EnvironmentID), HostBuild: row.HostBuild,
	}
	until := now.Add(tokenCacheTTL)
	if expires.Before(until) {
		until = expires
	}
	s.tokens.put(string(hash), id, until)
	return id, nil
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

func refused() error {
	return plxerr.New(plxerr.AuthenticationRequired, "the device credential is not valid")
}

func deviceOf(r dbgen.Device) Device {
	return Device{
		ID: storage.ID(r.ID), AppID: storage.ID(r.AppID), EnvironmentID: storage.ID(r.EnvironmentID),
		Platform: r.Platform, OSVersion: r.OsVersion, RuntimeVersion: r.RuntimeVersion, Build: r.HostBuild,
		AssuranceLevel: r.AssuranceLevel, InstalledSequence: r.InstalledSequence,
		RegisteredAt: storage.Time(r.RegisteredAt), LastSeenAt: storage.Time(r.LastSeenAt),
	}
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
