// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// degradedNotifyEvery is the shortest interval between two onDegraded calls.
const degradedNotifyEvery = 10 * time.Second

// Fallback is what the replay check does when the shared cache fails.
type Fallback int

const (
	// FailClosed refuses the request with ReplayCacheUnavailable. It is
	// the zero value and the secure default.
	FailClosed Fallback = iota
	// PerReplica records the proof in a bounded in-memory store and
	// reports the check as degraded, so the caller applies the narrower
	// fallback time window (ADR-0012).
	PerReplica
)

// Replay detects reused proofs (SEC-023). It asks the shared cache first on
// every call; only a failing shared cache engages the fallback. A Replay is
// safe for concurrent use.
type Replay struct {
	shared     cache.Cache
	policy     Fallback
	now        func() time.Time
	onDegraded func(err error)

	mu       sync.Mutex
	limit    int
	seen     map[string]*list.Element
	order    *list.List // of *seenEntry, oldest first
	notified time.Time
}

// seenEntry is one proof identifier held by the fallback store.
type seenEntry struct {
	key     string
	expires time.Time
}

// NewReplay returns a replay check over the shared cache. entries bounds
// the fallback store (the limit dpop.replayCacheEntries); now supplies the
// clock, time.Now when nil; onDegraded, when set, is called with the cache
// error at most once every ten seconds while the fallback is in use.
func NewReplay(shared cache.Cache, policy Fallback, entries int, now func() time.Time, onDegraded func(err error)) *Replay {
	if now == nil {
		now = time.Now
	}
	return &Replay{
		shared:     shared,
		policy:     policy,
		now:        now,
		onDegraded: onDegraded,
		limit:      max(entries, 1),
		seen:       make(map[string]*list.Element),
		order:      list.New(),
	}
}

// replayKey returns the cache key of a proof: the hash of the key
// thumbprint and the identifier, so neither is stored (SEC-092).
func replayKey(jkt, jti string) string {
	h := sha256.New()
	h.Write([]byte(jkt))
	h.Write([]byte{0})
	h.Write([]byte(jti))
	return "dpop:jti:" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Check records a proof identifier for ttl and reports DPoPReplay when it
// was already recorded. degraded is true when the shared cache failed and
// the per-replica store decided; the caller then applies the narrower
// time window. With FailClosed a shared-cache failure is
// ReplayCacheUnavailable.
func (r *Replay) Check(ctx context.Context, jkt, jti string, ttl time.Duration) (degraded bool, err error) {
	if ttl <= 0 {
		return false, errors.New("dpop: replay lifetime must be positive")
	}
	key := replayKey(jkt, jti)
	fresh, cacheErr := r.shared.SetNX(ctx, key, []byte{1}, ttl)
	if cacheErr == nil {
		return false, r.afterShared(key, fresh)
	}
	if r.policy != PerReplica {
		return false, plxerr.Wrap(plxerr.ReplayCacheUnavailable, cacheErr, "dpop replay cache unavailable")
	}
	r.notify(cacheErr)
	if !r.record(key, ttl) {
		return true, plxerr.New(plxerr.DPoPReplay, "dpop proof was already used")
	}
	return true, nil
}

// afterShared turns the shared cache's answer into the result. A proof the
// shared cache has not seen may still be in the fallback store from an
// outage, so that store is consulted before the proof is accepted.
func (r *Replay) afterShared(key string, fresh bool) error {
	if !fresh || r.held(key) {
		return plxerr.New(plxerr.DPoPReplay, "dpop proof was already used")
	}
	return nil
}

// held reports whether the fallback store holds an unexpired key. It does
// not record anything.
func (r *Replay) held(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	el, ok := r.seen[key]
	return ok && r.now().Before(el.Value.(*seenEntry).expires)
}

// record stores a key in the fallback store and reports whether it was new.
// Expired entries go first; a full store then drops its oldest entry, so it
// never holds more than its limit.
func (r *Replay) record(key string, ttl time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if el, ok := r.seen[key]; ok {
		if now.Before(el.Value.(*seenEntry).expires) {
			return false
		}
		r.order.Remove(el)
		delete(r.seen, key)
	}
	r.evictExpired(now)
	for r.order.Len() >= r.limit {
		r.drop(r.order.Front())
	}
	r.seen[key] = r.order.PushBack(&seenEntry{key: key, expires: now.Add(ttl)})
	return true
}

// evictExpired drops expired entries from the front of the queue. Lifetimes
// are normally equal, so the queue is ordered by expiry; an entry with a
// longer lifetime that sits behind others is dropped when it reaches the
// front or is looked up.
func (r *Replay) evictExpired(now time.Time) {
	for el := r.order.Front(); el != nil; el = r.order.Front() {
		if now.Before(el.Value.(*seenEntry).expires) {
			return
		}
		r.drop(el)
	}
}

// drop removes one element from the store. The caller holds the mutex.
func (r *Replay) drop(el *list.Element) {
	delete(r.seen, el.Value.(*seenEntry).key)
	r.order.Remove(el)
}

// notify calls onDegraded unless it was called in the last ten seconds.
func (r *Replay) notify(err error) {
	if r.onDegraded == nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	due := r.notified.IsZero() || now.Sub(r.notified) >= degradedNotifyEvery
	if due {
		r.notified = now
	}
	r.mu.Unlock()
	if due {
		r.onDegraded(err)
	}
}
