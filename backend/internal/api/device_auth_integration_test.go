// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const manifestProcedure = pluxv1connect.ManifestServiceGetManifestProcedure

// brokenCache fails the atomic set the replay check relies on.
type brokenCache struct{ cache.Cache }

func (brokenCache) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("the cache is down")
}

// authFixture is a world with an app, an authenticated Android device and
// the device authentication in front of a GetManifest call.
type authFixture struct {
	w      *world
	admin  caller
	app    string
	envs   map[string]string
	dev    *testDevice
	around api.Around
}

func newAuthFixture(t *testing.T, tune func(*api.DeviceAuth)) *authFixture {
	t.Helper()
	w := newWorld(t)
	admin, app, envs := w.appOnly(t)
	dev := w.registerAndroid(t, app, "1.0.0")
	dev.refresh(t)
	a := w.deviceAuth
	a.People = func(context.Context, api.Call, func(context.Context) error) error {
		return errors.New("not a device call")
	}
	if tune != nil {
		tune(&a)
	}
	return &authFixture{w: w, admin: admin, app: app, envs: envs, dev: dev, around: api.DeviceAuthentication(a)}
}

// run sends a GetManifest call with a header through the authentication
// and returns the call, the identity the handler saw and the error.
func (f *authFixture) run(h http.Header) (api.Call, device.Identity, error) {
	c := api.Call{Procedure: manifestProcedure, Header: h, ResponseHeader: http.Header{}}
	var id device.Identity
	err := f.around(context.Background(), c, func(ctx context.Context) error {
		id, _ = api.DeviceFrom(ctx)
		return nil
	})
	return c, id, err
}

// header builds the headers of a device call.
func header(auth, proof string) http.Header {
	h := http.Header{}
	if auth != "" {
		h.Set("Authorization", auth)
	}
	if proof != "" {
		h.Set("DPoP", proof)
	}
	return h
}

// proofFor signs a proof for a procedure with the given token, key and
// nonce, adjusted by tweak.
func (f *authFixture) proofFor(t *testing.T, key *ecdsa.PrivateKey, token, nonce string, tweak func(*devicetest.Proof)) string {
	t.Helper()
	p := devicetest.Proof{Method: "POST", URL: f.w.base + manifestProcedure, AccessToken: token, Nonce: nonce}
	if tweak != nil {
		tweak(&p)
	}
	return devicetest.SignProof(t, key, p)
}

