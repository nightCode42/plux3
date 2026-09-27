// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// problems collects validation failures so that one run reports every
// problem, in the order the sections appear (SRV-008).
type problems struct{ errs []error }

// addf records a problem at a configuration path.
func (p *problems) addf(path, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

// oneOf records a problem unless v is one of the allowed values.
func (p *problems) oneOf(path, v string, allowed ...string) bool {
	if slices.Contains(allowed, v) {
		return true
	}
	p.addf(path, "%q is not one of %s", v, strings.Join(allowed, ", "))
	return false
}

// positive records a problem unless v is greater than zero.
func (p *problems) positive(path string, v int64) {
	if v <= 0 {
		p.addf(path, "must be greater than zero")
	}
}

// Validate checks every section and returns every problem it finds. It
// makes no network calls, so it runs offline in
// `plux-server config validate` (SRV-008).
func (c *Config) Validate() error {
	var p problems
	c.validateServer(&p)
	c.validateDatabase(&p)
	c.validateObjectStorage(&p)
	c.validateCache(&p)
	c.validateSigning(&p)
	c.validateAuth(&p)
	c.validateObservability(&p)
	c.validateTelemetry(&p)
	c.validateLimits(&p)
	c.validateRetention(&p)
	return errors.Join(p.errs...)
}

func (c *Config) validateServer(p *problems) {
	if len(c.Server.Roles) == 0 {
		p.addf("server.roles", "must name at least one of api, worker, fnrunner")
	}
	seen := map[Role]bool{}
	for i, r := range c.Server.Roles {
		path := "server.roles[" + strconv.Itoa(i) + "]"
		switch r {
		case RoleAPI, RoleWorker:
		case RoleFnRunner:
			p.addf(path, "the fnrunner role arrives in P7")
		default:
			p.addf(path, "%q is not one of api, worker", r)
		}
		if seen[r] {
			p.addf(path, "%q is listed twice", r)
		}
		seen[r] = true
	}
	if c.Has(RoleAPI) {
		if c.Server.Listen == "" {
			p.addf("server.listen", "must be set when the api role runs")
		}
		if c.Server.PublicBaseURL == "" {
			p.addf("server.publicBaseURL", "must be set when the api role runs, so that the server can build the links it hands out")
		} else {
			checkURL(p, "server.publicBaseURL", c.Server.PublicBaseURL)
		}
	}
	p.positive("server.shutdownGrace", int64(c.Server.ShutdownGrace))
	for i, cidr := range c.Server.TrustedProxies {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			p.addf("server.trustedProxies["+strconv.Itoa(i)+"]", "%q is not a CIDR prefix such as 10.0.0.0/8", cidr)
		}
	}
}

func (c *Config) validateDatabase(p *problems) {
	if c.Database.URL == "" {
		p.addf("database.url", "must be set (or PLUX_DATABASE_URL)")
	} else if u, err := url.Parse(c.Database.URL.Value()); err != nil {
		p.addf("database.url", "is not a URL")
	} else if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		p.addf("database.url", "must be a postgres:// URL")
	}
	if c.Database.MaxConnections < 1 {
		p.addf("database.maxConnections", "must be at least 1")
	}
}

func (c *Config) validateObjectStorage(p *problems) {
	if !p.oneOf("objectStorage.backend", c.ObjectStorage.Backend, "s3", "filesystem") {
		return
	}
	switch c.ObjectStorage.Backend {
	case "s3":
		if c.ObjectStorage.Bucket == "" {
			p.addf("objectStorage.bucket", "must be set for the s3 backend")
		}
		if c.ObjectStorage.Endpoint == "" && c.ObjectStorage.Region == "" {
			p.addf("objectStorage.endpoint", "set an endpoint or a region for the s3 backend")
		}
		if c.ObjectStorage.Endpoint != "" {
			checkURL(p, "objectStorage.endpoint", c.ObjectStorage.Endpoint)
		}
		if (c.ObjectStorage.AccessKeyID == "") != (c.ObjectStorage.SecretAccessKey == "") {
			p.addf("objectStorage.accessKeyID", "set both the access key and the secret, or neither to use an ambient credential")
		}
	case "filesystem":
		if c.ObjectStorage.Directory == "" {
			p.addf("objectStorage.directory", "must be set for the filesystem backend")
		}
	}
	if c.ObjectStorage.CDNBaseURL != "" {
		checkURL(p, "objectStorage.cdnBaseURL", c.ObjectStorage.CDNBaseURL)
	}
	p.positive("objectStorage.signedURLTTL", int64(c.ObjectStorage.SignedURLTTL))
}

