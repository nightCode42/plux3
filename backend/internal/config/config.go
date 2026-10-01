// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"slices"
	"strings"
)

// Role is one deployable role of the server (SRV-001, ADR-0006).
type Role string

// The roles. A process runs any subset; a single-node install runs them
// all.
const (
	RoleAPI      Role = "api"
	RoleWorker   Role = "worker"
	RoleFnRunner Role = "fnrunner"
)

// Config is the whole configuration (Appendix H.1).
type Config struct {
	Server        Server            `json:"server"`
	Database      Database          `json:"database"`
	ObjectStorage ObjectStorage     `json:"objectStorage"`
	Cache         Cache             `json:"cache"`
	Signing       Signing           `json:"signing"`
	Auth          Auth              `json:"auth"`
	Observability Observability     `json:"observability"`
	Telemetry     Telemetry         `json:"telemetry"`
	Limits        map[string]string `json:"limits"`
	Retention     Retention         `json:"retention"`
	Assets        Assets            `json:"assets"`
}

// Assets configures the handling of uploaded asset files (SRV-060).
type Assets struct {
	// MalwareScanner is ClamAV's daemon, "tcp://host:3310" or
	// "unix:///path/clamd.sock"; empty scans nothing.
	MalwareScanner string `json:"malwareScanner"`
	// SVGCompiler is the plux-svgc executable the worker compiles SVGs
	// with (CMP-031); empty compiles none, so SVG assets fail.
	SVGCompiler string `json:"svgCompiler"`
	// PathOps is the libpath_ops library plux-svgc's optimisers load.
	PathOps string `json:"pathOps"`
}

// Server is the process itself.
type Server struct {
	// Roles are the roles this process runs.
	Roles []Role `json:"roles"`
	// Listen is the address the api role serves on.
	Listen string `json:"listen"`
	// PublicBaseURL is the externally reachable base URL, used to build
	// the links the server hands out.
	PublicBaseURL string `json:"publicBaseURL"`
	// ShutdownGrace is how long shutdown waits for requests and jobs to
	// finish before giving up (SRV-007).
	ShutdownGrace Duration `json:"shutdownGrace"`
	// TrustedProxies are the networks whose X-Forwarded-For header is
	// believed, in CIDR form. The client address — used for rate limits
	// (SRV-065) and recorded in the audit log (SEC-140) — is taken from
	// the header only when the immediate peer is one of them; otherwise
	// a client could claim any address it liked.
	TrustedProxies []string `json:"trustedProxies"`
}

// Database is PostgreSQL, the system of record (SRV-020).
type Database struct {
	// URL is the libpq connection string. It carries a password, so it is
	// a Secret and is never logged.
	URL Secret `json:"url"`
	// MaxConnections bounds the pool.
	MaxConnections int `json:"maxConnections"`
	// MigrateOnStart applies pending migrations under an advisory lock
	// before serving (SRV-021).
	MigrateOnStart bool `json:"migrateOnStart"`
}

