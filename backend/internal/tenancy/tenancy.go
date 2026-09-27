// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package tenancy is the Installation → Organization → Teams hierarchy,
// the apps an organisation owns with their environments, channels,
// variables and secrets, the access teams and users hold per app, and
// the trash deleted things wait in (GOV-001, GOV-010, GOV-031, SEC-106).
//
// Every method takes the principal the API edge resolved, authorises it
// against the permission the operation needs before doing anything
// (SEC-102), runs in a transaction bound to the principal's organisation
// so row-level security is the second barrier (SRV-022), and records
// what it changed in the audit log in the same transaction (SEC-140).
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// IDs generates identifiers for the rows this package writes.
type IDs interface {
	New() (string, error)
}

// Options configures a Service.
type Options struct {
	DB    *storage.DB
	Audit *audit.Log
	// Auth creates the accounts of invited members.
	Auth *auth.Service
	// Crypter seals environment secrets (SEC-106).
	Crypter signing.Crypter
	IDs     IDs
	// Limits are the installation's limits, which organisations and
	// apps tighten (LIM-002).
	Limits limits.Set
	// TrashDays is how long a deleted item can be restored (GOV-031).
	TrashDays int
	// SigningKeyPrefix names each environment's targets key,
	// "<prefix>-<environment ID>" (ADR-0004).
	SigningKeyPrefix string
	// TrashKinds are the kinds of item, beyond apps, that other services
	// put in the trash, with how to restore and purge each.
	TrashKinds map[string]TrashKind
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Service is the domain logic of tenancy.
type Service struct {
	o   Options
	now func() time.Time
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.DB == nil:
		return nil, errors.New("tenancy: a database is required")
	case o.Audit == nil:
		return nil, errors.New("tenancy: an audit log is required")
	case o.Auth == nil:
		return nil, errors.New("tenancy: the identity service is required")
	case o.Crypter == nil:
		return nil, errors.New("tenancy: a crypter is required; secrets are encrypted at rest")
	case o.IDs == nil:
		return nil, errors.New("tenancy: an identifier generator is required")
	case o.SigningKeyPrefix == "":
		return nil, errors.New("tenancy: a signing key prefix is required")
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	if o.TrashDays <= 0 {
		o.TrashDays = 30
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{o: o, now: now}, nil
}

// keyForm is the form of every key a person chooses: an organisation,
// team, app, environment or channel key.
var keyForm = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// checkKey refuses a key that is not of the key form.
func checkKey(what, key string) error {
	if !keyForm.MatchString(key) {
		return plxerr.New(plxerr.InvalidFormat,
			"a %s key is 1 to 64 lower-case letters, digits and hyphens, starting and ending with a letter or digit", what)
	}
	return nil
}

// checkName refuses an empty or oversized display name.
func checkName(what, name string) error {
	if name == "" || len(name) > 200 {
		return plxerr.New(plxerr.InvalidFormat, "a %s name is 1 to 200 characters", what)
	}
	return nil
}

// inOrg runs f in a transaction bound to the principal's organisation.
func (s *Service) inOrg(ctx context.Context, p auth.Principal, f func(context.Context, pgx.Tx) error) error {
	return s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, f) //nolint:wrapcheck // InTx wraps its own failures
}

// record appends an audit entry for the principal's organisation.
func (s *Service) record(ctx context.Context, tx pgx.Tx, p auth.Principal, action audit.Action, kind, id string) error {
	if _, err := s.o.Audit.Append(ctx, tx, audit.Entry{
		OrganizationID: p.OrganizationID, Actor: p.Actor(),
		Action: action, TargetKind: kind, TargetID: id,
	}); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	return nil
}

// newID generates an identifier.
func (s *Service) newID() (string, error) {
	id, err := s.o.IDs.New()
	if err != nil {
		return "", fmt.Errorf("tenancy: %w", err)
	}
	return id, nil
}

// failure turns a database error into the refusal a caller understands:
// no rows is not found, a unique violation is "already exists"; anything
// else is wrapped as an internal failure.
func failure(err error, what string) error {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return plxerr.New(plxerr.ResourceNotFound, "no such %s", what)
	case errors.As(err, &pg) && pg.Code == "23505":
		return plxerr.New(plxerr.ResourceExists, "a %s with this key already exists", what)
	default:
		return fmt.Errorf("tenancy: %s: %w", what, err)
	}
}

// parseID validates an identifier from a caller.
func parseID(s, what string) (pgtype.UUID, error) {
	id, err := storage.UUID(s)
	if err != nil {
		return pgtype.UUID{}, plxerr.New(plxerr.InvalidFormat, "the %s identifier is not valid", what)
	}
	return id, nil
}

// authorize is Principal.Authorize, with the refusal marked as this
// package's.
func authorize(p auth.Principal, want auth.Permission) error {
	if err := p.Authorize(want); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	return nil
}

// authorizeApp is Principal.AuthorizeApp, with the refusal marked as
// this package's.
func authorizeApp(p auth.Principal, want auth.Permission, appID string) error {
	if err := p.AuthorizeApp(want, appID); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	return nil
}

// checkRole refuses a name that is not a role.
func checkRole(s string) error {
	if _, err := auth.ParseRole(s); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	return nil
}

// Page asks for one page of a list.
type Page struct {
	After storage.Cursor
	Size  int32
}
