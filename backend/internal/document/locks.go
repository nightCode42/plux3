// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Lock is a draft's exclusive editing lock (SRV-040).
type Lock struct {
	AppID       string
	PluginID    string
	Holder      audit.Actor
	Session     string
	AcquiredAt  time.Time
	ExpiresAt   time.Time
	RequestedBy []audit.Actor
}

// Held reports whether the lock is in force at a moment.
func (l Lock) Held(at time.Time) bool { return l.Session != "" && at.Before(l.ExpiresAt) }

// checkSession refuses an editing session that is not a short printable
// string: the lock compares it, and the audit log records it.
func checkSession(session string) error {
	if session == "" || len(session) > 128 {
		return plxerr.New(plxerr.InvalidFormat, "an editing session is 1 to 128 characters")
	}
	for _, r := range session {
		if r < 0x21 || r > 0x7e {
			return plxerr.New(plxerr.InvalidFormat, "an editing session is printable ASCII without spaces")
		}
	}
	return nil
}

// AcquireLock takes a draft's editing lock (SRV-040). A lock held by
// someone else is refused with PLX-8020 unless force is set, which needs
// plugin.lock.override; a takeover snapshots the draft first, so the
// previous holder's work is recoverable, and is audited with both
// parties (SRV-041). The holder's own other session may take its lock
// back without the permission.
func (s *Service) AcquireLock(ctx context.Context, p auth.Principal, appID, pluginID, session string, force bool) (Lock, string, error) {
	if err := checkSession(session); err != nil {
		return Lock{}, "", err
	}
	var (
		out       Lock
		preserved string
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, d.editPermission(), appID); err != nil {
			return err
		}
		if _, err := q.GetDraftForUpdate(ctx, d.row.ID); err != nil {
			return failure(err, "draft")
		}
		out, preserved, err = s.takeLock(ctx, tx, q, d, p, session, force)
		return err
	})
	return out, preserved, err
}

// takeLock takes the lock of a resolved draft whose row the caller holds
// locked, snapshotting it first when it is taken over.
func (s *Service) takeLock(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, d draft, p auth.Principal, session string, force bool) (Lock, string, error) {
	current, held, err := s.currentLock(ctx, q, d)
	if err != nil {
		return Lock{}, "", err
	}
	takeover := held && (current.Holder.ID != p.ID || current.Holder.Kind != p.Kind)
	if takeover && !force {
		return Lock{}, "", lockHeld(current)
	}
	preserved := ""
	if takeover {
		if err := authorize(p, auth.PluginLockOverride, d.app); err != nil {
			return Lock{}, "", err
		}
		if preserved, err = s.snapshotDraft(ctx, q, d, p, reasonTakeover); err != nil {
			return Lock{}, "", err
		}
	}
	previousSession, previousHolder := "", ""
	if held && current.Session != session {
		previousSession, previousHolder = current.Session, current.Holder.ID
	}
	row, err := q.PutLock(ctx, dbgen.PutLockParams{
		DraftID: d.row.ID, OrganizationID: d.row.OrganizationID,
		HolderKind: p.Kind, HolderID: p.ID, HolderDisplay: p.Display, Session: session,
		ExpiresAt:       storage.Timestamp(s.now().Add(LockTTL)),
		PreviousSession: previousSession, PreviousHolder: previousHolder,
	})
	if err != nil {
		return Lock{}, "", fmt.Errorf("document: take the lock: %w", err)
	}
	e := audit.Entry{Action: audit.LockAcquired, TargetKind: "draft", TargetID: storage.ID(d.row.ID)}
	if takeover {
		e.Action = audit.LockTakenOver
		e.Detail = "took the lock from " + current.Holder.Kind + " " + current.Holder.ID + "; their draft is snapshot " + preserved
	}
	return lockOf(row, d), preserved, s.recordEntry(ctx, tx, p, e)
}

// RenewLock is the holder's heartbeat; it returns who has asked for the
// lock since it was taken, which is how the holder is told (SRV-041).
func (s *Service) RenewLock(ctx context.Context, p auth.Principal, appID, pluginID, session string) (Lock, error) {
	var out Lock
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := s.holds(ctx, q, d, p, session); err != nil {
			return err
		}
		row, err := q.RenewLock(ctx, dbgen.RenewLockParams{DraftID: d.row.ID, ExpiresAt: storage.Timestamp(s.now().Add(LockTTL))})
		if err != nil {
			return fmt.Errorf("document: renew the lock: %w", err)
		}
		out = lockOf(row, d)
		return nil
	})
	return out, err
}

