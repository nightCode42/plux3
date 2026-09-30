// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package netsim

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlow3GIsTheSpecsSlowNetwork(t *testing.T) {
	t.Parallel()
	p := Slow3G()
	if p.Down != 750_000 || p.Up != 250_000 || p.RTT != 300*time.Millisecond || p.Loss != 0.01 {
		t.Fatalf("profile %+v", p)
	}
	// Under 1% loss at 300 ms, TCP reaches 1460·8/0.3 · 1.22/0.1 ≈ 475
	// kbit/s: below the down link, above the up link.
	if got := p.Effective(p.Down); got < 474_000 || got > 476_000 {
		t.Errorf("down %d", got)
	}
	if got := p.Effective(p.Up); got != 250_000 {
		t.Errorf("up %d", got)
	}
	if got := (Profile{Down: 5}).Effective(5); got != 5 {
		t.Errorf("no loss: %d", got)
	}
}

func TestLinkSerialisesAndDelays(t *testing.T) {
	t.Parallel()
	l := &link{bps: 8000, delay: 100 * time.Millisecond} // 1,000 bytes a second
	t0 := time.Unix(0, 0)
	sent, arrive := l.send(t0, 500)
	if sent != t0.Add(500*time.Millisecond) || arrive != t0.Add(600*time.Millisecond) {
		t.Errorf("first: sent %v arrive %v", sent.Sub(t0), arrive.Sub(t0))
	}
	// Handed over while the link is busy: it queues behind the first.
	sent, arrive = l.send(t0.Add(100*time.Millisecond), 250)
	if sent != t0.Add(750*time.Millisecond) || arrive != t0.Add(850*time.Millisecond) {
		t.Errorf("queued: sent %v arrive %v", sent.Sub(t0), arrive.Sub(t0))
	}
	// Handed over after the link went idle.
	sent, _ = l.send(t0.Add(2*time.Second), 1000)
	if sent != t0.Add(3*time.Second) {
		t.Errorf("idle: sent %v", sent.Sub(t0))
	}
}

// echo serves a TCP echo on a random port.
func echo(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// TestForwarderIsNeverFasterThanThePath checks that bytes come through
// intact and no sooner than the path allows: the handshake, one round
// trip, and each direction's serialisation. Only the lower bound is
// asserted; a loaded machine may be slower.
func TestForwarderIsNeverFasterThanThePath(t *testing.T) {
	t.Parallel()
	p := Profile{Down: 800_000, Up: 400_000, RTT: 100 * time.Millisecond, MSS: 1460}
	n := New(p)
	f, err := n.Forward(t.Context(), "127.0.0.1:0", echo(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	payload := bytes.Repeat([]byte("plux"), 5000) // 20,000 bytes
	start := time.Now()
	var d net.Dialer
	c, err := d.DialContext(t.Context(), "tcp", f.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	go func() { _, _ = c.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if !bytes.Equal(got, payload) {
		t.Fatal("the bytes changed on the way")
	}
	// Handshake and a round trip: 200 ms; 20,000 bytes up at 50 kB/s:
	// 400 ms (the down link, twice as fast, overlaps with it).
	if minimum := 600 * time.Millisecond; elapsed < minimum {
		t.Errorf("took %v, less than the path's %v", elapsed, minimum)
	}
}

// TestProxyRecordsExchanges checks that every request is recorded with
// the bytes it cost, headers included, and that the requests reach the
// server as they were sent.
// Verifies: NFR-006.
func TestProxyRecordsExchanges_NFR_006(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "192.0.2.7" {
			http.Error(w, "no device address", http.StatusBadRequest)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(bytes.Repeat(body, 10))
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p, err := New(Profile{Down: 10_000_000, Up: 10_000_000, RTT: 10 * time.Millisecond, MSS: 1460}).Proxy(ctx, "127.0.0.1:0", server.URL, "192.0.2.7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	client := &http.Client{}
	post, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, p.URL()+"/v1/thing", strings.NewReader("0123456789"))
	res, err := client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if len(body) != 100 {
		t.Fatalf("body %q", body)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, p.URL()+"/v1/thing", nil)
	req.Header.Set("If-None-Match", `"v1"`)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	ex := p.Exchanges()
	if len(ex) != 2 {
		t.Fatalf("exchanges %+v", ex)
	}
	if ex[0].Method != http.MethodPost || ex[0].Path != "/v1/thing" || ex[0].Status != http.StatusOK || ex[1].Status != http.StatusNotModified {
		t.Errorf("exchanges %+v", ex)
	}
	// Headers and body each way: the POST's body of 10 bytes and its
	// response of 100 bytes, each with at least a request or status line.
	if ex[0].Up <= 10+20 || ex[0].Down <= 100+20 || ex[1].Up <= 20 || ex[1].Down <= 20 || ex[1].Down > 300 {
		t.Errorf("bytes %+v", ex)
	}
	if !ex[1].End.After(ex[1].Start) && !ex[1].End.Equal(ex[1].Start) {
		t.Errorf("times %+v", ex[1])
	}
	p.Reset()
	if len(p.Exchanges()) != 0 {
		t.Error("Reset kept exchanges")
	}
}

func TestProxyRefusesBadTargets(t *testing.T) {
	t.Parallel()
	if _, err := New(Slow3G()).Proxy(t.Context(), "127.0.0.1:0", "http://[::1", ""); err == nil {
		t.Error("parsed a bad URL")
	}
	if _, err := New(Slow3G()).Forward(t.Context(), "256.0.0.1:1", "x"); err == nil {
		t.Error("listened on a bad address")
	}
}
