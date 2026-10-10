// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const replayTTL = 2 * time.Minute

// fakeCache is a shared cache whose SetNX can be made to fail. Only SetNX
// is implemented; the embedded interface is nil.
type fakeCache struct {
	cache.Cache
	mu   sync.Mutex
	keys map[string]bool
	err  error
}

func newFakeCache() *fakeCache { return &fakeCache{keys: map[string]bool{}} }

func (f *fakeCache) SetNX(_ context.Context, key string, _ []byte, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if f.keys[key] {
		return false, nil
	}
	f.keys[key] = true
	return true, nil
}

func (f *fakeCache) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

var errCacheDown = errors.New("cache down")

// Verifies: SEC-023.
func TestReplaySharedCache(t *testing.T) {
	clock := &stepClock{t: testNow}
	mem := cache.NewMemory(clock.now)
	r := NewReplay(mem, FailClosed, 10, clock.now, nil)
	ctx := context.Background()

	degraded, err := r.Check(ctx, "jkt-a", "jti-1", replayTTL)
	if err != nil || degraded {
		t.Fatalf("fresh proof: degraded=%v err=%v", degraded, err)
	}
	degraded, err = r.Check(ctx, "jkt-a", "jti-1", replayTTL)
	wantCode(t, err, plxerr.DPoPReplay)
	if degraded {
		t.Fatal("a shared-cache replay was reported as degraded")
	}
	if _, err := r.Check(ctx, "jkt-b", "jti-1", replayTTL); err != nil {
		t.Fatalf("same jti under another key: %v", err)
	}
	if _, err := r.Check(ctx, "jkt-a", "jti-2", replayTTL); err != nil {
		t.Fatalf("another jti under the same key: %v", err)
	}
	clock.advance(replayTTL + time.Second)
	if _, err := r.Check(ctx, "jkt-a", "jti-1", replayTTL); err != nil {
		t.Fatalf("after the lifetime: %v", err)
	}
}

// Verifies: SEC-023, SEC-092.
func TestReplayKeyHidesInputs(t *testing.T) {
	var got []string
	spy := &spyCache{record: func(k string) { got = append(got, k) }}
	r := NewReplay(spy, FailClosed, 10, nil, nil)
	if _, err := r.Check(context.Background(), "thumb-print", "the-jti", replayTTL); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "dpop:jti:") {
		t.Fatalf("keys = %v", got)
	}
	if strings.Contains(got[0], "the-jti") || strings.Contains(got[0], "thumb-print") {
		t.Fatalf("key %q leaks its inputs", got[0])
	}
	if replayKey("ab", "c") == replayKey("a", "bc") {
		t.Fatal("key does not separate its inputs")
	}
}

// spyCache reports each SetNX key and always succeeds.
type spyCache struct {
	cache.Cache
	record func(string)
}

func (s *spyCache) SetNX(_ context.Context, key string, _ []byte, _ time.Duration) (bool, error) {
	s.record(key)
	return true, nil
}

// Verifies: SEC-023.
func TestReplayFailClosed(t *testing.T) {
	f := newFakeCache()
	f.fail(errCacheDown)
	called := false
	r := NewReplay(f, FailClosed, 10, nil, func(error) { called = true })
	degraded, err := r.Check(context.Background(), "k", "j", replayTTL)
	wantCode(t, err, plxerr.ReplayCacheUnavailable)
	if degraded || called {
		t.Fatalf("degraded=%v called=%v, want neither under FailClosed", degraded, called)
	}
	if !errors.Is(err, errCacheDown) {
		t.Fatal("the cache error is not wrapped")
	}
}

// Verifies: SEC-023.
func TestReplayRejectsNonPositiveLifetime(t *testing.T) {
	r := NewReplay(newFakeCache(), FailClosed, 10, nil, nil)
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := r.Check(context.Background(), "k", "j", ttl); err == nil {
			t.Fatalf("ttl %v accepted", ttl)
		}
	}
}

