// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pieces a configuration is built from in these tests. YAML refuses a
// repeated top-level key, so a case that changes a section writes that
// whole section rather than appending a second one.
const (
	db      = "database:\n  url: \"postgres://plux@db:5432/plux?sslmode=verify-full\"\n"
	minimal = "server:\n  publicBaseURL: \"https://plux.example\"\n" + db
)

// withServer returns a configuration whose server section has extra
// indented lines.
func withServer(extra string) string {
	return "server:\n  publicBaseURL: \"https://plux.example\"\n" + extra + db
}

// parse reads src and fails the test when it does not validate.
func parse(t *testing.T, src string) *Config {
	t.Helper()
	c, err := Parse([]byte(src), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

// refuse reads src and returns the error message, failing the test when
// the configuration is accepted.
func refuse(t *testing.T, src string) string {
	t.Helper()
	c, err := Parse([]byte(src), nil)
	if err == nil {
		t.Fatalf("accepted: %s", c)
	}
	return err.Error()
}

// mentions runs every case and checks that the refusal names the problem.
func mentions(t *testing.T, cases []struct{ name, src, want string }) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := refuse(t, tc.src); !strings.Contains(got, tc.want) {
				t.Errorf("error %q does not mention %q", got, tc.want)
			}
		})
	}
}

// Verifies: SRV-008.
func TestParseAppliesDefaults(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal)
	if !c.Has(RoleAPI) || !c.Has(RoleWorker) || c.Has(RoleFnRunner) {
		t.Errorf("roles %v", c.Server.Roles)
	}
	if c.Server.Listen != ":8080" {
		t.Errorf("listen %q", c.Server.Listen)
	}
	if c.ObjectStorage.Backend != "filesystem" || c.Cache.Backend != "memory" {
		t.Errorf("backends %q %q", c.ObjectStorage.Backend, c.Cache.Backend)
	}
	if got := c.Server.ShutdownGrace.Duration().String(); got != "30s" {
		t.Errorf("shutdownGrace %s", got)
	}
}

// Verifies: SEC-141.
// The audit checkpoint key and interval default to "audit" and an hour,
// and the interval can be set.
func TestAuditCheckpointSettings(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal)
	if c.Signing.Keys.Audit != "audit" || c.Audit.CheckpointInterval.Duration().String() != "1h0m0s" {
		t.Errorf("defaults = %q, %s", c.Signing.Keys.Audit, c.Audit.CheckpointInterval)
	}
	c = parse(t, minimal+"audit:\n  checkpointInterval: 15m\nsigning:\n  keys:\n    audit: audit-2\n")
	if c.Signing.Keys.Audit != "audit-2" || c.Audit.CheckpointInterval.Duration().String() != "15m0s" {
		t.Errorf("configured = %q, %s", c.Signing.Keys.Audit, c.Audit.CheckpointInterval)
	}
}

// Verifies: SRV-008.
func TestParseRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	mentions(t, []struct{ name, src, want string }{
		{"unknown section", minimal + "nonsense: {}\n", `unknown section "nonsense"`},
		{"later phase", minimal + "functions:\n  defaults: {}\n", `"functions" arrives in P7`},
		{"unknown key", withServer("  nonsense: 1\n"), "nonsense"},
		{"not yaml", "server: [", "not valid YAML"},
		{"repeated section", minimal + db, "already set"},
	})
}

