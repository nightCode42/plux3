// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package tenancy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// LimitUsage is one limit at one scope (LIM-005).
type LimitUsage struct {
	Key   limits.Key
	Unit  string
	Scope string
	// Value is the current usage, where this phase measures it; zero
	// otherwise.
	Value int64
	// Effective is the limit in force at the scope, after every scope
	// above it has tightened it (LIM-002).
	Effective int64
	// HardMax is the value no scope may exceed.
	HardMax int64
	// Warning is true once Value reaches the warning threshold (LIM-003).
	Warning bool
}

// Scope names used in storage and in responses.
const (
	scopeOrganization = "organization"
	scopeApp          = "app"
	scopePlugin       = "plugin"
)

// applyOverrides tightens a set by the stored overrides of one scope. An
// override that is no longer below the scope above — because that scope
// was tightened further since — simply has no effect.
func applyOverrides(set limits.Set, scope limits.Scope, rows []dbgen.LimitOverride) limits.Set {
	for _, row := range rows {
		k := limits.Key(row.LimitKey)
		if _, ok := limits.Lookup(k); !ok {
			continue
		}
		if tightened, err := set.Tighten(k, scope, row.Value); err == nil {
			set = tightened
		}
	}
	return set
}

// Effective returns the limits in force for an organisation and, when
// appID or pluginID are set, for that app and plugin, in the caller's
// transaction (LIM-002).
func (s *Service) Effective(ctx context.Context, tx pgx.Tx, organizationID, appID, pluginID string) (limits.Set, error) {
	q := dbgen.New(tx)
	set := s.o.Limits
	levels := []struct {
		scope limits.Scope
		name  string
		id    string
	}{
		{limits.ScopeOrganization, scopeOrganization, organizationID},
		{limits.ScopeApp, scopeApp, appID},
		{limits.ScopePlugin, scopePlugin, pluginID},
	}
	for _, level := range levels {
		if level.id == "" {
			break
		}
		rows, err := q.ListLimitOverrides(ctx, dbgen.ListLimitOverridesParams{Scope: level.name, ScopeID: storage.MustUUID(level.id)})
		if err != nil {
			return limits.Set{}, fmt.Errorf("tenancy: read limit overrides: %w", err)
		}
		set = applyOverrides(set, level.scope, rows)
	}
	return set, nil
}

// usages describes every limit settable at a scope.
func usages(set limits.Set, scope limits.Scope, name string) []LimitUsage {
	var out []LimitUsage
	for _, def := range limits.Definitions() {
		if def.Scopes&scope == 0 {
			continue
		}
		unit := "count"
		if def.Unit == limits.UnitBytes {
			unit = "bytes"
		}
		out = append(out, LimitUsage{
			Key: def.Key, Unit: unit, Scope: name,
			Effective: set.Get(def.Key), HardMax: def.Max,
		})
	}
	return out
}

// ListOrganizationLimits reports every limit an organisation can set.
func (s *Service) ListOrganizationLimits(ctx context.Context, p auth.Principal) ([]LimitUsage, error) {
	if err := authorize(p, auth.OrganizationRead); err != nil {
		return nil, err
	}
	var out []LimitUsage
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		set, err := s.Effective(ctx, tx, p.OrganizationID, "", "")
		if err != nil {
			return err
		}
		out = usages(set, limits.ScopeOrganization, scopeOrganization)
		return nil
	})
	return out, err
}

// SetOrganizationLimit tightens a limit for the organisation; it can
// never raise one above the installation's (LIM-002).
func (s *Service) SetOrganizationLimit(ctx context.Context, p auth.Principal, key string, value int64) (LimitUsage, error) {
	if err := authorize(p, auth.LimitsManage); err != nil {
		return LimitUsage{}, err
	}
	return s.setLimit(ctx, p, limits.ScopeOrganization, scopeOrganization, p.OrganizationID, "", key, value)
}

// ListAppLimits reports every limit an app can set.
func (s *Service) ListAppLimits(ctx context.Context, p auth.Principal, appID string) ([]LimitUsage, error) {
	var out []LimitUsage
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.app(ctx, dbgen.New(tx), p, auth.AppRead, appID); err != nil {
			return err
		}
		set, err := s.Effective(ctx, tx, p.OrganizationID, appID, "")
		if err != nil {
			return err
		}
		out = usages(set, limits.ScopeApp, scopeApp)
		return nil
	})
	return out, err
}

