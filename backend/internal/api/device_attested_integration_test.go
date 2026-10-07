// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// deviceFakes are the attestation verifiers the world's device service
// uses; a test sets what they vouch for.
type deviceFakes struct {
	key   *devicetest.KeyAttestor
	play  *devicetest.IntegrityChecker
	apple *devicetest.AppAttestor
	// assert vouches for the assertions of iOS refreshes.
	assert *devicetest.AppAssertor
}

// vouchAndroid makes the fakes vouch for an Android key in a TEE with
// the given Play Integrity device labels.
func (f *deviceFakes) vouchAndroid(pub *ecdsa.PublicKey, labels ...playintegrity.DeviceLabel) {
	f.key.Result = keyattest.Result{
		AttestationLevel: keyattest.SecurityTrustedEnvironment, KeyMintLevel: keyattest.SecurityTrustedEnvironment,
		PackageName: "com.example.app", PublicKey: pub,
	}
	f.play.Verdict = playintegrity.Verdict{Device: labels, AppRecognised: true}
}

// challenge asks for a registration challenge, anonymously.
func (w *world) challenge(t *testing.T, app, env string) []byte {
	t.Helper()
	res := must(w.device.CreateRegistrationChallenge(context.Background(), connect.NewRequest(&pluxv1.CreateRegistrationChallengeRequest{AppId: app, Environment: env})))(t)
	if len(res.GetChallenge()) != 32 || res.GetExpiresAt() == nil {
		t.Fatalf("CreateRegistrationChallenge = %+v", res)
	}
	return res.GetChallenge()
}

// registerAttested registers anonymously with fresh challenge.
func (w *world) registerAttested(t *testing.T, app, platform string, jwk []byte, claim pluxv1.KeyStorage, e *pluxv1.AttestationEvidence) (*pluxv1.RegisterAttestedDeviceResponse, error) {
	t.Helper()
	res, err := w.device.RegisterAttestedDevice(context.Background(), connect.NewRequest(&pluxv1.RegisterAttestedDeviceRequest{
		AppId: app, Environment: "production", Platform: platform, OsVersion: "15", RuntimeVersion: "1.0.0", HostBuild: "42",
		Challenge: w.challenge(t, app, "production"), DpopPublicKeyJwk: jwk, KeyStorage: claim, Evidence: e,
	}))
	if err != nil {
		return nil, err //nolint:wrapcheck // the test reads the Connect error
	}
	return res.Msg, nil
}

func androidProto() *pluxv1.AttestationEvidence {
	return &pluxv1.AttestationEvidence{Evidence: &pluxv1.AttestationEvidence_Android{Android: &pluxv1.AndroidEvidence{
		KeyAttestationChain: [][]byte{{1}, {2}}, PlayIntegrityToken: "token",
	}}}
}

func iosProto() *pluxv1.AttestationEvidence {
	return &pluxv1.AttestationEvidence{Evidence: &pluxv1.AttestationEvidence_Ios{Ios: &pluxv1.IosEvidence{
		AppAttestKeyId: []byte("key"), AttestationObject: []byte("object"),
	}}}
}

func developmentProto() *pluxv1.AttestationEvidence {
	return &pluxv1.AttestationEvidence{Evidence: &pluxv1.AttestationEvidence_Development{Development: &pluxv1.DevelopmentEvidence{BuildId: "debug-1"}}}
}

