// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/ecdsa"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

const (
	// dpopIatWindow is the world's window for a proof's iat (dpopIatWindow).
	dpopIatWindow = time.Minute
	// tokenLifetime is the lifetime of the access tokens the world issues.
	tokenLifetime = 5 * time.Minute
	// nonceRotation is the rotation of the world's DPoP nonces.
	nonceRotation = 5 * time.Minute

	reportProcedure = pluxv1connect.DeviceServiceReportInstalledProcedure
)

// stepClock is a clock that stands still until the test advances it.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Now().Truncate(time.Second)}
}

// Now returns the clock's time.
func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorder is a round tripper that remembers the last HTTP response, so
// that a test can read the status and the headers the client library hides.
type recorder struct {
	mu     sync.Mutex
	status int
	header http.Header
}

// RoundTrip sends the request and records the response.
func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // the transport's error is the test's
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.header = res.StatusCode, res.Header.Clone()
	return res, nil
}

// last returns the status and headers of the last response.
func (r *recorder) last() (int, http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.header
}

// outcome is what a call came to: the error and the HTTP response.
type outcome struct {
	err    error
	status int
	header http.Header
}

// rejection is what a refused call must look like on the wire.
type rejection struct {
	code      plxerr.Code
	connect   connect.Code
	status    int
	challenge string
}

const (
	challengeNonce = `DPoP error="use_dpop_nonce"`
	challengeProof = `DPoP error="invalid_dpop_proof"`
	challengeToken = `DPoP error="invalid_token"` //nolint:gosec // G101: a WWW-Authenticate challenge, not a credential
)

// refusedAs is the rejection of a device call that fails authentication.
func refusedAs(code plxerr.Code, challenge string) rejection {
	return rejection{code: code, connect: connect.CodeUnauthenticated, status: http.StatusUnauthorized, challenge: challenge}
}

// negativeFixture is a development app with two registered devices, a
// clock the test advances and the real middleware and handlers served over
// HTTP.
type negativeFixture struct {
	w      *world
	clock  *stepClock
	rec    *recorder
	client pluxv1connect.DeviceServiceClient
	admin  caller
	app    string
	envs   map[string]string
	dev    *testDevice
	// strict holds the environments that run under the strict profile.
	strict *sync.Map
}

func newNegativeFixture(t *testing.T) *negativeFixture {
	t.Helper()
	f := &negativeFixture{clock: newStepClock(), rec: &recorder{}, strict: &sync.Map{}}
	f.w = newWorldWith(t, worldConfig{
		Now: f.clock.Now,
		Profiles: func(_ context.Context, _, env string) (settings.Profile, error) {
			if _, ok := f.strict.Load(env); ok {
				return settings.Strict, nil
			}
			return settings.Standard, nil
		},
	})
	f.client = pluxv1connect.NewDeviceServiceClient(&http.Client{Transport: f.rec}, f.w.base)
	f.admin, f.app, f.envs = f.w.appOnly(t)
	f.dev = f.w.registerDevelopment(t, f.app)
	f.refresh(t, f.dev)
	return f
}

// nonce is the server's current nonce.
func (f *negativeFixture) nonce() string { return f.w.deviceAuth.Nonces.Current() }

// refresh gets the device an access token the way the runtime does, with
// proofs dated by the test's clock.
func (f *negativeFixture) refresh(t *testing.T, d *testDevice) {
	t.Helper()
	r := connect.NewRequest(&pluxv1.RefreshDeviceTokenRequest{DeviceId: d.id})
	r.Header().Set("DPoP", devicetest.SignProof(t, d.key, devicetest.Proof{
		Method: "POST", URL: f.w.base + pluxv1connect.TokenServiceRefreshDeviceTokenProcedure,
		Nonce: f.nonce(), IssuedAt: f.clock.Now(),
	}))
	d.token = must(f.w.token.RefreshDeviceToken(context.Background(), r))(t).GetAccessToken()
}

// request builds a ReportInstalled call with a token and a proof by key,
// adjusted by tweak.
func (f *negativeFixture) request(t *testing.T, key *ecdsa.PrivateKey, token string, tweak func(*devicetest.Proof)) *connect.Request[pluxv1.ReportInstalledRequest] {
	t.Helper()
	p := devicetest.Proof{
		Method: "POST", URL: f.w.base + reportProcedure, AccessToken: token,
		Nonce: f.nonce(), IssuedAt: f.clock.Now(),
	}
	if tweak != nil {
		tweak(&p)
	}
	r := connect.NewRequest(&pluxv1.ReportInstalledRequest{ReleaseSequence: 0})
	r.Header().Set("Authorization", "DPoP "+token)
	r.Header().Set("DPoP", devicetest.SignProof(t, key, p))
	return r
}

