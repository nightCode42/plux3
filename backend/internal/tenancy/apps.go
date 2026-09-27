// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package tenancy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// App is owned by an organisation (GOV-001).
type App struct {
	ID               string
	OrganizationID   string
	Key              string
	Name             string
	DefaultPluginKey string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Environment isolates variables, secrets, signing keys and device
// registrations (GOV-010).
type Environment struct {
	ID            string
	AppID         string
	Key           string
	Name          string
	Production    bool
	SigningKeyRef string
	CreatedAt     time.Time
}

// Channel is a release stream within an environment (REL-005).
type Channel struct {
	ID              string
	EnvironmentID   string
	Key             string
	ReleaseSequence int64
	UpdatedAt       time.Time
}

// Variable is a non-secret value of an environment (DAT-003).
type Variable struct {
	Key   string
	Value string
}

// Secret describes a stored secret; its value is never returned after
// it is set (SEC-106).
type Secret struct {
	Key       string
	Hint      string
	UpdatedAt time.Time
	UpdatedBy string
}

// AccessGrant is a role on one app held by a team or a user (GOV-001).
type AccessGrant struct {
	ID        string
	AppID     string
	TeamID    string
	UserID    string
	Role      string
	GrantedAt time.Time
}

// defaultEnvironments are created with every app (GOV-010), each with
// the default channel (REL-005).
var defaultEnvironments = []struct {
	key, name  string
	production bool
}{
	{"development", "Development", false},
	{"staging", "Staging", false},
	{"production", "Production", true},
}

// defaultChannel is the channel every environment starts with.
const defaultChannel = "production"

// CreateApp creates an app with the default environments and channels.
func (s *Service) CreateApp(ctx context.Context, p auth.Principal, key, name string) (App, error) {
	if err := authorize(p, auth.AppManage); err != nil {
		return App{}, err
	}
	name = strings.TrimSpace(name)
	if err := checkKey("app", key); err != nil {
		return App{}, err
	}
	if err := checkName("app", name); err != nil {
		return App{}, err
	}
	appID, err := s.newID()
	if err != nil {
		return App{}, err
	}
	var out App
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.CreateApp(ctx, dbgen.CreateAppParams{
			ID: storage.MustUUID(appID), OrganizationID: storage.MustUUID(p.OrganizationID), Key: key, Name: name,
		})
		if err != nil {
			return failure(err, "app")
		}
		out = appOf(row)
		if err := s.record(ctx, tx, p, audit.AppCreated, "app", appID); err != nil {
			return err
		}
		for _, d := range defaultEnvironments {
			if _, err := s.createEnvironment(ctx, tx, p, row, d.key, d.name, d.production); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// createEnvironment adds an environment with its default channel.
func (s *Service) createEnvironment(ctx context.Context, tx pgx.Tx, p auth.Principal, app dbgen.App, key, name string, production bool) (Environment, error) {
	envID, err := s.newID()
	if err != nil {
		return Environment{}, err
	}
	channelID, err := s.newID()
	if err != nil {
		return Environment{}, err
	}
	q := dbgen.New(tx)
	env, err := q.CreateEnvironment(ctx, dbgen.CreateEnvironmentParams{
		ID: storage.MustUUID(envID), OrganizationID: app.OrganizationID, AppID: app.ID,
		Key: key, Name: name, Production: production,
		SigningKeyRef: s.o.SigningKeyPrefix + "-" + envID,
	})
	if err != nil {
		return Environment{}, failure(err, "environment")
	}
	if _, err := q.CreateChannel(ctx, dbgen.CreateChannelParams{
		ID: storage.MustUUID(channelID), OrganizationID: app.OrganizationID, EnvironmentID: env.ID, Key: defaultChannel,
	}); err != nil {
		return Environment{}, failure(err, "channel")
	}
	if err := s.record(ctx, tx, p, audit.EnvironmentAdded, "environment", envID); err != nil {
		return Environment{}, err
	}
	if err := s.record(ctx, tx, p, audit.ChannelCreated, "channel", channelID); err != nil {
		return Environment{}, err
	}
	return environmentOf(env), nil
}

// app reads an app the principal may act on with a permission.
func (*Service) app(ctx context.Context, q *dbgen.Queries, p auth.Principal, want auth.Permission, appID string) (dbgen.App, error) {
	id, err := parseID(appID, "app")
	if err != nil {
		return dbgen.App{}, err
	}
	// The permission is checked before the read, so that an app the
	// caller may not see is not found rather than forbidden.
	if !p.HoldsOnApp(auth.AppRead, appID) && !p.HoldsOnApp(want, appID) {
		return dbgen.App{}, plxerr.New(plxerr.ResourceNotFound, "no such app")
	}
	if err := authorizeApp(p, want, appID); err != nil {
		return dbgen.App{}, err
	}
	row, err := q.GetApp(ctx, id)
	if err != nil {
		return dbgen.App{}, failure(err, "app")
	}
	return row, nil
}

// environment reads an environment and authorises a permission on its
// app.
func (s *Service) environment(ctx context.Context, q *dbgen.Queries, p auth.Principal, want auth.Permission, envID string) (dbgen.Environment, error) {
	id, err := parseID(envID, "environment")
	if err != nil {
		return dbgen.Environment{}, err
	}
	env, err := q.GetEnvironment(ctx, id)
	if err != nil {
		return dbgen.Environment{}, failure(err, "environment")
	}
	if _, err := s.app(ctx, q, p, want, storage.ID(env.AppID)); err != nil {
		return dbgen.Environment{}, err
	}
	return env, nil
}

// GetApp returns an app.
func (s *Service) GetApp(ctx context.Context, p auth.Principal, appID string) (App, error) {
	var out App
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.app(ctx, dbgen.New(tx), p, auth.AppRead, appID)
		out = appOf(row)
		return err
	})
	return out, err
}

// ListApps lists the apps the principal may read, in key order.
func (s *Service) ListApps(ctx context.Context, p auth.Principal, page Page) ([]App, error) {
	allowed := p.AllowedApps()
	var only []pgtype.UUID
	if allowed != nil {
		if len(allowed) == 0 {
			return nil, authorize(p, auth.AppRead)
		}
		for _, id := range allowed {
			only = append(only, storage.MustUUID(id))
		}
	}
	var out []App
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListApps(ctx, dbgen.ListAppsParams{
			OrganizationID: storage.MustUUID(p.OrganizationID), OnlyIds: only,
			AfterKey: page.After.Key, AfterID: page.After.AfterID(), PageSize: page.Size,
		})
		if err != nil {
			return failure(err, "app")
		}
		out = make([]App, len(rows))
		for i, row := range rows {
			out[i] = appOf(row)
		}
		return nil
	})
	return out, err
}

