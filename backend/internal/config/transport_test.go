// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"slices"
	"strings"
	"testing"
)

// prod is a production configuration that passes every transport rule:
// it terminates TLS itself, reaches PostgreSQL with verify-full and
// Valkey over TLS.
const prod = "server:\n  publicBaseURL: \"https://plux.example\"\n  production: true\n" +
	"  tls:\n    certFile: /etc/plux/tls.crt\n    keyFile: /etc/plux/tls.key\n" +
	"database:\n  url: \"postgres://plux@db/plux?sslmode=verify-full\"\n" +
	"cache:\n  backend: valkey\n  valkeyURL: \"rediss://valkey:6379\"\n"

// Verifies: SEC-040.
func TestTransportDefaultsAreSecure(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal)
	if c.Server.Production || c.Server.BehindTLSProxy || c.Server.TLS.Enabled() || c.Server.TLS.AllowTLS12 {
		t.Errorf("an insecure transport setting is on by default: %+v", c.Server)
	}
	h := c.Server.HSTS
	if h.MaxAge.Duration().Seconds() != 63072000 || !h.IncludeSubDomains || h.Preload {
		t.Errorf("HSTS defaults = %+v, want max-age 63072000 with includeSubDomains, no preload", h)
	}
}

// Verifies: SEC-040.
func TestProductionTransportIsAccepted(t *testing.T) {
	t.Parallel()
	c := parse(t, prod)
	if len(c.Warnings()) != 0 {
		t.Errorf("warnings for a safe production configuration: %v", c.Warnings())
	}
	proxied := strings.Replace(prod, "  tls:\n    certFile: /etc/plux/tls.crt\n    keyFile: /etc/plux/tls.key\n",
		"  behindTLSProxy: true\n", 1)
	parse(t, proxied)
	for _, url := range []string{"valkeys://v:6379", "rediss+sentinel://a:26379,b:26379/m", "valkeys+sentinel://a:26379/m"} {
		parse(t, strings.Replace(prod, "rediss://valkey:6379", url, 1))
	}
}

// Verifies: SEC-040.
func TestProductionRefusesAnUnprotectedTransport(t *testing.T) {
	t.Parallel()
	noTLS := strings.Replace(prod, "  tls:\n    certFile: /etc/plux/tls.crt\n    keyFile: /etc/plux/tls.key\n", "", 1)
	mentions(t, []struct{ name, src, want string }{
		{"no tls and no proxy", noTLS, "server.tls"},
		{"no sslmode", strings.Replace(prod, "?sslmode=verify-full", "", 1), "sslmode=verify-full"},
		{"sslmode require", strings.Replace(prod, "verify-full", "require", 1), `not "require"`},
		{"sslmode disable", strings.Replace(prod, "verify-full", "disable", 1), `not "disable"`},
		{"sslmode prefer", strings.Replace(prod, "verify-full", "prefer", 1), `not "prefer"`},
		{"redis", strings.Replace(prod, "rediss://", "redis://", 1), "cache.valkeyURL"},
		{"valkey", strings.Replace(prod, "rediss://", "valkey://", 1), "cache.valkeyURL"},
		{"redis sentinel", strings.Replace(prod, "rediss://valkey:6379", "redis+sentinel://a:26379/m", 1), "cache.valkeyURL"},
		{"valkey sentinel", strings.Replace(prod, "rediss://valkey:6379", "valkey+sentinel://a:26379/m", 1), "cache.valkeyURL"},
	})
}

// Verifies: SEC-040.
// The same configuration is accepted outside production, so a developer's
// plain transport keeps working.
func TestDevelopmentAcceptsAPlainTransport(t *testing.T) {
	t.Parallel()
	dev := strings.Replace(prod, "  production: true\n", "", 1)
	dev = strings.Replace(dev, "  tls:\n    certFile: /etc/plux/tls.crt\n    keyFile: /etc/plux/tls.key\n", "", 1)
	dev = strings.Replace(dev, "?sslmode=verify-full", "", 1)
	dev = strings.Replace(dev, "rediss://", "redis://", 1)
	c := parse(t, dev)
	if !slices.ContainsFunc(c.Warnings(), func(w string) bool { return strings.Contains(w, "plain HTTP") }) {
		t.Errorf("no warning about plain HTTP: %v", c.Warnings())
	}
}