// ReleaseLock gives the lock up.
func (s *Service) ReleaseLock(ctx context.Context, p auth.Principal, appID, pluginID, session string) error {
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := s.holds(ctx, q, d, p, session); err != nil {
			return err
		}
		if _, err := q.DeleteLock(ctx, d.row.ID); err != nil {
			return fmt.Errorf("document: release the lock: %w", err)
		}
		return s.record(ctx, tx, p, audit.LockReleased, "draft", storage.ID(d.row.ID), "", "")
	})
}

// RequestLock asks the holder for the lock; the holder sees the request
// on their next heartbeat (SRV-041).
func (s *Service) RequestLock(ctx context.Context, p auth.Principal, appID, pluginID string) (Lock, error) {
	var out Lock
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, d.editPermission(), appID); err != nil {
			return err
		}
		current, held, err := s.currentLock(ctx, q, d)
		if err != nil {
			return err
		}
		if !held {
			return plxerr.New(plxerr.PreconditionFailed, "nobody holds the lock; acquire it")
		}
		requester, err := json.Marshal([]audit.Actor{p.Actor()})
		if err != nil {
			return fmt.Errorf("document: %w", err)
		}
		row, err := q.RequestLock(ctx, dbgen.RequestLockParams{DraftID: d.row.ID, Requester: requester})
		if err != nil {
			return fmt.Errorf("document: request the lock: %w", err)
		}
		out = lockOf(row, d)
		return s.recordEntry(ctx, tx, p, audit.Entry{
			Action: audit.LockRequested, TargetKind: "draft", TargetID: storage.ID(d.row.ID),
			Detail: "asked " + current.Holder.Kind + " " + current.Holder.ID + " for the lock",
		})
	})
	return out, err
}

// GetLock returns a draft's lock; an unheld lock has an empty session.
func (s *Service) GetLock(ctx context.Context, p auth.Principal, appID, pluginID string) (Lock, error) {
	var out Lock
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		current, held, err := s.currentLock(ctx, q, d)
		if err != nil {
			return err
		}
		if held {
			out = current
		} else {
			out = Lock{AppID: d.app, PluginID: d.pluginID()}
		}
		return nil
	})
	return out, err
}

// currentLock reads a draft's lock and whether it is in force.
func (s *Service) currentLock(ctx context.Context, q *dbgen.Queries, d draft) (Lock, bool, error) {
	row, err := q.GetLockForUpdate(ctx, d.row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lock{}, false, nil
	}
	if err != nil {
		return Lock{}, false, fmt.Errorf("document: read the lock: %w", err)
	}
	l := lockOf(row, d)
	return l, l.Held(s.now()), nil
}

// holds refuses a principal and session that do not hold the lock.
func (s *Service) holds(ctx context.Context, q *dbgen.Queries, d draft, p auth.Principal, session string) error {
	current, held, err := s.currentLock(ctx, q, d)
	if err != nil {
		return err
	}
	if !held {
		return plxerr.New(plxerr.EditingLockHeld, "acquire the editing lock first; it expires %s after the last heartbeat", LockTTL)
	}
	if current.Session != session || current.Holder.ID != p.ID || current.Holder.Kind != p.Kind {
		return lockHeld(current)
	}
	return nil
}

// lockHeld is the refusal of a write or a lock held by someone else,
// naming the holder (SRV-040).
func lockHeld(l Lock) error {
	e := plxerr.New(plxerr.EditingLockHeld, "%s holds the editing lock until %s", display(l.Holder), l.ExpiresAt.Format(time.RFC3339))
	return withDetail(withDetail(e, "holder", l.Holder.ID), "expiresAt", l.ExpiresAt.Format(time.RFC3339))
}

// display names an actor for a message.
func display(a audit.Actor) string {
	if a.Display != "" {
		return a.Display
	}
	return a.Kind + " " + a.ID
}

// lockOf converts a stored lock.
func lockOf(row dbgen.Lock, d draft) Lock {
	var requested []audit.Actor
	_ = json.Unmarshal(row.RequestedBy, &requested)
	return Lock{
		AppID: d.app, PluginID: d.pluginID(),
		Holder:     audit.Actor{Kind: row.HolderKind, ID: row.HolderID, Display: row.HolderDisplay},
		Session:    row.Session,
		AcquiredAt: storage.Time(row.AcquiredAt), ExpiresAt: storage.Time(row.ExpiresAt),
		RequestedBy: requested,
	}
}
