// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/dpop"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// DeviceProcedures are the procedures a device calls with its access
// token and a DPoP proof bound to it (SEC-021).
var DeviceProcedures = map[string]bool{
	pluxv1connect.ManifestServiceGetManifestProcedure:   true,
	pluxv1connect.DeviceServiceReportInstalledProcedure: true,
	pluxv1connect.TelemetryServiceIngestEventsProcedure: true,
	pluxv1connect.ManifestServiceGetRootKeysProcedure:   true,
}

// DeviceProofOnly are the procedures a device calls with a DPoP proof
// alone: it has no valid access token yet, or must not need one. They
// authenticate the key; the handler checks that the key is the named
// device's (SEC-025).
var DeviceProofOnly = map[string]bool{
	pluxv1connect.TokenServiceRefreshDeviceTokenProcedure: true,
	pluxv1connect.DeviceServiceReattestDeviceProcedure:    true,
}

// DevicePublic are the device procedures that take no credential: each
// authenticates by what it carries, or, for the two of the secret-based
// registration that P6 retired, is refused.
var DevicePublic = map[string]bool{
	pluxv1connect.DeviceServiceRegisterDeviceProcedure:              true,
	pluxv1connect.TokenServiceIssueDeviceTokenProcedure:             true,
	pluxv1connect.DeviceServiceCreateRegistrationChallengeProcedure: true,
	pluxv1connect.DeviceServiceRegisterAttestedDeviceProcedure:      true,
}

const (
	// nonceHeader carries the server's current DPoP nonce (SEC-024).
	nonceHeader = "DPoP-Nonce"
	// challengeHeader carries the authentication challenge of a refusal
	// (RFC 9449 section 7).
	challengeHeader = "WWW-Authenticate"
	// proofHeader carries the DPoP proof.
	proofHeader = "DPoP"
	// proofMethod is the HTTP method of every Connect call.
	proofMethod = "POST"
	// replayMargin is added to the replay memory beyond the proof's window.
	replayMargin = time.Minute
)

// deviceKey carries an authenticated device in a context.
type deviceKey struct{}

// proofKey carries the verified proof of a proof-only call.
type proofKey struct{}

// DeviceFrom returns the authenticated device, when there is one.
func DeviceFrom(ctx context.Context) (device.Identity, bool) {
	d, ok := ctx.Value(deviceKey{}).(device.Identity)
	return d, ok
}

// DeviceProof is the verified DPoP proof of a proof-only call.
type DeviceProof struct {
	// JKT is the thumbprint of the key that signed the proof.
	JKT string
	// Raw is the proof as sent, which an App Attest assertion signs.
	Raw string
}

// ProofFrom returns the verified proof of a proof-only call.
func ProofFrom(ctx context.Context) (DeviceProof, bool) {
	p, ok := ctx.Value(proofKey{}).(DeviceProof)
	return p, ok
}

// DeviceAuth is what DeviceAuthentication needs.
type DeviceAuth struct {
	// Devices answers the questions about devices: environments and
	// revocations.
	Devices *device.Service
	// Tokens verifies access tokens.
	Tokens *devtoken.Verifier
	// Proofs verifies proofs; its Nonces must be set, so that a proof
	// without a current server nonce is refused.
	Proofs *dpop.Verifier
	// Nonces issues the nonce every device response carries.
	Nonces *dpop.Nonces
	// Replay refuses a proof that was used before.
	Replay *dpop.Replay
	// BaseURL is the externally reachable base URL, which the htu claim
	// of every proof names with the procedure's path.
	BaseURL string
	// Window is how far a proof's iat may differ from the clock, and
	// FallbackWindow the narrower window that applies while the replay
	// cache runs degraded (SEC-023).
	Window, FallbackWindow time.Duration
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Limiter and PerDevice rate limit a device on its own allowance
	// (SRV-065).
	Limiter   RateLimiter
	PerDevice int64
	// People authenticates every other call: people and CI.
	People Around
}

