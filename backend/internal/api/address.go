// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientAddress returns the address of the client that made a call. The
// immediate peer is the answer unless it is a trusted proxy, in which
// case X-Forwarded-For is read from the right, skipping trusted proxies,
// and the first untrusted hop is the client. Reading from the right
// means a client cannot choose its own address by sending the header
// itself: only hops the installation trusts are skipped.
func ClientAddress(peer string, header http.Header, trusted []netip.Prefix) string {
	addr, ok := parseAddr(peer)
	if !ok {
		return peer
	}
	if !trustedAddr(addr, trusted) {
		return addr.String()
	}
	hops := strings.Split(strings.Join(header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseAddr(strings.TrimSpace(hops[i]))
		if !ok {
			// A malformed hop ends the trust chain; the last hop that
			// could be read is the best answer.
			return addr.String()
		}
		addr = hop
		if !trustedAddr(hop, trusted) {
			return hop.String()
		}
	}
	return addr.String()
}

// parseAddr reads "host", "host:port" or "[v6]:port".
func parseAddr(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// trustedAddr reports whether an address belongs to a trusted proxy.
func trustedAddr(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
