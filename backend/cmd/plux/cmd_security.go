// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

const securityUsage = "Usage: plux security config show|set|diff [flags]\n"

// security runs plux security config show|set|diff (SEC-180, SEC-182).
func (e env) security(args []string) int {
	if len(args) < 2 || args[0] != "config" {
		_, _ = fmt.Fprint(e.stderr, securityUsage)
		return exitUsage
	}
	switch args[1] {
	case "show":
		return e.securityShow(args[2:])
	case "set":
		return e.securitySet(args[2:])
	case "diff":
		return e.securityDiff(args[2:])
	}
	_, _ = fmt.Fprint(e.stderr, securityUsage)
	return exitUsage
}

// effectiveConfig is an environment's security configuration as the
// server reports it.
type effectiveConfig struct {
	Profile   string
	Version   int64
	SHA256    string
	Effective map[string]any
}

// securityTarget is the app and environment a security command acts on,
// with the client of the service.
type securityTarget struct {
	c      common
	app    string
	envID  string
	client pluxv1connect.SecurityAdminServiceClient
}

// securityConnect resolves the common flags and the environment key.
func (e env) securityConnect(c common, envKey string) (securityTarget, error) {
	if err := c.resolve(); err != nil {
		return securityTarget{}, err
	}
	if err := needApp(c); err != nil {
		return securityTarget{}, err
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return securityTarget{}, err
	}
	id, err := environmentID(context.Background(), cl, c.app, firstOf(envKey, projectEnvironment(c.dir), "development"))
	if err != nil {
		return securityTarget{}, err
	}
	client := pluxv1connect.NewSecurityAdminServiceClient(e.httpClient, c.server, connect.WithInterceptors(headers{token: cl.token, org: c.org}))
	return securityTarget{c: c, app: c.app, envID: id, client: client}, nil
}

// fetch reads the effective configuration.
func (t securityTarget) fetch(ctx context.Context) (effectiveConfig, error) {
	res, err := t.client.GetEffectiveSecurityConfig(ctx, connect.NewRequest(&pluxv1.GetEffectiveSecurityConfigRequest{
		AppId: t.app, EnvironmentId: t.envID,
	}))
	if err != nil {
		return effectiveConfig{}, err //nolint:wrapcheck // reported as is
	}
	m := res.Msg
	out := effectiveConfig{Profile: m.GetProfile(), Version: m.GetVersion(), SHA256: hex.EncodeToString(m.GetSha256())}
	if err := json.Unmarshal(m.GetEffectiveJson(), &out.Effective); err != nil {
		return effectiveConfig{}, fmt.Errorf("read the effective configuration: %w", err)
	}
	return out, nil
}

// securityShow prints the effective configuration.
func (e env) securityShow(args []string) int {
	var c common
	set := flag.NewFlagSet("security config show", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "", "the environment `key` (default plux.json, then development)")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux security config show [--env key] [-C dir] [--app id] [--json]\n\nShows the profile, the version and the value of every security setting in force.", 0); !ok {
		return code
	}
	t, err := e.securityConnect(c, *envKey)
	if err != nil {
		return e.fail("security config show", err)
	}
	cfg, err := t.fetch(context.Background())
	if err != nil {
		return e.fail("security config show", err)
	}
	return e.emit(c.json, cfg.view(), cfg.showText())
}

// view is the JSON form of a configuration.
func (c effectiveConfig) view() map[string]any {
	return map[string]any{"profile": c.Profile, "version": c.Version, "sha256": c.SHA256, "effective": c.Effective}
}

// showText renders every setting with its value and whether it comes from
// the profile or from an override.
func (c effectiveConfig) showText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "profile %s, version %d, device configuration sha256 %s\n\n", c.Profile, c.Version, c.SHA256)
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE")
	for _, s := range settings.All() {
		value, ok := c.Effective[string(s.Key)]
		if !ok {
			continue
		}
		source := "profile"
		if preset, differs := c.differs(s); differs {
			source = "override (profile: " + preset + ")"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, renderValue(value), source)
	}
	_ = w.Flush()
	return b.String()
}

// differs reports whether the effective value of a setting is not the
// preset of the profile, and gives the preset.
func (c effectiveConfig) differs(s settings.Setting) (string, bool) {
	preset, ok := s.Defaults.For(settings.Profile(c.Profile))
	if !ok {
		return "", false
	}
	want := presetJSON(s, preset)
	return renderValue(want), renderValue(c.Effective[string(s.Key)]) != renderValue(want)
}

// presetJSON is the JSON value of a preset, as the server writes it.
func presetJSON(s settings.Setting, v settings.Value) any {
	switch s.Type {
	case settings.TypeBool:
		return v.Bool()
	case settings.TypeSeconds, settings.TypeCount:
		return float64(v.Int())
	default:
		return v.Text()
	}
}

