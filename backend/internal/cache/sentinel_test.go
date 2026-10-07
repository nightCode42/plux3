// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// newSentinelClient builds a Sentinel-mode client for the master name
// "mymaster", with short timeouts so that a hung node fails fast.
func newSentinelClient(t testing.TB, query string, sentinels ...*respServer) *Valkey {
	t.Helper()
	addrs := make([]string, len(sentinels))
	for i, s := range sentinels {
		addrs[i] = s.addr
	}
	v, err := NewValkey("valkey+sentinel://" + strings.Join(addrs, ",") + "/mymaster" + query)
	if err != nil {
		t.Fatal(err)
	}
	v.timeout = 2 * time.Second
	t.Cleanup(func() { _ = v.Close() })
	return v
}

// poolState reports the cached master, its generation and the
// generations of the pooled connections.
func poolState(v *Valkey) (master string, gen uint64, pooled []*conn) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sentinel.master, v.sentinel.gen, slices.Clone(v.idle)
}

// failedOverClient returns a client whose pool holds a connection to a
// master that has since crashed, with the Sentinel already naming the
// promoted node.
func failedOverClient(t *testing.T) (v *Valkey, oldMaster, newMaster, sentinel *respServer) {
	t.Helper()
	oldMaster, newMaster, sentinel = newRESPServer(t), newRESPServer(t), newRESPServer(t)
	sentinel.setSentinelMaster(oldMaster.addr)
	v = newSentinelClient(t, "", sentinel)
	if err := v.Ping(t.Context()); err != nil {
		t.Fatalf("Ping on the first master: %v", err)
	}
	if _, _, pooled := poolState(v); len(pooled) != 1 {
		t.Fatalf("pool holds %d connections; want 1", len(pooled))
	}
	sentinel.setSentinelMaster(newMaster.addr)
	oldMaster.kill()
	return v, oldMaster, newMaster, sentinel
}

func TestSentinelURLs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw       string
		addrs     []string
		name      string
		database  string
		password  string
		sentinelP string
		secure    bool
	}{
		{raw: "valkey+sentinel://a:26379,b:26380,c:26381/mymaster", addrs: []string{"a:26379", "b:26380", "c:26381"}, name: "mymaster"},
		{raw: "redis+sentinel://a,b/mymaster/2", addrs: []string{"a:26379", "b:26379"}, name: "mymaster", database: "2"},
		{raw: "valkeys+sentinel://:s3cret@a/m?sentinelPassword=sp", addrs: []string{"a:26379"}, name: "m", password: "s3cret", sentinelP: "sp", secure: true},
		{raw: "rediss+sentinel://user:p%40ss@[::1]:26379,[::2]/m/0", addrs: []string{"[::1]:26379", "[::2]:26379"}, name: "m", database: "0", password: "p@ss", secure: true},
		{raw: "VALKEY+SENTINEL://a/m", addrs: []string{"a:26379"}, name: "m"},
		{raw: "valkey+sentinel://h1:1,h2/name%20x/", addrs: []string{"h1:1", "h2:26379"}, name: "name x"},
	} {
		v, err := NewValkey(tc.raw)
		if err != nil {
			t.Errorf("NewValkey(%q): %v", tc.raw, err)
			continue
		}
		s := v.sentinel
		if s == nil {
			t.Errorf("NewValkey(%q) is not in Sentinel mode", tc.raw)
			continue
		}
		if !slices.Equal(s.addrs, tc.addrs) || s.name != tc.name || v.database != tc.database ||
			v.password != tc.password || s.password != tc.sentinelP || s.secure != tc.secure {
			t.Errorf("NewValkey(%q) = addrs %v name %q db %q pw %q sentinel pw %q secure %v",
				tc.raw, s.addrs, s.name, v.database, v.password, s.password, s.secure)
		}
	}
}

