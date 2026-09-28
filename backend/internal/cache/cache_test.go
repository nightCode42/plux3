// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

// clock is a test clock that only moves when a test moves it.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// backends returns each backend under test, with a name. The Valkey one
// talks to the in-process server of valkey_test.go.
func backends(t *testing.T) map[string]Cache {
	t.Helper()
	srv := newRESPServer(t)
	v, err := NewValkey("redis://" + srv.addr)
	if err != nil {
		t.Fatalf("NewValkey: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	out := map[string]Cache{"memory": NewMemory(nil), "valkey": v}
	// A real Valkey, when one is configured, runs every test too (QA-005).
	if url := os.Getenv("PLUX_TEST_VALKEY_URL"); url != "" {
		real, err := NewValkey(url)
		if err != nil {
			t.Fatalf("NewValkey(%s): %v", url, err)
		}
		t.Cleanup(func() { _ = real.Close() })
		out["valkey-server"] = prefixed{Cache: real, prefix: t.Name() + ":"}
	}
	return out
}

// prefixed keeps each test's keys apart on a shared server.
type prefixed struct {
	Cache
	prefix string
}

func (p prefixed) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return p.Cache.Get(ctx, p.prefix+key)
}

func (p prefixed) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return p.Cache.Set(ctx, p.prefix+key, value, ttl)
}

func (p prefixed) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return p.Cache.SetNX(ctx, p.prefix+key, value, ttl)
}

func (p prefixed) Increment(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return p.Cache.Increment(ctx, p.prefix+key, ttl)
}

func (p prefixed) Delete(ctx context.Context, key string) error {
	return p.Cache.Delete(ctx, p.prefix+key)
}

func TestCacheRoundTrip(t *testing.T) {
	t.Parallel()
	for name, c := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, ok, err := c.Get(ctx, "absent"); ok || err != nil {
				t.Errorf("Get of an absent key = %v, %v", ok, err)
			}
			if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, ok, err := c.Get(ctx, "k")
			if err != nil || !ok || string(got) != "v" {
				t.Fatalf("Get = %q, %v, %v", got, ok, err)
			}
			if err := c.Delete(ctx, "k"); err != nil {
				t.Errorf("Delete: %v", err)
			}
			if _, ok, _ := c.Get(ctx, "k"); ok {
				t.Error("the key survived Delete")
			}
			if err := c.Delete(ctx, "k"); err != nil {
				t.Errorf("deleting twice must succeed: %v", err)
			}
			if err := c.Ping(ctx); err != nil {
				t.Errorf("Ping: %v", err)
			}
			if err := c.Set(ctx, "k", []byte("v"), 0); err == nil {
				t.Error("a value without a lifetime was accepted")
			}
		})
	}
}

// Verifies: REL-022.
// SetNX is what makes a delta computed once when several requests ask
// for it at the same moment.
func TestSetNXGrantsOnce(t *testing.T) {
	t.Parallel()
	for name, c := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			first, err := c.SetNX(ctx, "flight", []byte("me"), time.Minute)
			if err != nil || !first {
				t.Fatalf("first SetNX = %v, %v", first, err)
			}
			second, err := c.SetNX(ctx, "flight", []byte("you"), time.Minute)
			if err != nil || second {
				t.Fatalf("second SetNX = %v, %v", second, err)
			}
			if got, _, _ := c.Get(ctx, "flight"); string(got) != "me" {
				t.Errorf("the second caller overwrote the value: %q", got)
			}
			if _, err := c.SetNX(ctx, "flight", nil, 0); err == nil {
				t.Error("a marker without a lifetime was accepted")
			}
		})
	}
}

// Verifies: SRV-065.
func TestIncrementCounts(t *testing.T) {
	t.Parallel()
	for name, c := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for want := int64(1); want <= 3; want++ {
				got, err := c.Increment(ctx, "rate", time.Minute)
				if err != nil || got != want {
					t.Fatalf("Increment = %d, %v; want %d", got, err, want)
				}
			}
			if _, err := c.Increment(ctx, "rate", 0); err == nil {
				t.Error("a counter without a lifetime was accepted")
			}
		})
	}
}

func TestMemoryExpires(t *testing.T) {
	t.Parallel()
	cl := &clock{t: time.Unix(1_700_000_000, 0)}
	m := NewMemory(cl.now)
	ctx := context.Background()
	if err := m.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if n, err := m.Increment(ctx, "n", time.Minute); err != nil || n != 1 {
		t.Fatalf("Increment = %d, %v", n, err)
	}
	cl.advance(61 * time.Second)
	if _, ok, _ := m.Get(ctx, "k"); ok {
		t.Error("an expired value was returned")
	}
	if ok, err := m.SetNX(ctx, "k", []byte("again"), time.Minute); err != nil || !ok {
		t.Errorf("SetNX after expiry = %v, %v", ok, err)
	}
	if n, err := m.Increment(ctx, "n", time.Minute); err != nil || n != 1 {
		t.Errorf("an expired counter restarts at 1; got %d, %v", n, err)
	}
	if err := m.Set(ctx, "text", []byte("not a number"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Increment(ctx, "text", time.Minute); err == nil {
		t.Error("incrementing a value that is not a counter was accepted")
	}
}

func TestMemorySweepsWhenItGrows(t *testing.T) {
	t.Parallel()
	cl := &clock{t: time.Unix(1_700_000_000, 0)}
	m := NewMemory(cl.now)
	ctx := context.Background()
	for i := range sweepAt + 1 {
		if err := m.Set(ctx, key(i), []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	cl.advance(2 * time.Minute)
	if err := m.Set(ctx, "fresh", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	n := len(m.entries)
	m.mu.Unlock()
	if n != 1 {
		t.Errorf("%d entries survived the sweep; want 1", n)
	}
}

// key returns a distinct key for i.
func key(i int) string { return "k" + strconv.Itoa(i) }

func TestMemoryAfterClose(t *testing.T) {
	t.Parallel()
	m := NewMemory(nil)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := m.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Get = %v", err)
	}
	if err := m.Set(ctx, "k", nil, time.Minute); !errors.Is(err, ErrClosed) {
		t.Errorf("Set = %v", err)
	}
	if _, err := m.SetNX(ctx, "k", nil, time.Minute); !errors.Is(err, ErrClosed) {
		t.Errorf("SetNX = %v", err)
	}
	if _, err := m.Increment(ctx, "k", time.Minute); !errors.Is(err, ErrClosed) {
		t.Errorf("Increment = %v", err)
	}
	if err := m.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Delete = %v", err)
	}
	if err := m.Ping(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Ping = %v", err)
	}
}
