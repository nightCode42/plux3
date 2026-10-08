// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// HistoryKept is how many versions of an environment's configuration the
// server keeps, the newest ones. It is the patch window: a device that
// holds an older version gets the whole device document as a patch from
// nothing (SEC-182, ADR-0053).
const HistoryKept = 20

const (
	// cacheTTL is how long a replica reuses the configuration it read, so
	// that registering and refreshing devices cost the database one read
	// per environment per interval.
	cacheTTL = 30 * time.Second
	// maxCached bounds the memory: past it, the cache starts over.
	maxCached = 4096
)

// Options configures a Service.
type Options struct {
	DB    *storage.DB
	Audit *audit.Log
	// Limits are the installation's limits; securityConfig.bytes and
	// securityConfig.patchBytes are read from them. The zero value uses
	// the defaults.
	Limits limits.Set
	// Tenancy, when set, resolves the limits an organisation or app
	// tightened (LIM-002); without it the installation's apply.
	Tenancy *tenancy.Service
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Service is the domain logic of security configurations.
type Service struct {
	o   Options
	now func() time.Time

	// onChange runs in the transaction of every change; see OnChange.
	onChange func(ctx context.Context, tx pgx.Tx, organizationID, environmentID string) error

	mu    sync.Mutex
	cache map[string]cached
}

// cached is one remembered configuration.
type cached struct {
	config Config
	until  time.Time
}

// New returns the service.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil:
		return nil, errors.New("seccfg: a database is required")
	case o.Audit == nil:
		return nil, errors.New("seccfg: an audit log is required")
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{o: o, now: now}, nil
}

// OnChange registers what must happen in the transaction of every
// change: the release service signs the environment's manifests again, so
// that they pin the new version. Call it before the service is used.
func (s *Service) OnChange(f func(ctx context.Context, tx pgx.Tx, organizationID, environmentID string) error) {
	s.onChange = f
}

// Config is the security configuration of an environment at one version.
type Config struct {
	// Version is 0 for the built-in defaults, else the version stored.
	Version int64
	Values  Values
}

// tree is the device document of the version; version 0 is D0, empty.
func (c Config) tree() map[string]any {
	if c.Version == 0 {
		return emptyDocument()
	}
	return c.Values.document()
}

// DeviceDocument returns the canonical JSON of the device document and its
// SHA-256 (SEC-182).
func (c Config) DeviceDocument() ([]byte, [sha256.Size]byte, error) {
	return documentJSON(c.tree())
}

// Ref names a version of the device document: what the signed manifest
// pins.
type Ref struct {
	Version int64
	SHA256  [sha256.Size]byte
}

// Change is a request to set an environment's configuration.
type Change struct {
	AppID, EnvironmentID string
	Profile              string
	// Overrides is a JSON object of settings; empty means none.
	Overrides []byte
	// ExpectedVersion is the version the caller read, 0 for an
	// environment with no configuration yet.
	ExpectedVersion int64
}

// Set replaces an environment's profile and overrides with a new version
// and returns it (SEC-180, SEC-182). It fails when ExpectedVersion is not
// the current version, when a setting is unknown, of the wrong type, out
// of bounds, or looser than the profile's preset. Setting what is already
// current changes nothing and returns the current version. The change is
// audited in the same transaction.
func (s *Service) Set(ctx context.Context, p auth.Principal, c Change) (int64, error) {
	if err := p.AuthorizeApp(auth.AppManage, c.AppID); err != nil {
		return 0, fmt.Errorf("seccfg: %w", err)
	}
	if c.ExpectedVersion < 0 {
		return 0, plxerr.New(plxerr.InvalidFormat, "the expected version is not negative")
	}
	want, err := Resolve(c.Profile, c.Overrides)
	if err != nil {
		return 0, err
	}
	var version int64
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		version, err = s.write(ctx, tx, p, c, want)
		return err
	})
	if err != nil {
		return 0, err
	}
	s.forget(c.AppID, c.EnvironmentID)
	return version, nil
}

// write adds the version in the caller's transaction.
func (s *Service) write(ctx context.Context, tx pgx.Tx, p auth.Principal, c Change, want Values) (int64, error) {
	q := dbgen.New(tx)
	env, err := environment(ctx, q, c.AppID, c.EnvironmentID)
	if err != nil {
		return 0, err
	}
	lim, err := s.limitsFor(ctx, tx, p.OrganizationID, c.AppID)
	if err != nil {
		return 0, err
	}
	if err := checkSize(want, len(c.Overrides), lim.Get(limits.SecurityConfigBytes)); err != nil {
		return 0, err
	}
	// The lock serialises the changes of one environment, so the version
	// read below is the version the new one follows.
	if _, err := q.LockEnvironmentForConfig(ctx, env.ID); err != nil {
		return 0, failure(err, "environment")
	}
	current, err := latest(ctx, q, env.ID)
	if err != nil {
		return 0, err
	}
	if current.Version != c.ExpectedVersion {
		return 0, plxerr.New(plxerr.RevisionConflict,
			"the security configuration is at version %d, not %d; read it again", current.Version, c.ExpectedVersion)
	}
	if current.Version > 0 && same(current.Values, want) {
		return current.Version, nil
	}
	next := current.Version + 1
	if err := s.insert(ctx, q, p, env, next, want); err != nil {
		return 0, err
	}
	if err := s.record(ctx, tx, p, env, next, want); err != nil {
		return 0, err
	}
	if s.onChange != nil {
		if err := s.onChange(ctx, tx, p.OrganizationID, c.EnvironmentID); err != nil {
			return 0, err
		}
	}
	return next, nil
}