// Verifies: SRV-008.
func TestValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()
	mentions(t, []struct{ name, src, want string }{
		{"no database", "server:\n  publicBaseURL: \"https://p.example\"\n", "database.url"},
		{"database not postgres", "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \"mysql://x\"\n", "must be a postgres:// URL"},
		{"no public base URL", db, "server.publicBaseURL"},
		{"relative base URL", "server:\n  publicBaseURL: \"/api\"\n" + db, "is not an absolute URL"},
		{"unknown role", withServer("  roles: [api, nonsense]\n"), "is not one of api, worker"},
		{"later-phase role", withServer("  roles: [api, worker, fnrunner]\n"), "fnrunner role arrives in P7"},
		{"repeated role", withServer("  roles: [api, api, worker]\n"), "listed twice"},
		{"no roles", withServer("  roles: []\n"), "at least one"},
		{"no listen address", withServer("  listen: \"\"\n"), "server.listen"},
		{"no shutdown grace", withServer("  shutdownGrace: 0s\n"), "greater than zero"},
		{"s3 without bucket", minimal + "objectStorage:\n  backend: s3\n  region: eu-west-1\n", "objectStorage.bucket"},
		{"s3 without endpoint or region", minimal + "objectStorage:\n  backend: s3\n  bucket: plux\n", "objectStorage.endpoint"},
		{"half a credential", minimal + "objectStorage:\n  backend: s3\n  bucket: plux\n  region: eu-west-1\n  accessKeyID: k\n", "both the access key and the secret"},
		{"unknown storage backend", minimal + "objectStorage:\n  backend: gcs\n", "not one of s3, filesystem"},
		{"no storage directory", minimal + "objectStorage:\n  directory: \"\"\n", "objectStorage.directory"},
		{"relative CDN URL", minimal + "objectStorage:\n  cdnBaseURL: \"cdn\"\n", "objectStorage.cdnBaseURL"},
		{"valkey without URL", minimal + "cache:\n  backend: valkey\n", "cache.valkeyURL"},
		{"unknown cache backend", minimal + "cache:\n  backend: memcache\n", "not one of memory, valkey"},
		{"memory cache when roles are split", withServer("  roles: [api]\n"), "single-node only"},
		{"later-phase signing backend", minimal + "signing:\n  backend: awskms\n", "arrives in P6"},
		{"unknown signing backend", minimal + "signing:\n  backend: magic\n", "signing.backend"},
		{"no signing directory", minimal + "signing:\n  directory: \"\"\n", "signing.directory"},
		{"no targets key", minimal + "signing:\n  keys:\n    targets: \"\"\n", "signing.keys.targets"},
		{"nobody can sign in", minimal + "auth:\n  studio:\n    allowPasswordLogin: false\n", "nobody can sign in"},
		{"an incomplete OIDC provider", minimal + "auth:\n  studio:\n    oidc:\n      issuer: \"https://idp.example\"\n", "PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET"},
		{"a malware scanner over http", minimal + "assets:\n  malwareScanner: \"http://clamav:3310\"\n", "assets.malwareScanner"},
		{"an OIDC issuer without https", minimal + "auth:\n  studio:\n    oidc:\n      issuer: \"http://idp.example\"\n", "auth.studio.oidc.issuer"},
		{"password login off", minimal + "auth:\n  studio:\n    allowPasswordLogin: false\n", "allowPasswordLogin"},
		{"a cookie domain", minimal + "auth:\n  studio:\n    cookieDomain: example.com\n", "__Host-"},
		{"mfa for publish dropped", minimal + "auth:\n  studio:\n    mfaRequiredFor: [approve, keys, members]\n", "\"publish\""},
		{"vault without address", minimal + "signing:\n  backend: vault\n", "signing.vault.address"},
		{"vault without token", minimal + "signing:\n  backend: vault\n  vault:\n    address: \"https://vault:8200\"\n", "PLUX_SIGNING_VAULT_TOKEN"},
		{"vault wrap key", minimal + "signing:\n  backend: vault\n  vault:\n    address: \"https://vault:8200\"\n    token: t\n    wrapKey: \"Bad Key\"\n", "signing.vault.wrapKey"},
		{"a later signing backend", minimal + "signing:\n  backend: awskms\n", "arrives in P6"},
		{"targets prefix", minimal + "signing:\n  keys:\n    targets: \"Targets\"\n", "signing.keys.targets"},
		{"unknown mfa capability", minimal + "auth:\n  studio:\n    mfaRequiredFor: [publish, nonsense]\n", "mfaRequiredFor[1]"},
		{"zero session lifetime", minimal + "auth:\n  studio:\n    sessionTTL: 0s\n", "auth.studio.sessionTTL"},
		{"ci issuer without audience", minimal + "auth:\n  ci:\n    issuers:\n      - issuer: \"https://token.actions.githubusercontent.com\"\n        subjectPattern: \"repo:acme/*\"\n", "audience"},
		{"ci issuer without a subject pattern", minimal + "auth:\n  ci:\n    issuers:\n      - issuer: \"https://token.actions.githubusercontent.com\"\n        audience: plux\n", "subjectPattern"},
		{"ci issuer without an issuer", minimal + "auth:\n  ci:\n    issuers:\n      - audience: plux\n        subjectPattern: \"repo:acme/*\"\n", "issuers[0].issuer"},
		{"unknown log level", minimal + "observability:\n  logLevel: loud\n", "observability.logLevel"},
		{"unknown log format", minimal + "observability:\n  logFormat: xml\n", "observability.logFormat"},
		{"relative otlp endpoint", minimal + "observability:\n  otlpEndpoint: \"collector\"\n", "observability.otlpEndpoint"},
		{"sample ratio out of range", minimal + "observability:\n  traceSampleRatio: 2\n", "between 0 and 1"},
		{"later-phase telemetry store", minimal + "telemetry:\n  store: clickhouse\n", "arrives in P9"},
		{"unknown telemetry store", minimal + "telemetry:\n  store: files\n", "telemetry.store"},
		{"short snapshot retention", minimal + "retention:\n  snapshotDays: 10\n", "at least 90"},
		{"short trash retention", minimal + "retention:\n  trashDays: 7\n", "at least 30"},
		{"no audit retention", minimal + "retention:\n  auditYears: 0\n", "retention.auditYears"},
		{"audit key name", minimal + "signing:\n  keys:\n    audit: \"Audit Key\"\n", "signing.keys.audit"},
		{"zero checkpoint interval", minimal + "audit:\n  checkpointInterval: 0s\n", "audit.checkpointInterval"},
		{"bad duration", withServer("  shutdownGrace: soon\n"), "invalid duration"},
		{"negative duration", withServer("  shutdownGrace: -1s\n"), "must not be negative"},
		{"an app key that is not an identifier", minimal + "attestation:\n  apps:\n    demo:\n      iosAppID: \"T.com.example\"\n", "attestation.apps[demo]"},
		{"a certificate digest that is not SHA-256", minimal + "attestation:\n  apps:\n    0190a1b2-0000-7000-8000-000000000001:\n      androidCertDigests: [\"abcd\"]\n", "androidCertDigests[0]"},
		{"half the Play Integrity keys", minimal + "attestation:\n  apps:\n    0190a1b2-0000-7000-8000-000000000001:\n      androidPackages: [com.example]\n      playIntegrityDecryptionKey: k\n", "set together"},
		{"Play Integrity without packages", minimal + "attestation:\n  apps:\n    0190a1b2-0000-7000-8000-000000000001:\n      playIntegrityDecryptionKey: k\n      playIntegrityVerificationKey: v\n", "androidPackages"},
		{"bad trusted proxy", withServer("  trustedProxies: [\"10.0.0.0/8\", \"proxy\"]\n"), "trustedProxies[1]"},
	})
}

