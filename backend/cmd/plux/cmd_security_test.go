// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// fakeAppEnvs lists the environments of an app.
type fakeAppEnvs struct {
	pluxv1connect.UnimplementedAppServiceHandler
}

func (fakeAppEnvs) ListEnvironments(context.Context, *connect.Request[pluxv1.ListEnvironmentsRequest]) (*connect.Response[pluxv1.ListEnvironmentsResponse], error) {
	return connect.NewResponse(&pluxv1.ListEnvironmentsResponse{Environments: []*pluxv1.Environment{
		{Id: "e-dev", Key: "development"}, {Id: "e-prod", Key: "production"},
	}}), nil
}

// fakeSecurity is SecurityAdminService with one environment, strict with
// one override, at version 3.
type fakeSecurity struct {
	pluxv1connect.UnimplementedSecurityAdminServiceHandler
	mu  sync.Mutex
	set *pluxv1.SetSecurityConfigRequest
}

func (*fakeSecurity) GetEffectiveSecurityConfig(_ context.Context, r *connect.Request[pluxv1.GetEffectiveSecurityConfigRequest]) (*connect.Response[pluxv1.GetEffectiveSecurityConfigResponse], error) {
	if r.Msg.GetEnvironmentId() != "e-prod" || r.Msg.GetAppId() != "a1" {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	all := map[string]any{}
	for _, s := range settings.All() {
		v, _ := s.Defaults.For(settings.Strict)
		switch s.Type {
		case settings.TypeBool:
			all[string(s.Key)] = v.Bool()
		case settings.TypeSeconds, settings.TypeCount:
			all[string(s.Key)] = v.Int()
		default:
			all[string(s.Key)] = v.Text()
		}
	}
	all["inactivityLockTimeout"] = 60
	raw, _ := json.Marshal(all)
	sum := make([]byte, 32)
	sum[0], sum[31] = 0xab, 0xcd
	return connect.NewResponse(&pluxv1.GetEffectiveSecurityConfigResponse{Profile: "strict", Version: 3, EffectiveJson: raw, Sha256: sum}), nil
}

func (f *fakeSecurity) SetSecurityConfig(_ context.Context, r *connect.Request[pluxv1.SetSecurityConfigRequest]) (*connect.Response[pluxv1.SetSecurityConfigResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set = r.Msg
	if r.Msg.GetExpectedVersion() != 3 {
		return nil, connect.NewError(connect.CodeAborted, nil)
	}
	return connect.NewResponse(&pluxv1.SetSecurityConfigResponse{Version: 4}), nil
}

// Verifies: SEC-180, SEC-182.
// plux security config show, set and diff against a fake server, with the
// output pinned.
func TestSecurityConfig(t *testing.T) { //nolint:paralleltest // the environment is process-wide
	t.Setenv(tokenEnv, "plux_pat_sec")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	f := &fakeSecurity{}
	mux := http.NewServeMux()
	mux.Handle(pluxv1connect.NewAppServiceHandler(fakeAppEnvs{}))
	mux.Handle(pluxv1connect.NewSecurityAdminServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base := []string{"--server", srv.URL, "--app", "a1", "--env", "production"}
	run := func(sub string, extra ...string) (int, string, string) {
		args := append([]string{"security", "config", sub}, base...)
		return cli(t, t.TempDir(), append(args, extra...)...)
	}

	code, out, stderr := run("show")
	golden := strings.TrimPrefix(`
profile strict, version 3, device configuration sha256 ab000000000000000000000000000000000000000000000000000000000000cd

SETTING                          VALUE                   SOURCE
accessTokenLifetime              300                     profile
allowDirectDataSources           true                    profile
allowSoftwareKeys                false                   profile
androidDeviceVerdictAL2          MEETS_DEVICE_INTEGRITY  profile
androidRefreshRequiresIntegrity  false                   profile
attestationOutageGrace           3600                    profile
confidentialBundles              optional                profile
defaultFunctionAssurance         AL2                     profile
dpopIatWindow                    60                      profile
dpopIatWindowFallback            15                      profile
dpopNonceRotation                300                     profile
encryptLocalStores               true                    profile
inactivityLock                   true                    profile
inactivityLockTimeout            60                      override (profile: 300)
minAssuranceForSync              AL1                     profile
raspRootHookingResponse          degrade                 profile
reattestationInterval            86400                   profile
registrationChallengeTtl         300                     profile
replayCacheFallback              memory                  profile
riskMetricMaxAL3                 3                       profile
screenshotBlockingDefault        false                   profile
tls12Allowed                     false                   profile
`, "\n")
	if code != exitOK || out != golden {
		t.Errorf("show: %d %s\n%s", code, stderr, out)
	}
	if code, out, _ := run("show", "--json"); code != exitOK || !strings.Contains(out, `"version": 3`) || !strings.Contains(out, `"inactivityLockTimeout": 60`) {
		t.Errorf("show --json: %d %s", code, out)
	}

	const wantDiff = "profile strict, version 3\ninactivityLockTimeout: 300 -> 60\n"
	if code, out, stderr := run("diff"); code != exitOK || out != wantDiff {
		t.Errorf("diff: %d %q %s", code, out, stderr)
	}
	if code, out, _ := run("diff", "--json"); code != exitOK || !strings.Contains(out, `"setting": "inactivityLockTimeout"`) {
		t.Errorf("diff --json: %d %s", code, out)
	}

	file := filepath.Join(t.TempDir(), "overrides.json")
	putFile(t, file, `{"dpopIatWindow": 30, "inactivityLockTimeout": 600}`)
	code, out, stderr = run("set", "--profile", "strict", "--expected-version", "3", "--file", file,
		"--set", "inactivityLockTimeout=60", "--set", "allowSoftwareKeys=false", "--set", "raspRootHookingResponse=block")
	if code != exitOK || out != "security configuration is now at version 4\n" {
		t.Fatalf("set: %d %q %s", code, out, stderr)
	}
	var sent map[string]any
	if err := json.Unmarshal(f.set.GetOverridesJson(), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 4 || sent["dpopIatWindow"] != float64(30) || sent["inactivityLockTimeout"] != float64(60) ||
		sent["allowSoftwareKeys"] != false || sent["raspRootHookingResponse"] != "block" ||
		f.set.GetProfile() != "strict" || f.set.GetExpectedVersion() != 3 || f.set.GetEnvironmentId() != "e-prod" {
		t.Errorf("the request: %+v", f.set)
	}

	failures := []struct {
		name string
		args []string
		code int
	}{
		{"no profile", []string{"set", "--expected-version", "3"}, exitUsage},
		{"no expected version", []string{"set", "--profile", "strict"}, exitUsage},
		{"an unknown setting", []string{"set", "--profile", "strict", "--expected-version", "3", "--set", "nope=1"}, exitUsage},
		{"a value of the wrong type", []string{"set", "--profile", "strict", "--expected-version", "3", "--set", "dpopIatWindow=soon"}, exitUsage},
		{"not key=value", []string{"set", "--profile", "strict", "--expected-version", "3", "--set", "dpopIatWindow"}, exitUsage},
		{"a stale version", []string{"set", "--profile", "strict", "--expected-version", "2"}, exitFailed},
	}
	for _, tt := range failures {
		if code, _, _ := run(tt.args[0], tt.args[1:]...); code != tt.code {
			t.Errorf("%s: exit %d, want %d", tt.name, code, tt.code)
		}
	}
	if code, _, _ := cli(t, t.TempDir(), "security", "config", "nope"); code != exitUsage {
		t.Errorf("an unknown subcommand: %d", code)
	}
	if code, _, _ := cli(t, t.TempDir(), "security"); code != exitUsage {
		t.Errorf("no subcommand: %d", code)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}