// send makes the call through the real middleware and handler.
func (f *negativeFixture) send(r *connect.Request[pluxv1.ReportInstalledRequest]) outcome {
	_, err := f.client.ReportInstalled(context.Background(), r)
	status, header := f.rec.last()
	return outcome{err: err, status: status, header: header}
}

// accepted fails the test unless the call went through.
func accepted(t *testing.T, got outcome) {
	t.Helper()
	if got.err != nil || got.status != http.StatusOK {
		t.Fatalf("the call was refused: %v (HTTP %d)", got.err, got.status)
	}
}

// refused fails the test unless the call was refused as want says: the
// Plux code in the error detail, the Connect code, the HTTP status and the
// challenge.
func (f *negativeFixture) refused(t *testing.T, got outcome, want rejection) {
	t.Helper()
	if got.err == nil {
		t.Fatalf("the call was accepted, want %s", want.code)
	}
	if info := errorInfo(t, got.err); info.GetMetadata()["code"] != want.code.String() {
		t.Errorf("Plux code = %q, want %q (%v)", info.GetMetadata()["code"], want.code, got.err)
	}
	if codeOf(got.err) != want.connect || got.status != want.status {
		t.Errorf("Connect code %v, HTTP %d; want %v, HTTP %d", codeOf(got.err), got.status, want.connect, want.status)
	}
	if got.header.Get("WWW-Authenticate") != want.challenge {
		t.Errorf("WWW-Authenticate = %q, want %q", got.header.Get("WWW-Authenticate"), want.challenge)
	}
	if got.header.Get("DPoP-Nonce") != f.nonce() {
		t.Errorf("DPoP-Nonce = %q, want the server's current nonce", got.header.Get("DPoP-Nonce"))
	}
}