// Verifies: SEC-003.
func TestAttestationAppsParse(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal+"attestation:\n  apps:\n    0190a1b2-0000-7000-8000-000000000001:\n      androidPackages: [com.example.app]\n      androidCertDigests: [\""+strings.Repeat("ab", 32)+"\"]\n      iosAppID: \"TEAMID.com.example.app\"\n      appAttestProduction: true\n")
	a := c.Attestation.Apps["0190a1b2-0000-7000-8000-000000000001"]
	if len(a.AndroidPackages) != 1 || a.IOSAppID != "TEAMID.com.example.app" || !a.AppAttestProduction {
		t.Errorf("apps = %+v", c.Attestation.Apps)
	}
}

// Verifies: LIM-001, LIM-002.
func TestLimitsTightenOnly(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal+"limits:\n  \"bundle.pluginSize\": \"8MiB\"\n  \"page.nodes\": \"2000\"\n")
	set, err := c.LimitSet()
	if err != nil {
		t.Fatalf("LimitSet: %v", err)
	}
	if got := set.Get("bundle.pluginSize"); got != 8<<20 {
		t.Errorf("bundle.pluginSize = %d", got)
	}
	if got := set.Get("page.nodes"); got != 2000 {
		t.Errorf("page.nodes = %d", got)
	}
	mentions(t, []struct{ name, src, want string }{
		{"unknown limit", minimal + "limits:\n  \"nonsense.key\": \"1\"\n", "not a limit of the registry"},
		{"raised", minimal + "limits:\n  \"page.nodes\": \"100000\"\n", "must be between 1 and the current"},
		{"not a number", minimal + "limits:\n  \"page.nodes\": \"many\"\n", "whole number"},
		{"bad size", minimal + "limits:\n  \"bundle.pluginSize\": \"8 potatoes\"\n", "invalid size"},
	})
}

