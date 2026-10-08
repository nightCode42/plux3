// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"net/url"
	"strings"
	"time"
)

// hstsPreloadMinimum is the shortest max-age browsers accept for their
// preload lists.
const hstsPreloadMinimum = 365 * 24 * time.Hour

// validateTransport checks the transport security of the server's own
// listeners and, for a production installation, of its connections to
// PostgreSQL and Valkey (SEC-040, SEC-043).
func (c *Config) validateTransport(p *problems) {
	s := c.Server
	if s.TLS.Enabled() && (s.TLS.CertFile == "" || s.TLS.KeyFile == "") {
		p.addf("server.tls", "certFile and keyFile must be set together")
	}
	if s.TLS.AllowTLS12 && !s.TLS.Enabled() {
		p.addf("server.tls.allowTLS12", "has no effect without server.tls.certFile and keyFile")
	}
	if s.HSTS.Preload && (!s.HSTS.IncludeSubDomains || s.HSTS.MaxAge.Duration() < hstsPreloadMinimum) {
		p.addf("server.hsts.preload", "needs includeSubDomains and a maxAge of at least 8760h, or browsers refuse the preload entry")
	}
	if s.InternalTLS.Enabled() && (s.InternalTLS.CAFile == "" || s.InternalTLS.CertFile == "" || s.InternalTLS.KeyFile == "") {
		p.addf("server.internalTLS", "caFile, certFile and keyFile must be set together")
	}
	if s.InternalListen != "" {
		if !c.Has(RoleAPI) {
			p.addf("server.internalListen", "needs the api role")
		}
		if !s.InternalTLS.Enabled() {
			p.addf("server.internalListen", "needs server.internalTLS: the internal listener requires mutual TLS (SEC-043)")
		}
	}
	if s.Production {
		c.validateProduction(p)
	}
}

// validateProduction refuses the transports that are acceptable on a
// developer's machine and nowhere else (SEC-040).
func (c *Config) validateProduction(p *problems) {
	if c.Has(RoleAPI) && !c.Server.TLS.Enabled() && !c.Server.BehindTLSProxy {
		p.addf("server.tls", "a production installation terminates TLS itself; set certFile and keyFile, or set server.behindTLSProxy to say that a proxy terminates TLS 1.3 in front")
	}
	if c.Database.URL != "" {
		if u, err := url.Parse(c.Database.URL.Value()); err == nil {
			if mode := u.Query().Get("sslmode"); mode != "verify-full" && mode != "verify-ca" {
				p.addf("database.url", "a production installation needs sslmode=verify-full (or verify-ca), not %q", mode)
			}
		}
	}
	if c.Cache.Backend == "valkey" && c.Cache.ValkeyURL != "" && !valkeyTLS(c.Cache.ValkeyURL.Value()) {
		p.addf("cache.valkeyURL", "a production installation needs TLS to Valkey: use rediss:// or valkeys:// (also with +sentinel), not %q", valkeyScheme(c.Cache.ValkeyURL.Value()))
	}
}

// valkeyScheme returns the lower-case scheme of a Valkey URL without the
// rest of it, which carries a password.
func valkeyScheme(raw string) string {
	scheme, _, _ := strings.Cut(raw, "://")
	return strings.ToLower(scheme)
}

// valkeyTLS reports whether the scheme of a Valkey URL selects TLS.
func valkeyTLS(raw string) bool {
	switch valkeyScheme(raw) {
	case "rediss", "valkeys", "rediss+sentinel", "valkeys+sentinel":
		return true
	}
	return false
}

// Warnings lists the settings that are valid but weaken the transport.
// The server logs each at start-up, and `config validate` prints them,
// so an unsafe choice is never silent (SEC-040).
func (c *Config) Warnings() []string {
	var w []string
	s := c.Server
	if c.Has(RoleAPI) {
		if !s.TLS.Enabled() && !s.BehindTLSProxy {
			w = append(w, "server.tls is not set and server.behindTLSProxy is false: the api role serves plain HTTP; terminate TLS 1.3 in front of it")
		}
		if s.HSTS.MaxAge == 0 {
			w = append(w, "server.hsts.maxAge is 0: Strict-Transport-Security is disabled and browsers may downgrade to HTTP")
		}
	}
	if s.TLS.Enabled() && s.TLS.AllowTLS12 {
		w = append(w, "server.tls.allowTLS12 is set: TLS 1.2 clients are accepted (AEAD ECDHE suites only); the default is TLS 1.3 only")
	}
	if s.Production && c.Database.URL != "" {
		if u, err := url.Parse(c.Database.URL.Value()); err == nil && u.Query().Get("sslmode") == "verify-ca" {
			w = append(w, "database.url uses sslmode=verify-ca: the server certificate's host name is not checked; prefer verify-full")
		}
	}
	return w
}
