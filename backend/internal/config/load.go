// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Defaults returns the configuration of a single-node installation with
// nothing set. It is the starting point of Load, so an absent key keeps
// its documented default rather than a zero value.
func Defaults() Config {
	return Config{
		Server: Server{
			Roles:         []Role{RoleAPI, RoleWorker},
			Listen:        ":8080",
			ShutdownGrace: Duration(30e9),
			// Two years, with subdomains: the HSTS preload minimum plus
			// a margin (SEC-040). Preload itself is a deliberate opt-in.
			HSTS: HSTS{MaxAge: Duration(17520 * 3600e9), IncludeSubDomains: true},
		},
		Database: Database{MaxConnections: 50, MigrateOnStart: true},
		ObjectStorage: ObjectStorage{
			Backend:      "filesystem",
			Directory:    "data/objects",
			SignedURLTTL: Duration(15 * 60e9),
		},
		Cache:   Cache{Backend: "memory"},
		Signing: Signing{Backend: "file", Directory: "data/keys", Keys: SigningKeys{Targets: "targets", Audit: "audit"}},
		Auth: Auth{
			Studio: StudioAuth{
				AllowPasswordLogin: true,
				MFARequiredFor:     []string{"publish", "approve", "keys", "members"},
				SessionTTL:         Duration(12 * 3600e9),
			},
			Device: DeviceAuth{AccessTokenTTL: Duration(5 * 60e9), RefreshTokenTTL: Duration(30 * 24 * 3600e9)},
			CI:     CIAuth{TokenTTL: Duration(60 * 60e9)},
		},
		Observability: Observability{LogLevel: "info", LogFormat: "json", TraceSampleRatio: 1},
		Telemetry:     Telemetry{Store: "postgres"},
		// Where the server image installs them (backend/Dockerfile).
		Assets: Assets{SVGCompiler: "/usr/local/bin/plux-svgc", PathOps: "/usr/local/lib/plux/libpath_ops.so"},
		Audit:  Audit{CheckpointInterval: Duration(3600e9)},
		Retention: Retention{
			AuditYears:             10,
			DevelopmentReleaseDays: 90,
			SnapshotDays:           90,
			TrashDays:              30,
		},
	}
}

// knownSections are the top-level keys this phase accepts.
var knownSections = []string{
	"server", "database", "objectStorage", "cache", "signing", "auth",
	"observability", "telemetry", "limits", "retention", "assets", "attestation", "audit",
}

// futureSections are sections of Appendix H that belong to a later phase.
// They are refused by name, so an operator who copies the full reference
// is told when the section starts working instead of being told it does
// not exist.
var futureSections = map[string]string{
	"functions": "P7",
	"ai":        "P12",
	"payments":  "P13",
}

// Load reads the configuration file at path, applies the environment
// overrides of Environ and validates the result (SRV-008).
func Load(path string, environ func(string) (string, bool)) (*Config, error) {
	// The path comes from the operator's own command line or container
	// configuration, which is the point of the flag.
	data, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	return Parse(data, environ)
}

// Parse reads configuration from YAML bytes. It is what Load and
// `plux-server config validate` share, and what the tests use.
func Parse(data []byte, environ func(string) (string, bool)) (*Config, error) {
	if err := checkSections(data); err != nil {
		return nil, err
	}
	c := Defaults()
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("configuration is not valid: %w", err)
	}
	if environ != nil {
		if err := applyEnv(&c, environ); err != nil {
			return nil, err
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// checkSections reports an unknown or later-phase top-level section
// before the strict decoder reports it less helpfully.
func checkSections(data []byte) error {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("configuration is not valid YAML: %w", err)
	}
	names := make([]string, 0, len(top))
	for k := range top {
		names = append(names, k)
	}
	sort.Strings(names)
	var errs []error
	for _, k := range names {
		if slices.Contains(knownSections, k) {
			continue
		}
		if phase, ok := futureSections[k]; ok {
			errs = append(errs, fmt.Errorf("section %q arrives in %s and is not configurable yet", k, phase))
			continue
		}
		errs = append(errs, fmt.Errorf("unknown section %q; known sections are %s", k, strings.Join(knownSections, ", ")))
	}
	return errors.Join(errs...)
}