// insert stores a version and drops the ones past the history.
func (*Service) insert(ctx context.Context, q *dbgen.Queries, p auth.Principal, env dbgen.Environment, version int64, v Values) error {
	overrides, err := v.OverridesJSON()
	if err != nil {
		return err
	}
	actor := p.Actor()
	_, err = q.InsertSecurityConfig(ctx, dbgen.InsertSecurityConfigParams{
		OrganizationID: env.OrganizationID, AppID: env.AppID, EnvironmentID: env.ID, Version: version,
		Profile: string(v.Profile()), Overrides: overrides,
		CreatedByKind: actor.Kind, CreatedByID: actor.ID, CreatedBy: actor.Display,
	})
	if err != nil {
		return failure(err, "security configuration")
	}
	if _, err := q.PruneSecurityConfigs(ctx, dbgen.PruneSecurityConfigsParams{EnvironmentID: env.ID, Version: version - HistoryKept}); err != nil {
		return failure(err, "security configuration")
	}
	return nil
}

// record audits a change; the detail names the settings, never their
// values.
func (s *Service) record(ctx context.Context, tx pgx.Tx, p auth.Principal, env dbgen.Environment, version int64, v Values) error {
	keys := make([]string, 0, len(v.overrides))
	for _, k := range v.Overrides() {
		keys = append(keys, string(k))
	}
	_, err := s.o.Audit.Append(ctx, tx, audit.Entry{
		OrganizationID: p.OrganizationID, Actor: p.Actor(), Action: audit.SecurityConfigSet,
		TargetKind: "environment", TargetID: storage.ID(env.ID),
		Detail: fmt.Sprintf("version %d, profile %s, overrides [%s]", version, v.Profile(), strings.Join(keys, ", ")),
	})
	if err != nil {
		return fmt.Errorf("seccfg: %w", err)
	}
	return nil
}

// same reports whether two sets of values make the same configuration.
func same(a, b Values) bool {
	if a.Profile() != b.Profile() {
		return false
	}
	x, errX := a.OverridesJSON()
	y, errY := b.OverridesJSON()
	return errX == nil && errY == nil && string(x) == string(y)
}

// checkSize refuses a configuration or a request beyond
// securityConfig.bytes (LIM-001).
func checkSize(v Values, requestBytes int, limit int64) error {
	if int64(requestBytes) > limit {
		return plxerr.New(plxerr.LimitExceeded, "the overrides are %d bytes; securityConfig.bytes allows %d", requestBytes, limit)
	}
	doc, _, err := Config{Version: 1, Values: v}.DeviceDocument()
	if err != nil {
		return err
	}
	if int64(len(doc)) > limit {
		return plxerr.New(plxerr.LimitExceeded, "the device configuration is %d bytes; securityConfig.bytes allows %d", len(doc), limit)
	}
	return nil
}

// Get returns an environment's configuration at its current version.
func (s *Service) Get(ctx context.Context, p auth.Principal, appID, environmentID string) (Config, error) {
	if err := p.AuthorizeApp(auth.AppRead, appID); err != nil {
		return Config{}, fmt.Errorf("seccfg: %w", err)
	}
	var out Config
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := environment(ctx, q, appID, environmentID)
		if err != nil {
			return err
		}
		out, err = latest(ctx, q, env.ID)
		return err
	})
	return out, err
}

// Effective returns the configuration in force for an environment and its
// version. The server's own readers use it, in any scope; the answer may
// be up to cacheTTL old, which is how long a change takes to reach
// another replica. An environment with no stored configuration has the
// standard profile (version 0).
func (s *Service) Effective(ctx context.Context, appID, environmentID string) (Values, int64, error) {
	key := appID + "/" + environmentID
	now := s.now()
	s.mu.Lock()
	c, ok := s.cache[key]
	s.mu.Unlock()
	if ok && now.Before(c.until) {
		return c.config.Values, c.config.Version, nil
	}
	env, err := storage.UUID(environmentID)
	if err != nil {
		return Values{}, 0, plxerr.New(plxerr.ResourceNotFound, "no such environment")
	}
	var config Config
	err = s.o.DB.InTx(ctx, storage.Tenant{Scope: storage.ScopeRegistration}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		config, err = latestOf(ctx, dbgen.New(tx), env, appID)
		return err
	})
	if err != nil {
		return Values{}, 0, err //nolint:wrapcheck // InTx wraps its own failures
	}
	s.mu.Lock()
	if s.cache == nil || len(s.cache) >= maxCached {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{config: config, until: now.Add(cacheTTL)}
	s.mu.Unlock()
	return config.Values, config.Version, nil
}