func TestSentinelURLsRejectBadInput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"valkey+sentinel:///m",
		"valkey+sentinel://a,,b/m",
		"valkey+sentinel://:s3cret@a:0/m",
		"valkey+sentinel://:s3cret@a:99999/m",
		"valkey+sentinel://:s3cret@a:x/m",
		"valkey+sentinel://:s3cret@a",
		"valkey+sentinel://:s3cret@a/",
		"valkey+sentinel://:s3cret@a/m/1/2",
		"valkey+sentinel://:s3cret@a/m/x",
		"valkey+sentinel://:s3cret@a/m/-1",
		"valkey+sentinel://:s3cret@::1/m",
		"valkey+sentinel://:s3cret@a/m?sentinelPassword=%zz",
		"valkey+sentinel://us%zz:s3cret@a/m",
		"valkey+sentinel://:s3cret@[]/m",
	} {
		_, err := NewValkey(raw)
		if err == nil {
			t.Errorf("NewValkey(%q) was accepted", raw)
			continue
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("NewValkey(%q) leaked the password: %v", raw, err)
		}
	}
}

func TestSentinelResolvesViaAnotherSentinel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"down", "unknown master"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			master, first, second := newRESPServer(t), newRESPServer(t), newRESPServer(t)
			second.setSentinelMaster(master.addr)
			if name == "down" {
				first.kill()
			}
			v := newSentinelClient(t, "", first, second)
			if err := v.Ping(t.Context()); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			if got := v.sentinel.addrs[0]; got != second.addr {
				t.Errorf("first Sentinel to ask = %s; want the one that answered, %s", got, second.addr)
			}
			if got := master.count("ROLE"); got != 1 {
				t.Errorf("master received %d ROLE commands; want 1", got)
			}
		})
	}
}

func TestSentinelTimeoutBoundsEveryQuery(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	master, live := newRESPServer(t), newRESPServer(t)
	live.setSentinelMaster(master.addr)
	v, err := NewValkey("valkey+sentinel://" + ln.Addr().String() + "," + live.addr + "/mymaster")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	v.timeout = 100 * time.Millisecond
	start := time.Now()
	if err := v.Ping(t.Context()); err != nil {
		t.Fatalf("Ping past a hung Sentinel: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a hung Sentinel held the call for %s", elapsed)
	}
}

// Verifies: SEC-023.
func TestSentinelFollowsFailover(t *testing.T) {
	t.Parallel()
	v, oldMaster, newMaster, sentinel := failedOverClient(t)
	_, oldGen, _ := poolState(v)
	if err := v.Set(t.Context(), "k", []byte("v"), time.Minute); err == nil {
		// The pooled connection to the crashed master must not succeed.
		t.Fatal("Set on a crashed master succeeded")
	}
	if master, gen, pooled := poolState(v); master != "" || gen == oldGen || len(pooled) != 0 {
		t.Errorf("after the failure: master %q, generation %d (was %d), %d pooled; want a stale address and an empty pool",
			master, gen, oldGen, len(pooled))
	}
	if err := v.Set(t.Context(), "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set after the failover: %v", err)
	}
	master, gen, pooled := poolState(v)
	if master != newMaster.addr {
		t.Errorf("master = %s; want the promoted node %s", master, newMaster.addr)
	}
	if len(pooled) != 1 || pooled[0].gen != gen || pooled[0].RemoteAddr().String() != newMaster.addr {
		t.Errorf("the pool does not hold exactly one connection to the new master")
	}
	if got, ok, err := v.Get(t.Context(), "k"); err != nil || !ok || string(got) != "v" {
		t.Errorf("Get on the new master = %q, %v, %v", got, ok, err)
	}
	if got := oldMaster.count("SET"); got != 0 {
		t.Errorf("the crashed master received %d SET commands", got)
	}
	if got := sentinel.count("SENTINEL"); got != 2 {
		t.Errorf("Sentinel was asked %d times; want 2 (start and failover)", got)
	}
}

func TestSentinelRetriesIdempotentCommandsOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		run     func(*Valkey, context.Context) error
	}{
		{"Get", "GET", func(v *Valkey, ctx context.Context) error { _, _, err := v.Get(ctx, "k"); return err }},
		{"Delete", "DEL", func(v *Valkey, ctx context.Context) error { return v.Delete(ctx, "k") }},
		{"Ping", "PING", func(v *Valkey, ctx context.Context) error { return v.Ping(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, _, newMaster, _ := failedOverClient(t)
			if err := tc.run(v, t.Context()); err != nil {
				t.Fatalf("%s across a failover: %v", tc.name, err)
			}
			// Ping was also sent once to warm the pool on the first master.
			if got := newMaster.count(tc.command); got != 1 {
				t.Errorf("the new master received %d %s commands; want exactly one retry", got, tc.command)
			}
		})
	}
}