// SetAppLimit tightens a limit for one app.
func (s *Service) SetAppLimit(ctx context.Context, p auth.Principal, appID, key string, value int64) (LimitUsage, error) {
	if err := authorize(p, auth.LimitsManage); err != nil {
		return LimitUsage{}, err
	}
	if _, err := parseID(appID, "app"); err != nil {
		return LimitUsage{}, err
	}
	return s.setLimit(ctx, p, limits.ScopeApp, scopeApp, appID, appID, key, value)
}

// ListPluginLimits reports every limit a plugin can set. The caller has
// checked that the plugin belongs to the app.
func (s *Service) ListPluginLimits(ctx context.Context, p auth.Principal, appID, pluginID string) ([]LimitUsage, error) {
	var out []LimitUsage
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.app(ctx, dbgen.New(tx), p, auth.PluginRead, appID); err != nil {
			return err
		}
		set, err := s.Effective(ctx, tx, p.OrganizationID, appID, pluginID)
		if err != nil {
			return err
		}
		out = usages(set, limits.ScopePlugin, scopePlugin)
		return nil
	})
	return out, err
}

// SetPluginLimit tightens a limit for one plugin, within its app's.
func (s *Service) SetPluginLimit(ctx context.Context, p auth.Principal, appID, pluginID, key string, value int64) (LimitUsage, error) {
	if err := authorize(p, auth.LimitsManage); err != nil {
		return LimitUsage{}, err
	}
	if _, err := parseID(pluginID, "plugin"); err != nil {
		return LimitUsage{}, err
	}
	return s.setLimit(ctx, p, limits.ScopePlugin, scopePlugin, pluginID, appID, key, value)
}

// setLimit validates a tightening against the scope above and stores it.
func (s *Service) setLimit(ctx context.Context, p auth.Principal, scope limits.Scope, name, scopeID, appID, key string, value int64) (LimitUsage, error) {
	k := limits.Key(key)
	def, ok := limits.Lookup(k)
	if !ok {
		return LimitUsage{}, plxerr.New(plxerr.InvalidEnumValue, "%q is not a limit; see the registry", key)
	}
	var out LimitUsage
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		if appID != "" {
			if _, err := s.app(ctx, q, p, auth.AppRead, appID); err != nil {
				return err
			}
		}
		parentApp := ""
		if scope == limits.ScopePlugin {
			parentApp = appID
		}
		parent, err := s.Effective(ctx, tx, p.OrganizationID, parentApp, "")
		if err != nil {
			return err
		}
		tightened, err := parent.Tighten(k, scope, value)
		if err != nil {
			return plxerr.New(plxerr.OutOfRange,
				"%s can be set here only between 1 and %d, the limit in force above this scope (LIM-002)", key, parent.Get(k))
		}
		if _, err := q.SetLimitOverride(ctx, dbgen.SetLimitOverrideParams{
			OrganizationID: storage.MustUUID(p.OrganizationID), Scope: name,
			ScopeID: storage.MustUUID(scopeID), LimitKey: key, Value: value,
		}); err != nil {
			return failure(err, "limit")
		}
		unit := "count"
		if def.Unit == limits.UnitBytes {
			unit = "bytes"
		}
		out = LimitUsage{Key: k, Unit: unit, Scope: name, Effective: tightened.Get(k), HardMax: def.Max}
		return s.record(ctx, tx, p, audit.LimitSet, name, scopeID)
	})
	return out, err
}

// OrganizationLimits returns the limits in force for an organisation,
// for callers outside a transaction such as the API's rate limits.
func (s *Service) OrganizationLimits(ctx context.Context, organizationID string) (limits.Set, error) {
	if _, err := parseID(organizationID, "organisation"); err != nil {
		return limits.Set{}, err
	}
	var set limits.Set
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		set, err = s.Effective(ctx, tx, organizationID, "", "")
		return err
	})
	return set, err //nolint:wrapcheck // InTx wraps its own failures
}
