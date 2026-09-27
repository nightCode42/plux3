// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// IDs generates the identifier of a new entry.
type IDs interface {
	New() (string, error)
}

// Log appends to, and reads, the audit chain.
type Log struct {
	ids IDs
	now func() time.Time
}

// NewLog returns a log. A nil clock uses time.Now.
func NewLog(ids IDs, now func() time.Time) *Log {
	if now == nil {
		now = time.Now
	}
	return &Log{ids: ids, now: now}
}

// Append writes an entry inside the caller's transaction, so the record
// and the change it describes commit together (SEC-140). The sequence,
// the hashes and the time are set here; the source address, user agent
// and request ID come from the request in the context (WithRequest);
// the caller supplies the rest.
//
// An entry with no organisation belongs to the installation's own chain,
// which only a transaction in the installation scope can write
// (storage.ScopeInstallation).
//
// Appends to one chain are serialised by a transaction-scoped advisory
// lock taken before the last entry is read, so two concurrent writers
// cannot both extend the same entry; the unique sequence is the
// backstop, never the mechanism.
func (l *Log) Append(ctx context.Context, tx pgx.Tx, e Entry) (Entry, error) {
	if !Registered(e.Action) {
		return Entry{}, fmt.Errorf("audit: %q is not a registered action", e.Action)
	}
	if e.Actor.Kind == "" {
		return Entry{}, errors.New("audit: an entry needs an actor")
	}
	r := RequestFrom(ctx)
	e.SourceIP, e.UserAgent, e.RequestID = r.SourceIP, r.UserAgent, r.RequestID
	q := dbgen.New(tx)
	org, err := storage.NullUUID(e.OrganizationID)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	if err := q.LockAuditChain(ctx, org); err != nil {
		return Entry{}, fmt.Errorf("audit: lock the chain: %w", err)
	}
	e.Sequence, e.PreviousHash = 1, ""
	last, err := q.LastAuditEntry(ctx, org)
	switch {
	case err == nil:
		e.Sequence, e.PreviousHash = last.Sequence+1, last.EntryHash
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return Entry{}, fmt.Errorf("audit: read the last entry: %w", err)
	}

	id, err := l.ids.New()
	if err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	e.ID = id
	e.At = l.now().UTC().Truncate(time.Microsecond)
	e.EntryHash = e.Hash()

	entryID, err := storage.UUID(e.ID)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	row, err := q.AppendAuditEntry(ctx, dbgen.AppendAuditEntryParams{
		ID:             entryID,
		OrganizationID: org,
		Sequence:       e.Sequence,
		OccurredAt:     storage.Timestamp(e.At),
		ActorKind:      e.Actor.Kind,
		ActorID:        e.Actor.ID,
		ActorDisplay:   e.Actor.Display,
		Action:         string(e.Action),
		TargetKind:     e.TargetKind,
		TargetID:       e.TargetID,
		SourceIp:       e.SourceIP,
		UserAgent:      e.UserAgent,
		RequestID:      e.RequestID,
		BeforeHash:     e.BeforeHash,
		AfterHash:      e.AfterHash,
		PreviousHash:   e.PreviousHash,
		EntryHash:      e.EntryHash,
	})
	if err != nil {
		return Entry{}, fmt.Errorf("audit: append: %w", err)
	}
	return fromRow(row), nil
}

// List returns entries of a chain after a sequence number, in order;
// organizationID "" is the installation's chain.
func (*Log) List(ctx context.Context, tx pgx.Tx, organizationID string, afterSequence int64, limit int32) ([]Entry, error) {
	org, err := storage.NullUUID(organizationID)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	rows, err := dbgen.New(tx).ListAuditEntries(ctx, dbgen.ListAuditEntriesParams{
		OrganizationID: org,
		AfterSequence:  afterSequence,
		PageSize:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	out := make([]Entry, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, nil
}

// VerifyChain reads a whole chain in pages and checks it (SEC-140). It
// reports the first entry that was changed, removed or inserted.
func (l *Log) VerifyChain(ctx context.Context, tx pgx.Tx, organizationID string) (int64, error) {
	var (
		after    int64
		previous string
		count    int64
	)
	for {
		page, err := l.List(ctx, tx, organizationID, after, 1000)
		if err != nil {
			return count, err
		}
		if len(page) == 0 {
			return count, nil
		}
		if page[0].Sequence != after+1 {
			return count, fmt.Errorf("audit: sequence jumps from %d to %d: an entry is missing", after, page[0].Sequence)
		}
		if err := Verify(page, previous); err != nil {
			return count, err
		}
		count += int64(len(page))
		after, previous = page[len(page)-1].Sequence, page[len(page)-1].EntryHash
	}
}

// Request is what the audit log records about the call that caused an
// entry.
type Request struct {
	SourceIP  string
	UserAgent string
	RequestID string
}

// requestKey carries a Request in a context.
type requestKey struct{}

// WithRequest returns a context carrying the request an entry records.
// The API edge sets it once per call, so no domain method has to thread
// it through.
func WithRequest(ctx context.Context, r Request) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}

// RequestFrom returns the request in a context; the zero Request when
// there is none.
func RequestFrom(ctx context.Context) Request {
	r, _ := ctx.Value(requestKey{}).(Request)
	return r
}

// fromRow converts a stored row.
func fromRow(row dbgen.AuditLog) Entry {
	return Entry{
		ID:             storage.ID(row.ID),
		OrganizationID: storage.ID(row.OrganizationID),
		Sequence:       row.Sequence,
		At:             storage.Time(row.OccurredAt),
		Actor:          Actor{Kind: row.ActorKind, ID: row.ActorID, Display: row.ActorDisplay},
		Action:         Action(row.Action),
		TargetKind:     row.TargetKind,
		TargetID:       row.TargetID,
		SourceIP:       row.SourceIp,
		UserAgent:      row.UserAgent,
		RequestID:      row.RequestID,
		BeforeHash:     row.BeforeHash,
		AfterHash:      row.AfterHash,
		PreviousHash:   row.PreviousHash,
		EntryHash:      row.EntryHash,
	}
}
