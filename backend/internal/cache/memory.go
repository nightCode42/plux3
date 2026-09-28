// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Memory is the single-node backend. It is correct only while one
// process holds every role; the configuration refuses it otherwise.
type Memory struct {
	// now is the clock, injected so that tests do not sleep.
	now func() time.Time

	mu     sync.Mutex
	closed bool
	// entries are swept lazily on read and in bulk when the map grows
	// past sweepAt, so an idle cache does not need a goroutine.
	entries map[string]entry
}

// entry is one cached value.
type entry struct {
	value   []byte
	expires time.Time
}

// sweepAt is the number of entries above which a write sweeps expired
// ones before inserting.
const sweepAt = 1024

// NewMemory returns an in-memory cache. A nil clock uses time.Now.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{now: now, entries: map[string]entry{}}
}

// live returns the entry if it is present and unexpired.
func (m *Memory) live(key string) (entry, bool) {
	e, ok := m.entries[key]
	if !ok || !e.expires.After(m.now()) {
		delete(m.entries, key)
		return entry{}, false
	}
	return e, true
}

// sweep removes expired entries when the map has grown.
func (m *Memory) sweep() {
	if len(m.entries) < sweepAt {
		return
	}
	now := m.now()
	for k, e := range m.entries {
		if !e.expires.After(now) {
			delete(m.entries, k)
		}
	}
}

// Get returns a value and whether it was present.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	e, ok := m.live(key)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), e.value...), true, nil
}

// Set stores a value with a lifetime.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("cache: a value needs a lifetime")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.sweep()
	m.entries[key] = entry{value: append([]byte(nil), value...), expires: m.now().Add(ttl)}
	return nil
}

// SetNX stores a value only if the key is absent.
func (m *Memory) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, errors.New("cache: a value needs a lifetime")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false, ErrClosed
	}
	if _, ok := m.live(key); ok {
		return false, nil
	}
	m.sweep()
	m.entries[key] = entry{value: append([]byte(nil), value...), expires: m.now().Add(ttl)}
	return true, nil
}

// Increment adds one to a counter.
func (m *Memory) Increment(_ context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, errors.New("cache: a counter needs a lifetime")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	var n int64
	expires := m.now().Add(ttl)
	if e, ok := m.live(key); ok {
		parsed, err := strconv.ParseInt(string(e.value), 10, 64)
		if err != nil {
			return 0, errors.New("cache: " + key + " does not hold a counter")
		}
		n, expires = parsed, e.expires
	}
	n++
	m.sweep()
	m.entries[key] = entry{value: []byte(strconv.FormatInt(n, 10)), expires: expires}
	return n, nil
}

// Delete removes a key.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	delete(m.entries, key)
	return nil
}

// Ping always succeeds: the backend is this process.
func (m *Memory) Ping(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

// Close drops every entry.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.entries = nil
	return nil
}