// renderValue prints a setting's value.
func renderValue(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// securityDiff prints the settings that differ from the profile's preset.
func (e env) securityDiff(args []string) int {
	var c common
	set := flag.NewFlagSet("security config diff", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "", "the environment `key` (default plux.json, then development)")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux security config diff [--env key] [-C dir] [--app id] [--json]\n\nShows the overrides: the settings whose value differs from the profile's preset.", 0); !ok {
		return code
	}
	t, err := e.securityConnect(c, *envKey)
	if err != nil {
		return e.fail("security config diff", err)
	}
	cfg, err := t.fetch(context.Background())
	if err != nil {
		return e.fail("security config diff", err)
	}
	var lines []map[string]any
	var text strings.Builder
	fmt.Fprintf(&text, "profile %s, version %d\n", cfg.Profile, cfg.Version)
	for _, s := range settings.All() {
		if preset, differs := cfg.differs(s); differs {
			value := renderValue(cfg.Effective[string(s.Key)])
			lines = append(lines, map[string]any{"setting": string(s.Key), "profile": preset, "value": value})
			fmt.Fprintf(&text, "%s: %s -> %s\n", s.Key, preset, value)
		}
	}
	if len(lines) == 0 {
		text.WriteString("no overrides: every setting has the profile's value\n")
	}
	return e.emit(c.json, map[string]any{"profile": cfg.Profile, "version": cfg.Version, "overrides": lines}, text.String())
}

// settingFlags collects repeated --set key=value flags.
type settingFlags []string

func (s *settingFlags) String() string     { return strings.Join(*s, ",") }
func (s *settingFlags) Set(v string) error { *s = append(*s, v); return nil }

// securitySet sets the profile and the overrides of an environment.
func (e env) securitySet(args []string) int {
	var c common
	set := flag.NewFlagSet("security config set", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "", "the environment `key` (default plux.json, then development)")
	profile := set.String("profile", "", "the security `profile`: standard, strict or maximum")
	file := set.String("file", "", "a JSON `file` of overrides, an object of setting keys and values")
	expected := set.Int64("expected-version", -1, "the `version` you read with show; 0 when none was set yet")
	var sets settingFlags
	set.Var(&sets, "set", "an override `key=value`; repeatable, and wins over --file")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux security config set --profile name --expected-version n [--set key=value]... [--file overrides.json] [--env key] [-C dir] [--json]\n\nReplaces the profile and the overrides. Overrides may only tighten the profile's preset.", 0); !ok {
		return code
	}
	overrides, err := buildOverrides(*file, sets)
	if err == nil && *profile == "" {
		err = usageError("no --profile")
	}
	if err == nil && *expected < 0 {
		err = usageError("no --expected-version: give the version `plux security config show` prints, 0 for none")
	}
	if err != nil {
		return e.fail("security config set", err)
	}
	t, err := e.securityConnect(c, *envKey)
	if err != nil {
		return e.fail("security config set", err)
	}
	res, err := t.client.SetSecurityConfig(context.Background(), connect.NewRequest(&pluxv1.SetSecurityConfigRequest{
		AppId: t.app, EnvironmentId: t.envID, Profile: *profile, OverridesJson: overrides, ExpectedVersion: *expected,
	}))
	if err != nil {
		return e.fail("security config set", err)
	}
	return e.emit(c.json, map[string]any{"version": res.Msg.GetVersion()},
		fmt.Sprintf("security configuration is now at version %d\n", res.Msg.GetVersion()))
}

// buildOverrides reads the overrides of a file, then those given by --set.
func buildOverrides(file string, sets []string) ([]byte, error) {
	out := map[string]any{}
	if file != "" {
		data, err := os.ReadFile(file) //nolint:gosec // the developer's own file
		if err != nil {
			return nil, fmt.Errorf("read the overrides: %w", err)
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, usageError("the overrides file is not a JSON object: " + err.Error())
		}
	}
	for _, kv := range sets {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, usageError(fmt.Sprintf("--set %q is not key=value", kv))
		}
		v, err := typedValue(key, value)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode the overrides: %w", err)
	}
	return raw, nil
}

// typedValue reads a --set value as its setting's type.
func typedValue(key, value string) (any, error) {
	i := slices.IndexFunc(settings.All(), func(s settings.Setting) bool { return string(s.Key) == key })
	if i < 0 {
		return nil, usageError(fmt.Sprintf("%q is not a security setting; `plux security config show` lists them", key))
	}
	switch settings.All()[i].Type {
	case settings.TypeBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, usageError(fmt.Sprintf("%s is true or false, not %q", key, value))
		}
		return b, nil
	case settings.TypeSeconds, settings.TypeCount:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, usageError(fmt.Sprintf("%s is a whole number, not %q", key, value))
		}
		return n, nil
	default:
		return value, nil
	}
}
