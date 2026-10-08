// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// testDevice is a registered device with its key and what a client keeps
// between calls: the access token and the latest server nonce.
type testDevice struct {
	w     *world
	id    string
	key   *ecdsa.PrivateKey
	jkt   string
	token string
	nonce string
}

// registerAndroid registers an Android device with a hardware key and the
// given runtime version in the production environment of the app.
func (w *world) registerAndroid(t *testing.T, app, runtime string) *testDevice {
	t.Helper()
	key, jwk := devicetest.NewKey(t)
	w.fakes.vouchAndroid(&key.PublicKey, playintegrity.LabelBasic, playintegrity.LabelDevice)
	res := must(w.device.RegisterAttestedDevice(t.Context(), connect.NewRequest(&pluxv1.RegisterAttestedDeviceRequest{
		AppId: app, Environment: "production", Platform: "android", OsVersion: "15", RuntimeVersion: runtime, HostBuild: "42",
		Challenge: w.challenge(t, app, "production"), DpopPublicKeyJwk: jwk,
		KeyStorage: pluxv1.KeyStorage_KEY_STORAGE_TEE, Evidence: androidProto(),
	})))(t)
	return &testDevice{w: w, id: res.GetDevice().GetId(), key: key, jkt: res.GetDevice().GetDpopJkt()}
}

// registerDevelopment registers a development-build device in the
// development environment of the app.
func (w *world) registerDevelopment(t *testing.T, app string) *testDevice {
	t.Helper()
	key, jwk := devicetest.NewKey(t)
	res := must(w.device.RegisterAttestedDevice(t.Context(), connect.NewRequest(&pluxv1.RegisterAttestedDeviceRequest{
		AppId: app, Environment: "development", Platform: "linux", OsVersion: "6", RuntimeVersion: "1.0.0", HostBuild: "42",
		Challenge: w.challenge(t, app, "development"), DpopPublicKeyJwk: jwk,
		KeyStorage: pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE, Evidence: developmentProto(),
	})))(t)
	return &testDevice{w: w, id: res.GetDevice().GetId(), key: key, jkt: res.GetDevice().GetDpopJkt()}
}

// bound builds a request to a procedure that carries the device's token
// and a proof bound to it.
func bound[T any](t *testing.T, d *testDevice, procedure string, msg *T) *connect.Request[T] {
	t.Helper()
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "DPoP "+d.token)
	r.Header().Set("DPoP", devicetest.SignProof(t, d.key, devicetest.Proof{
		Method: "POST", URL: d.w.base + procedure, AccessToken: d.token, Nonce: d.nonce,
	}))
	return r
}

// proven builds a request to a procedure that carries a proof and no
// token, as a refresh does.
func proven[T any](t *testing.T, d *testDevice, procedure string, msg *T) *connect.Request[T] {
	t.Helper()
	r := connect.NewRequest(msg)
	r.Header().Set("DPoP", devicetest.SignProof(t, d.key, devicetest.Proof{
		Method: "POST", URL: d.w.base + procedure, Nonce: d.nonce,
	}))
	return r
}

// nonceOf returns the DPoP-Nonce a refusal carried.
func nonceOf(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Meta().Get("DPoP-Nonce")
	}
	return ""
}

// challengeOf returns the WWW-Authenticate challenge a refusal carried.
func challengeOf(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Meta().Get("WWW-Authenticate")
	}
	return ""
}

// refresh obtains an access token the way the runtime does: the first
// attempt carries no nonce and is told to use the server's, the second
// succeeds.
func (d *testDevice) refresh(t *testing.T) *pluxv1.RefreshDeviceTokenResponse {
	t.Helper()
	msg := func() *pluxv1.RefreshDeviceTokenRequest { return &pluxv1.RefreshDeviceTokenRequest{DeviceId: d.id} }
	d.nonce = ""
	_, err := d.w.token.RefreshDeviceToken(t.Context(), proven(t, d, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, msg()))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a refresh without a nonce: %v", err)
	}
	d.nonce = nonceOf(err)
	if d.nonce == "" {
		t.Fatal("the refusal carried no DPoP-Nonce")
	}
	res := must(d.w.token.RefreshDeviceToken(t.Context(), proven(t, d, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, msg())))(t)
	d.token = res.GetAccessToken()
	return res
}

// vouchIOS makes the fakes vouch for an iOS App Attest key.
func (w *world) vouchIOS(t *testing.T) {
	t.Helper()
	appKey, _ := devicetest.NewKey(t)
	w.fakes.apple.Attestation.PublicKey = &appKey.PublicKey
	w.fakes.apple.Attestation.Receipt = []byte("receipt")
}

// must2 returns the first of two results, for a key whose JWK is not needed.
func must2(key *ecdsa.PrivateKey, _ []byte) *ecdsa.PrivateKey { return key }

// sha256Proof is the hash an App Attest assertion signs.
func sha256Proof(proof string) [32]byte { return sha256.Sum256([]byte(proof)) }