func TestSentinelDoesNotRetryOtherCommands(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		run     func(*Valkey, context.Context) error
	}{
		{"Set", "SET", func(v *Valkey, ctx context.Context) error { return v.Set(ctx, "k", []byte("v"), time.Minute) }},
		{"SetNX", "SET", func(v *Valkey, ctx context.Context) error {
			_, err := v.SetNX(ctx, "k", []byte("v"), time.Minute)
			return err
		}},
		{"Increment", "INCR", func(v *Valkey, ctx context.Context) error { _, err := v.Increment(ctx, "k", time.Minute); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, _, newMaster, _ := failedOverClient(t)
			if err := tc.run(v, t.Context()); err == nil {
				t.Fatalf("%s on a crashed master succeeded", tc.name)
			}
			if got := newMaster.count(tc.command); got != 0 {
				t.Errorf("the new master received %d %s commands; the caller decides whether to retry", got, tc.command)
			}
			if err := tc.run(v, t.Context()); err != nil {
				t.Errorf("%s on the next call: %v", tc.name, err)
			}
			if got := newMaster.count(tc.command); got != 1 {
				t.Errorf("the new master received %d %s commands after the next call; want 1", got, tc.command)
			}
		})
	}
}

func TestSentinelReadonlyReplyTriggersResolution(t *testing.T) {
	t.Parallel()
	demoted, promoted, sentinel := newRESPServer(t), newRESPServer(t), newRESPServer(t)
	sentinel.setSentinelMaster(demoted.addr)
	v := newSentinelClient(t, "", sentinel)
	if err := v.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	demoted.setReadonly(true)
	sentinel.setSentinelMaster(promoted.addr)

	err := v.Set(t.Context(), "k", []byte("v"), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "READONLY") {
		t.Fatalf("Set on a demoted master = %v; want the READONLY error", err)
	}
	if master, _, pooled := poolState(v); master != "" || len(pooled) != 0 {
		t.Errorf("after READONLY: master %q, %d pooled; want a stale address and an empty pool", master, len(pooled))
	}
	if err := v.Set(t.Context(), "k", []byte("v"), time.Minute); err != nil {
		t.Errorf("Set on the next call: %v", err)
	}
	if got := promoted.count("SET"); got != 1 {
		t.Errorf("the promoted node received %d SET commands; want 1", got)
	}
}

func TestSentinelRetriesAfterReadonlyWhenIdempotent(t *testing.T) {
	t.Parallel()
	demoted, promoted, sentinel := newRESPServer(t), newRESPServer(t), newRESPServer(t)
	sentinel.setSentinelMaster(demoted.addr)
	v := newSentinelClient(t, "", sentinel)
	if err := v.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	demoted.setReadonly(true)
	sentinel.setSentinelMaster(promoted.addr)
	if err := v.Delete(t.Context(), "k"); err != nil {
		t.Fatalf("Delete across a demotion: %v", err)
	}
	if got := demoted.count("DEL"); got != 1 {
		t.Errorf("the demoted node received %d DEL commands; want 1", got)
	}
	if got := promoted.count("DEL"); got != 1 {
		t.Errorf("the promoted node received %d DEL commands; want 1", got)
	}
}

func TestSentinelRejectsAReplica(t *testing.T) {
	t.Parallel()
	node, sentinel := newRESPServer(t), newRESPServer(t)
	node.setRole(true)
	sentinel.setSentinelMaster(node.addr)
	v := newSentinelClient(t, "", sentinel)

	err := v.Ping(t.Context())
	if err == nil || !errors.Is(err, errNotMaster) {
		t.Fatalf("Ping on a replica = %v; want errNotMaster", err)
	}
	if got := sentinel.count("SENTINEL"); got != masterAttempts {
		t.Errorf("Sentinel was asked %d times; want %d (one pass per attempt)", got, masterAttempts)
	}
	if got := node.count("PING"); got != 0 {
		t.Errorf("the replica received %d PING commands", got)
	}
	if _, _, pooled := poolState(v); len(pooled) != 0 {
		t.Errorf("%d connections to a replica were pooled", len(pooled))
	}
	node.setRole(false)
	if err := v.Ping(t.Context()); err != nil {
		t.Errorf("Ping once the node is a master: %v", err)
	}
}

