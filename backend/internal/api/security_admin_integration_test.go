// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// Verifies: SEC-180, SEC-182.
// An administrator reads the effective configuration of an environment,
// changes it with the version they read, and is refused a stale version
// or a setting looser than the profile; the unfinished parts of the
// service say so.
func TestSecurityAdminService(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := t.Context()
	admin, app, envs := w.appOnly(t)
	prod := envs["production"]

	got := must(w.secAdmin.GetEffectiveSecurityConfig(ctx, req(admin, &pluxv1.GetEffectiveSecurityConfigRequest{AppId: app, EnvironmentId: prod})))(t)
	empty := sha256.Sum256([]byte(`{}`))
	if got.GetProfile() != "standard" || got.GetVersion() != 0 || string(got.GetSha256()) != string(empty[:]) {
		t.Fatalf("a new environment: %+v", got)
	}
	var effective map[string]any
	if err := json.Unmarshal(got.GetEffectiveJson(), &effective); err != nil || len(effective) != len(settings.All()) {
		t.Fatalf("effective_json = %s, %v", got.GetEffectiveJson(), err)
	}

	set := must(w.secAdmin.SetSecurityConfig(ctx, req(admin, &pluxv1.SetSecurityConfigRequest{
		AppId: app, EnvironmentId: prod, Profile: "strict", OverridesJson: []byte(`{"inactivityLockTimeout": 60, "dpopIatWindow": 20}`),
	})))(t)
	if set.GetVersion() != 1 {
		t.Fatalf("SetSecurityConfig = %+v", set)
	}
	got = must(w.secAdmin.GetEffectiveSecurityConfig(ctx, req(admin, &pluxv1.GetEffectiveSecurityConfigRequest{AppId: app, EnvironmentId: prod})))(t)
	device := sha256.Sum256([]byte(`{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}`))
	if err := json.Unmarshal(got.GetEffectiveJson(), &effective); err != nil {
		t.Fatal(err)
	}
	if got.GetProfile() != "strict" || got.GetVersion() != 1 || string(got.GetSha256()) != string(device[:]) ||
		effective["inactivityLockTimeout"] != float64(60) || effective["dpopIatWindow"] != float64(20) || effective["inactivityLock"] != true {
		t.Errorf("after the change: %+v %v", got, effective)
	}

	refusals := []struct {
		name   string
		msg    *pluxv1.SetSecurityConfigRequest
		code   connect.Code
		reason string
	}{
		{"a stale version", &pluxv1.SetSecurityConfigRequest{AppId: app, EnvironmentId: prod, Profile: "maximum", ExpectedVersion: 0}, connect.CodeAborted, "REVISION_CONFLICT"},
		{"a loosening override", &pluxv1.SetSecurityConfigRequest{AppId: app, EnvironmentId: prod, Profile: "strict", OverridesJson: []byte(`{"inactivityLock": false}`), ExpectedVersion: 1}, connect.CodeInvalidArgument, "SECURITY_CONFIG_LOOSENS_PRESET"},
		{"an out-of-bounds override", &pluxv1.SetSecurityConfigRequest{AppId: app, EnvironmentId: prod, Profile: "strict", OverridesJson: []byte(`{"dpopIatWindow": 1}`), ExpectedVersion: 1}, connect.CodeInvalidArgument, "SECURITY_CONFIG_OUT_OF_BOUNDS"},
	}
	for _, tt := range refusals {
		_, err := w.secAdmin.SetSecurityConfig(ctx, req(admin, tt.msg))
		if codeOf(err) != tt.code || reasonOf(t, err) != tt.reason {
			t.Errorf("%s: %v", tt.name, err)
		}
	}

	if _, err := w.secAdmin.GetEffectiveSecurityConfig(ctx, connect.NewRequest(&pluxv1.GetEffectiveSecurityConfigRequest{AppId: app, EnvironmentId: prod})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous read: %v", err)
	}
	// The gateway's upstreams and the audit checkpoints are other
	// milestones' work.
	if _, err := w.secAdmin.ListUpstreams(ctx, req(admin, &pluxv1.ListUpstreamsRequest{AppId: app, EnvironmentId: prod})); codeOf(err) != connect.CodeUnimplemented {
		t.Errorf("ListUpstreams: %v", err)
	}
	if _, err := w.secAdmin.ListAuditCheckpoints(ctx, req(admin, &pluxv1.ListAuditCheckpointsRequest{})); codeOf(err) != connect.CodeUnimplemented {
		t.Errorf("ListAuditCheckpoints: %v", err)
	}
}