// Verifies: SRV-008.
func TestEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"PLUX_DATABASE_URL":  "postgres://from-env/plux",
		"PLUX_SERVER_ROLES":  "api, worker",
		"PLUX_SERVER_LISTEN": "",
	}
	c, err := Parse([]byte(minimal), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Database.URL.Value() != "postgres://from-env/plux" {
		t.Errorf("database.url = %s", c.Database.URL.Value())
	}
	if c.Server.Listen != ":8080" {
		t.Errorf("an empty variable changed listen to %q", c.Server.Listen)
	}
	if len(c.Server.Roles) != 2 {
		t.Errorf("roles %v", c.Server.Roles)
	}
	bad := map[string]string{"PLUX_SERVER_ROLES": " , "}
	if _, err := Parse([]byte(minimal), func(k string) (string, bool) { v, ok := bad[k]; return v, ok }); err == nil {
		t.Error("an empty role list was accepted")
	}
	if len(EnvironNames()) != len(overrides) {
		t.Error("EnvironNames does not list every override")
	}
}

// Verifies: OBS-003, SEC-092.
func TestSecretsAreNeverPrinted(t *testing.T) {
	t.Parallel()
	c := parse(t, minimal)
	for _, s := range []string{c.String(), Secret("hunter2").String(), Secret("hunter2").GoString()} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "verify-full") {
			t.Errorf("a secret leaked into %q", s)
		}
	}
	if b, err := Secret("hunter2").MarshalJSON(); err != nil || strings.Contains(string(b), "hunter2") {
		t.Errorf("MarshalJSON = %s, %v", b, err)
	}
	if Secret("").String() != "" {
		t.Error("an empty secret should render as empty")
	}
	if Secret("hunter2").Value() != "hunter2" {
		t.Error("Value must return the secret")
	}
}

// Verifies: SRV-008.
func TestLoadReadsAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "plux-server.yaml")
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml"), nil); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestBytesAndDurationRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"512", 512},
		{"1KiB", 1024},
		{"20MiB", 20 << 20},
		{"1GiB", 1 << 30},
		{"2TiB", 2 << 40},
		{"1KB", 1000},
		{"1.5KiB", 1536},
		{" 4 MiB ", 4 << 20},
		{"3MB", 3_000_000},
		{"1GB", 1_000_000_000},
		{"1TB", 1_000_000_000_000},
		{"7B", 7},
	} {
		got, err := ParseBytes(tc.in)
		if err != nil || got.Int64() != tc.want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "-1", "potatoes", "1.5B", "-2MiB", "9223372036854775807MiB"} {
		if got, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q) = %d; want an error", in, got)
		}
	}
	for _, tc := range []struct {
		in   Bytes
		want string
	}{{0, "0B"}, {7, "7B"}, {1024, "1KiB"}, {20 << 20, "20MiB"}, {1 << 30, "1GiB"}, {1 << 40, "1TiB"}} {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("Bytes(%d).String() = %q; want %q", int64(tc.in), got, tc.want)
		}
	}
	if b, err := Bytes(1024).MarshalJSON(); err != nil || string(b) != "1024" {
		t.Errorf("Bytes.MarshalJSON = %s, %v", b, err)
	}
	var b Bytes
	if err := b.UnmarshalJSON([]byte("4096")); err != nil || b != 4096 {
		t.Errorf("Bytes from a number = %d, %v", int64(b), err)
	}
	if err := b.UnmarshalJSON([]byte("-1")); err == nil {
		t.Error("a negative number of bytes was accepted")
	}
	if err := b.UnmarshalJSON([]byte("true")); err == nil {
		t.Error("a boolean was accepted as a size")
	}
	var d Duration
	if err := d.UnmarshalJSON([]byte(`"90s"`)); err != nil || d.Duration().String() != "1m30s" {
		t.Errorf("Duration = %s, %v", d, err)
	}
	if err := d.UnmarshalJSON([]byte("5")); err == nil {
		t.Error("a bare number was accepted as a duration")
	}
	if j, err := Duration(90e9).MarshalJSON(); err != nil || string(j) != `"1m30s"` {
		t.Errorf("Duration.MarshalJSON = %s, %v", j, err)
	}
}