func (c *Config) validateCache(p *problems) {
	if !p.oneOf("cache.backend", c.Cache.Backend, "memory", "valkey") {
		return
	}
	if c.Cache.Backend == "valkey" && c.Cache.ValkeyURL == "" {
		p.addf("cache.valkeyURL", "must be set for the valkey backend (or PLUX_CACHE_VALKEY_URL)")
	}
	if c.Cache.Backend == "memory" && c.Has(RoleAPI) && c.Has(RoleWorker) {
		return // single-node: the in-memory cache is correct
	}
	if c.Cache.Backend == "memory" {
		p.addf("cache.backend", "the memory backend is single-node only; several replicas need valkey")
	}
}

func (c *Config) validateSigning(p *problems) {
	if !p.oneOf("signing.backend", c.Signing.Backend,
		"file", "pkcs11", "awskms", "gcpkms", "azurekv", "vault") {
		return
	}
	switch c.Signing.Backend {
	case "file":
		if c.Signing.Directory == "" {
			p.addf("signing.directory", "must be set for the file backend")
		}
	case "vault":
		v := c.Signing.Vault
		if v.Address == "" {
			p.addf("signing.vault.address", "must be set for the vault backend")
		} else {
			checkURL(p, "signing.vault.address", v.Address)
		}
		if v.Token == "" {
			p.addf("signing.vault.token", "must be set for the vault backend; use PLUX_SIGNING_VAULT_TOKEN")
		}
		if v.WrapKey != "" && !keyPattern.MatchString(v.WrapKey) {
			p.addf("signing.vault.wrapKey", "must be lower-case letters, digits and hyphens")
		}
	default:
		p.addf("signing.backend", "%q arrives in P6; P2 supports vault and the file backend, which is refused for production environments", c.Signing.Backend)
	}
	// The prefix leaves room for "-" and a 36-character identifier within
	// the 64 characters a key reference may have.
	if !keyPattern.MatchString(c.Signing.Keys.Targets) || len(c.Signing.Keys.Targets) > 27 {
		p.addf("signing.keys.targets", "must be a prefix of at most 27 lower-case letters, digits and hyphens")
	}
}

// keyPattern is the form of a key reference (SEC-120).
var keyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func (c *Config) validateAuth(p *problems) {
	s := c.Auth.Studio
	if s.OIDC != (OIDC{}) {
		p.addf("auth.studio.oidc", "single sign-on arrives with GOV-004 in P9; P2 signs in with built-in accounts")
	}
	if !s.AllowPasswordLogin {
		p.addf("auth.studio.allowPasswordLogin", "must be true: built-in accounts are the only sign-in until P9, so otherwise nobody can sign in")
	}
	if s.CookieDomain != "" {
		p.addf("auth.studio.cookieDomain", "must be empty: the __Host- prefix of the session cookie forbids a Domain attribute (SEC-101)")
	}
	for i, m := range s.MFARequiredFor {
		p.oneOf("auth.studio.mfaRequiredFor["+strconv.Itoa(i)+"]", m, "publish", "approve", "keys", "members")
	}
	for _, m := range []string{"publish", "approve", "keys", "members"} {
		if !slices.Contains(s.MFARequiredFor, m) {
			p.addf("auth.studio.mfaRequiredFor", "must include %q: SEC-100 makes a second factor mandatory for it", m)
		}
	}
	p.positive("auth.studio.sessionTTL", int64(s.SessionTTL))
	p.positive("auth.device.accessTokenTTL", int64(c.Auth.Device.AccessTokenTTL))
	p.positive("auth.device.refreshTokenTTL", int64(c.Auth.Device.RefreshTokenTTL))
	p.positive("auth.ci.tokenTTL", int64(c.Auth.CI.TokenTTL))
	for i, is := range c.Auth.CI.Issuers {
		path := "auth.ci.issuers[" + strconv.Itoa(i) + "]"
		if is.Issuer == "" {
			p.addf(path+".issuer", "must be set")
		} else {
			checkURL(p, path+".issuer", is.Issuer)
		}
		if is.Audience == "" {
			p.addf(path+".audience", "must be set, so a token minted for another service cannot be replayed here")
		}
		if is.SubjectPattern == "" {
			p.addf(path+".subjectPattern", "must be set, so that any workload of the issuer cannot exchange a token")
		}
	}
}