// Verifies: SEC-182.
// The signed manifest pins the configuration version and hash, and a
// device that sends the version it holds gets nothing, or the merge patch
// to the pinned one.
func TestManifestConfigOnTheWire(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := t.Context()
	admin, app, envs := w.releasedApp(t)
	dev := w.registerAndroid(t, app, "1.0.0")
	dev.refresh(t)
	manifest := func(configVersion int64) *pluxv1.GetManifestResponse {
		t.Helper()
		return must(w.manifest.GetManifest(ctx, bound(t, dev, pluxv1connect.ManifestServiceGetManifestProcedure, &pluxv1.GetManifestRequest{ConfigVersion: configVersion})))(t)
	}

	if m := manifest(0); m.GetManifest().GetConfig() != nil || len(m.GetConfigPatch()) != 0 || m.GetConfigFullRequired() {
		t.Errorf("an environment with the defaults: %+v", m)
	}
	must(w.secAdmin.SetSecurityConfig(ctx, req(admin, &pluxv1.SetSecurityConfigRequest{
		AppId: app, EnvironmentId: envs["production"], Profile: "strict", OverridesJson: []byte(`{"inactivityLockTimeout": 60}`),
	})))(t)
	w.runPublishes(t)
	hash := sha256.Sum256([]byte(`{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}`))

	behind := manifest(0)
	if c := behind.GetManifest().GetConfig(); c.GetVersion() != 1 || string(c.GetSha256()) != string(hash[:]) {
		t.Errorf("the manifest pins %+v", c)
	}
	if string(behind.GetConfigPatch()) != `{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}` || behind.GetConfigFullRequired() {
		t.Errorf("a device with the defaults: %q full %v", behind.GetConfigPatch(), behind.GetConfigFullRequired())
	}
	current := manifest(1)
	if len(current.GetConfigPatch()) != 0 || current.GetConfigFullRequired() {
		t.Errorf("a device at the pinned version: %q", current.GetConfigPatch())
	}
	if _, err := w.manifest.GetManifest(ctx, bound(t, dev, pluxv1connect.ManifestServiceGetManifestProcedure, &pluxv1.GetManifestRequest{ConfigVersion: -1})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a negative config_version: %v", err)
	}
}

// Verifies: SEC-182, SEC-022.
// The window for a proof's issued-at time is the environment's: a proof
// that the installation's window admits is refused where the environment's
// configuration narrowed it, and still accepted elsewhere.
func TestDPoPWindowFollowsTheEnvironment(t *testing.T) {
	t.Parallel()
	narrow := map[string]time.Duration{}
	var mu sync.Mutex
	f := newNegativeFixtureWith(t, func(_ context.Context, _, env string) (time.Duration, time.Duration, error) {
		mu.Lock()
		defer mu.Unlock()
		if w, ok := narrow[env]; ok {
			return w, w, nil
		}
		return dpopIatWindow, 15 * time.Second, nil
	})
	late := func(p *devicetest.Proof) { p.IssuedAt = p.IssuedAt.Add(-30 * time.Second) }
	accepted(t, f.send(f.request(t, f.dev.key, f.dev.token, late)))
	mu.Lock()
	narrow[f.envs["development"]] = 10 * time.Second
	mu.Unlock()
	f.refused(t, f.send(f.request(t, f.dev.key, f.dev.token, late)), refusedAs(plxerr.DPoPProofInvalid, challengeProof))
	accepted(t, f.send(f.request(t, f.dev.key, f.dev.token, nil)))
}