// DeviceAuthentication authenticates device calls and hands every other
// call to a.People. A device call carries `Authorization: DPoP <token>`
// and a DPoP header, verified in the order of ADR-0012: the shape of the
// headers, the token, the proof with its nonce, the binding of proof to
// token, replay, and revocation; then the device is rate limited on its
// own allowance. A device token is accepted only by device procedures.
// GetRootKeys accepts either kind of caller: devices and `plux pull` both
// need the keys.
//
// RefreshDeviceToken and ReattestDevice are authenticated by the proof
// alone (DeviceProofOnly): the middleware verifies it, checks replay and
// puts it in the context, and the handler checks that its key is the
// named device's. Every response to a device call, whether it succeeds or
// not, carries the current DPoP-Nonce, and a refusal carries a
// WWW-Authenticate challenge naming what failed (SEC-024).
func DeviceAuthentication(a DeviceAuth) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		if DeviceProcedures[c.Procedure] || DeviceProofOnly[c.Procedure] || DevicePublic[c.Procedure] {
			c.ResponseHeader.Set(nonceHeader, a.Nonces.Current())
		}
		switch {
		case DevicePublic[c.Procedure]:
			return next(ctx)
		case DeviceProofOnly[c.Procedure]:
			ctx, err := a.authenticateProof(ctx, c)
			if err != nil {
				return refuse(c, err)
			}
			return next(ctx)
		case DeviceProcedures[c.Procedure] && (c.Procedure != pluxv1connect.ManifestServiceGetRootKeysProcedure || usesDPoP(c.Header)):
			return a.deviceCall(ctx, c, next)
		case usesDPoP(c.Header):
			return plxerr.New(plxerr.PermissionDenied, "a device token cannot call %s", c.Procedure)
		default:
			return a.People(ctx, c, next)
		}
	}
}

// deviceCall authenticates a call made with an access token and runs it.
func (a DeviceAuth) deviceCall(ctx context.Context, c Call, next func(context.Context) error) error {
	d, err := a.authenticate(ctx, c)
	if err != nil {
		return refuse(c, err)
	}
	if a.Limiter.Count != nil {
		retry, err := a.Limiter.Allow(ctx, "rate:device:"+d.DeviceID, a.PerDevice)
		if err != nil {
			if retry > 0 {
				c.ResponseHeader.Set("Retry-After", retryAfterHeader(retry))
			}
			return err
		}
	}
	return next(context.WithValue(ctx, deviceKey{}, d))
}

// authenticate checks the token and the proof of a device call.
func (a DeviceAuth) authenticate(ctx context.Context, c Call) (device.Identity, error) {
	token, err := dpopToken(c.Header)
	if err != nil {
		return device.Identity{}, err
	}
	rawProof, err := proofOf(c.Header)
	if err != nil {
		return device.Identity{}, err
	}
	claims, err := a.Tokens.VerifyFor(ctx, token, a.production)
	if err != nil {
		return device.Identity{}, err //nolint:wrapcheck // a domain error
	}
	proof, err := a.Proofs.Verify(rawProof, a.expect(c, token))
	if err != nil {
		return device.Identity{}, err //nolint:wrapcheck // a domain error
	}
	if err := dpop.CheckBinding(claims.JKT, proof.JKT); err != nil {
		return device.Identity{}, err //nolint:wrapcheck // a domain error
	}
	if err := a.checkReplay(ctx, proof); err != nil {
		return device.Identity{}, err
	}
	revoked, err := a.Devices.IsRevoked(ctx, claims.JKT)
	if err != nil {
		return device.Identity{}, err //nolint:wrapcheck // a domain error
	}
	if revoked {
		return device.Identity{}, plxerr.New(plxerr.DeviceRevoked, "the device was revoked")
	}
	return device.Identity{
		DeviceID: claims.DeviceID, OrganizationID: claims.OrganizationID, AppID: claims.AppID,
		EnvironmentID: claims.Environment, HostBuild: claims.HostBuild,
	}, nil
}

// authenticateProof checks the proof of a call that carries no token and
// puts it in the context.
func (a DeviceAuth) authenticateProof(ctx context.Context, c Call) (context.Context, error) {
	rawProof, err := proofOf(c.Header)
	if err != nil {
		return ctx, err
	}
	proof, err := a.Proofs.Verify(rawProof, a.expect(c, ""))
	if err != nil {
		return ctx, err //nolint:wrapcheck // a domain error
	}
	if err := a.checkReplay(ctx, proof); err != nil {
		return ctx, err
	}
	if a.Limiter.Count != nil {
		retry, err := a.Limiter.Allow(ctx, "rate:device:"+proof.JKT, a.PerDevice)
		if err != nil {
			if retry > 0 {
				c.ResponseHeader.Set("Retry-After", retryAfterHeader(retry))
			}
			return ctx, err
		}
	}
	return context.WithValue(ctx, proofKey{}, DeviceProof{JKT: proof.JKT, Raw: rawProof}), nil
}