// UpdateApp renames an app or changes its default plugin.
func (s *Service) UpdateApp(ctx context.Context, p auth.Principal, appID, name, defaultPlugin string) (App, error) {
	name = strings.TrimSpace(name)
	if err := checkName("app", name); err != nil {
		return App{}, err
	}
	if defaultPlugin != "" {
		if err := checkKey("plugin", defaultPlugin); err != nil {
			return App{}, err
		}
	}
	var out App
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := s.app(ctx, q, p, auth.AppManage, appID)
		if err != nil {
			return err
		}
		updated, err := q.UpdateApp(ctx, dbgen.UpdateAppParams{ID: row.ID, Name: name, DefaultPluginKey: defaultPlugin})
		if err != nil {
			return failure(err, "app")
		}
		out = appOf(updated)
		return s.record(ctx, tx, p, audit.AppUpdated, "app", appID)
	})
	return out, err
}

// CreateEnvironment adds a custom environment to an app (GOV-010).
func (s *Service) CreateEnvironment(ctx context.Context, p auth.Principal, appID, key, name string, production bool) (Environment, error) {
	name = strings.TrimSpace(name)
	if err := checkKey("environment", key); err != nil {
		return Environment{}, err
	}
	if err := checkName("environment", name); err != nil {
		return Environment{}, err
	}
	var out Environment
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		app, err := s.app(ctx, dbgen.New(tx), p, auth.EnvironmentManage, appID)
		if err != nil {
			return err
		}
		out, err = s.createEnvironment(ctx, tx, p, app, key, name, production)
		return err
	})
	return out, err
}