// Verifies: SEC-040.
func TestProductionNeverPrintsTheValkeyPassword(t *testing.T) {
	t.Parallel()
	got := refuse(t, strings.Replace(prod, "rediss://valkey:6379", "redis://:hunter2@valkey:6379", 1))
	if strings.Contains(got, "hunter2") {
		t.Errorf("the refusal leaks the password: %s", got)
	}
}

// Verifies: SEC-040.
func TestVerifyCAIsAcceptedWithAWarning(t *testing.T) {
	t.Parallel()
	c := parse(t, strings.Replace(prod, "verify-full", "verify-ca", 1))
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "verify-ca") {
		t.Errorf("warnings = %v", w)
	}
}

// Verifies: SEC-040.
func TestTLSSettingsAreChecked(t *testing.T) {
	t.Parallel()
	mentions(t, []struct{ name, src, want string }{
		{"certificate without key", withServer("  tls:\n    certFile: /c.crt\n"), "certFile and keyFile must be set together"},
		{"key without certificate", withServer("  tls:\n    keyFile: /c.key\n"), "certFile and keyFile must be set together"},
		{"TLS 1.2 without TLS", withServer("  tls:\n    allowTLS12: true\n"), "server.tls.allowTLS12"},
		{"preload without subdomains", withServer("  hsts:\n    preload: true\n    includeSubDomains: false\n"), "server.hsts.preload"},
		{"preload with a short max-age", withServer("  hsts:\n    preload: true\n    maxAge: 24h\n"), "server.hsts.preload"},
		{"preload with HSTS off", withServer("  hsts:\n    preload: true\n    maxAge: 0s\n"), "server.hsts.preload"},
	})
}

// Verifies: SEC-040.
func TestWarningsNameEveryWeakenedSetting(t *testing.T) {
	t.Parallel()
	c := parse(t, strings.Replace(prod, "    keyFile: /etc/plux/tls.key\n",
		"    keyFile: /etc/plux/tls.key\n    allowTLS12: true\n", 1))
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "allowTLS12") {
		t.Errorf("TLS 1.2 warning: %v", w)
	}
	c = parse(t, strings.Replace(prod, "  production: true\n", "  production: true\n  hsts:\n    maxAge: 0s\n", 1))
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "server.hsts.maxAge is 0") {
		t.Errorf("HSTS warning: %v", w)
	}
	c = parse(t, strings.Replace(prod, "  production: true\n", "  production: true\n  hsts:\n    preload: true\n", 1))
	if h := c.Server.HSTS; !h.Preload || h.MaxAge.Duration().Hours() != 17520 {
		t.Errorf("preload = %+v", h)
	}
}

// Verifies: SEC-043.
func TestInternalTLSIsChecked(t *testing.T) {
	t.Parallel()
	internal := "  internalTLS:\n    caFile: /ca.crt\n    certFile: /r.crt\n    keyFile: /r.key\n"
	c := parse(t, withServer("  internalListen: \":8443\"\n"+internal))
	if !c.Server.InternalTLS.Enabled() || c.Server.InternalListen != ":8443" {
		t.Errorf("internal = %+v", c.Server)
	}
	mentions(t, []struct{ name, src, want string }{
		{"a listener without TLS", withServer("  internalListen: \":8443\"\n"), "mutual TLS"},
		{"half an identity", withServer("  internalTLS:\n    caFile: /ca.crt\n    certFile: /r.crt\n"), "caFile, certFile and keyFile must be set together"},
		{"a listener without the api role", withServer("  roles: [worker]\n  internalListen: \":8443\"\n" + internal), "needs the api role"},
	})
}
