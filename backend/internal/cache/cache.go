// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package cache is the shared, expendable store: rate-limit counters
// (SRV-065), single-flight markers for on-demand deltas (REL-022) and,
// from P6, the DPoP replay cache (ADR-0007).
//
// Nothing that matters survives only here. Every value has a lifetime,
// and losing the cache costs work, never correctness: a rate limit
// restarts, a delta is computed twice.
//
// Two backends implement the interface: an in-memory one for a
// single-node installation, and Valkey — reached with the small RESP
// client in this package — for several replicas.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrClosed is returned once a cache has been closed.
var ErrClosed = errors.New("cache: closed")

// Cache is the shared cache.
type Cache interface {
	// Get returns a value and whether it was present.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores a value with a lifetime. A ttl of zero is refused: a
	// value without one would outlive its meaning.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetNX stores a value only if the key is absent, and reports
	// whether it did. It is how single-flight and replay detection are
	// built (REL-022).
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Increment adds one to a counter, setting the lifetime when it is
	// created, and returns the new value. It is how rate limits are
	// counted (SRV-065).
	Increment(ctx context.Context, key string, ttl time.Duration) (int64, error)
	// Delete removes a key. Deleting what is absent succeeds.
	Delete(ctx context.Context, key string) error
	// Ping reports whether the backend answers, for /readyz (SRV-007).
	Ping(ctx context.Context) error
	// Close releases the backend's resources.
	Close() error
}
