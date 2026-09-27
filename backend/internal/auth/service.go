// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// IDs generates identifiers for the rows this package writes.
type IDs interface {
	New() (string, error)
}

// TrustedIssuer is a CI provider whose identity tokens may be exchanged
// (SRV-064). The installation trusts it; an organisation then says
// which of its workloads may act for it.
type TrustedIssuer struct {
	// Verifier checks the provider's tokens.
	Verifier *Verifier
	// Audience is the audience every exchanged token must carry.
	Audience string
	// SubjectPattern bounds the subjects any organisation may accept.
	SubjectPattern string
}

// Options configures a Service.
type Options struct {
	DB    *storage.DB
	Audit *audit.Log
	// Cache counts failed sign-ins (auth.failedSignIns).
	Cache  cache.Cache
	Limits limits.Set
	// Crypter seals the TOTP secrets stored at rest (SEC-106).
	Crypter signing.Crypter
	IDs     IDs
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// SessionTTL is how long a browser session lasts.
	SessionTTL time.Duration
	// CITokenTTL is how long a token exchanged from a CI identity lasts.
	CITokenTTL time.Duration
	// CLITokenTTL is how long a token from `plux login` lasts.
	CLITokenTTL time.Duration
	// Issuer names this installation in an otpauth URL.
	Issuer string
	// VerificationURI is where a person approves `plux login`.
	VerificationURI string
	// Issuers are the trusted CI providers, by issuer URL.
	Issuers map[string]TrustedIssuer
}

// Durations the requirements or RFCs fix rather than configuration.
const (
	// challengeTTL is how long a sign-in waits for its second factor.
	challengeTTL = 5 * time.Minute
	// challengeAttempts is how many wrong codes a challenge accepts.
	challengeAttempts = 5
	// deviceCodeTTL is how long a device authorization grant waits
	// (RFC 8628 §3.2 leaves it to the server).
	deviceCodeTTL = 15 * time.Minute
	// devicePollInterval is the least time between two polls.
	devicePollInterval = 5 * time.Second
	// invitationTTL is how long an invitation may be accepted.
	invitationTTL = 7 * 24 * time.Hour
	// maxTokenTTL bounds a personal access token: tokens never last for
	// ever (SRV-064).
	maxTokenTTL = 366 * 24 * time.Hour
	// failureWindow is the window auth.failedSignIns counts in.
	failureWindow = 15 * time.Minute
)

// Service is the domain logic of identity: accounts, sessions, second
// factors, tokens and workload identities. Every method that changes
// state records it in the audit log in the same transaction (SEC-140).
type Service struct {
	o   Options
	now func() time.Time
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.DB == nil:
		return nil, errors.New("auth: a database is required")
	case o.Audit == nil:
		return nil, errors.New("auth: an audit log is required")
	case o.Cache == nil:
		return nil, errors.New("auth: a cache is required to count failed sign-ins")
	case o.Crypter == nil:
		return nil, errors.New("auth: a crypter is required; second factors are encrypted at rest")
	case o.IDs == nil:
		return nil, errors.New("auth: an identifier generator is required")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	if o.SessionTTL <= 0 {
		o.SessionTTL = 12 * time.Hour
	}
	if o.CITokenTTL <= 0 {
		o.CITokenTTL = time.Hour
	}
	if o.CLITokenTTL <= 0 {
		o.CLITokenTTL = 30 * 24 * time.Hour
	}
	if o.Issuer == "" {
		o.Issuer = "Plux"
	}
	return &Service{o: o, now: now}, nil
}

// Kinds of identity.
const (
	// KindUser is a person signed in with a browser session.
	KindUser = "user"
	// KindToken is a personal access token or a `plux login` token.
	KindToken = "token"
	// KindCI is a token exchanged from a CI identity.
	KindCI = "ci"
	// KindSystem is the server acting for itself.
	KindSystem = "system"
)