// Verifies: SEC-023.
func TestReplayPerReplicaFallback(t *testing.T) {
	clock := &stepClock{t: testNow}
	f := newFakeCache()
	f.fail(errCacheDown)
	r := NewReplay(f, PerReplica, 10, clock.now, nil)
	ctx := context.Background()

	degraded, err := r.Check(ctx, "k", "j1", replayTTL)
	if err != nil || !degraded {
		t.Fatalf("first: degraded=%v err=%v", degraded, err)
	}
	degraded, err = r.Check(ctx, "k", "j1", replayTTL)
	wantCode(t, err, plxerr.DPoPReplay)
	if !degraded {
		t.Fatal("a fallback replay was not reported as degraded")
	}
	if _, err := r.Check(ctx, "k", "j2", replayTTL); err != nil {
		t.Fatalf("another jti: %v", err)
	}
	if _, err := r.Check(ctx, "other", "j1", replayTTL); err != nil {
		t.Fatalf("same jti under another key: %v", err)
	}

	clock.advance(replayTTL + time.Second)
	if _, err := r.Check(ctx, "k", "j1", replayTTL); err != nil {
		t.Fatalf("after the lifetime: %v", err)
	}
}

// Verifies: SEC-023.
func TestReplayRecovery(t *testing.T) {
	clock := &stepClock{t: testNow}
	f := newFakeCache()
	f.fail(errCacheDown)
	r := NewReplay(f, PerReplica, 10, clock.now, nil)
	ctx := context.Background()

	if _, err := r.Check(ctx, "k", "during-outage", replayTTL); err != nil {
		t.Fatal(err)
	}
	f.fail(nil)

	// The shared cache is asked first again and decides what it knows.
	degraded, err := r.Check(ctx, "k", "after-outage", replayTTL)
	if err != nil || degraded {
		t.Fatalf("fresh after recovery: degraded=%v err=%v", degraded, err)
	}
	_, err = r.Check(ctx, "k", "after-outage", replayTTL)
	wantCode(t, err, plxerr.DPoPReplay)

	// A proof accepted during the outage is still refused once it is back.
	degraded, err = r.Check(ctx, "k", "during-outage", replayTTL)
	wantCode(t, err, plxerr.DPoPReplay)
	if degraded {
		t.Fatal("recovered check reported as degraded")
	}
}

// Verifies: SEC-023.
func TestReplayStoreIsBounded(t *testing.T) {
	clock := &stepClock{t: testNow}
	f := newFakeCache()
	f.fail(errCacheDown)
	const limit = 3
	r := NewReplay(f, PerReplica, limit, clock.now, nil)
	ctx := context.Background()

	for i := range 20 {
		if _, err := r.Check(ctx, "k", fmt.Sprintf("j%d", i), replayTTL); err != nil {
			t.Fatal(err)
		}
		if len(r.seen) > limit || r.order.Len() > limit || len(r.seen) != r.order.Len() {
			t.Fatalf("store holds %d/%d entries, limit %d", len(r.seen), r.order.Len(), limit)
		}
	}
	// The newest are remembered; the oldest was evicted to make room.
	for _, j := range []string{"j19", "j18", "j17"} {
		_, err := r.Check(ctx, "k", j, replayTTL)
		wantCode(t, err, plxerr.DPoPReplay)
	}
	if _, err := r.Check(ctx, "k", "j0", replayTTL); err != nil {
		t.Fatalf("evicted entry still remembered: %v", err)
	}
}

