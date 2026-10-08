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
	Attestation   Attestation       `json:"attestation"`
	Audit         Audit             `json:"audit"`
	// UpdateMetadata sets the expiries and the root threshold of the
	// update metadata (SEC-050, ADR-0054).
	UpdateMetadata UpdateMetadata `json:"updateMetadata"`
}

// Audit configures the audit log's signed checkpoints (SEC-141).
type Audit struct {
	// CheckpointInterval is how often the worker signs a checkpoint over
	// each organisation's chain, for those with new entries. It bounds
	// how many entries could be removed from the end of a chain before a
	// signed checkpoint betrays it.
	CheckpointInterval Duration `json:"checkpointInterval"`
}

// UpdateMetadata configures the four roles of the update metadata
// (SEC-050, Appendix H.1).
type UpdateMetadata struct {
	// RootThreshold is the least number of offline root keys that must
	// sign a root document, 2 of 3 by default; a root that asks for fewer
	// is refused on upload.
	RootThreshold int `json:"rootThreshold"`
	// Expiry is how long each role's metadata stays valid.
	Expiry MetadataExpiry `json:"expiry"`
}

// MetadataExpiry is the lifetime of each role's metadata. The worker
// signs a fresh timestamp before its expiry, a fresh snapshot when a
// quarter of its lifetime remains, and fresh manifests (the targets
// role) likewise; a root is signed offline.
type MetadataExpiry struct {
	Timestamp Duration `json:"timestamp"`
	Snapshot  Duration `json:"snapshot"`
	Targets   Duration `json:"targets"`
	Root      Duration `json:"root"`
}

// Attestation says what the server trusts about the builds of each app
// (SEC-003). It is the interim source of that trust: the remote security
// configuration replaces it, and an app it does not list has no
// attestation configured, so Android and iOS evidence is refused as
// unavailable while development evidence still works outside production
// (SEC-008).
type Attestation struct {
	// Apps maps an app identifier (a UUID) to its builds.
	Apps map[string]AppAttestation `json:"apps"`
}

// AppAttestation is what the server trusts about one app's builds.
type AppAttestation struct {
	// AndroidPackages are the application identifiers the app ships as.
	AndroidPackages []string `json:"androidPackages"`
	// AndroidCertDigests are the hex SHA-256 digests of the app's signing
	// certificates; empty skips the certificate check.
	AndroidCertDigests []string `json:"androidCertDigests"`
	// PlayIntegrityDecryptionKey and PlayIntegrityVerificationKey are the
	// app's Play Console keys, in standard base64; both or neither.
	PlayIntegrityDecryptionKey   Secret `json:"playIntegrityDecryptionKey"`
	PlayIntegrityVerificationKey Secret `json:"playIntegrityVerificationKey"`
	// IOSAppID is the team and bundle identifier, "TEAMID.com.example.app";
	// empty means App Attest is not configured.
	IOSAppID string `json:"iosAppID"`
	// AppAttestProduction selects App Attest's production environment
	// rather than its sandbox.
	AppAttestProduction bool `json:"appAttestProduction"`
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
	// Production declares a production installation. It makes validation
	// refuse an unprotected transport to PostgreSQL, to Valkey and from
	// clients (SEC-040, SEC-041).
	Production bool `json:"production"`
	// BehindTLSProxy states that a proxy in front of the api role
	// terminates TLS 1.3 for it. A production installation without
	// server.tls must set it explicitly.
	BehindTLSProxy bool `json:"behindTLSProxy"`
	// TLS makes the api role terminate TLS itself; absent, it speaks
	// plain HTTP behind a terminating proxy.
	TLS TLS `json:"tls"`
	// HSTS configures the Strict-Transport-Security header.
	HSTS HSTS `json:"hsts"`
	// InternalListen is the address of the listener for the traffic
	// between roles; empty serves none. It requires InternalTLS.
	InternalListen string `json:"internalListen"`
	// InternalTLS is the identity of this role on the internal network:
	// the CA every role's certificate is signed by, and this role's own
	// certificate and key (SEC-043).
	InternalTLS InternalTLS `json:"internalTLS"`
}

// TLS is the certificate the api role terminates TLS with (SEC-040).
type TLS struct {
	// CertFile and KeyFile are PEM files; both or neither.
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// AllowTLS12 accepts TLS 1.2 with AEAD ECDHE suites besides TLS 1.3.
	// It is the installation default of the tls12Allowed setting, which
	// is false, and every start with it set logs a warning.
	AllowTLS12 bool `json:"allowTLS12"`
}

// Enabled reports whether the api role terminates TLS itself.
func (t TLS) Enabled() bool { return t.CertFile != "" || t.KeyFile != "" }

// HSTS configures Strict-Transport-Security (SEC-040).
type HSTS struct {
	// MaxAge is how long a browser remembers to use HTTPS only; zero
	// disables the header, with a warning.
	MaxAge Duration `json:"maxAge"`
	// IncludeSubDomains extends the policy to every subdomain.
	IncludeSubDomains bool `json:"includeSubDomains"`
	// Preload consents to browser preload lists, which are hard to leave;
	// it needs IncludeSubDomains and a MaxAge of at least a year.
	Preload bool `json:"preload"`
}

// InternalTLS is a role's identity for mutual TLS between roles
// (SEC-043).
type InternalTLS struct {
	// CAFile signs every role's certificate; CertFile and KeyFile are
	// this role's own. All three or none.
	CAFile   string `json:"caFile"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

// Enabled reports whether an internal identity is configured.
func (t InternalTLS) Enabled() bool {
	return t.CAFile != "" || t.CertFile != "" || t.KeyFile != ""
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
	// Keys names the online keys of the update-metadata roles; the root
	// role is signed offline and has none here (SEC-050).
	Keys SigningKeys `json:"keys"`
	// Directory is the root of the file backend's keys.
	Directory string `json:"directory"`
	// Vault configures the HashiCorp Vault Transit backend.
	Vault Vault `json:"vault"`
	// PKCS11 configures the PKCS#11 backend.
	PKCS11 PKCS11 `json:"pkcs11"`
}

// SigningKeys names the keys of each update-metadata role.
type SigningKeys struct {
	// Targets is the prefix of every environment's targets key, which
	// signs bundle hashes and manifests (SRV-052, REL-031). Each
	// environment has its own key, "<prefix>-<environment ID>"
	// (ADR-0004, GOV-010).
	Targets string `json:"targets"`
	// Audit is the key that signs the audit log's checkpoints (SEC-141).
	// One key serves the installation: the checkpoints name it, so it can
	// be rotated without invalidating the old ones.
	Audit string `json:"audit"`
	// Snapshot and Timestamp are the prefixes of the snapshot and
	// timestamp keys, named like the targets key. Every environment has
	// its own key per role, so a development key never signs for
	// production (SEC-050, SEC-056).
	Snapshot  string `json:"snapshot"`
	Timestamp string `json:"timestamp"`
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

// PKCS11 is a hardware security module reached through the PKCS#11 helper
// process (SEC-120, ADR-0060). The PIN and the vendor module belong to the
// helper, never to the server's configuration.
type PKCS11 struct {
	// Socket is the absolute path of the helper's Unix socket.
	Socket string `json:"socket"`
	// WrapKey is the label of the AES key on the token that wraps the data
	// keys of stored secrets (SEC-106); "" is "plux-secrets".
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
