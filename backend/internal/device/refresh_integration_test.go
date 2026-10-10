// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// refresh asks for a token for a device with a proof from jkt.
func (r *rig) refresh(d device.Device, jkt, proof string) (device.AccessToken, error) {
	return r.svc.Refresh(context.Background(), device.RefreshRequest{DeviceID: d.ID, ProofJKT: jkt, Proof: proof})
}

// Verifies: SEC-020.
// The installation's accessTokenLifetime (auth.device.accessTokenTTL) may
// only tighten the profile's value, like every override (maintainer Q3):
// a shorter one applies, a longer one leaves the profile's.
func TestRefreshUsesTheInstallationLifetimeOnlyToTighten(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		installation time.Duration
		want         time.Duration
	}{
		{"shorter than the profile", 3 * time.Minute, 3 * time.Minute},
		{"longer than the profile", 11 * time.Minute, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRigWith(t, func(o *device.Options) { o.AccessTokenLifetime = tc.installation })
			_, jwk := devicetest.NewKey(t)
			dev, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
			if err != nil {
				t.Fatal(err)
			}
			tok, err := r.refresh(dev, dev.DPoPJKT, "proof")
			if err != nil {
				t.Fatal(err)
			}
			if want := r.now.Add(tc.want).UTC().Truncate(time.Second); !tok.ExpiresAt.Equal(want) {
				t.Errorf("the token expires at %v, want %v", tok.ExpiresAt, want)
			}
		})
	}
}

// Verifies: SEC-020, SEC-025.
// A device that proves its key gets a token bound to that key, carrying
// its identity and assurance, signed with the class of key its
// environment calls for, and living as long as the setting says.
func TestRefreshIssuesATokenBoundToTheKey(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()

	_, jwk := devicetest.NewKey(t)
	dev, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}
	tok, err := r.refresh(dev, dev.DPoPJKT, "proof")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := r.verifier.Verify(ctx, tok.Value, false)
	if err != nil {
		t.Fatal(err)
	}
	if claims.DeviceID != dev.ID || claims.AppID != r.app || claims.Environment != r.envs["development"] ||
		claims.OrganizationID != r.org || claims.HostBuild != "42" || claims.Assurance != "AL0" || claims.JKT != dev.DPoPJKT || claims.Production {
		t.Errorf("claims = %+v", claims)
	}
	if want := r.now.Add(5 * time.Minute).UTC().Truncate(time.Second); !tok.ExpiresAt.Equal(want) || !claims.ExpiresAt.Equal(want) {
		t.Errorf("the token expires at %v (claims %v), want %v", tok.ExpiresAt, claims.ExpiresAt, want)
	}
	if tok.Assurance != claims.Assurance {
		t.Errorf("the response says %q, the token's al claim %q", tok.Assurance, claims.Assurance)
	}

	// A production environment gets a production key, and the assurance
	// the evidence earned.
	key, androidJWK := devicetest.NewKey(t)
	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelDevice)
	phone, err := r.register(t, "production", "android", androidJWK, device.KeyStorageTEE, androidEvidence())
	if err != nil {
		t.Fatal(err)
	}
	tok, err = r.refresh(phone, phone.DPoPJKT, "proof")
	if err != nil {
		t.Fatal(err)
	}
	if claims, err = r.verifier.Verify(ctx, tok.Value, true); err != nil || !claims.Production || claims.Assurance != "AL2" || claims.Environment != r.envs["production"] {
		t.Errorf("a production device's claims = %+v, %v", claims, err)
	}
	if _, err := r.verifier.Verify(ctx, tok.Value, false); err == nil {
		t.Error("a production token verified with the development key")
	}

	// The environment lookup is remembered for a minute and is right.
	for env, want := range map[string]bool{r.envs["production"]: true, r.envs["development"]: false, r.envs["staging"]: false} {
		for range 2 {
			if got, err := r.svc.EnvironmentIsProduction(ctx, env); err != nil || got != want {
				t.Errorf("EnvironmentIsProduction(%s) = %v, %v", env, got, err)
			}
		}
	}
	for _, unknown := range []string{r.org, "nope"} {
		for range 2 {
			if _, err := r.svc.EnvironmentIsProduction(ctx, unknown); code(err) != plxerr.ResourceNotFound {
				t.Errorf("EnvironmentIsProduction(%q): %v", unknown, err)
			}
		}
	}
}

