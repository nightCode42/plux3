// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

const (
	tokenIssuer    = "https://plux.test" //nolint:gosec // G101: an issuer URL in a test fixture, not a credential
	androidPackage = "com.example.app"
	iosAppID       = "TEAMID.com.example.app"
	// challengeTTL is the default of the registrationChallengeTtl setting.
	challengeTTL = 5 * time.Minute
)

// rig is a fixture whose service verifies evidence with fakes, on a cache
// that follows the fixture's clock.
type rig struct {
	*fixture
	cache  *cache.Memory
	key    *devicetest.KeyAttestor
	play   *devicetest.IntegrityChecker
	apple  *devicetest.AppAttestor
	assert *devicetest.AppAssertor
	// assertWith is the assertion verifier in use; it starts as assert.
	assertWith device.AppAssertor
	trust      device.TrustConfig
	profiles   map[string]settings.Profile
	// verifier checks the tokens the service issues.
	verifier *devtoken.Verifier
}

func newRig(t *testing.T) *rig { return newRigWith(t, nil) }

// newRigWith is newRig with a say over the service's options.
func newRigWith(t *testing.T, configure func(*device.Options)) *rig {
	t.Helper()
	r := &rig{
		key: &devicetest.KeyAttestor{}, play: &devicetest.IntegrityChecker{}, apple: &devicetest.AppAttestor{},
		assert: &devicetest.AppAssertor{},
		trust: device.TrustConfig{
			AndroidPackages: []string{androidPackage}, PlayIntegrity: &playintegrity.Keys{}, IOSAppID: iosAppID,
		},
		profiles: map[string]settings.Profile{},
	}
	r.assertWith = r.assert
	r.fixture = newFixtureWith(t, func(f *fixture, o *device.Options) {
		r.cache = cache.NewMemory(func() time.Time { return f.now })
		o.Cache = r.cache
		o.Attestors = device.Attestors{KeyAttestation: r.key, PlayIntegrity: r.play, AppAttest: r.apple, AppAssertions: r}
		signer := devicetest.NewTokenSigner(t)
		o.Tokens = &devtoken.Issuer{Signer: signer, Issuer: tokenIssuer, Audience: tokenIssuer, Lifetime: time.Minute, Now: func() time.Time { return f.now }}
		r.verifier = &devtoken.Verifier{Keys: signer.TokenKeys, Issuer: tokenIssuer, Audience: tokenIssuer, Now: func() time.Time { return f.now }}
		o.AppTrust = func(context.Context, string) (device.TrustConfig, error) { return r.trust, nil }
		o.Profiles = func(_ context.Context, _, env string) (settings.Profile, error) {
			if p, ok := r.profiles[env]; ok {
				return p, nil
			}
			return settings.Standard, nil
		}
		if configure != nil {
			configure(o)
		}
	})
	return r
}

// VerifyAssertion delegates to the assertion verifier in use, so that a
// test can swap it after the service was built.
func (r *rig) VerifyAssertion(assertion []byte, pub *ecdsa.PublicKey, h [32]byte, appID string, last uint32) (uint32, error) {
	return r.assertWith.VerifyAssertion(assertion, pub, h, appID, last)
}

