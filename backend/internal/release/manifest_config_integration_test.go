// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/seccfg"
)

// configFixture is a production channel with a signed manifest.
type configFixture struct {
	*fixture
	env string
	seq int64
}

func newConfigFixture(t *testing.T, securityLimits limits.Set) *configFixture {
	t.Helper()
	ctx := context.Background()
	f := newFixtureWith(t, "loan-calculator", securityLimits, nil)
	prod := f.envs["production"]
	f.publish(t, "", false)
	f.publish(t, f.loans, false)
	rel, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, rel.Sequence, prod.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	return &configFixture{fixture: f, env: prod.ID, seq: rel.Sequence}
}

// set changes the production configuration and runs the signing the
// change enqueued.
func (c *configFixture) set(t *testing.T, profile, overrides string, expected int64) {
	t.Helper()
	if _, err := c.sec.Set(context.Background(), c.owner, seccfg.Change{
		AppID: c.app, EnvironmentID: c.env, Profile: profile, Overrides: []byte(overrides), ExpectedVersion: expected,
	}); err != nil {
		t.Fatalf("Set(%s, %s, %d): %v", profile, overrides, expected, err)
	}
	c.run(t)
}

func (c *configFixture) manifest(t *testing.T, configVersion int64) release.ServedManifest {
	t.Helper()
	m, err := c.rel.GetManifest(context.Background(), release.ManifestRequest{
		OrganizationID: c.org, AppID: c.app, EnvironmentID: c.env, ConfigVersion: configVersion,
	})
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	return m
}

// Verifies: SEC-182.
// The signed manifest pins the version and the hash of the device
// document; an environment with only the built-in defaults pins nothing,
// and a change of the configuration has the manifest signed again.
func TestManifestPinsConfig(t *testing.T) {
	t.Parallel()
	c := newConfigFixture(t, limits.Set{})
	before := c.manifest(t, 0)
	if before.Document.Config != nil || len(before.ConfigPatch) != 0 || before.ConfigFullRequired {
		t.Fatalf("no configuration: %+v", before.Document.Config)
	}
	c.set(t, "strict", `{"inactivityLockTimeout": 60, "dpopIatWindow": 20}`, 0)
	after := c.manifest(t, 0)
	cfg, err := c.sec.Get(context.Background(), c.owner, c.app, c.env)
	if err != nil || cfg.Version != 1 {
		t.Fatalf("Get: %+v %v", cfg, err)
	}
	raw, sum, err := cfg.DeviceDocument()
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Document.Config; got == nil || got.Version != 1 || got.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("the manifest pins %+v, want version 1 and %x", got, sum)
	}
	if after.ETag == before.ETag {
		t.Error("the ETag did not change with the configuration")
	}
	// The server-only setting stays out of the document the device gets.
	if want := `{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}`; string(raw) != want {
		t.Errorf("device document %s", raw)
	}
}

// Verifies: SEC-182.
// What a device receives depends on the version it holds: nothing for the
// current one, the patch for an older one that is still kept, and the
// patch from nothing for version 0 or one that has left the history.
func TestManifestConfigDelivery(t *testing.T) {
	t.Parallel()
	c := newConfigFixture(t, limits.Set{})
	profiles := []string{"standard", "strict", "maximum"}
	c.set(t, "standard", `{"allowSoftwareKeys": false}`, 0) // 1
	for v := int64(1); v < seccfg.HistoryKept+4; v++ {
		c.set(t, profiles[v%3], `{"allowSoftwareKeys": false}`, v)
	}
	current := int64(seccfg.HistoryKept + 4)
	kept := current - seccfg.HistoryKept + 1
	pinned := c.manifest(t, current)
	if pinned.Document.Config == nil || pinned.Document.Config.Version != current {
		t.Fatalf("pinned %+v, want version %d", pinned.Document.Config, current)
	}
	if len(pinned.ConfigPatch) != 0 || pinned.ConfigFullRequired {
		t.Errorf("a device at the current version got %q", pinned.ConfigPatch)
	}
	fromNothing := c.manifest(t, 0)
	if len(fromNothing.ConfigPatch) == 0 || fromNothing.ConfigFullRequired {
		t.Errorf("version 0: %q full %v", fromNothing.ConfigPatch, fromNothing.ConfigFullRequired)
	}
	older := c.manifest(t, current-1)
	// Version n has the profile profiles[(n-1)%3] and the same overrides
	// as its neighbours, so only the profile differs.
	if want := `{"profile":"` + profiles[(current-1)%3] + `"}`; string(older.ConfigPatch) != want || older.ConfigFullRequired {
		t.Errorf("one behind: %q full %v, want %s", older.ConfigPatch, older.ConfigFullRequired, want)
	}
	atEdge := c.manifest(t, kept)
	if len(atEdge.ConfigPatch) == 0 || atEdge.ConfigFullRequired {
		t.Errorf("the oldest kept version: %q", atEdge.ConfigPatch)
	}
	gone := c.manifest(t, 1)
	if string(gone.ConfigPatch) != string(fromNothing.ConfigPatch) || gone.ConfigFullRequired {
		t.Errorf("a version out of the history: %q, want the patch from nothing %q", gone.ConfigPatch, fromNothing.ConfigPatch)
	}
}