// Verifies: SEC-025, SEC-006.
// A proof from another key, for an unknown device or for a revoked one is
// refused, and a device whose attestation is older than the interval must
// attest again, after which it refreshes.
func TestRefreshRefusals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.now = r.now.Truncate(time.Second) // the database keeps microseconds
	ctx := context.Background()
	_, jwk := devicetest.NewKey(t)
	dev, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}
	stranger, _ := devicetest.NewKey(t)

	if _, err := r.refresh(dev, thumbprint(t, &stranger.PublicKey), "p"); code(err) != plxerr.TokenBindingMismatch {
		t.Errorf("another key: %v", err)
	}
	if _, err := r.refresh(dev, "", "p"); code(err) != plxerr.TokenBindingMismatch {
		t.Errorf("no key: %v", err)
	}
	unknown := device.Device{ID: "01a0c450-6c00-7002-8000-000000003dde"}
	if _, err := r.refresh(unknown, dev.DPoPJKT, "p"); code(err) != plxerr.TokenBindingMismatch {
		t.Errorf("an unknown device is told apart from a wrong key: %v", err)
	}
	if _, err := r.refresh(device.Device{ID: "nope"}, dev.DPoPJKT, "p"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid identifier: %v", err)
	}

	// Exactly one interval old still refreshes; a second later it must
	// attest again.
	r.now = r.now.Add(24 * time.Hour)
	if _, err := r.refresh(dev, dev.DPoPJKT, "p"); err != nil {
		t.Errorf("an attestation one interval old: %v", err)
	}
	r.now = r.now.Add(time.Second)
	if _, err := r.refresh(dev, dev.DPoPJKT, "p"); code(err) != plxerr.ReattestationRequired {
		t.Errorf("an attestation past the interval: %v", err)
	}
	if _, err := r.svc.Reattest(ctx, dev.ID, r.challenge(t, "development"), developmentEvidence()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.refresh(dev, dev.DPoPJKT, "p"); err != nil {
		t.Errorf("after re-attesting: %v", err)
	}

	if _, err := r.svc.Revoke(ctx, r.owner, dev.ID, "stolen"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.refresh(dev, dev.DPoPJKT, "p"); code(err) != plxerr.DeviceRevoked {
		t.Errorf("a revoked device: %v", err)
	}
	if _, err := r.refresh(dev, thumbprint(t, &stranger.PublicKey), "p"); code(err) != plxerr.TokenBindingMismatch {
		t.Errorf("the revocation is told to the key's holder only: %v", err)
	}
	if err := r.svc.VerifyKey(ctx, dev.ID, dev.DPoPJKT); code(err) != plxerr.DeviceRevoked {
		t.Errorf("VerifyKey of a revoked device: %v", err)
	}
	if err := r.svc.VerifyKey(ctx, dev.ID, "other"); code(err) != plxerr.TokenBindingMismatch {
		t.Errorf("VerifyKey with another key: %v", err)
	}
}

// bumpingAssertor is an assertion verifier that lets another refresh store
// its counter while the assertion is being checked.
type bumpingAssertor struct {
	devicetest.AppAssertor
	during func()
}

func (b *bumpingAssertor) VerifyAssertion(a []byte, pub *ecdsa.PublicKey, h [32]byte, appID string, last uint32) (uint32, error) {
	b.during()
	return b.AppAssertor.VerifyAssertion(a, pub, h, appID, last)
}

// Verifies: SEC-025.
// An iOS device with an App Attest key must send an assertion over the
// proof; its counter must advance, is stored, and is stored once even when
// two refreshes race.
func TestRefreshNeedsAnAppAttestAssertionOnIOS(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	appKey, _ := devicetest.NewKey(t)
	r.apple.Attestation = appattest.Attestation{PublicKey: &appKey.PublicKey, Receipt: []byte("receipt")}
	_, jwk := devicetest.NewKey(t)
	phone, err := r.register(t, "production", "ios", jwk, device.KeyStorageSecureEnclave, iosEvidence())
	if err != nil {
		t.Fatal(err)
	}
	ask := func(assertion []byte, proof string) (device.AccessToken, error) {
		return r.svc.Refresh(ctx, device.RefreshRequest{DeviceID: phone.ID, ProofJKT: phone.DPoPJKT, Proof: proof, AppAttestAssertion: assertion})
	}
	if _, err := ask(nil, "p"); code(err) != plxerr.AttestationFailed {
		t.Errorf("no assertion: %v", err)
	}

	r.assert.Counter = 1
	if _, err := ask([]byte("assertion"), "proof-1"); err != nil {
		t.Fatal(err)
	}
	if want := sha256Of("proof-1"); r.assert.ClientDataHash != want || r.assert.AppID != iosAppID || r.assert.LastCounter != 0 {
		t.Errorf("the assertion was checked against %x for %q after %d", r.assert.ClientDataHash, r.assert.AppID, r.assert.LastCounter)
	}
	if n := r.counterOf(t, phone.ID); n != 1 {
		t.Errorf("the stored counter = %d, want 1", n)
	}
	if _, err := ask([]byte("assertion"), "proof-2"); code(err) != plxerr.AttestationFailed {
		t.Errorf("a counter that did not advance: %v", err)
	}
	r.assert.Err = plxerr.New(plxerr.AttestationFailed, "app attest: the signature does not verify")
	if _, err := ask([]byte("assertion"), "proof-3"); code(err) != plxerr.AttestationFailed {
		t.Errorf("a refused assertion: %v", err)
	}
	if n := r.counterOf(t, phone.ID); n != 1 {
		t.Errorf("a refused assertion moved the counter to %d", n)
	}
	r.assert.Err = nil

	// Two refreshes carrying the same assertion: the one that stores its
	// counter second loses.
	raced := &bumpingAssertor{AppAssertor: devicetest.AppAssertor{Counter: 2}}
	raced.during = func() { r.setCounter(t, phone.ID, 5) }
	r.assertWith = raced
	if _, err := ask([]byte("assertion"), "proof-4"); code(err) != plxerr.AttestationFailed {
		t.Errorf("a counter taken by a concurrent refresh: %v", err)
	}
	if n := r.counterOf(t, phone.ID); n != 5 {
		t.Errorf("the losing refresh changed the counter to %d", n)
	}

	// Without App Attest configured for the app, the refresh cannot be
	// proven.
	r.trust.IOSAppID = ""
	r.assert.Counter = 9
	if _, err := ask([]byte("assertion"), "proof-5"); code(err) != plxerr.AttestationUnavailable {
		t.Errorf("an app without App Attest: %v", err)
	}
}

// brokenCache fails every read, as a cache that is down does.
type brokenCache struct{ cache.Cache }

func (brokenCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("the cache is down")
}

// Verifies: SEC-006.
// Refusing a revoked key does not depend on the cache: when it cannot
// answer, the database does.
func TestIsRevokedFallsBackToTheDatabase(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	_, jwk := devicetest.NewKey(t)
	revoked, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}
	_, jwk = devicetest.NewKey(t)
	trusted, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, revoked.ID, "lost"); err != nil {
		t.Fatal(err)
	}
	blind, err := device.NewService(device.Options{DB: r.db, IDs: ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}, Cache: brokenCache{}})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{revoked.DPoPJKT: true, trusted.DPoPJKT: false, "unknown-key": false} {
		if got, err := blind.IsRevoked(ctx, key); err != nil || got != want {
			t.Errorf("IsRevoked(%.8s) with the cache down = %v, %v, want %v", key, got, err, want)
		}
	}
}