// ListEnvironments lists an app's environments, in key order.
func (s *Service) ListEnvironments(ctx context.Context, p auth.Principal, appID string, page Page) ([]Environment, error) {
	var out []Environment
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, err := s.app(ctx, q, p, auth.AppRead, appID)
		if err != nil {
			return err
		}
		rows, err := q.ListEnvironments(ctx, dbgen.ListEnvironmentsParams{AppID: app.ID, AfterKey: page.After.Key, PageSize: page.Size})
		if err != nil {
			return failure(err, "environment")
		}
		out = make([]Environment, len(rows))
		for i, row := range rows {
			out[i] = environmentOf(row)
		}
		return nil
	})
	return out, err
}

// UpdateEnvironment renames an environment.
func (s *Service) UpdateEnvironment(ctx context.Context, p auth.Principal, envID, name string) (Environment, error) {
	name = strings.TrimSpace(name)
	if err := checkName("environment", name); err != nil {
		return Environment{}, err
	}
	var out Environment
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.EnvironmentManage, envID)
		if err != nil {
			return err
		}
		row, err := q.UpdateEnvironment(ctx, dbgen.UpdateEnvironmentParams{ID: env.ID, Name: name})
		if err != nil {
			return failure(err, "environment")
		}
		out = environmentOf(row)
		return s.record(ctx, tx, p, audit.EnvironmentSet, "environment", envID)
	})
	return out, err
}

// DeleteEnvironment removes an environment with its channels, variables
// and secrets. A production environment is refused: it is where devices
// are, and removing it would strand them.
func (s *Service) DeleteEnvironment(ctx context.Context, p auth.Principal, envID string) error {
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.EnvironmentManage, envID)
		if err != nil {
			return err
		}
		if env.Production {
			return plxerr.New(plxerr.PreconditionFailed, "a production environment cannot be deleted")
		}
		if _, err := q.DeleteEnvironment(ctx, env.ID); err != nil {
			return failure(err, "environment")
		}
		return s.record(ctx, tx, p, audit.EnvironmentGone, "environment", envID)
	})
}

// variableKey is the form of a variable or secret key: an identifier,
// as a program reading it would spell it.
var variableKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// checkVariableKey refuses a key that is not an identifier.
func checkVariableKey(key string) error {
	if !variableKey.MatchString(key) {
		return plxerr.New(plxerr.InvalidFormat, "a variable or secret key is a letter or underscore followed by up to 127 letters, digits and underscores")
	}
	return nil
}

// SetVariable sets a non-secret variable of an environment.
func (s *Service) SetVariable(ctx context.Context, p auth.Principal, envID, key, value string) (Variable, error) {
	if err := checkVariableKey(key); err != nil {
		return Variable{}, err
	}
	var out Variable
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.EnvironmentManage, envID)
		if err != nil {
			return err
		}
		row, err := q.SetVariable(ctx, dbgen.SetVariableParams{
			OrganizationID: env.OrganizationID, EnvironmentID: env.ID, Key: key, Value: value,
		})
		if err != nil {
			return failure(err, "variable")
		}
		out = Variable{Key: row.Key, Value: row.Value}
		return s.record(ctx, tx, p, audit.VariableSet, "environment", envID)
	})
	return out, err
}

// ListVariables lists an environment's variables, in key order.
func (s *Service) ListVariables(ctx context.Context, p auth.Principal, envID string, page Page) ([]Variable, error) {
	var out []Variable
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.AppRead, envID)
		if err != nil {
			return err
		}
		rows, err := q.ListVariables(ctx, dbgen.ListVariablesParams{EnvironmentID: env.ID, AfterKey: page.After.Key, PageSize: page.Size})
		if err != nil {
			return failure(err, "variable")
		}
		out = make([]Variable, len(rows))
		for i, row := range rows {
			out[i] = Variable{Key: row.Key, Value: row.Value}
		}
		return nil
	})
	return out, err
}

