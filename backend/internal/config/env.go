// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"strings"
)

// override is one environment variable that sets one configuration value.
type override struct {
	// name is the variable, always prefixed PLUX_.
	name string
	// set applies the value, or reports why it cannot be used.
	set func(c *Config, v string) error
}

// overrides are the values that may come from the environment rather than
// from the file (SRV-008). They are deliberately few: everything a
// deployment must not write into a file — credentials — plus the handful
// of values a container image is told at start-up.
var overrides = []override{
	{"PLUX_SERVER_ROLES", func(c *Config, v string) error {
		var roles []Role
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			roles = append(roles, Role(part))
		}
		if len(roles) == 0 {
			return errors.New("must name at least one role")
		}
		c.Server.Roles = roles
		return nil
	}},
	{"PLUX_SERVER_LISTEN", func(c *Config, v string) error { c.Server.Listen = v; return nil }},
	{"PLUX_SERVER_PUBLIC_BASE_URL", func(c *Config, v string) error { c.Server.PublicBaseURL = v; return nil }},
	{"PLUX_DATABASE_URL", func(c *Config, v string) error { c.Database.URL = Secret(v); return nil }},
	{"PLUX_OBJECT_STORAGE_ENDPOINT", func(c *Config, v string) error { c.ObjectStorage.Endpoint = v; return nil }},
	{"PLUX_OBJECT_STORAGE_BUCKET", func(c *Config, v string) error { c.ObjectStorage.Bucket = v; return nil }},
	{"PLUX_OBJECT_STORAGE_ACCESS_KEY_ID", func(c *Config, v string) error { c.ObjectStorage.AccessKeyID = v; return nil }},
	{"PLUX_OBJECT_STORAGE_SECRET_ACCESS_KEY", func(c *Config, v string) error {
		c.ObjectStorage.SecretAccessKey = Secret(v)
		return nil
	}},
	{"PLUX_CACHE_VALKEY_URL", func(c *Config, v string) error { c.Cache.ValkeyURL = Secret(v); return nil }},
	{"PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET", func(c *Config, v string) error { c.Auth.Studio.OIDC.ClientSecret = Secret(v); return nil }},
	{"PLUX_SIGNING_VAULT_TOKEN", func(c *Config, v string) error { c.Signing.Vault.Token = Secret(v); return nil }},
	{"PLUX_OBSERVABILITY_LOG_LEVEL", func(c *Config, v string) error { c.Observability.LogLevel = v; return nil }},
	{"PLUX_OBSERVABILITY_OTLP_ENDPOINT", func(c *Config, v string) error { c.Observability.OTLPEndpoint = v; return nil }},
}

// EnvironNames returns the environment variables that override a
// configuration value, in the order they are documented.
func EnvironNames() []string {
	names := make([]string, len(overrides))
	for i, o := range overrides {
		names[i] = o.name
	}
	return names
}

// applyEnv applies every override that is set. An empty value is treated
// as unset, so an empty variable in a container cannot blank a setting.
func applyEnv(c *Config, environ func(string) (string, bool)) error {
	var errs []error
	for _, o := range overrides {
		v, ok := environ(o.name)
		if !ok || v == "" {
			continue
		}
		if err := o.set(c, v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.name, err))
		}
	}
	return errors.Join(errs...)
}
