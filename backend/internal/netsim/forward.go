// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package netsim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// chunk is bytes on their way through a link.
type chunk struct {
	data   []byte
	arrive time.Time
}

// Forwarder accepts TCP connections and forwards each to a target
// address through the network: what the device sends crosses the up
// link, what it receives the down link.
type Forwarder struct {
	network *Network
	ln      net.Listener
	target  string
	wg      sync.WaitGroup
}

// Forward listens on addr (host:port; port 0 for any) and forwards every
// connection to target through n until ctx ends or Close is called.
func (n *Network) Forward(ctx context.Context, addr, target string) (*Forwarder, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("netsim.Forward: %w", err)
	}
	f := &Forwarder{network: n, ln: ln, target: target}
	f.wg.Go(func() { f.serve(ctx) })
	context.AfterFunc(ctx, func() { _ = ln.Close() })
	return f, nil
}

// Addr is the address devices connect to.
func (f *Forwarder) Addr() string { return f.ln.Addr().String() }

// Close stops accepting and waits for the open connections to end.
func (f *Forwarder) Close() error {
	err := f.ln.Close()
	f.wg.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("netsim: %w", err)
	}
	return nil
}

func (f *Forwarder) serve(ctx context.Context) {
	var d net.Dialer
	for {
		device, err := f.ln.Accept()
		if err != nil {
			return
		}
		// The TCP handshake: the device's first bytes leave one round
		// trip after it connected.
		ready := time.Now().Add(f.network.profile.RTT)
		server, err := d.DialContext(ctx, "tcp", f.target)
		if err != nil {
			_ = device.Close()
			continue
		}
		f.wg.Go(func() {
			var both sync.WaitGroup
			both.Go(func() { pipe(server, device, f.network.up, ready) })
			both.Go(func() { pipe(device, server, f.network.down, time.Time{}) })
			both.Wait()
			_ = device.Close()
			_ = server.Close()
		})
	}
}

// pipe copies src to dst across l, starting no earlier than ready; at
// the end of src it closes dst for writing, or entirely when dst cannot
// half-close.
func pipe(dst, src net.Conn, l *link, ready time.Time) {
	queue := make(chan chunk, 1024)
	var deliver sync.WaitGroup
	deliver.Go(func() {
		failed := false
		for c := range queue {
			if failed {
				continue
			}
			time.Sleep(time.Until(c.arrive))
			if _, err := dst.Write(c.data); err != nil {
				failed = true
			}
		}
	})
	buf := make([]byte, 16*1024)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			now := time.Now()
			if now.Before(ready) {
				now = ready
			}
			sent, arrive := l.send(now, k)
			queue <- chunk{data: append([]byte(nil), buf[:k]...), arrive: arrive}
			// The sender waits while its bytes are serialised, as a
			// full TCP window would make it.
			time.Sleep(time.Until(sent))
		}
		if err != nil {
			break
		}
	}
	close(queue)
	deliver.Wait()
	if tc, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = tc.CloseWrite()
	} else {
		_ = dst.Close()
	}
	// Drain what src may still send, so its writer is never blocked.
	_, _ = io.Copy(io.Discard, src)
}