func TestSentinelAllDown(t *testing.T) {
	t.Parallel()
	a, b := newRESPServer(t), newRESPServer(t)
	a.kill()
	b.kill()
	v := newSentinelClient(t, "?sentinelPassword=sp-secret", a, b)
	v.password = "master-secret"
	err := v.Ping(t.Context())
	if err == nil || !strings.Contains(err.Error(), "none of the 2 Valkey Sentinels") {
		t.Fatalf("Ping with every Sentinel down = %v", err)
	}
	for _, secret := range []string{"sp-secret", "master-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error leaks a password: %v", err)
		}
	}
	// The next call asks again instead of caching the failure.
	if err := v.Ping(t.Context()); err == nil {
		t.Error("Ping with every Sentinel down succeeded")
	}
}

func TestSentinelAuthentication(t *testing.T) {
	t.Parallel()
	master, sentinel := newRESPServer(t), newRESPServer(t)
	master.password, sentinel.password = "master-secret", "sentinel-secret"
	sentinel.setSentinelMaster(master.addr)

	v, err := NewValkey("valkey+sentinel://:master-secret@" + sentinel.addr + "/mymaster/4?sentinelPassword=sentinel-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	if err := v.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if !slices.Equal(sentinel.auths, []string{"sentinel-secret"}) || !slices.Equal(master.auths, []string{"master-secret"}) {
		t.Errorf("AUTH sent to the Sentinel %v and the master %v", sentinel.auths, master.auths)
	}
	if got := master.count("SELECT"); got != 1 {
		t.Errorf("the master received %d SELECT commands; want 1", got)
	}

	for _, wrong := range []string{"?sentinelPassword=wrong-one", ""} {
		bad, err := NewValkey("valkey+sentinel://:wrong-two@" + sentinel.addr + "/mymaster" + wrong)
		if err != nil {
			t.Fatal(err)
		}
		err = bad.Ping(t.Context())
		_ = bad.Close()
		if err == nil {
			t.Errorf("Ping with wrong passwords %q succeeded", wrong)
			continue
		}
		for _, secret := range []string{"wrong-one", "wrong-two", "master-secret", "sentinel-secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the error leaks a password: %v", err)
			}
		}
	}
}

func TestSentinelConcurrentCallersShareOneResolution(t *testing.T) {
	t.Parallel()
	oldMaster, newMaster, sentinel := newRESPServer(t), newRESPServer(t), newRESPServer(t)
	sentinel.setSentinelMaster(oldMaster.addr)
	v := newSentinelClient(t, "", sentinel)
	run := func(n int) {
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				if _, _, err := v.Get(t.Context(), "k"); err != nil {
					t.Errorf("Get: %v", err)
				}
			})
		}
		wg.Wait()
	}
	run(8)
	sentinel.setSentinelMaster(newMaster.addr)
	oldMaster.kill()
	run(64)

	if got := sentinel.count("SENTINEL"); got != 2 {
		t.Errorf("Sentinel was asked %d times; want 2 (start and one failover)", got)
	}
	if master, _, _ := poolState(v); master != newMaster.addr {
		t.Errorf("master = %s; want %s", master, newMaster.addr)
	}
}

func TestSentinelClosedClient(t *testing.T) {
	t.Parallel()
	master, sentinel := newRESPServer(t), newRESPServer(t)
	sentinel.setSentinelMaster(master.addr)
	v := newSentinelClient(t, "", sentinel)
	if err := v.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Ping(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("Ping after Close = %v", err)
	}
}

func TestMasterChanged(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{replyError{msg: "READONLY You can't write against a read only replica."}, true},
		{replyError{msg: "LOADING Valkey is loading the dataset in memory"}, true},
		{replyError{msg: "WRONGPASS invalid username-password pair"}, false},
		{replyError{msg: "ERR unknown command"}, false},
		{errors.New("cache: read: EOF"), true},
	} {
		if got := masterChanged(tc.err); got != tc.want {
			t.Errorf("masterChanged(%q) = %v; want %v", tc.err, got, tc.want)
		}
	}
}