func (r *rig) challenge(t *testing.T, env string) []byte {
	t.Helper()
	c, _, err := r.svc.CreateChallenge(context.Background(), r.app, env)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// android makes the fakes vouch for key: a key at the given security
// level and a Play verdict with the given device labels.
func (r *rig) android(key *ecdsa.PublicKey, level keyattest.SecurityLevel, labels ...playintegrity.DeviceLabel) {
	r.key.Err = nil
	r.key.Result = keyattest.Result{AttestationLevel: level, KeyMintLevel: level, PackageName: androidPackage, PublicKey: key}
	r.play.Verdict = playintegrity.Verdict{Device: labels, AppRecognised: true}
}

func androidEvidence() device.Evidence {
	return device.Evidence{Android: &device.AndroidEvidence{KeyAttestationChain: [][]byte{{1}, {2}}, PlayIntegrityToken: "token"}}
}

func iosEvidence() device.Evidence {
	return device.Evidence{IOS: &device.IOSEvidence{KeyID: []byte("key"), AttestationObject: []byte("object")}}
}

func developmentEvidence() device.Evidence {
	return device.Evidence{Development: &device.DevelopmentEvidence{BuildID: "debug-1"}}
}

// register presents evidence for a fresh challenge.
func (r *rig) register(t *testing.T, env, platform string, jwk []byte, claim device.KeyStorage, e device.Evidence) (device.Device, error) {
	t.Helper()
	return r.registerWith(env, platform, r.challenge(t, env), jwk, claim, e)
}

func (r *rig) registerWith(env, platform string, challenge, jwk []byte, claim device.KeyStorage, e device.Evidence) (device.Device, error) {
	return r.svc.RegisterAttested(context.Background(), device.AttestedRegistration{
		AppID: r.app, Environment: env, Platform: platform, OSVersion: "15", RuntimeVersion: "1.0.0", Build: "42",
		Challenge: challenge, DPoPKeyJWK: jwk, KeyStorage: claim, Evidence: e,
	})
}

func thumbprint(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	jkt, err := dpop.Thumbprint(pub)
	if err != nil {
		t.Fatal(err)
	}
	return jkt
}

// Verifies: SEC-001, SEC-003, SEC-005, SEC-007.
// An Android device registers with a key and its attestation: the chain
// is checked against the challenge, the leaf must be the DPoP key, Play
// Integrity is bound to the challenge and the key, and the level follows
// from what was proven.
func TestRegisterAndroid(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	key, jwk := devicetest.NewKey(t)
	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelDevice)
	challenge := r.challenge(t, "production")

	d, err := r.registerWith("production", "android", challenge, jwk, device.KeyStorageTEE, androidEvidence())
	if err != nil {
		t.Fatal(err)
	}
	jkt := thumbprint(t, &key.PublicKey)
	if d.AssuranceLevel != "AL2" || d.KeyStorage != device.KeyStorageTEE || d.DPoPJKT != jkt || d.Provider != device.ProviderPlayIntegrity ||
		len(d.Verdicts) != 1 || d.Verdicts[0] != "MEETS_DEVICE_INTEGRITY" || !d.RevokedAt.IsZero() || d.AttestedAt.IsZero() {
		t.Fatalf("RegisterAttested = %+v", d)
	}
	if string(r.key.Challenge) != string(challenge) || r.key.Policy.RequireHardware ||
		len(r.key.Policy.PackageNames) != 1 || r.key.Policy.PackageNames[0] != androidPackage {
		t.Errorf("key attestation was asked %x %+v", r.key.Challenge, r.key.Policy)
	}
	if want := playintegrity.RequestHash(challenge, jkt); r.play.Expect.RequestHash != want ||
		r.play.Expect.PackageName != androidPackage || r.play.Expect.MaxAge != 10*time.Minute {
		t.Errorf("Play Integrity was asked %+v", r.play.Expect)
	}

	// The same key cannot register twice.
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a second device with the key: %v", err)
	}

	// Strong integrity in a StrongBox is AL3.
	strong, strongJWK := devicetest.NewKey(t)
	r.android(&strong.PublicKey, keyattest.SecurityStrongBox, playintegrity.LabelBasic, playintegrity.LabelStrong)
	d, err = r.register(t, "production", "android", strongJWK, device.KeyStorageStrongBox, androidEvidence())
	if err != nil || d.AssuranceLevel != "AL3" || d.KeyStorage != device.KeyStorageStrongBox {
		t.Errorf("a strong device = %+v, %v", d, err)
	}

	// A software key is accepted under standard and capped at AL1, however
	// strong the device is.
	soft, softJWK := devicetest.NewKey(t)
	r.android(&soft.PublicKey, keyattest.SecuritySoftware, playintegrity.LabelStrong)
	d, err = r.register(t, "production", "android", softJWK, device.KeyStorageUnspecified, androidEvidence())
	if err != nil || d.AssuranceLevel != "AL1" || d.KeyStorage != device.KeyStorageSoftware {
		t.Errorf("a software key = %+v, %v", d, err)
	}
}