// secretBinding ties a sealed secret to its environment and key.
func secretBinding(envID, key string) []byte {
	return []byte("environment-secret:" + envID + ":" + key)
}

// SetSecret stores a secret with envelope encryption (SEC-106). The
// value is never returned; only a hint of its last characters is.
func (s *Service) SetSecret(ctx context.Context, p auth.Principal, envID, key, value string) (Secret, error) {
	if err := checkVariableKey(key); err != nil {
		return Secret{}, err
	}
	if value == "" {
		return Secret{}, plxerr.New(plxerr.MissingProperty, "a secret needs a value")
	}
	var out Secret
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.SecretsManage, envID)
		if err != nil {
			return err
		}
		sealed, err := signing.Seal(ctx, s.o.Crypter, []byte(value), secretBinding(storage.ID(env.ID), key))
		if err != nil {
			return fmt.Errorf("tenancy: seal a secret: %w", err)
		}
		var by pgtype.UUID
		if p.UserID != "" {
			by = storage.MustUUID(p.UserID)
		}
		row, err := q.SetSecret(ctx, dbgen.SetSecretParams{
			OrganizationID: env.OrganizationID, EnvironmentID: env.ID, Key: key,
			Ciphertext: sealed.Ciphertext, WrappedKey: sealed.WrappedKey, KeyID: sealed.KeyID,
			Hint: sealed.Hint, UpdatedBy: by,
		})
		if err != nil {
			return failure(err, "secret")
		}
		out = Secret{Key: row.Key, Hint: row.Hint, UpdatedAt: storage.Time(row.UpdatedAt), UpdatedBy: storage.ID(row.UpdatedBy)}
		return s.record(ctx, tx, p, audit.SecretSet, "environment", envID)
	})
	return out, err
}

// OpenSecret decrypts a secret for a component that needs its value,
// such as the function runner from P7. It is not reachable from the API.
func (s *Service) OpenSecret(ctx context.Context, p auth.Principal, envID, key string) ([]byte, error) {
	var out []byte
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.SecretsManage, envID)
		if err != nil {
			return err
		}
		row, err := q.GetSecret(ctx, dbgen.GetSecretParams{EnvironmentID: env.ID, Key: key})
		if err != nil {
			return failure(err, "secret")
		}
		out, err = signing.Open(ctx, s.o.Crypter, signing.Sealed{
			Ciphertext: row.Ciphertext, WrappedKey: row.WrappedKey, KeyID: row.KeyID,
		}, secretBinding(storage.ID(env.ID), key))
		if err != nil {
			return fmt.Errorf("tenancy: open a secret: %w", err)
		}
		return nil
	})
	return out, err
}

// ListSecrets lists an environment's secrets without their values.
func (s *Service) ListSecrets(ctx context.Context, p auth.Principal, envID string, page Page) ([]Secret, error) {
	var out []Secret
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.EnvironmentManage, envID)
		if err != nil {
			return err
		}
		rows, err := q.ListSecrets(ctx, dbgen.ListSecretsParams{EnvironmentID: env.ID, AfterKey: page.After.Key, PageSize: page.Size})
		if err != nil {
			return failure(err, "secret")
		}
		out = make([]Secret, len(rows))
		for i, row := range rows {
			out[i] = Secret{Key: row.Key, Hint: row.Hint, UpdatedAt: storage.Time(row.UpdatedAt), UpdatedBy: storage.ID(row.UpdatedBy)}
		}
		return nil
	})
	return out, err
}

// DeleteSecret removes a secret.
func (s *Service) DeleteSecret(ctx context.Context, p auth.Principal, envID, key string) error {
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.SecretsManage, envID)
		if err != nil {
			return err
		}
		n, err := q.DeleteSecret(ctx, dbgen.DeleteSecretParams{EnvironmentID: env.ID, Key: key})
		if err != nil {
			return failure(err, "secret")
		}
		if n == 0 {
			return plxerr.New(plxerr.ResourceNotFound, "no such secret")
		}
		return s.record(ctx, tx, p, audit.SecretDeleted, "environment", envID)
	})
}