// sha256Of is the SHA-256 of a string.
func sha256Of(s string) [32]byte { return sha256.Sum256([]byte(s)) }

// counterOf reads the stored App Attest counter of a device.
func (r *rig) counterOf(t *testing.T, deviceID string) int64 {
	t.Helper()
	var n int64
	err := r.db.InTx(context.Background(), storage.Tenant{OrganizationID: r.org}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT app_attest_counter FROM devices WHERE id = $1`, deviceID).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// setCounter stores an App Attest counter, as a concurrent refresh would.
func (r *rig) setCounter(t *testing.T, deviceID string, n int64) {
	t.Helper()
	err := r.db.InTx(context.Background(), storage.Tenant{OrganizationID: r.org}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices SET app_attest_counter = $2 WHERE id = $1`, deviceID, n)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Verifies: SEC-007.
// A device whose token carries a level below the environment's
// minAssuranceForSync is refused with PLX-6002; one at or above it, or
// any device under a profile that asks for AL0, is not.
func TestCheckSyncAssurance(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	env := r.envs["development"]
	id := func(level string) device.Identity {
		return device.Identity{AppID: r.app, EnvironmentID: env, Assurance: level}
	}

	if err := r.svc.CheckSyncAssurance(ctx, id("AL0")); err != nil {
		t.Errorf("AL0 under standard: %v", err)
	}
	r.profiles[env] = settings.Strict
	if err := r.svc.CheckSyncAssurance(ctx, id("AL0")); code(err) != plxerr.AssuranceInsufficient {
		t.Errorf("AL0 under strict: %v", err)
	}
	if err := r.svc.CheckSyncAssurance(ctx, id("")); code(err) != plxerr.AssuranceInsufficient {
		t.Errorf("a token with no level under strict: %v", err)
	}
	if err := r.svc.CheckSyncAssurance(ctx, id("AL1")); err != nil {
		t.Errorf("AL1 under strict: %v", err)
	}
	r.profiles[env] = settings.Maximum
	if err := r.svc.CheckSyncAssurance(ctx, id("AL1")); code(err) != plxerr.AssuranceInsufficient {
		t.Errorf("AL1 under maximum: %v", err)
	}
	if err := r.svc.CheckSyncAssurance(ctx, id("AL3")); err != nil {
		t.Errorf("AL3 under maximum: %v", err)
	}
}