// Verifies: SEC-001, SEC-003, SEC-005.
// Evidence that does not fit the key, the platform or the claim is
// refused, and none of it registers a device.
func TestRegisterAndroidRefusals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	key, jwk := devicetest.NewKey(t)
	other, _ := devicetest.NewKey(t)

	// The chain attests another key than the one the device will use.
	r.android(&other.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelDevice)
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a leaf that is not the DPoP key: %v", err)
	}

	r.android(&key.PublicKey, keyattest.SecuritySoftware, playintegrity.LabelDevice)
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a claim above the proof: %v", err)
	}

	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelDevice)
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageStrongBox, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a StrongBox claim for a TEE key: %v", err)
	}
	r.play.Err = plxerr.New(plxerr.AttestationFailed, "play integrity: the verdict is stale")
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a refused Play Integrity token: %v", err)
	}
	r.play.Err = nil
	r.key.Err = plxerr.New(plxerr.AttestationFailed, "key attestation: the chain is not trusted")
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a refused chain: %v", err)
	}
	r.key.Err = nil

	for name, e := range map[string]device.Evidence{
		"no evidence":      {},
		"no token":         {Android: &device.AndroidEvidence{KeyAttestationChain: [][]byte{{1}}}},
		"no chain":         {Android: &device.AndroidEvidence{PlayIntegrityToken: "t"}},
		"two providers":    {Android: androidEvidence().Android, Development: developmentEvidence().Development},
		"ios on a phone":   iosEvidence(),
		"empty ios object": {IOS: &device.IOSEvidence{KeyID: []byte("k")}},
	} {
		if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, e); code(err) != plxerr.AttestationFailed {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.register(t, "production", "ios", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("Android evidence from an iPhone: %v", err)
	}

	// Keys that are not public P-256 keys never reach the verifiers.
	private, err := json.Marshal(jose.JSONWebKey{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"not json": []byte("{"), "private key": private, "empty": nil} {
		if _, err := r.register(t, "production", "android", raw, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.InvalidFormat {
			t.Errorf("%s: %v", name, err)
		}
	}
	if list := r.countDevices(t); list != 0 {
		t.Errorf("%d devices registered by refused evidence", list)
	}
}

// countDevices counts the attested devices of the fixture's organisation.
func (r *rig) countDevices(t *testing.T) int {
	t.Helper()
	ds, err := r.svc.List(context.Background(), r.owner, r.app, "", storage.Cursor{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(ds)
}

// Verifies: SEC-001, SEC-007.
// A strict environment refuses a software key, asks the Key Attestation
// verifier for hardware, and needs a stronger device verdict for AL2.
func TestStrictEnvironment(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.profiles[r.envs["production"]] = settings.Strict

	soft, softJWK := devicetest.NewKey(t)
	r.android(&soft.PublicKey, keyattest.SecuritySoftware, playintegrity.LabelDevice)
	if _, err := r.register(t, "production", "android", softJWK, device.KeyStorageUnspecified, androidEvidence()); code(err) != plxerr.KeyNotHardwareBacked {
		t.Errorf("a software key in a strict environment: %v", err)
	}
	if !r.key.Policy.RequireHardware {
		t.Error("the verifier was not asked to require hardware")
	}

	basic, basicJWK := devicetest.NewKey(t)
	r.android(&basic.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelBasic)
	d, err := r.register(t, "production", "android", basicJWK, device.KeyStorageTEE, androidEvidence())
	if err != nil || d.AssuranceLevel != "AL1" {
		t.Errorf("a basic verdict in a strict environment = %+v, %v", d, err)
	}
	good, goodJWK := devicetest.NewKey(t)
	r.android(&good.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelDevice)
	d, err = r.register(t, "production", "android", goodJWK, device.KeyStorageTEE, androidEvidence())
	if err != nil || d.AssuranceLevel != "AL2" {
		t.Errorf("a device verdict in a strict environment = %+v, %v", d, err)
	}

	// An iPhone that admits to a software key is refused as well.
	appKey, _ := devicetest.NewKey(t)
	r.apple.Attestation = appattest.Attestation{PublicKey: &appKey.PublicKey, Receipt: []byte("r")}
	_, iosJWK := devicetest.NewKey(t)
	if _, err := r.register(t, "production", "ios", iosJWK, device.KeyStorageSoftware, iosEvidence()); code(err) != plxerr.KeyNotHardwareBacked {
		t.Errorf("an iOS software key in a strict environment: %v", err)
	}
}

// Verifies: SEC-003, SEC-007.
// An iOS device registers with App Attest bound to the challenge and the
// key; the Secure Enclave earns AL2, a software key AL1, and the device's
// claim cannot name Android storage.
func TestRegisterIOS(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	appKey, _ := devicetest.NewKey(t)
	r.apple.Attestation = appattest.Attestation{PublicKey: &appKey.PublicKey, Receipt: []byte("receipt")}
	key, jwk := devicetest.NewKey(t)
	challenge := r.challenge(t, "production")

	d, err := r.registerWith("production", "ios", challenge, jwk, device.KeyStorageSecureEnclave, iosEvidence())
	if err != nil {
		t.Fatal(err)
	}
	jkt := thumbprint(t, &key.PublicKey)
	if d.AssuranceLevel != "AL2" || d.KeyStorage != device.KeyStorageSecureEnclave || d.Provider != device.ProviderAppAttest || d.DPoPJKT != jkt {
		t.Errorf("RegisterAttested = %+v", d)
	}
	if r.apple.ClientDataHash != appattest.ClientDataHash(challenge, jkt) || r.apple.AppID != iosAppID || r.apple.Environment != appattest.Development {
		t.Errorf("App Attest was asked %x %q %v", r.apple.ClientDataHash, r.apple.AppID, r.apple.Environment)
	}
	r.assertAppAttest(t, d.ID, []byte("key"), []byte("receipt"))

	r.trust.AppAttestProduction = true
	for _, claim := range []device.KeyStorage{device.KeyStorageSoftware, device.KeyStorageUnspecified} {
		_, jwk := devicetest.NewKey(t)
		d, err = r.register(t, "production", "ios", jwk, claim, iosEvidence())
		if err != nil || d.AssuranceLevel != "AL1" || d.KeyStorage != device.KeyStorageSoftware {
			t.Errorf("claim %q = %+v, %v", claim, d, err)
		}
	}
	if r.apple.Environment != appattest.Production {
		t.Error("the production App Attest environment was not asked for")
	}
	_, jwk = devicetest.NewKey(t)
	if _, err := r.register(t, "production", "ios", jwk, device.KeyStorageTEE, iosEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("an Android storage claim from an iPhone: %v", err)
	}
	r.apple.Err = plxerr.New(plxerr.AttestationFailed, "app attest: the nonce does not match")
	if _, err := r.register(t, "production", "ios", jwk, device.KeyStorageSecureEnclave, iosEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a refused attestation: %v", err)
	}
}

// assertAppAttest checks what is stored of an App Attest attestation.
func (r *rig) assertAppAttest(t *testing.T, deviceID string, keyID, receipt []byte) {
	t.Helper()
	var gotKey, gotReceipt, gotPublic []byte
	var counter int64
	err := r.db.InTx(context.Background(), storage.Tenant{OrganizationID: r.org}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT app_attest_key_id, app_attest_public_key, app_attest_receipt, app_attest_counter FROM devices WHERE id = $1`, deviceID).
			Scan(&gotKey, &gotPublic, &gotReceipt, &counter)
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(gotKey) != string(keyID) || string(gotReceipt) != string(receipt) || len(gotPublic) == 0 || counter != 0 {
		t.Errorf("stored App Attest key %q receipt %q public %d bytes counter %d", gotKey, gotReceipt, len(gotPublic), counter)
	}
}

// Verifies: SEC-008.
// The development provider registers at AL0 outside production only.
func TestRegisterDevelopment(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, jwk := devicetest.NewKey(t)
	d, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil || d.AssuranceLevel != "AL0" || d.Provider != device.ProviderDevelopment || d.KeyStorage != device.KeyStorageSoftware {
		t.Fatalf("a development device = %+v, %v", d, err)
	}
	_, jwk = devicetest.NewKey(t)
	if _, err := r.register(t, "production", "linux", jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.DevProviderInProduction {
		t.Errorf("the development provider in production: %v", err)
	}
	if _, err := r.register(t, "development", "linux", jwk, device.KeyStorageTEE, developmentEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("a hardware claim without proof: %v", err)
	}
	if _, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, device.Evidence{Development: &device.DevelopmentEvidence{}}); code(err) != plxerr.InvalidFormat {
		t.Errorf("a development build without an identifier: %v", err)
	}
	r.profiles[r.envs["development"]] = settings.Strict
	if _, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.KeyNotHardwareBacked {
		t.Errorf("a software key in a strict development environment: %v", err)
	}
}

// Verifies: SEC-008.
// Without the installation's say-so the development provider is refused in
// every environment; production refuses it regardless.
func TestDevelopmentProviderIsOffUnlessEnabled(t *testing.T) {
	t.Parallel()
	r := newRigWith(t, func(o *device.Options) { o.DevelopmentProvider = false })
	_, jwk := devicetest.NewKey(t)
	_, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if code(err) != plxerr.AttestationFailed || !strings.Contains(err.Error(), "the development provider is not enabled") {
		t.Errorf("development evidence while the provider is off: %v", err)
	}
	if _, err := r.register(t, "production", "linux", jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.DevProviderInProduction {
		t.Errorf("development evidence in production while the provider is off: %v", err)
	}
}

// Verifies: SEC-005.
// The lifetime of a registration challenge is the registrationChallengeTtl
// setting of its environment's profile.
func TestChallengeLifetimeFollowsTheSetting(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.profiles[r.envs["development"]] = settings.Maximum
	_, expires, err := r.svc.CreateChallenge(context.Background(), r.app, "development")
	if err != nil {
		t.Fatal(err)
	}
	if want := r.now.Add(120 * time.Second).UTC().Truncate(time.Second); !expires.Equal(want) {
		t.Errorf("a maximum-profile challenge expires at %v, want %v", expires, want)
	}
	_, expires, err = r.svc.CreateChallenge(context.Background(), r.app, "production")
	if err != nil {
		t.Fatal(err)
	}
	if want := r.now.Add(challengeTTL).UTC().Truncate(time.Second); !expires.Equal(want) {
		t.Errorf("a standard-profile challenge expires at %v, want %v", expires, want)
	}
}

// Verifies: SEC-005.
// A challenge is random, bound to its app and environment, valid for five
// minutes and usable once; the cache holds its hash, not its value.
func TestChallenges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()

	first, expires, err := r.svc.CreateChallenge(ctx, r.app, "production")
	if err != nil {
		t.Fatal(err)
	}
	second := r.challenge(t, "production")
	if len(first) != 32 || string(first) == string(second) || !expires.Equal(r.now.Add(challengeTTL).UTC().Truncate(time.Second)) {
		t.Errorf("challenges %x %x expire %v", first, second, expires)
	}
	sum := sha256.Sum256(first)
	if _, found, err := r.cache.Get(ctx, "regchallenge:"+base64.RawURLEncoding.EncodeToString(sum[:])); err != nil || !found {
		t.Errorf("the challenge is not stored under its hash: %v", err)
	}
	if _, found, _ := r.cache.Get(ctx, "regchallenge:"+base64.RawURLEncoding.EncodeToString(first)); found {
		t.Error("the challenge is stored as sent")
	}
	if _, _, err := r.svc.CreateChallenge(ctx, r.app, "nope"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown environment: %v", err)
	}
	if _, _, err := r.svc.CreateChallenge(ctx, "nope", "production"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid app: %v", err)
	}

	// Single use.
	_, jwk := devicetest.NewKey(t)
	if _, err := r.registerWith("development", "linux", r.challenge(t, "development"), jwk, device.KeyStorageSoftware, developmentEvidence()); err != nil {
		t.Fatal(err)
	}
	used := r.challenge(t, "development")
	_, jwk = devicetest.NewKey(t)
	if _, err := r.registerWith("development", "linux", used, jwk, device.KeyStorageSoftware, developmentEvidence()); err != nil {
		t.Fatal(err)
	}
	_, jwk = devicetest.NewKey(t)
	if _, err := r.registerWith("development", "linux", used, jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.RegistrationChallengeInvalid {
		t.Errorf("a challenge used twice: %v", err)
	}

	// Another environment, another app, garbage and strangers.
	if _, err := r.registerWith("development", "linux", first, jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.RegistrationChallengeInvalid {
		t.Errorf("a production challenge in development: %v", err)
	}
	o, err := r.ten.CreateApp(ctx, r.owner, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.RegisterAttested(ctx, device.AttestedRegistration{
		AppID: o.ID, Environment: "production", Platform: "linux", Challenge: first, DPoPKeyJWK: jwk,
		KeyStorage: device.KeyStorageSoftware, Evidence: developmentEvidence(),
	}); code(err) != plxerr.RegistrationChallengeInvalid {
		t.Errorf("another app's challenge: %v", err)
	}
	for name, c := range map[string][]byte{"empty": nil, "short": []byte("short"), "unissued": make([]byte, 32)} {
		if _, err := r.registerWith("development", "linux", c, jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.RegistrationChallengeInvalid {
			t.Errorf("%s challenge: %v", name, err)
		}
	}

	// Expiry.
	stale := r.challenge(t, "development")
	r.now = r.now.Add(challengeTTL + time.Second)
	if _, err := r.registerWith("development", "linux", stale, jwk, device.KeyStorageSoftware, developmentEvidence()); code(err) != plxerr.RegistrationChallengeInvalid {
		t.Errorf("an expired challenge: %v", err)
	}
}

// Verifies: SEC-003, SEC-005.
// A platform with no verifier, or an app with no trust configuration,
// cannot register; no challenge is issued without a store for it.
func TestAttestationUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, jwk := devicetest.NewKey(t)

	r.trust.PlayIntegrity = nil
	if _, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence()); code(err) != plxerr.AttestationUnavailable {
		t.Errorf("Android without Play Integrity keys: %v", err)
	}
	r.trust.IOSAppID = ""
	if _, err := r.register(t, "production", "ios", jwk, device.KeyStorageSecureEnclave, iosEvidence()); code(err) != plxerr.AttestationUnavailable {
		t.Errorf("iOS without an app identifier: %v", err)
	}

	bare := newFixtureWith(t, nil)
	if _, _, err := bare.svc.CreateChallenge(context.Background(), bare.app, "production"); code(err) != plxerr.AttestationUnavailable {
		t.Errorf("a challenge without a store: %v", err)
	}
	unconfigured := newFixtureWith(t, func(f *fixture, o *device.Options) { o.Cache = cache.NewMemory(func() time.Time { return f.now }) })
	c, _, err := unconfigured.svc.CreateChallenge(context.Background(), unconfigured.app, "production")
	if err != nil {
		t.Fatal(err)
	}
	for platform, e := range map[string]device.Evidence{"android": androidEvidence(), "ios": iosEvidence()} {
		_, err := unconfigured.svc.RegisterAttested(context.Background(), device.AttestedRegistration{
			AppID: unconfigured.app, Environment: "production", Platform: platform, Challenge: c, DPoPKeyJWK: jwk, Evidence: e,
		})
		if code(err) != plxerr.AttestationUnavailable {
			t.Errorf("%s without a verifier: %v", platform, err)
		}
		c, _, _ = unconfigured.svc.CreateChallenge(context.Background(), unconfigured.app, "production")
	}
}

// Verifies: SEC-006.
// Re-attestation recomputes the level for the same key and consumes a
// fresh challenge; a different key, a reused challenge, a revoked device
// and the development provider in production are refused.
func TestReattest(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	key, jwk := devicetest.NewKey(t)
	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment)
	d, err := r.register(t, "production", "android", jwk, device.KeyStorageTEE, androidEvidence())
	if err != nil || d.AssuranceLevel != "AL1" {
		t.Fatalf("a device without a verdict = %+v, %v", d, err)
	}

	r.now = r.now.Add(time.Hour)
	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelStrong)
	challenge := r.challenge(t, "production")
	again, err := r.svc.Reattest(ctx, d.ID, challenge, androidEvidence())
	if err != nil || again.AssuranceLevel != "AL3" || !again.AttestedAt.After(d.AttestedAt) || again.DPoPJKT != d.DPoPJKT {
		t.Fatalf("Reattest = %+v, %v", again, err)
	}
	if string(r.key.Challenge) != string(challenge) {
		t.Error("the chain was not checked against the new challenge")
	}
	if _, err := r.svc.Reattest(ctx, d.ID, challenge, androidEvidence()); code(err) != plxerr.RegistrationChallengeInvalid {
		t.Errorf("a reused challenge: %v", err)
	}

	other, _ := devicetest.NewKey(t)
	r.android(&other.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelStrong)
	if _, err := r.svc.Reattest(ctx, d.ID, r.challenge(t, "production"), androidEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("another key: %v", err)
	}
	if _, err := r.svc.Reattest(ctx, d.ID, r.challenge(t, "production"), developmentEvidence()); code(err) != plxerr.DevProviderInProduction {
		t.Errorf("the development provider in production: %v", err)
	}
	if _, err := r.svc.Reattest(ctx, d.ID, r.challenge(t, "production"), iosEvidence()); code(err) != plxerr.AttestationFailed {
		t.Errorf("iOS evidence for an Android device: %v", err)
	}
	if _, err := r.svc.Reattest(ctx, "nope", r.challenge(t, "production"), androidEvidence()); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid device: %v", err)
	}
	if _, err := r.svc.Reattest(ctx, r.org, r.challenge(t, "production"), androidEvidence()); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown device: %v", err)
	}

	if _, err := r.svc.Revoke(ctx, r.owner, d.ID, "stolen"); err != nil {
		t.Fatal(err)
	}
	r.android(&key.PublicKey, keyattest.SecurityTrustedEnvironment, playintegrity.LabelStrong)
	if _, err := r.svc.Reattest(ctx, d.ID, r.challenge(t, "production"), androidEvidence()); code(err) != plxerr.DeviceRevoked {
		t.Errorf("a revoked device: %v", err)
	}
}

// Verifies: SEC-006.
// Revoking is audited once, keeps the first reason, needs the permission
// to manage the app, and publishes the revoked key to the shared cache.
func TestRevoke(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	_, jwk := devicetest.NewKey(t)
	d, err := r.register(t, "development", "linux", jwk, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}

	viewer := auth.Principal{
		Identity:       auth.Identity{Kind: auth.KindUser, ID: r.owner.UserID, UserID: r.owner.UserID, Display: "Viewer"},
		OrganizationID: r.org, Permissions: []auth.Permission{auth.AppRead},
	}
	if _, err := r.svc.Revoke(ctx, viewer, d.ID, "no"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer revoked a device: %v", err)
	}
	outsider := auth.Principal{Identity: viewer.Identity, OrganizationID: r.org}
	if _, err := r.svc.Revoke(ctx, outsider, d.ID, "no"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a stranger revoked a device: %v", err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, "nope", ""); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid identifier: %v", err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, r.org, ""); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown device: %v", err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, d.ID, string(make([]byte, 300))); code(err) != plxerr.InvalidFormat {
		t.Errorf("a long reason: %v", err)
	}

	revoked, err := r.svc.Revoke(ctx, r.owner, d.ID, "lost phone")
	if err != nil || revoked.RevokedAt.IsZero() || !revoked.RevokedAt.Equal(r.now.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("Revoke = %+v, %v", revoked, err)
	}
	r.now = r.now.Add(time.Minute)
	repeat, err := r.svc.Revoke(ctx, r.owner, d.ID, "another reason")
	if err != nil || !repeat.RevokedAt.Equal(revoked.RevokedAt) {
		t.Errorf("a second revocation = %+v, %v", repeat, err)
	}
	var entries []audit.Entry
	if err := r.db.InTx(ctx, storage.Tenant{OrganizationID: r.org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		entries, err = r.log.List(ctx, tx, r.org, 0, 1000)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var revocations []audit.Entry
	for _, e := range entries {
		if e.Action == audit.DeviceRevoked {
			revocations = append(revocations, e)
		}
	}
	if len(revocations) != 1 || revocations[0].TargetID != d.ID || revocations[0].Detail != "lost phone" || revocations[0].Actor.ID != r.owner.UserID {
		t.Errorf("audit entries = %+v", revocations)
	}

	// The revocation reaches the shared cache, so every replica refuses the
	// key at once, and only the revoked device's key (SEC-006).
	if revokedKey, err := r.svc.IsRevoked(ctx, d.DPoPJKT); err != nil || !revokedKey {
		t.Errorf("IsRevoked(the revoked key) = %v, %v", revokedKey, err)
	}
	_, otherJWK := devicetest.NewKey(t)
	old, err := r.register(t, "development", "linux", otherJWK, device.KeyStorageSoftware, developmentEvidence())
	if err != nil {
		t.Fatal(err)
	}
	if revokedKey, err := r.svc.IsRevoked(ctx, old.DPoPJKT); err != nil || revokedKey {
		t.Errorf("IsRevoked(a trusted key) = %v, %v", revokedKey, err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, old.ID, "retired"); err != nil {
		t.Fatal(err)
	}
	if revokedKey, err := r.svc.IsRevoked(ctx, old.DPoPJKT); err != nil || !revokedKey {
		t.Errorf("IsRevoked(a second revoked key) = %v, %v", revokedKey, err)
	}
	// The cache is a fast path, not the record: once it is empty the
	// database still says the key is revoked, and a cache that fails
	// does not make a revoked device look trusted.
	if err := r.cache.Delete(ctx, "device:revoked:"+old.DPoPJKT); err != nil {
		t.Fatal(err)
	}
	if revokedKey, err := r.svc.IsRevoked(ctx, old.DPoPJKT); err != nil || revokedKey {
		t.Errorf("IsRevoked after the cache lost the key = %v, %v", revokedKey, err)
	}
	if _, err := r.svc.Revoke(ctx, r.owner, old.ID, "again"); err != nil {
		t.Fatal(err)
	}
	if revokedKey, err := r.svc.IsRevoked(ctx, old.DPoPJKT); err != nil || !revokedKey {
		t.Errorf("a repeated revocation publishes again: %v, %v", revokedKey, err)
	}
	got, err := r.svc.Get(ctx, r.owner, old.ID)
	if err != nil || got.RevokedAt.IsZero() {
		t.Errorf("Get = %+v, %v", got, err)
	}
}