// Verifies: SEC-001, SEC-003, SEC-005, SEC-006, SEC-007, SEC-008.
// Devices register anonymously over the API with Android, iOS and
// development evidence and get the level their evidence earns; refusals
// carry their Plux code; a duplicate key is refused; people see the
// attestation and revoke a device, once.
func TestAttestedDevicesOverTheAPI(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	admin, app, envs := w.releasedApp(t)

	// The registration calls take no credential, the refusals of the old
	// flow neither; revoking does.
	people := func(context.Context, api.Call, func(context.Context) error) error { return errors.New("not public") }
	auth := w.deviceAuth
	auth.People = people
	around := api.DeviceAuthentication(auth)
	for procedure, public := range map[string]bool{
		pluxv1connect.DeviceServiceCreateRegistrationChallengeProcedure: true,
		pluxv1connect.DeviceServiceRegisterAttestedDeviceProcedure:      true,
		pluxv1connect.DeviceServiceRegisterDeviceProcedure:              true,
		pluxv1connect.TokenServiceIssueDeviceTokenProcedure:             true,
		pluxv1connect.DeviceServiceRevokeDeviceProcedure:                false,
	} {
		err := around(ctx, api.Call{Procedure: procedure, Header: http.Header{}, ResponseHeader: http.Header{}}, func(context.Context) error { return nil })
		if (err == nil) != public {
			t.Errorf("%s: public = %v, error = %v", procedure, public, err)
		}
	}
	if _, err := w.device.CreateRegistrationChallenge(ctx, connect.NewRequest(&pluxv1.CreateRegistrationChallengeRequest{AppId: app, Environment: "nope"})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("a challenge for an unknown environment: %v", err)
	}

	// Android.
	key, jwk := devicetest.NewKey(t)
	w.fakes.vouchAndroid(&key.PublicKey, playintegrity.LabelBasic, playintegrity.LabelDevice)
	android, err := w.registerAttested(t, app, "android", jwk, pluxv1.KeyStorage_KEY_STORAGE_TEE, androidProto())
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := dpop.Thumbprint(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	d := android.GetDevice()
	if d.GetAssuranceLevel() != "AL2" || d.GetKeyStorage() != pluxv1.KeyStorage_KEY_STORAGE_TEE || d.GetDpopJkt() != jkt ||
		d.GetAttestation().GetProvider() != "play_integrity" || d.GetAttestation().GetVerifiedAt() == nil ||
		len(d.GetAttestation().GetVerdicts()) != 2 || d.GetRevokedAt() != nil || d.GetEnvironmentId() != envs["production"] {
		t.Fatalf("RegisterAttestedDevice = %+v", d)
	}

	// The same key again, and a key the chain does not attest.
	if _, err := w.registerAttested(t, app, "android", jwk, pluxv1.KeyStorage_KEY_STORAGE_TEE, androidProto()); reasonOf(t, err) != "ATTESTATION_FAILED" {
		t.Errorf("a duplicate key: %v", err)
	}
	_, other := devicetest.NewKey(t)
	if _, err := w.registerAttested(t, app, "android", other, pluxv1.KeyStorage_KEY_STORAGE_TEE, androidProto()); reasonOf(t, err) != "ATTESTATION_FAILED" {
		t.Errorf("a leaf that is not the DPoP key: %v", err)
	}
	if _, err := w.registerAttested(t, app, "android", []byte("{"), pluxv1.KeyStorage_KEY_STORAGE_TEE, androidProto()); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a malformed key: %v", err)
	}
	if _, err := w.registerAttested(t, app, "android", other, pluxv1.KeyStorage_KEY_STORAGE_TEE, nil); reasonOf(t, err) != "ATTESTATION_FAILED" {
		t.Errorf("no evidence: %v", err)
	}

	// A challenge works once.
	_, devJWK := devicetest.NewKey(t)
	challenge := w.challenge(t, app, "development")
	register := &pluxv1.RegisterAttestedDeviceRequest{
		AppId: app, Environment: "development", Platform: "linux", Challenge: challenge, DpopPublicKeyJwk: devJWK,
		KeyStorage: pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE, Evidence: developmentProto(),
	}
	dev := must(w.device.RegisterAttestedDevice(ctx, connect.NewRequest(register)))(t).GetDevice()
	if dev.GetAssuranceLevel() != "AL0" || dev.GetAttestation().GetProvider() != "development" || dev.GetKeyStorage() != pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE {
		t.Errorf("a development device = %+v", dev)
	}
	register.DpopPublicKeyJwk = other
	if _, err := w.device.RegisterAttestedDevice(ctx, connect.NewRequest(register)); reasonOf(t, err) != "REGISTRATION_CHALLENGE_INVALID" {
		t.Errorf("a reused challenge: %v", err)
	}
	if _, err := w.registerAttested(t, app, "linux", other, pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE, developmentProto()); reasonOf(t, err) != "DEV_PROVIDER_IN_PRODUCTION" {
		t.Errorf("the development provider in production: %v", err)
	}

	// iOS.
	appKey, _ := devicetest.NewKey(t)
	w.fakes.apple.Attestation = appattest.Attestation{PublicKey: &appKey.PublicKey, Receipt: []byte("receipt")}
	_, iosJWK := devicetest.NewKey(t)
	ios, err := w.registerAttested(t, app, "ios", iosJWK, pluxv1.KeyStorage_KEY_STORAGE_SECURE_ENCLAVE, iosProto())
	if err != nil || ios.GetDevice().GetAssuranceLevel() != "AL2" || ios.GetDevice().GetAttestation().GetProvider() != "app_attest" ||
		ios.GetDevice().GetKeyStorage() != pluxv1.KeyStorage_KEY_STORAGE_SECURE_ENCLAVE {
		t.Errorf("an iOS device = %+v, %v", ios, err)
	}

	// People see the attestation, and revoke the device once.
	got := must(w.device.GetDevice(ctx, req(admin, &pluxv1.GetDeviceRequest{Id: d.GetId()})))(t).GetDevice()
	if got.GetAttestation().GetProvider() != "play_integrity" || got.GetRevokedAt() != nil {
		t.Errorf("GetDevice = %+v", got)
	}
	if _, err := w.device.RevokeDevice(ctx, connect.NewRequest(&pluxv1.RevokeDeviceRequest{DeviceId: d.GetId()})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous revocation: %v", err)
	}
	revoked := must(w.device.RevokeDevice(ctx, req(admin, &pluxv1.RevokeDeviceRequest{DeviceId: d.GetId(), Reason: "stolen"})))(t).GetDevice()
	if revoked.GetRevokedAt() == nil || revoked.GetId() != d.GetId() {
		t.Fatalf("RevokeDevice = %+v", revoked)
	}
	again := must(w.device.RevokeDevice(ctx, req(admin, &pluxv1.RevokeDeviceRequest{DeviceId: d.GetId(), Reason: "again"})))(t).GetDevice()
	if !again.GetRevokedAt().AsTime().Equal(revoked.GetRevokedAt().AsTime()) {
		t.Errorf("a second revocation moved the time: %v -> %v", revoked.GetRevokedAt(), again.GetRevokedAt())
	}
	if _, err := w.device.RevokeDevice(ctx, req(admin, &pluxv1.RevokeDeviceRequest{DeviceId: envs["production"]})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("revoking an unknown device: %v", err)
	}
	// Re-attesting is authenticated by a DPoP proof, not by a person.
	if _, err := w.device.ReattestDevice(ctx, req(admin, &pluxv1.ReattestDeviceRequest{DeviceId: d.GetId()})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("ReattestDevice without a proof: %v", err)
	}
}

// reasonOf returns the ErrorInfo reason of a Connect error.
func reasonOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	return errorInfo(t, err).GetReason()
}