func (c *Config) validateObservability(p *problems) {
	p.oneOf("observability.logLevel", c.Observability.LogLevel, "debug", "info", "warn", "error")
	p.oneOf("observability.logFormat", c.Observability.LogFormat, "json", "text")
	if c.Observability.OTLPEndpoint != "" {
		checkURL(p, "observability.otlpEndpoint", c.Observability.OTLPEndpoint)
	}
	if r := c.Observability.TraceSampleRatio; r < 0 || r > 1 {
		p.addf("observability.traceSampleRatio", "must be between 0 and 1")
	}
}

func (c *Config) validateTelemetry(p *problems) {
	if p.oneOf("telemetry.store", c.Telemetry.Store, "postgres", "clickhouse") && c.Telemetry.Store == "clickhouse" {
		p.addf("telemetry.store", "clickhouse arrives in P9 (ANL-010)")
	}
}

// validateLimits checks the installation-level tightenings against the
// registry: a key must exist, be settable at the installation scope, and
// only ever tighten (LIM-001, LIM-002).
func (c *Config) validateLimits(p *problems) {
	_, errs := c.resolveLimits()
	p.errs = append(p.errs, errs...)
}

// LimitSet returns the installation-level limits: the registry defaults
// with every configured tightening applied (LIM-001). Validate reports the
// same problems, so a validated configuration never fails here.
func (c *Config) LimitSet() (limits.Set, error) {
	set, errs := c.resolveLimits()
	return set, errors.Join(errs...)
}

// resolveLimits applies the configured tightenings in key order and
// returns the resulting set together with every problem it found.
func (c *Config) resolveLimits() (limits.Set, []error) {
	keys := make([]string, 0, len(c.Limits))
	for k := range c.Limits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	set := limits.Defaults()
	var errs []error
	for _, k := range keys {
		path := "limits." + k
		def, ok := limits.Lookup(limits.Key(k))
		if !ok {
			errs = append(errs, fmt.Errorf("%s: is not a limit of the registry (schema/limits.json)", path))
			continue
		}
		v, err := limitValue(def, c.Limits[k])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		next, err := set.Tighten(def.Key, limits.ScopeInstallation, v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s", path, strings.TrimPrefix(err.Error(), "limits.Tighten: ")))
			continue
		}
		set = next
	}
	return set, errs
}

// limitValue reads a configured limit in the unit of its definition:
// sizes accept a unit suffix, everything else is a plain number.
func limitValue(def limits.Definition, s string) (int64, error) {
	if def.Unit == limits.UnitBytes {
		b, err := ParseBytes(s)
		return b.Int64(), err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	return v, nil
}

func (c *Config) validateRetention(p *problems) {
	p.positive("retention.auditYears", int64(c.Retention.AuditYears))
	p.positive("retention.developmentReleaseDays", int64(c.Retention.DevelopmentReleaseDays))
	if c.Retention.SnapshotDays < 90 {
		p.addf("retention.snapshotDays", "must be at least 90: snapshots are retained for at least 90 days (SRV-031)")
	}
	if c.Retention.TrashDays < 30 {
		p.addf("retention.trashDays", "must be at least 30: deleted items are restorable for 30 days (GOV-031)")
	}
}

// checkURL records a problem unless s is an absolute URL.
func checkURL(p *problems, path, s string) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		p.addf(path, "%q is not an absolute URL", s)
	}
}