// Verifies: SEC-182.
// A patch beyond securityConfig.patchBytes is replaced by the whole device
// document, marked as such.
func TestManifestConfigTooLarge(t *testing.T) {
	t.Parallel()
	tight, err := limits.Defaults().Tighten(limits.SecurityConfigPatchBytes, limits.ScopeApp, 100)
	if err != nil {
		t.Fatal(err)
	}
	c := newConfigFixture(t, tight)
	c.set(t, "strict", ``, 0)
	c.set(t, "strict", `{"allowSoftwareKeys": false, "inactivityLock": true, "inactivityLockTimeout": 60, "screenshotBlockingDefault": true}`, 1)
	big := c.manifest(t, 1)
	if !big.ConfigFullRequired || len(big.ConfigPatch) <= 100 {
		t.Errorf("a patch beyond the limit: %q full %v", big.ConfigPatch, big.ConfigFullRequired)
	}
	c.set(t, "strict", `{"allowSoftwareKeys": false, "inactivityLock": true, "inactivityLockTimeout": 30, "screenshotBlockingDefault": true}`, 2)
	small := c.manifest(t, 2)
	if small.ConfigFullRequired || string(small.ConfigPatch) != `{"overrides":{"inactivityLockTimeout":30}}` {
		t.Errorf("a patch within the limit: %q full %v", small.ConfigPatch, small.ConfigFullRequired)
	}
}

// Verifies: SEC-182, REL-031.
// "Not modified" needs the ETag and the configuration the manifest pins:
// a device with the right ETag but another configuration version is given
// the manifest and its patch.
func TestManifestETagCoversConfig(t *testing.T) {
	t.Parallel()
	c := newConfigFixture(t, limits.Set{})
	c.set(t, "strict", ``, 0)
	first := c.manifest(t, 0)
	// A device that holds the manifest's bundles and configuration.
	held := func(configVersion int64) release.ServedManifest {
		t.Helper()
		in := map[string][]byte{"": mustHash(t, first.Document.AppBundle.Hash)}
		for _, p := range first.Document.Plugins {
			in[p.Key] = mustHash(t, p.Hash)
		}
		m, err := c.rel.GetManifest(context.Background(), release.ManifestRequest{
			OrganizationID: c.org, AppID: c.app, EnvironmentID: c.env, Installed: in, IfNoneMatch: first.ETag, ConfigVersion: configVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := held(1); !m.NotModified {
		t.Error("a device with the manifest and its configuration was not told \"not modified\"")
	}
	m := held(0)
	if m.NotModified || len(m.ConfigPatch) == 0 || m.Document.Config == nil {
		t.Errorf("a device at another configuration version: not modified %v, patch %q", m.NotModified, m.ConfigPatch)
	}
	c.set(t, "maximum", ``, 1)
	if m := held(1); m.NotModified || len(m.ConfigPatch) == 0 {
		t.Errorf("after a change the old ETag matched: %v %q", m.NotModified, m.ConfigPatch)
	}
}

// mustHash reads a "sha256:<hex>" bundle reference.
func mustHash(t *testing.T, ref string) []byte {
	t.Helper()
	b, err := hex.DecodeString(ref[len("sha256:"):])
	if err != nil {
		t.Fatal(err)
	}
	return b
}
