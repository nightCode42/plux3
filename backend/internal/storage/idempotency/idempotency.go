// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package idempotency lets a mutating call be retried safely: a repeat
// with the same key within 24 hours returns the original result instead
// of acting twice (SRV-005).
//
// A key belongs to the caller's subject, so one caller can never read
// another's result. The first call claims the key before it acts; a
// repeat that arrives while it is still running is refused rather than
// run concurrently; a failed call releases the key, so the retry acts.
// Results are sealed with envelope encryption, because some carry a
// secret that is returned exactly once (SEC-106).
package idempotency

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// TTL is how long a key is remembered (SRV-005).
const TTL = 24 * time.Hour

// MaxKeyLength bounds a key.
const MaxKeyLength = 128

// Store remembers keys and results.
type Store struct {
	db      *storage.DB
	crypter signing.Crypter
	now     func() time.Time
}

// NewStore returns a store. A nil clock uses time.Now.
func NewStore(db *storage.DB, crypter signing.Crypter, now func() time.Time) (*Store, error) {
	if db == nil || crypter == nil {
		return nil, errors.New("idempotency: a database and a crypter are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, crypter: crypter, now: now}, nil
}

// Call identifies one keyed call.
type Call struct {
	// Subject is the caller, such as "user:<id>" or "token:<id>".
	Subject string
	// Key is the caller's idempotency key.
	Key string
	// Procedure is the RPC.
	Procedure string
	// Request is the request's deterministic encoding.
	Request []byte
}

// valid refuses a key that is empty, too long or not printable ASCII.
func (c Call) valid() error {
	if c.Subject == "" {
		return plxerr.New(plxerr.AuthenticationRequired, "an idempotency key needs an authenticated caller")
	}
	if c.Key == "" || len(c.Key) > MaxKeyLength {
		return plxerr.New(plxerr.InvalidFormat, "an idempotency key is 1 to %d characters", MaxKeyLength)
	}
	for _, r := range c.Key {
		if r < 0x21 || r > 0x7e {
			return plxerr.New(plxerr.InvalidFormat, "an idempotency key is printable ASCII without spaces")
		}
	}
	return nil
}

// hash commits to the procedure and the request, so a key reused for a
// different request is recognised.
func (c Call) hash() []byte {
	h := sha256.New()
	h.Write([]byte(c.Procedure))
	h.Write([]byte{0})
	h.Write(c.Request)
	return h.Sum(nil)
}

// binding ties a sealed result to its subject and key.
func (c Call) binding() []byte { return []byte("idempotency:" + c.Subject + ":" + c.Key) }

// Begin claims a key. It returns the stored result and true when the
// call was already completed, and false when this call should act and
// then call Complete or Abandon. An empty result is still a result, so
// the flag, not the bytes, says which.
func (s *Store) Begin(ctx context.Context, c Call) ([]byte, bool, error) {
	if err := c.valid(); err != nil {
		return nil, false, err
	}
	var (
		replay []byte
		done   bool
	)
	err := s.db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		if _, err := q.ReplaceExpiredIdempotent(ctx, dbgen.ReplaceExpiredIdempotentParams{Subject: c.Subject, Key: c.Key}); err != nil {
			return fmt.Errorf("idempotency: %w", err)
		}
		_, err := q.BeginIdempotent(ctx, dbgen.BeginIdempotentParams{
			Subject: c.Subject, Key: c.Key, Procedure: c.Procedure, RequestHash: c.hash(),
			ExpiresAt: storage.Timestamp(s.now().Add(TTL)),
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("idempotency: claim a key: %w", err)
		}
		existing, err := q.GetIdempotent(ctx, dbgen.GetIdempotentParams{Subject: c.Subject, Key: c.Key})
		if err != nil {
			return fmt.Errorf("idempotency: read a key: %w", err)
		}
		if existing.Procedure != c.Procedure || string(existing.RequestHash) != string(c.hash()) {
			return plxerr.New(plxerr.IdempotencyConflict, "this idempotency key was used for a different request")
		}
		if existing.State != "done" {
			return plxerr.New(plxerr.IdempotencyConflict, "a call with this idempotency key is still in progress")
		}
		sealed, err := signing.DecodeSealed(existing.Response)
		if err != nil {
			return fmt.Errorf("idempotency: %w", err)
		}
		replay, err = signing.Open(ctx, s.crypter, sealed, c.binding())
		if err != nil {
			return fmt.Errorf("idempotency: open a result: %w", err)
		}
		done = true
		return nil
	})
	if err != nil {
		return nil, false, err //nolint:wrapcheck // already domain errors or wrapped
	}
	return replay, done, nil
}

// Complete stores the result of a call that Begin let act.
func (s *Store) Complete(ctx context.Context, c Call, response []byte) error {
	sealed, err := signing.Seal(ctx, s.crypter, response, c.binding())
	if err != nil {
		return fmt.Errorf("idempotency: seal a result: %w", err)
	}
	return s.db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck // InTx wraps its own failures
		if _, err := dbgen.New(tx).CompleteIdempotent(ctx, dbgen.CompleteIdempotentParams{
			Subject: c.Subject, Key: c.Key, Response: sealed.Encode(),
		}); err != nil {
			return fmt.Errorf("idempotency: store a result: %w", err)
		}
		return nil
	})
}

// Abandon releases a key whose call failed, so that a retry acts. A
// failure is not remembered: repeating a refused call must be allowed
// to succeed once its cause is fixed.
func (s *Store) Abandon(ctx context.Context, c Call) error {
	return s.db.InTx(context.WithoutCancel(ctx), storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck // InTx wraps its own failures
		if _, err := dbgen.New(tx).AbandonIdempotent(ctx, dbgen.AbandonIdempotentParams{Subject: c.Subject, Key: c.Key}); err != nil {
			return fmt.Errorf("idempotency: release a key: %w", err)
		}
		return nil
	})
}

// Expire deletes keys older than the TTL, for the maintenance job.
func (s *Store) Expire(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = dbgen.New(tx).DeleteExpiredIdempotent(ctx)
		if err != nil {
			return fmt.Errorf("idempotency: expire keys: %w", err)
		}
		return nil
	})
	return n, err //nolint:wrapcheck // InTx wraps its own failures
}