// CreateChannel adds a channel to an environment (REL-005).
func (s *Service) CreateChannel(ctx context.Context, p auth.Principal, envID, key string) (Channel, error) {
	if err := checkKey("channel", key); err != nil {
		return Channel{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Channel{}, err
	}
	var out Channel
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.EnvironmentManage, envID)
		if err != nil {
			return err
		}
		row, err := q.CreateChannel(ctx, dbgen.CreateChannelParams{
			ID: storage.MustUUID(id), OrganizationID: env.OrganizationID, EnvironmentID: env.ID, Key: key,
		})
		if err != nil {
			return failure(err, "channel")
		}
		out = channelOf(row)
		return s.record(ctx, tx, p, audit.ChannelCreated, "channel", id)
	})
	return out, err
}

// ListChannels lists an environment's channels, in key order.
func (s *Service) ListChannels(ctx context.Context, p auth.Principal, envID string, page Page) ([]Channel, error) {
	var out []Channel
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := s.environment(ctx, q, p, auth.AppRead, envID)
		if err != nil {
			return err
		}
		rows, err := q.ListChannels(ctx, dbgen.ListChannelsParams{EnvironmentID: env.ID, AfterKey: page.After.Key, PageSize: page.Size})
		if err != nil {
			return failure(err, "channel")
		}
		out = make([]Channel, len(rows))
		for i, row := range rows {
			out[i] = channelOf(row)
		}
		return nil
	})
	return out, err
}

// DeleteChannel removes a channel. The default channel stays: devices
// follow it unless configured otherwise (REL-005).
func (s *Service) DeleteChannel(ctx context.Context, p auth.Principal, channelID string) error {
	cid, err := parseID(channelID, "channel")
	if err != nil {
		return err
	}
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		ch, err := q.GetChannelByID(ctx, cid)
		if err != nil {
			return failure(err, "channel")
		}
		if _, err := s.environment(ctx, q, p, auth.EnvironmentManage, storage.ID(ch.EnvironmentID)); err != nil {
			return err
		}
		if ch.Key == defaultChannel {
			return plxerr.New(plxerr.PreconditionFailed, "the %q channel cannot be deleted", defaultChannel)
		}
		if _, err := q.DeleteChannel(ctx, cid); err != nil {
			return failure(err, "channel")
		}
		return s.record(ctx, tx, p, audit.ChannelDeleted, "channel", channelID)
	})
}

// GrantAccess gives a team or a user a role on one app (GOV-001).
// Granting again replaces the role.
func (s *Service) GrantAccess(ctx context.Context, p auth.Principal, appID, teamID, userID, role string) (AccessGrant, error) {
	if err := authorize(p, auth.MembersManage); err != nil {
		return AccessGrant{}, err
	}
	if err := checkRole(role); err != nil {
		return AccessGrant{}, err
	}
	team, user, err := holder(teamID, userID)
	if err != nil {
		return AccessGrant{}, err
	}
	id, err := s.newID()
	if err != nil {
		return AccessGrant{}, err
	}
	var out AccessGrant
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, err := s.app(ctx, q, p, auth.AppRead, appID)
		if err != nil {
			return err
		}
		if err := holderExists(ctx, q, app.OrganizationID, team, user); err != nil {
			return err
		}
		existing, err := q.FindAppAccess(ctx, dbgen.FindAppAccessParams{AppID: app.ID, TeamID: team, UserID: user})
		var row dbgen.AppAccess
		switch {
		case err == nil:
			row, err = q.UpdateAppAccess(ctx, dbgen.UpdateAppAccessParams{ID: existing.ID, Role: role})
		case errors.Is(err, pgx.ErrNoRows):
			row, err = q.GrantAppAccess(ctx, dbgen.GrantAppAccessParams{
				ID: storage.MustUUID(id), OrganizationID: app.OrganizationID, AppID: app.ID,
				TeamID: team, UserID: user, Role: role,
			})
		}
		if err != nil {
			return failure(err, "grant")
		}
		out = grantOf(row)
		return s.record(ctx, tx, p, audit.AccessGranted, "app", appID)
	})
	return out, err
}