// Identity is who presented a credential, before an organisation is
// chosen.
type Identity struct {
	// Kind is KindUser, KindToken, KindCI or KindSystem.
	Kind string
	// ID is the user for a session, the token for a token.
	ID string
	// UserID is the person behind the credential; "" for CI.
	UserID string
	// Display is a human-readable name; never an email address.
	Display string
	// SessionID is set for a browser session.
	SessionID string
	// CSRFToken is the session's token, which a state-changing call
	// must echo (SEC-101).
	CSRFToken string
	// OrganizationID is the organisation a token is bound to; "" for a
	// session, which may act in any organisation the user belongs to.
	OrganizationID string
	// Scopes restrict a token; nil for a session.
	Scopes []Permission
	// SecondFactor is true when a second factor was presented in the
	// session. A token is minted only in a session that presented one,
	// or by an administrator who did, so it counts as having one.
	SecondFactor bool
	// InstallationAdmin may create organisations.
	InstallationAdmin bool
}

// Actor is how the audit log records the identity.
func (id Identity) Actor() audit.Actor {
	return audit.Actor{Kind: id.Kind, ID: id.ID, Display: id.Display}
}

// Anonymous is the actor of an unauthenticated call, such as a refused
// sign-in.
var Anonymous = audit.Actor{Kind: "anonymous", Display: "anonymous"}

// newID generates an identifier.
func (s *Service) newID() (string, error) {
	id, err := s.o.IDs.New()
	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	return id, nil
}

// inTx runs f in a transaction with the given visibility.
func (s *Service) inTx(ctx context.Context, t storage.Tenant, f func(context.Context, pgx.Tx) error) error {
	return s.o.DB.InTx(ctx, t, f) //nolint:wrapcheck // InTx wraps its own failures; f's are already domain errors
}

// record appends an audit entry, failing the transaction when it cannot
// be written: an operation never succeeds unrecorded (SEC-140).
func (s *Service) record(ctx context.Context, tx pgx.Tx, e audit.Entry) error {
	if _, err := s.o.Audit.Append(ctx, tx, e); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// throttleKey is the counter of failed attempts for an account. The
// address is hashed so that it never sits in the cache in the clear.
func throttleKey(account string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account))))
	return "auth:failed:" + hex.EncodeToString(sum[:16])
}

// throttled refuses once an account has failed auth.failedSignIns times
// in the window (SEC-100). A cache that cannot answer does not lock
// everyone out: the limit is a protection, not a correctness property.
func (s *Service) throttled(ctx context.Context, account string) error {
	v, ok, err := s.o.Cache.Get(ctx, throttleKey(account))
	if err != nil || !ok {
		return nil //nolint:nilerr // failing open is the decision (ADR-0007)
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		return nil //nolint:nilerr // an unreadable counter is treated as none
	}
	if limit := s.o.Limits.Get(limits.AuthFailedSignIns); n >= limit {
		return plxerr.New(plxerr.RateLimited,
			"too many failed attempts for this account; wait %d minutes", int(failureWindow.Minutes()))
	}
	return nil
}

// failed counts a failed attempt against an account.
func (s *Service) failed(ctx context.Context, account string) {
	_, _ = s.o.Cache.Increment(ctx, throttleKey(account), failureWindow)
}

// succeeded clears an account's failures.
func (s *Service) succeeded(ctx context.Context, account string) {
	_ = s.o.Cache.Delete(ctx, throttleKey(account))
}

// uniqueViolation reports whether an error is PostgreSQL's unique
// violation, so that a duplicate key becomes RESOURCE_EXISTS.
func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// notFound turns "no rows" into the not-found refusal, and wraps other
// failures.
func notFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return plxerr.New(plxerr.ResourceNotFound, "no such %s", what)
	}
	return fmt.Errorf("auth: read %s: %w", what, err)
}

// parseID validates an identifier from a caller.
func parseID(s, what string) (pgtype.UUID, error) {
	id, err := storage.UUID(s)
	if err != nil {
		return pgtype.UUID{}, plxerr.New(plxerr.InvalidFormat, "the %s identifier is not valid", what)
	}
	return id, nil
}