// Verifies: SEC-023.
func TestReplayEvictsExpiredBeforeLive(t *testing.T) {
	clock := &stepClock{t: testNow}
	f := newFakeCache()
	f.fail(errCacheDown)
	r := NewReplay(f, PerReplica, 2, clock.now, nil)
	ctx := context.Background()

	for _, j := range []string{"old1", "old2"} {
		if _, err := r.Check(ctx, "k", j, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	clock.advance(2 * time.Second)
	for _, j := range []string{"new1", "new2"} {
		if _, err := r.Check(ctx, "k", j, replayTTL); err != nil {
			t.Fatal(err)
		}
	}
	// Both live entries survived: the expired ones made the room.
	for _, j := range []string{"new1", "new2"} {
		_, err := r.Check(ctx, "k", j, replayTTL)
		wantCode(t, err, plxerr.DPoPReplay)
	}
}

func TestReplayMinimumLimit(t *testing.T) {
	f := newFakeCache()
	f.fail(errCacheDown)
	r := NewReplay(f, PerReplica, 0, nil, nil)
	if _, err := r.Check(context.Background(), "k", "j", replayTTL); err != nil {
		t.Fatal(err)
	}
	if len(r.seen) != 1 {
		t.Fatalf("store holds %d entries, want 1", len(r.seen))
	}
}

// Verifies: SEC-023.
func TestReplayDegradedCallbackIsRateLimited(t *testing.T) {
	clock := &stepClock{t: testNow}
	f := newFakeCache()
	f.fail(errCacheDown)
	var calls []error
	r := NewReplay(f, PerReplica, 100, clock.now, func(err error) { calls = append(calls, err) })
	ctx := context.Background()
	check := func(i int) {
		t.Helper()
		if _, err := r.Check(ctx, "k", fmt.Sprintf("j%d", i), replayTTL); err != nil {
			t.Fatal(err)
		}
	}

	check(0)
	check(1)
	clock.advance(9 * time.Second)
	check(2)
	if len(calls) != 1 || !errors.Is(calls[0], errCacheDown) {
		t.Fatalf("calls = %v, want one with the cache error", calls)
	}
	clock.advance(time.Second) // ten seconds since the first call
	check(3)
	check(4)
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2", len(calls))
	}
}

// Verifies: SEC-023.
func TestReplayConcurrentSharedCache(t *testing.T) {
	r := NewReplay(newFakeCache(), FailClosed, 10, nil, nil)
	if got := raceSameProof(t, r); got != 1 {
		t.Fatalf("%d concurrent checks accepted the same proof, want 1", got)
	}
}

// Verifies: SEC-023.
func TestReplayConcurrentFallback(t *testing.T) {
	f := newFakeCache()
	f.fail(errCacheDown)
	var notified atomic.Int32
	r := NewReplay(f, PerReplica, 8, nil, func(error) { notified.Add(1) })
	if got := raceSameProof(t, r); got != 1 {
		t.Fatalf("%d concurrent checks accepted the same proof, want 1", got)
	}

	// Distinct proofs under contention never grow the store past its limit.
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				_, _ = r.Check(context.Background(), "k", fmt.Sprintf("g%d-%d", g, i), replayTTL)
			}
		}()
	}
	wg.Wait()
	if len(r.seen) > 8 || r.order.Len() > 8 {
		t.Fatalf("store holds %d entries, limit 8", len(r.seen))
	}
	if notified.Load() < 1 {
		t.Fatal("onDegraded never called")
	}
}

// raceSameProof checks one proof from many goroutines and returns how many
// were accepted.
func raceSameProof(t *testing.T, r *Replay) int {
	t.Helper()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.Check(context.Background(), "k", "same", replayTTL); err == nil {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	return int(accepted.Load())
}

// Verifies: SEC-023.
// The health observer hears every fallback decision, and hears the
// recovery once when the shared cache answers again.
func TestReplayHealthTransitions(t *testing.T) {
	f := newFakeCache()
	var fallbacks, recoveries int
	r := NewReplay(f, PerReplica, 100, nil, nil, WithHealth(Health{
		Fallback:  func() { fallbacks++ },
		Recovered: func() { recoveries++ },
	}))
	ctx := context.Background()
	check := func(jti string) {
		t.Helper()
		if _, err := r.Check(ctx, "k", jti, replayTTL); err != nil {
			t.Fatal(err)
		}
	}

	check("healthy")
	if fallbacks != 0 || recoveries != 0 {
		t.Fatalf("a healthy cache: fallbacks=%d recoveries=%d", fallbacks, recoveries)
	}
	f.fail(errCacheDown)
	check("down-1")
	check("down-2")
	if fallbacks != 2 || recoveries != 0 {
		t.Fatalf("an outage: fallbacks=%d recoveries=%d, want 2 and 0", fallbacks, recoveries)
	}
	f.fail(nil)
	check("up-1")
	check("up-2")
	if fallbacks != 2 || recoveries != 1 {
		t.Fatalf("after recovery: fallbacks=%d recoveries=%d, want 2 and 1", fallbacks, recoveries)
	}
	f.fail(errCacheDown)
	check("down-3")
	if fallbacks != 3 {
		t.Fatalf("a second outage: fallbacks=%d, want 3", fallbacks)
	}
}

// Verifies: SEC-023.
// Under FailClosed the cache's failure is an error, not a fallback, and
// the health observer hears nothing.
func TestReplayHealthIgnoresFailClosed(t *testing.T) {
	f := newFakeCache()
	f.fail(errCacheDown)
	heard := false
	r := NewReplay(f, FailClosed, 10, nil, nil, WithHealth(Health{
		Fallback:  func() { heard = true },
		Recovered: func() { heard = true },
	}))
	if _, err := r.Check(context.Background(), "k", "j", replayTTL); err == nil || heard {
		t.Fatalf("err=%v heard=%v, want an error and silence", err, heard)
	}
}