// forget drops the remembered configuration of an environment, so that
// this replica sees a change at once.
func (s *Service) forget(appID, environmentID string) {
	s.mu.Lock()
	delete(s.cache, appID+"/"+environmentID)
	s.mu.Unlock()
}

// Current names the version of the device document in force for an
// environment and its hash, in the caller's transaction. It is what the
// manifest signing pins; version 0 is the built-in default.
func (*Service) Current(ctx context.Context, q *dbgen.Queries, environmentID pgtype.UUID) (Ref, error) {
	c, err := latest(ctx, q, environmentID)
	if err != nil {
		return Ref{}, err
	}
	_, sum, err := c.DeviceDocument()
	if err != nil {
		return Ref{}, err
	}
	return Ref{Version: c.Version, SHA256: sum}, nil
}

// inOrg runs f in a transaction bound to the principal's organisation.
func (s *Service) inOrg(ctx context.Context, p auth.Principal, f func(context.Context, pgx.Tx) error) error {
	return s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, f) //nolint:wrapcheck // InTx wraps its own failures
}

// limitsFor returns the limits in force for an app.
func (s *Service) limitsFor(ctx context.Context, tx pgx.Tx, organizationID, appID string) (limits.Set, error) {
	if s.o.Tenancy == nil {
		return s.o.Limits, nil
	}
	set, err := s.o.Tenancy.Effective(ctx, tx, organizationID, appID, "")
	if err != nil {
		return limits.Set{}, err //nolint:wrapcheck // a domain error
	}
	return set, nil
}

// environment reads an environment of an app.
func environment(ctx context.Context, q *dbgen.Queries, appID, environmentID string) (dbgen.Environment, error) {
	app, err := storage.UUID(appID)
	if err != nil {
		return dbgen.Environment{}, plxerr.New(plxerr.InvalidFormat, "the app identifier is not valid")
	}
	id, err := storage.UUID(environmentID)
	if err != nil {
		return dbgen.Environment{}, plxerr.New(plxerr.InvalidFormat, "the environment identifier is not valid")
	}
	env, err := q.GetEnvironment(ctx, id)
	if err != nil {
		return dbgen.Environment{}, failure(err, "environment")
	}
	if env.AppID != app {
		return dbgen.Environment{}, plxerr.New(plxerr.ResourceNotFound, "no such environment in this app")
	}
	return env, nil
}

// newest reads an environment's newest row; found is false when none was
// ever stored.
func newest(ctx context.Context, q *dbgen.Queries, environmentID pgtype.UUID) (row dbgen.SecurityConfigVersion, found bool, err error) {
	row, err = q.LatestSecurityConfig(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, failure(err, "security configuration")
	}
	return row, true, nil
}

// latest reads an environment's newest version, or the built-in default.
func latest(ctx context.Context, q *dbgen.Queries, environmentID pgtype.UUID) (Config, error) {
	row, found, err := newest(ctx, q, environmentID)
	if err != nil || !found {
		return Config{}, err
	}
	return configOf(row)
}

// latestOf is latest for a reader that names the app as well: an
// environment of another app has no configuration to give.
func latestOf(ctx context.Context, q *dbgen.Queries, environmentID pgtype.UUID, appID string) (Config, error) {
	row, found, err := newest(ctx, q, environmentID)
	if err != nil || !found {
		return Config{}, err
	}
	if storage.ID(row.AppID) != appID {
		return Config{}, plxerr.New(plxerr.ResourceNotFound, "no such environment in this app")
	}
	return configOf(row)
}

// configOf builds a configuration from its row.
func configOf(row dbgen.SecurityConfigVersion) (Config, error) {
	v, err := stored(row.Profile, row.Overrides)
	if err != nil {
		return Config{}, err
	}
	return Config{Version: row.Version, Values: v}, nil
}

// failure turns a database error into the refusal a caller understands.
func failure(err error, what string) error {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return plxerr.New(plxerr.ResourceNotFound, "no such %s", what)
	case errors.As(err, &pg) && pg.Code == "23505":
		return plxerr.New(plxerr.RevisionConflict, "the %s was changed meanwhile; read it again", what)
	}
	return fmt.Errorf("seccfg: %s: %w", what, err)
}
