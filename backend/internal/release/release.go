// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package release turns drafts into immutable plugin versions and sets of
// them into app releases, promotes releases between environments and
// rolls them back (ADR-0020).
//
// A publish is a durable job (SRV-050): the api role freezes the draft
// at a revision and enqueues the job in the same transaction; the worker
// role compiles, checks, signs and stores it, and only its last step —
// recording the version — changes anything a device could see, so a
// failed publish leaves nothing behind (SRV-051). Signing happens only in
// the worker, which alone is given a signer (SRV-052, ADR-0006).
package release

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// IDs generates identifiers for the rows this package writes.
type IDs interface {
	New() (string, error)
}

// Job asks the worker to run a publish.
type Job struct {
	OrganizationID string `json:"organizationId"`
	JobID          string `json:"jobId"`
}

// Kind names the job.
func (Job) Kind() string { return "publish.run" }

// Work is a job this package asks the worker to run: a Job, a
// ManifestJob or a DeltaJob.
type Work interface {
	Kind() string
}

// Enqueuer adds a job in the caller's transaction.
type Enqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, job Work) error
}

// Devices counts the registered devices of an app that cannot use a
// release, for REL-080; nil counts none.
type Devices interface {
	Incompatible(ctx context.Context, tx pgx.Tx, appID, minRuntime string) (int64, error)
}

// Options configures a Service.
type Options struct {
	DB        *storage.DB
	Audit     *audit.Log
	Tenancy   *tenancy.Service
	Documents *document.Service
	Objects   objects.Store
	IDs       IDs
	// Jobs enqueues publishes; the api role sets it.
	Jobs Enqueuer
	// Signer signs bundle hashes and manifests; only the worker role is
	// given one (SRV-052). Nil refuses to run a publish.
	Signer signing.Signer
	// ProductionSigning reports whether the signer may sign for a
	// production environment; the file backend may not (SEC-056).
	ProductionSigning bool
	// PublicBaseURL is where this server is reached, for object URLs
	// when the store has no location of its own (DEP-041).
	PublicBaseURL string
	// Devices answers REL-080's question; nil counts none.
	Devices Devices
	// Limits are the installation's limits.
	Limits limits.Set
	// CompilerVersion is recorded in every bundle (CMP-005).
	CompilerVersion string
	// DevelopmentDays is how long a release never promoted to production
	// is kept (REL-007).
	DevelopmentDays int
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Service is the domain logic of versions and releases.
type Service struct {
	o         Options
	now       func() time.Time
	flights   *flightGroup
	manifests *manifestCache
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.DB == nil, o.Audit == nil, o.Tenancy == nil, o.Documents == nil, o.IDs == nil:
		return nil, errors.New("release: a database, audit log, tenancy, documents and identifiers are required")
	case o.Objects == nil:
		return nil, errors.New("release: object storage is required for bundles")
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	if o.CompilerVersion == "" {
		o.CompilerVersion = "dev"
	}
	if o.DevelopmentDays <= 0 {
		o.DevelopmentDays = 90
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{o: o, now: now, flights: &flightGroup{}, manifests: &manifestCache{}}, nil
}

// inOrg runs f in a transaction bound to the principal's organisation.
func (s *Service) inOrg(ctx context.Context, p auth.Principal, f func(context.Context, pgx.Tx) error) error {
	return s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, f) //nolint:wrapcheck // InTx wraps its own failures
}

// newID generates an identifier.
func (s *Service) newID() (string, error) {
	id, err := s.o.IDs.New()
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	return id, nil
}

// record appends an audit entry for the principal.
func (s *Service) record(ctx context.Context, tx pgx.Tx, p auth.Principal, e audit.Entry) error {
	e.OrganizationID, e.Actor = p.OrganizationID, p.Actor()
	if _, err := s.o.Audit.Append(ctx, tx, e); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// authorize is Principal.AuthorizeApp with the refusal marked as this
// package's.
func authorize(p auth.Principal, want auth.Permission, appID string) error {
	if err := p.AuthorizeApp(want, appID); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// failure turns a database error into the refusal a caller understands.
func failure(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return plxerr.New(plxerr.ResourceNotFound, "no such %s", what)
	}
	return fmt.Errorf("release: %s: %w", what, err)
}

// parseID validates an identifier from a caller.
func parseID(s, what string) (pgtype.UUID, error) {
	id, err := storage.UUID(s)
	if err != nil {
		return pgtype.UUID{}, plxerr.New(plxerr.InvalidFormat, "the %s identifier is not valid", what)
	}
	return id, nil
}