// Verifies: SEC-021, SEC-022, SEC-023, SEC-024, SEC-006, SEC-020.
// Every refusal of a device call carries the nonce and a challenge that
// names what failed, in the order ADR-0012 sets; a good call carries the
// nonce too and puts the device, from its token, in the context.
func TestDeviceAuthenticationMiddleware(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, nil)
	nonce := f.w.deviceAuth.Nonces.Current()
	token := f.dev.token
	good := func(tweak func(*devicetest.Proof)) http.Header {
		return header("DPoP "+token, f.proofFor(t, f.dev.key, token, nonce, tweak))
	}
	stranger, _ := devicetest.NewKey(t)
	issue := func(c devtoken.Claims) string {
		c.DeviceID, c.AppID, c.OrganizationID, c.Assurance = f.dev.id, f.app, f.admin.org, "AL2"
		if c.JKT == "" {
			c.JKT = f.dev.jkt
		}
		tok, _, err := f.w.issuer.Issue(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	wrongClass := issue(devtoken.Claims{Environment: f.envs["production"], Production: false})
	unknownEnv := issue(devtoken.Claims{Environment: f.admin.org, Production: true})
	otherBinding := issue(devtoken.Claims{Environment: f.envs["production"], Production: true, JKT: "another-thumbprint"})

	const (
		useNonce     = `DPoP error="use_dpop_nonce"`
		invalidProof = `DPoP error="invalid_dpop_proof"`
		invalidToken = `DPoP error="invalid_token"` //nolint:gosec // G101: a WWW-Authenticate challenge, not a credential
	)
	for _, tc := range []struct {
		name      string
		h         http.Header
		want      plxerr.Code
		challenge string
	}{
		{"no credential", header("", ""), plxerr.AuthenticationRequired, "DPoP"},
		{"a bearer token", header("Bearer "+token, good(nil).Get("DPoP")), plxerr.AccessTokenInvalid, invalidToken},
		{"an empty token", header("DPoP ", good(nil).Get("DPoP")), plxerr.AccessTokenInvalid, invalidToken},
		{"a token with a space", header("DPoP a b", good(nil).Get("DPoP")), plxerr.AccessTokenInvalid, invalidToken},
		{"no proof", header("DPoP "+token, ""), plxerr.DPoPProofInvalid, invalidProof},
		{"a garbage token", header("DPoP garbage", good(nil).Get("DPoP")), plxerr.AccessTokenInvalid, invalidToken},
		{"a token of the wrong class", header("DPoP "+wrongClass, f.proofFor(t, f.dev.key, wrongClass, nonce, nil)), plxerr.AccessTokenInvalid, invalidToken},
		{"a token for an unknown environment", header("DPoP "+unknownEnv, f.proofFor(t, f.dev.key, unknownEnv, nonce, nil)), plxerr.AccessTokenInvalid, invalidToken},
		{"a garbage proof", header("DPoP "+token, "garbage"), plxerr.DPoPProofInvalid, invalidProof},
		{"a proof for another method", good(func(p *devicetest.Proof) { p.Method = "GET" }), plxerr.DPoPProofInvalid, invalidProof},
		{"a proof for another procedure", good(func(p *devicetest.Proof) { p.URL = f.w.base + pluxv1connect.DeviceServiceReportInstalledProcedure }), plxerr.DPoPProofInvalid, invalidProof},
		{"a proof not bound to the token", good(func(p *devicetest.Proof) { p.AccessToken = "" }), plxerr.DPoPProofInvalid, invalidProof},
		{"a proof from long ago", good(func(p *devicetest.Proof) { p.IssuedAt = time.Now().Add(-10 * time.Minute) }), plxerr.DPoPProofInvalid, invalidProof},
		{"a proof from another key", header("DPoP "+token, f.proofFor(t, stranger, token, nonce, nil)), plxerr.TokenBindingMismatch, invalidProof},
		{"a token bound to another key", header("DPoP "+otherBinding, f.proofFor(t, f.dev.key, otherBinding, nonce, nil)), plxerr.TokenBindingMismatch, invalidProof},
		{"no nonce", good(func(p *devicetest.Proof) { p.Nonce = "" }), plxerr.DPoPNonceRequired, useNonce},
		{"a nonce the server never issued", good(func(p *devicetest.Proof) { p.Nonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" }), plxerr.DPoPNonceRequired, useNonce},
	} {
		c, id, err := f.run(tc.h)
		if code(err) != tc.want || c.ResponseHeader.Get("WWW-Authenticate") != tc.challenge || id != (device.Identity{}) {
			t.Errorf("%s: error %v, challenge %q, identity %+v", tc.name, err, c.ResponseHeader.Get("WWW-Authenticate"), id)
		}
		if c.ResponseHeader.Get("DPoP-Nonce") != nonce {
			t.Errorf("%s: the refusal carries nonce %q", tc.name, c.ResponseHeader.Get("DPoP-Nonce"))
		}
	}
	if _, _, err := f.run(http.Header{"Authorization": {"DPoP " + token, "DPoP " + token}, "Dpop": {good(nil).Get("DPoP")}}); code(err) != plxerr.AccessTokenInvalid {
		t.Errorf("two Authorization headers: %v", err)
	}
	two := good(nil)
	two.Add("DPoP", f.proofFor(t, f.dev.key, token, nonce, nil))
	if _, _, err := f.run(two); code(err) != plxerr.DPoPProofInvalid {
		t.Errorf("two proofs: %v", err)
	}

	// A good call puts the device from the token in the context, and a
	// proof is accepted once.
	h := good(nil)
	c, id, err := f.run(h)
	want := device.Identity{DeviceID: f.dev.id, OrganizationID: f.admin.org, AppID: f.app, EnvironmentID: f.envs["production"], HostBuild: "42"}
	if err != nil || id != want || c.ResponseHeader.Get("DPoP-Nonce") != nonce || c.ResponseHeader.Get("WWW-Authenticate") != "" {
		t.Fatalf("a good call: %+v, %v, headers %v", id, err, c.ResponseHeader)
	}
	c, _, err = f.run(h)
	if code(err) != plxerr.DPoPReplay || c.ResponseHeader.Get("WWW-Authenticate") != invalidProof {
		t.Errorf("a replayed proof: %v, challenge %q", err, c.ResponseHeader.Get("WWW-Authenticate"))
	}

	// A revoked device is refused at once, with a challenge about the token.
	must(f.w.device.RevokeDevice(context.Background(), req(f.admin, &pluxv1.RevokeDeviceRequest{DeviceId: f.dev.id, Reason: "stolen"})))(t)
	c, _, err = f.run(good(nil))
	if code(err) != plxerr.DeviceRevoked || c.ResponseHeader.Get("WWW-Authenticate") != invalidToken {
		t.Errorf("a revoked device: %v, challenge %q", err, c.ResponseHeader.Get("WWW-Authenticate"))
	}
}

// Verifies: SEC-022, SEC-023.
// A device token is not a credential for people's procedures, a proof
// is not needed for them, and GetRootKeys serves both kinds of caller.
func TestDeviceAuthenticationRouting(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, nil)
	people := 0
	a := f.w.deviceAuth
	a.People = func(context.Context, api.Call, func(context.Context) error) error { people++; return nil }
	around := api.DeviceAuthentication(a)
	call := func(procedure string, h http.Header) error {
		return around(context.Background(), api.Call{Procedure: procedure, Header: h, ResponseHeader: http.Header{}}, func(context.Context) error { return nil })
	}
	if err := call(pluxv1connect.AppServiceListAppsProcedure, header("DPoP "+f.dev.token, "")); code(err) != plxerr.PermissionDenied {
		t.Errorf("a device token on a people's procedure: %v", err)
	}
	if err := call(pluxv1connect.AppServiceListAppsProcedure, header("Bearer plux_pat_x", "")); err != nil || people != 1 {
		t.Errorf("a person's call: %v (handed over %d times)", err, people)
	}
	if err := call(pluxv1connect.ManifestServiceGetRootKeysProcedure, header("Bearer plux_pat_x", "")); err != nil || people != 2 {
		t.Errorf("GetRootKeys for a person: %v (handed over %d times)", err, people)
	}
	if err := call(pluxv1connect.ManifestServiceGetRootKeysProcedure, header("DPoP "+f.dev.token, "")); code(err) != plxerr.DPoPProofInvalid {
		t.Errorf("GetRootKeys for a device needs a proof: %v", err)
	}
	if err := call(manifestProcedure, header("Bearer plux_pat_x", "")); code(err) != plxerr.AccessTokenInvalid {
		t.Errorf("a person's token on a device procedure: %v", err)
	}
}

// Verifies: SEC-023, SEC-022.
// While the shared replay cache is down, a replica under the standard
// profile remembers proofs itself and applies the narrower window; under
// the maximum profile it refuses.
func TestDeviceAuthenticationDegradedReplayCache(t *testing.T) {
	t.Parallel()
	var warnings int
	broken := brokenCache{Cache: cache.NewMemory(nil)}
	f := newAuthFixture(t, func(a *api.DeviceAuth) {
		a.Replay = dpop.NewReplay(broken, dpop.PerReplica, 100, nil, func(error) { warnings++ })
	})
	nonce := f.w.deviceAuth.Nonces.Current()
	token := f.dev.token
	send := func(age time.Duration) (api.Call, error) {
		proof := f.proofFor(t, f.dev.key, token, nonce, func(p *devicetest.Proof) { p.IssuedAt = time.Now().Add(-age) })
		c, _, err := f.run(header("DPoP "+token, proof))
		return c, err
	}
	if _, err := send(5 * time.Second); err != nil {
		t.Errorf("a recent proof: %v", err)
	}
	if warnings == 0 {
		t.Error("the degraded cache was not reported")
	}
	if _, err := send(40 * time.Second); code(err) != plxerr.DPoPProofInvalid {
		t.Errorf("a proof outside the fallback window: %v", err)
	}
	h := header("DPoP "+token, f.proofFor(t, f.dev.key, token, nonce, nil))
	if _, _, err := f.run(h); err != nil {
		t.Fatalf("a fresh proof: %v", err)
	}
	if _, _, err := f.run(h); code(err) != plxerr.DPoPReplay {
		t.Errorf("a replay on the same replica: %v", err)
	}

	closed := newAuthFixture(t, func(a *api.DeviceAuth) {
		a.Replay = dpop.NewReplay(broken, dpop.FailClosed, 100, nil, nil)
	})
	proof := closed.proofFor(t, closed.dev.key, closed.dev.token, closed.w.deviceAuth.Nonces.Current(), nil)
	if _, _, err := closed.run(header("DPoP "+closed.dev.token, proof)); code(err) != plxerr.ReplayCacheUnavailable {
		t.Errorf("a failing cache that fails closed: %v", err)
	}
}

// Verifies: SEC-024, SEC-021.
// Over HTTP a call without the server's nonce is refused with 401 and
// the nonce to use, and the retry succeeds, carrying a nonce again.
func TestDeviceCallsOverHTTPNegotiateTheNonce(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, nil)
	ctx := context.Background()
	dev := f.dev
	dev.nonce = ""
	_, err := f.w.device.ReportInstalled(ctx, bound(t, dev, pluxv1connect.DeviceServiceReportInstalledProcedure, &pluxv1.ReportInstalledRequest{ReleaseSequence: 0}))
	if codeOf(err) != connect.CodeUnauthenticated || challengeOf(err) != `DPoP error="use_dpop_nonce"` || nonceOf(err) == "" {
		t.Fatalf("a call without a nonce: %v (challenge %q, nonce %q)", err, challengeOf(err), nonceOf(err))
	}
	dev.nonce = nonceOf(err)
	res, err := f.w.device.ReportInstalled(ctx, bound(t, dev, pluxv1connect.DeviceServiceReportInstalledProcedure, &pluxv1.ReportInstalledRequest{ReleaseSequence: 0}))
	if err != nil || res.Header().Get("DPoP-Nonce") == "" {
		t.Fatalf("the retry: %v (nonce %q)", err, res.Header().Get("DPoP-Nonce"))
	}
	// An error from the handler carries the nonce as well.
	_, err = f.w.device.ReportInstalled(ctx, bound(t, dev, pluxv1connect.DeviceServiceReportInstalledProcedure, &pluxv1.ReportInstalledRequest{DeviceId: f.app}))
	if codeOf(err) != connect.CodePermissionDenied || nonceOf(err) == "" {
		t.Errorf("a refusal by the handler: %v (nonce %q)", err, nonceOf(err))
	}
}

// Verifies: SEC-025, SEC-021, SEC-006.
// A device refreshes with its proof alone: the proof's key must be the
// device's, a proof works once, a revoked device is refused, and an iOS
// device must add an App Attest assertion.
func TestRefreshOverTheAPI(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, nil)
	w, dev, ctx := f.w, f.dev, context.Background()
	stranger := &testDevice{w: w, id: dev.id, key: must2(devicetest.NewKey(t)), nonce: dev.nonce}

	_, err := w.token.RefreshDeviceToken(ctx, proven(t, stranger, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, &pluxv1.RefreshDeviceTokenRequest{DeviceId: dev.id}))
	if codeOf(err) != connect.CodeUnauthenticated || reasonOf(t, err) != "TOKEN_BINDING_MISMATCH" {
		t.Errorf("a proof from another key: %v", err)
	}
	r := proven(t, dev, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, &pluxv1.RefreshDeviceTokenRequest{DeviceId: dev.id})
	r.Header().Set("Authorization", "Bearer an-expired-token")
	if _, err := w.token.RefreshDeviceToken(ctx, r); err != nil {
		t.Fatalf("a refresh: %v", err)
	}
	if _, err := w.token.RefreshDeviceToken(ctx, r); reasonOf(t, err) != "DPOP_REPLAY" {
		t.Errorf("a replayed refresh: %v", err)
	}
	if _, err := w.token.RefreshDeviceToken(ctx, connect.NewRequest(&pluxv1.RefreshDeviceTokenRequest{DeviceId: dev.id})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a refresh without a proof: %v", err)
	}
	if _, err := w.token.RefreshDeviceToken(ctx, proven(t, dev, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, &pluxv1.RefreshDeviceTokenRequest{DeviceId: "nope"})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a refresh for an invalid device: %v", err)
	}

	// iOS: the assertion is required and its counter must advance.
	w.vouchIOS(t)
	iosKey, iosJWK := devicetest.NewKey(t)
	registered, err := w.registerAttested(t, f.app, "ios", iosJWK, pluxv1.KeyStorage_KEY_STORAGE_SECURE_ENCLAVE, iosProto())
	if err != nil {
		t.Fatal(err)
	}
	phone := &testDevice{w: w, id: registered.GetDevice().GetId(), key: iosKey, nonce: dev.nonce}
	if _, err := w.token.RefreshDeviceToken(ctx, proven(t, phone, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, &pluxv1.RefreshDeviceTokenRequest{DeviceId: phone.id})); reasonOf(t, err) != "ATTESTATION_FAILED" {
		t.Errorf("an iOS refresh without an assertion: %v", err)
	}
	w.fakes.assert.Counter = 1
	assertion := &pluxv1.RefreshDeviceTokenRequest{DeviceId: phone.id, Evidence: &pluxv1.RefreshDeviceTokenRequest_AppAttestAssertion{AppAttestAssertion: []byte("assertion")}}
	ios := proven(t, phone, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, assertion)
	res, err := w.token.RefreshDeviceToken(ctx, ios)
	if err != nil || res.Msg.GetAccessToken() == "" {
		t.Fatalf("an iOS refresh with an assertion: %v", err)
	}
	if want := sha256Proof(ios.Header().Get("DPoP")); w.fakes.assert.ClientDataHash != want {
		t.Error("the assertion was not checked against the hash of the proof")
	}
	if _, err := w.token.RefreshDeviceToken(ctx, proven(t, phone, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, assertion)); reasonOf(t, err) != "ATTESTATION_FAILED" {
		t.Errorf("an assertion counter that did not advance: %v", err)
	}

	// Revoked: no new token, and the one in hand stops working.
	dev.refresh(t)
	must(w.device.RevokeDevice(ctx, req(f.admin, &pluxv1.RevokeDeviceRequest{DeviceId: dev.id})))(t)
	if _, err := w.token.RefreshDeviceToken(ctx, proven(t, dev, pluxv1connect.TokenServiceRefreshDeviceTokenProcedure, &pluxv1.RefreshDeviceTokenRequest{DeviceId: dev.id})); reasonOf(t, err) != "DEVICE_REVOKED" {
		t.Errorf("a refresh by a revoked device: %v", err)
	}
	_, err = w.device.ReportInstalled(ctx, bound(t, dev, pluxv1connect.DeviceServiceReportInstalledProcedure, &pluxv1.ReportInstalledRequest{}))
	if reasonOf(t, err) != "DEVICE_REVOKED" || challengeOf(err) != `DPoP error="invalid_token"` {
		t.Errorf("the token of a revoked device: %v", err)
	}
}

// Verifies: SEC-006, SEC-021.
// A device re-attests with a proof of its own key and a fresh challenge;
// another key, a missing proof and a revoked device are refused.
func TestReattestOverTheAPI(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	admin, app, _ := w.appOnly(t)
	ctx := context.Background()
	dev := w.registerDevelopment(t, app)
	dev.nonce = w.deviceAuth.Nonces.Current()
	stranger := &testDevice{w: w, id: dev.id, key: must2(devicetest.NewKey(t)), nonce: dev.nonce}
	body := func() *pluxv1.ReattestDeviceRequest {
		return &pluxv1.ReattestDeviceRequest{DeviceId: dev.id, Challenge: w.challenge(t, app, "development"), Evidence: developmentProto()}
	}
	procedure := pluxv1connect.DeviceServiceReattestDeviceProcedure

	if _, err := w.device.ReattestDevice(ctx, proven(t, stranger, procedure, body())); reasonOf(t, err) != "TOKEN_BINDING_MISMATCH" {
		t.Errorf("another key: %v", err)
	}
	if _, err := w.device.ReattestDevice(ctx, connect.NewRequest(body())); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no proof: %v", err)
	}
	before := must(w.device.GetDevice(ctx, req(admin, &pluxv1.GetDeviceRequest{Id: dev.id})))(t).GetDevice().GetAttestation().GetVerifiedAt().AsTime()
	time.Sleep(10 * time.Millisecond)
	res := must(w.device.ReattestDevice(ctx, proven(t, dev, procedure, body())))(t)
	if res.GetDevice().GetId() != dev.id || !res.GetDevice().GetAttestation().GetVerifiedAt().AsTime().After(before) {
		t.Errorf("ReattestDevice = %+v (attested before at %v)", res.GetDevice(), before)
	}
	must(w.device.RevokeDevice(ctx, req(admin, &pluxv1.RevokeDeviceRequest{DeviceId: dev.id})))(t)
	if _, err := w.device.ReattestDevice(ctx, proven(t, dev, procedure, body())); reasonOf(t, err) != "DEVICE_REVOKED" {
		t.Errorf("a revoked device: %v", err)
	}
}