// ObjectStorage holds bundles, deltas, assets and exports (SRV-023).
type ObjectStorage struct {
	// Backend is "s3" or "filesystem"; the filesystem backend is for
	// development and tests only.
	Backend string `json:"backend"`
	// Endpoint is the S3 endpoint, such as a MinIO address.
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`
	// PathStyle addresses the bucket in the path rather than in the host,
	// which MinIO and other self-hosted services need.
	PathStyle bool `json:"pathStyle"`
	// AccessKeyID and SecretAccessKey are used when no ambient credential
	// (instance role, environment) is available.
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey Secret `json:"secretAccessKey"`
	// Directory is the root of the filesystem backend.
	Directory string `json:"directory"`
	// CDNBaseURL is prefixed to artifact paths when a CDN is in front of
	// the storage; empty means the server hands out signed URLs or serves
	// the bytes itself (DEP-041).
	CDNBaseURL string `json:"cdnBaseURL"`
	// SignedURLTTL is how long a signed artifact URL stays valid.
	SignedURLTTL Duration `json:"signedURLTTL"`
}

// Cache is the shared, expendable cache: rate limits and single-flight
// markers in P2, the DPoP replay cache from P6.
type Cache struct {
	// Backend is "memory" or "valkey"; "memory" is single-node only.
	Backend string `json:"backend"`
	// ValkeyURL is the redis:// or rediss:// URL of the Valkey server.
	ValkeyURL Secret `json:"valkeyURL"`
}

// Signing reaches keys only through the signing abstraction (SEC-120).
type Signing struct {
	// Backend is "file", "pkcs11", "awskms", "gcpkms", "azurekv" or
	// "vault". "file" is refused for production environments (SEC-056).
	Backend string `json:"backend"`
	// Keys names the keys of the update-metadata roles. P2 uses the
	// targets role only (ADR-0004); the others arrive in P6.
	Keys SigningKeys `json:"keys"`
	// Directory is the root of the file backend's keys.
	Directory string `json:"directory"`
	// Vault configures the HashiCorp Vault Transit backend.
	Vault Vault `json:"vault"`
}

// SigningKeys names the keys of each update-metadata role.
type SigningKeys struct {
	// Targets is the prefix of every environment's targets key, which
	// signs bundle hashes and manifests (SRV-052, REL-031). Each
	// environment has its own key, "<prefix>-<environment ID>"
	// (ADR-0004, GOV-010).
	Targets string `json:"targets"`
}

// Vault is a HashiCorp Vault Transit engine (SEC-120).
type Vault struct {
	// Address is Vault's base URL.
	Address string `json:"address"`
	// Token authenticates to Vault; set it with PLUX_SIGNING_VAULT_TOKEN.
	Token Secret `json:"token"`
	// Mount is the Transit engine's mount path; "" is "transit".
	Mount string `json:"mount"`
	// Namespace is the Vault Enterprise namespace, or "".
	Namespace string `json:"namespace"`
	// WrapKey names the key that wraps the data keys of stored secrets
	// (SEC-106); "" is "plux-secrets".
	WrapKey string `json:"wrapKey"`
}

// Auth configures who may call the server.
type Auth struct {
	Studio StudioAuth `json:"studio"`
	Device DeviceAuth `json:"device"`
	CI     CIAuth     `json:"ci"`
}

// StudioAuth is how people sign in (SEC-100, SEC-101).
type StudioAuth struct {
	// OIDC is an OpenID Connect provider people may sign in with, beside
	// or instead of built-in accounts (SEC-100). SAML, provisioning and
	// group mapping arrive with GOV-004 in P9.
	OIDC OIDC `json:"oidc"`
	// AllowPasswordLogin enables built-in accounts with Argon2id hashing.
	AllowPasswordLogin bool `json:"allowPasswordLogin"`
	// MFARequiredFor lists the capabilities that demand a second factor.
	// It must name at least "publish", "approve", "keys" and "members",
	// the capabilities SEC-100 makes it mandatory for.
	MFARequiredFor []string `json:"mfaRequiredFor"`
	// SessionTTL is how long a browser session lasts.
	SessionTTL Duration `json:"sessionTTL"`
	// CookieDomain must stay empty: the __Host- prefix the session
	// cookie carries forbids a Domain attribute (SEC-101).
	CookieDomain string `json:"cookieDomain"`
}

// OIDC is an OpenID Connect provider.
type OIDC struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"clientID"`
	// ClientSecret authenticates the server to the provider; set it with
	// PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET.
	ClientSecret Secret `json:"clientSecret"`
	// RedirectURL is the callback Studio is returned to.
	RedirectURL string `json:"redirectURL"`
}

// DeviceAuth is how devices authenticate. DPoP and attestation arrive in
// P6; in P2 a device token is a short-lived bearer token.
type DeviceAuth struct {
	AccessTokenTTL  Duration `json:"accessTokenTTL"`
	RefreshTokenTTL Duration `json:"refreshTokenTTL"`
}

// CIAuth federates workload identity, so pipelines need no long-lived
// secret (SRV-064).
type CIAuth struct {
	// Issuers are the providers whose identity tokens may be exchanged.
	Issuers []CIIssuer `json:"issuers"`
	// TokenTTL is how long an exchanged token lasts.
	TokenTTL Duration `json:"tokenTTL"`
}

// CIIssuer is one trusted workload-identity provider.
type CIIssuer struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	// SubjectPattern restricts which workloads may exchange a token; it
	// is a glob such as "repo:acme/*:ref:refs/heads/main".
	SubjectPattern string `json:"subjectPattern"`
}

// Observability is logging, metrics and tracing (OBS-001–OBS-003).
type Observability struct {
	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `json:"logLevel"`
	// LogFormat is "json" for deployments or "text" for a terminal.
	LogFormat string `json:"logFormat"`
	// MetricsListen is the address the Prometheus endpoint serves on;
	// empty serves /metrics on the main listener.
	MetricsListen string `json:"metricsListen"`
	// OTLPEndpoint receives traces; empty disables exporting.
	OTLPEndpoint string `json:"otlpEndpoint"`
	// TraceSampleRatio is the fraction of traces sampled, 0 to 1.
	TraceSampleRatio float64 `json:"traceSampleRatio"`
}

// Telemetry is where the runtime's events are stored.
type Telemetry struct {
	// Store is "postgres"; "clickhouse" arrives with ANL-010 in P9.
	Store string `json:"store"`
}

// Retention is how long history is kept.
type Retention struct {
	AuditYears             int `json:"auditYears"`
	DevelopmentReleaseDays int `json:"developmentReleaseDays"`
	SnapshotDays           int `json:"snapshotDays"`
	TrashDays              int `json:"trashDays"`
}

// Has reports whether the process runs a role.
func (c *Config) Has(r Role) bool { return slices.Contains(c.Server.Roles, r) }

// String renders the configuration without its secrets, so that it can be
// logged at start-up (OBS-003).
func (c *Config) String() string {
	var b strings.Builder
	roles := make([]string, len(c.Server.Roles))
	for i, r := range c.Server.Roles {
		roles[i] = string(r)
	}
	fmt.Fprintf(&b, "roles=%s listen=%s storage=%s cache=%s signing=%s",
		strings.Join(roles, ","), c.Server.Listen, c.ObjectStorage.Backend, c.Cache.Backend, c.Signing.Backend)
	return b.String()
}