// holder reads who a grant is for: exactly one of a team and a user.
func holder(teamID, userID string) (pgtype.UUID, pgtype.UUID, error) {
	switch {
	case (teamID == "") == (userID == ""):
		return pgtype.UUID{}, pgtype.UUID{}, plxerr.New(plxerr.InvalidStructure, "a grant names exactly one of a team and a user")
	case teamID != "":
		team, err := parseID(teamID, "team")
		return team, pgtype.UUID{}, err
	default:
		user, err := parseID(userID, "user")
		return pgtype.UUID{}, user, err
	}
}

// holderExists checks that a grant's team exists, or that its user
// belongs to the organisation, directly or through a team: nobody
// outside it is granted anything in it.
func holderExists(ctx context.Context, q *dbgen.Queries, org, team, user pgtype.UUID) error {
	if team.Valid {
		if _, err := q.GetTeam(ctx, team); err != nil {
			return failure(err, "team")
		}
		return nil
	}
	rows, err := q.ListMembershipsForUser(ctx, dbgen.ListMembershipsForUserParams{OrganizationID: org, UserID: user})
	if err != nil {
		return failure(err, "membership")
	}
	if len(rows) == 0 {
		return plxerr.New(plxerr.ResourceNotFound, "no such member of this organisation")
	}
	return nil
}

// RevokeAccess removes a grant.
func (s *Service) RevokeAccess(ctx context.Context, p auth.Principal, grantID string) error {
	if err := authorize(p, auth.MembersManage); err != nil {
		return err
	}
	gid, err := parseID(grantID, "grant")
	if err != nil {
		return err
	}
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		n, err := dbgen.New(tx).RevokeAppAccess(ctx, gid)
		if err != nil {
			return failure(err, "grant")
		}
		if n == 0 {
			return plxerr.New(plxerr.ResourceNotFound, "no such grant")
		}
		return s.record(ctx, tx, p, audit.AccessRevoked, "grant", grantID)
	})
}

// ListAccess lists the grants on an app.
func (s *Service) ListAccess(ctx context.Context, p auth.Principal, appID string, page Page) ([]AccessGrant, error) {
	var out []AccessGrant
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, err := s.app(ctx, q, p, auth.AppRead, appID)
		if err != nil {
			return err
		}
		rows, err := q.ListAppAccess(ctx, dbgen.ListAppAccessParams{AppID: app.ID, AfterID: page.After.AfterID(), PageSize: page.Size})
		if err != nil {
			return failure(err, "grant")
		}
		out = make([]AccessGrant, len(rows))
		for i, row := range rows {
			out[i] = grantOf(row)
		}
		return nil
	})
	return out, err
}

// appOf converts a stored app.
func appOf(row dbgen.App) App {
	return App{
		ID: storage.ID(row.ID), OrganizationID: storage.ID(row.OrganizationID), Key: row.Key, Name: row.Name,
		DefaultPluginKey: row.DefaultPluginKey, CreatedAt: storage.Time(row.CreatedAt), UpdatedAt: storage.Time(row.UpdatedAt),
	}
}

// environmentOf converts a stored environment.
func environmentOf(row dbgen.Environment) Environment {
	return Environment{
		ID: storage.ID(row.ID), AppID: storage.ID(row.AppID), Key: row.Key, Name: row.Name,
		Production: row.Production, SigningKeyRef: row.SigningKeyRef, CreatedAt: storage.Time(row.CreatedAt),
	}
}

// channelOf converts a stored channel.
func channelOf(row dbgen.Channel) Channel {
	return Channel{
		ID: storage.ID(row.ID), EnvironmentID: storage.ID(row.EnvironmentID), Key: row.Key,
		ReleaseSequence: row.ReleaseSequence, UpdatedAt: storage.Time(row.UpdatedAt),
	}
}

// grantOf converts a stored grant.
func grantOf(row dbgen.AppAccess) AccessGrant {
	return AccessGrant{
		ID: storage.ID(row.ID), AppID: storage.ID(row.AppID), TeamID: storage.ID(row.TeamID),
		UserID: storage.ID(row.UserID), Role: row.Role, GrantedAt: storage.Time(row.GrantedAt),
	}
}