// Verifies: SEC-029.
// Every way a DPoP-bound call can go wrong is refused with its own Plux
// code and status, through the real middleware and handlers, and a
// revoked device is refused at once.
func TestDPoPNegativeSuite(t *testing.T) {
	t.Parallel()
	f := newNegativeFixture(t)
	dev := f.dev
	stranger := f.w.registerDevelopment(t, f.app)
	f.refresh(t, stranger)

	t.Run("a good call is accepted", func(t *testing.T) {
		accepted(t, f.send(f.request(t, dev.key, dev.token, nil)))
	})
	t.Run("a replayed proof is refused", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, nil)
		accepted(t, f.send(r))
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPReplay, challengeProof))
	})
	t.Run("a jti used again in a new proof is refused", func(t *testing.T) {
		reuse := func(p *devicetest.Proof) { p.ID = "a-jti-used-twice" }
		accepted(t, f.send(f.request(t, dev.key, dev.token, reuse)))
		again := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { reuse(p); p.IssuedAt = p.IssuedAt.Add(time.Second) })
		f.refused(t, f.send(again), refusedAs(plxerr.DPoPReplay, challengeProof))
	})
	t.Run("a proof for another URL is refused", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) {
			p.URL = f.w.base + pluxv1connect.DeviceServiceReattestDeviceProcedure
		})
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPProofInvalid, challengeProof))
	})
	t.Run("a proof for another method is refused", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { p.Method = http.MethodGet })
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPProofInvalid, challengeProof))
	})
	t.Run("a proof issued too long ago is refused", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { p.IssuedAt = p.IssuedAt.Add(-2 * dpopIatWindow) })
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPProofInvalid, challengeProof))
	})
	t.Run("a proof issued too far ahead is refused", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { p.IssuedAt = p.IssuedAt.Add(2 * dpopIatWindow) })
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPProofInvalid, challengeProof))
	})
	t.Run("a proof without a nonce is told to use the server's", func(t *testing.T) {
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { p.Nonce = "" })
		got := f.send(r)
		f.refused(t, got, refusedAs(plxerr.DPoPNonceRequired, challengeNonce))
		if got.header.Get("DPoP-Nonce") == "" {
			t.Error("the refusal carries no DPoP-Nonce")
		}
	})
	t.Run("a nonce two rotations old is refused", func(t *testing.T) {
		old := f.nonce()
		f.clock.Advance(2 * nonceRotation)
		f.refresh(t, dev)
		r := f.request(t, dev.key, dev.token, func(p *devicetest.Proof) { p.Nonce = old })
		f.refused(t, f.send(r), refusedAs(plxerr.DPoPNonceRequired, challengeNonce))
		accepted(t, f.send(f.request(t, dev.key, dev.token, nil)))
	})
	t.Run("a token used with another device's key is refused", func(t *testing.T) {
		f.refused(t, f.send(f.request(t, stranger.key, dev.token, nil)), refusedAs(plxerr.TokenBindingMismatch, challengeProof))
	})
	t.Run("the binding is checked before the nonce", func(t *testing.T) {
		r := f.request(t, stranger.key, dev.token, func(p *devicetest.Proof) { p.Nonce = "" })
		f.refused(t, f.send(r), refusedAs(plxerr.TokenBindingMismatch, challengeProof))
	})
	t.Run("a token bound to another key is refused", func(t *testing.T) {
		token := f.issue(t, devtoken.Claims{Environment: f.envs["development"], JKT: stranger.jkt})
		f.refused(t, f.send(f.request(t, dev.key, token, nil)), refusedAs(plxerr.TokenBindingMismatch, challengeProof))
	})
	t.Run("an expired token is refused", func(t *testing.T) {
		f.clock.Advance(tokenLifetime + time.Minute)
		f.refused(t, f.send(f.request(t, dev.key, dev.token, nil)), refusedAs(plxerr.AccessTokenInvalid, challengeToken))
		f.refresh(t, dev)
		accepted(t, f.send(f.request(t, dev.key, dev.token, nil)))
	})
	t.Run("a token of the other key class is refused", func(t *testing.T) {
		for name, c := range map[string]devtoken.Claims{
			"a development-class token for a production environment": {Environment: f.envs["production"], Production: false},
			"a production-class token for a development environment": {Environment: f.envs["development"], Production: true},
		} {
			got := f.send(f.request(t, dev.key, f.issue(t, c), nil))
			if got.err == nil {
				t.Fatalf("%s was accepted", name)
			}
			f.refused(t, got, refusedAs(plxerr.AccessTokenInvalid, challengeToken))
		}
	})
	t.Run("a software key is refused where hardware is required", func(t *testing.T) {
		f.strict.Store(f.envs["development"], true)
		t.Cleanup(func() { f.strict.Delete(f.envs["development"]) })
		_, jwk := devicetest.NewKey(t)
		_, err := f.client.RegisterAttestedDevice(context.Background(), connect.NewRequest(&pluxv1.RegisterAttestedDeviceRequest{
			AppId: f.app, Environment: "development", Platform: "linux", OsVersion: "6", RuntimeVersion: "1.0.0", HostBuild: "42",
			Challenge: f.w.challenge(t, f.app, "development"), DpopPublicKeyJwk: jwk,
			KeyStorage: pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE, Evidence: developmentProto(),
		}))
		status, _ := f.rec.last()
		if code := errorInfo(t, err).GetMetadata()["code"]; code != plxerr.KeyNotHardwareBacked.String() {
			t.Errorf("Plux code = %q (%v)", code, err)
		}
		if codeOf(err) != connect.CodePermissionDenied || status != http.StatusForbidden {
			t.Errorf("Connect code %v, HTTP %d", codeOf(err), status)
		}
	})
	t.Run("a revoked device is refused at once", func(t *testing.T) {
		accepted(t, f.send(f.request(t, dev.key, dev.token, nil)))
		must(f.w.device.RevokeDevice(context.Background(), req(f.admin, &pluxv1.RevokeDeviceRequest{DeviceId: dev.id, Reason: "stolen"})))(t)
		f.refused(t, f.send(f.request(t, dev.key, dev.token, nil)), refusedAs(plxerr.DeviceRevoked, challengeToken))
	})
}

// issue mints an access token for the first device that the service would
// not issue, with the claims filled in from the fixture.
func (f *negativeFixture) issue(t *testing.T, c devtoken.Claims) string {
	t.Helper()
	c.DeviceID, c.AppID, c.OrganizationID, c.Assurance = f.dev.id, f.app, f.admin.org, "AL0"
	if c.JKT == "" {
		c.JKT = f.dev.jkt
	}
	token, _, err := f.w.issuer.Issue(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