// expect is what a call's proof must be bound to.
func (a DeviceAuth) expect(c Call, token string) dpop.Expect {
	return dpop.Expect{
		Method: proofMethod, URL: strings.TrimSuffix(a.BaseURL, "/") + c.Procedure,
		AccessToken: token, Window: a.Window,
	}
}

// checkReplay refuses a proof that was used before. The proof is
// remembered for as long as it could be accepted: it may be dated a
// window ahead, and stays acceptable a window past its date. While the
// shared cache is down the narrower fallback window applies (SEC-023).
func (a DeviceAuth) checkReplay(ctx context.Context, p dpop.Proof) error {
	degraded, err := a.Replay.Check(ctx, p.JKT, p.JTI, 2*a.Window+replayMargin)
	if err != nil {
		return err //nolint:wrapcheck // a domain error
	}
	if degraded && a.distance(p.IssuedAt) > a.FallbackWindow {
		return plxerr.New(plxerr.DPoPProofInvalid, "dpop proof is outside the window that applies while the replay cache is degraded")
	}
	return nil
}

// distance is how far a time is from the clock, in either direction.
func (a DeviceAuth) distance(t time.Time) time.Duration {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return max(now().Sub(t), t.Sub(now()))
}

// production says whether the environment a token names is a production
// one; an environment that does not exist makes the token invalid.
func (a DeviceAuth) production(ctx context.Context, environment string) (bool, error) {
	production, err := a.Devices.EnvironmentIsProduction(ctx, environment)
	if code, _ := plxerr.CodeOf(err); code == plxerr.ResourceNotFound {
		return false, plxerr.New(plxerr.AccessTokenInvalid, "access token failed check %q", "env")
	}
	return production, err //nolint:wrapcheck // a domain error
}

// usesDPoP reports whether a call presents its credential with the DPoP
// scheme, the scheme of device tokens.
func usesDPoP(h http.Header) bool {
	scheme, _, _ := strings.Cut(h.Get("Authorization"), " ")
	return strings.EqualFold(scheme, "DPoP")
}

// dpopToken returns the access token of an `Authorization: DPoP <token>`
// header. A missing header, another scheme (a Bearer token is never
// accepted from a device) and a malformed value are refused apart.
func dpopToken(h http.Header) (string, error) {
	values := h.Values("Authorization")
	switch {
	case len(values) == 0:
		return "", plxerr.New(plxerr.AuthenticationRequired, "this call needs a device token")
	case len(values) > 1 || !usesDPoP(h):
		return "", plxerr.New(plxerr.AccessTokenInvalid, "access token failed check %q", "scheme")
	}
	_, token, _ := strings.Cut(values[0], " ")
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", plxerr.New(plxerr.AccessTokenInvalid, "access token failed check %q", "format")
	}
	return token, nil
}

// proofOf returns the one DPoP proof of a call.
func proofOf(h http.Header) (string, error) {
	values := h.Values(proofHeader)
	switch {
	case len(values) == 0:
		return "", plxerr.New(plxerr.DPoPProofInvalid, "dpop proof failed check %q", "missing")
	case len(values) > 1:
		return "", plxerr.New(plxerr.DPoPProofInvalid, "dpop proof failed check %q", "single")
	}
	return values[0], nil
}

// refuse sets the challenge that names why a device call was refused and
// returns the error.
func refuse(c Call, err error) error {
	code, _ := plxerr.CodeOf(err)
	switch code {
	case plxerr.DPoPNonceRequired:
		c.ResponseHeader.Set(challengeHeader, `DPoP error="use_dpop_nonce"`)
	case plxerr.DPoPProofInvalid, plxerr.DPoPReplay, plxerr.TokenBindingMismatch:
		c.ResponseHeader.Set(challengeHeader, `DPoP error="invalid_dpop_proof"`)
	case plxerr.AccessTokenInvalid, plxerr.DeviceRevoked:
		c.ResponseHeader.Set(challengeHeader, `DPoP error="invalid_token"`)
	case plxerr.AuthenticationRequired:
		c.ResponseHeader.Set(challengeHeader, "DPoP")
	}
	return err
}

// deviceOf returns the authenticated device.
func deviceOf(ctx context.Context) (device.Identity, error) {
	d, ok := DeviceFrom(ctx)
	if !ok {
		return device.Identity{}, plxerr.New(plxerr.AuthenticationRequired, "this call needs a device token")
	}
	return d, nil
}
