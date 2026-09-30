// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package netsim puts a simulated network path between a device and the
// server, for the sync benchmark (QA-007, NFR-006, NFR-007): an HTTP
// reverse proxy whose device-facing connections are slowed to a
// [Profile] — bandwidth each way, round-trip time, loss — and which
// records every request with the bytes it cost on the wire.
//
// The path is modelled per direction as one link shared by every
// connection: a chunk written at time t is serialised onto the link once
// the link is free, at the link's bandwidth, and arrives half a
// round-trip later. A new connection's first bytes wait one more
// round-trip, the TCP handshake. Packet loss is not simulated packet by
// packet; it caps each direction's bandwidth at the steady-state
// throughput TCP reaches under that loss (Mathis et al., 1997):
// MSS/RTT × 1.22/√p. TLS is not modelled: bytes are HTTP/1.1 in the
// clear, so the handshake round-trips and record overhead of HTTPS come
// on top.
//
// Safe for concurrent use.
package netsim

import (
	"math"
	"sync"
	"time"
)

// Profile is a network path.
type Profile struct {
	// Down and Up are the bandwidths, in bits per second.
	Down, Up int64
	// RTT is the round-trip time.
	RTT time.Duration
	// Loss is the packet loss rate, from 0 to 1.
	Loss float64
	// MSS is TCP's maximum segment size, in bytes.
	MSS int
}

// Slow3G is the spec's slow network (§30.1): 750 kbit/s down, 250 kbit/s
// up, 300 ms RTT, 1% loss.
func Slow3G() Profile {
	return Profile{Down: 750_000, Up: 250_000, RTT: 300 * time.Millisecond, Loss: 0.01, MSS: 1460}
}

// Effective is the bandwidth, in bits per second, a TCP connection
// reaches over a link of bps under the profile's loss: the smaller of the
// link's and the Mathis bound.
func (p Profile) Effective(bps int64) int64 {
	if p.Loss <= 0 || p.RTT <= 0 {
		return bps
	}
	mathis := float64(p.MSS*8) / p.RTT.Seconds() * 1.22 / math.Sqrt(p.Loss)
	return min(bps, int64(mathis))
}

// link is one direction of the path.
type link struct {
	bps   int64
	delay time.Duration // one way: half the round-trip time

	mu   sync.Mutex
	free time.Time // guarded by mu: when the link finishes sending what it has
}

// send schedules n bytes handed to the link at now: it returns when they
// have been serialised onto the link (sent) and when they arrive.
func (l *link) send(now time.Time, n int) (sent, arrive time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := now
	if l.free.After(start) {
		start = l.free
	}
	l.free = start.Add(time.Duration(float64(n*8) / float64(l.bps) * float64(time.Second)))
	return l.free, l.free.Add(l.delay)
}

// Network is a simulated path; its links are shared by every connection
// that goes through it.
type Network struct {
	profile  Profile
	up, down *link
}

// New creates a path with the profile.
func New(p Profile) *Network {
	return &Network{
		profile: p,
		up:      &link{bps: p.Effective(p.Up), delay: p.RTT / 2},
		down:    &link{bps: p.Effective(p.Down), delay: p.RTT / 2},
	}
}

// Profile is the path's profile.
func (n *Network) Profile() Profile { return n.profile }
