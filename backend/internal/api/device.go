// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/telemetry"
)

// legacyRegistration is what the retired secret-based calls answer.
func legacyRegistration() error {
	return plxerr.New(plxerr.LegacyRegistrationRefused, "registering a device with a secret is no longer supported; register with a hardware-bound DPoP key and attestation")
}

// Device serves DeviceService.
type Device struct {
	// Unimplemented answers the procedures this server does not serve.
	pluxv1connect.UnimplementedDeviceServiceHandler
	h *Handlers
}

var _ pluxv1connect.DeviceServiceHandler = Device{}

// Device returns the DeviceService handler.
func (h *Handlers) Device() Device { return Device{h: h} }

// RegisterDevice is refused: the secret-based registration of P2 is
// retired in favour of RegisterAttestedDevice (SEC-001, PLX-6008).
func (Device) RegisterDevice(context.Context, *connect.Request[pluxv1.RegisterDeviceRequest]) (*connect.Response[pluxv1.RegisterDeviceResponse], error) {
	return nil, legacyRegistration()
}

// CreateRegistrationChallenge issues the single-use challenge a
// registration binds its attestation to (SEC-005).
func (s Device) CreateRegistrationChallenge(ctx context.Context, req *connect.Request[pluxv1.CreateRegistrationChallengeRequest]) (*connect.Response[pluxv1.CreateRegistrationChallengeResponse], error) {
	challenge, expires, err := s.h.Devices.CreateChallenge(ctx, req.Msg.GetAppId(), req.Msg.GetEnvironment())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.CreateRegistrationChallengeResponse{Challenge: challenge, ExpiresAt: ts(expires)}), nil
}

// RegisterAttestedDevice registers a device that proves a DPoP key with
// platform attestation (SEC-001–SEC-008).
func (s Device) RegisterAttestedDevice(ctx context.Context, req *connect.Request[pluxv1.RegisterAttestedDeviceRequest]) (*connect.Response[pluxv1.RegisterAttestedDeviceResponse], error) {
	m := req.Msg
	d, err := s.h.Devices.RegisterAttested(ctx, device.AttestedRegistration{
		AppID: m.GetAppId(), Environment: m.GetEnvironment(), Platform: m.GetPlatform(),
		OSVersion: m.GetOsVersion(), RuntimeVersion: m.GetRuntimeVersion(), Build: m.GetHostBuild(),
		Challenge: m.GetChallenge(), DPoPKeyJWK: m.GetDpopPublicKeyJwk(), KeyStorage: keyStorageOf(m.GetKeyStorage()),
		Evidence: evidenceOf(m.GetEvidence()),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.RegisterAttestedDeviceResponse{Device: deviceProto(d)}), nil
}

// ReattestDevice replaces a device's attestation with a fresh one. The
// device is authenticated by the DPoP proof of the call alone, and the
// proof's key must be the one the device registered (SEC-006).
func (s Device) ReattestDevice(ctx context.Context, req *connect.Request[pluxv1.ReattestDeviceRequest]) (*connect.Response[pluxv1.ReattestDeviceResponse], error) {
	proof, ok := ProofFrom(ctx)
	if !ok {
		return nil, plxerr.New(plxerr.AuthenticationRequired, "this call needs a DPoP proof")
	}
	m := req.Msg
	if err := s.h.Devices.VerifyKey(ctx, m.GetDeviceId(), proof.JKT); err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	d, err := s.h.Devices.Reattest(ctx, m.GetDeviceId(), m.GetChallenge(), evidenceOf(m.GetEvidence()))
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.ReattestDeviceResponse{Device: deviceProto(d)}), nil
}

// RevokeDevice withdraws a device's trust (SEC-006).
func (s Device) RevokeDevice(ctx context.Context, req *connect.Request[pluxv1.RevokeDeviceRequest]) (*connect.Response[pluxv1.RevokeDeviceResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RevokeDeviceResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		d, err := s.h.Devices.Revoke(ctx, p, req.Msg.GetDeviceId(), req.Msg.GetReason())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RevokeDeviceResponse{Device: deviceProto(d)}, nil
	})
}

// GetDevice returns one device.
func (s Device) GetDevice(ctx context.Context, req *connect.Request[pluxv1.GetDeviceRequest]) (*connect.Response[pluxv1.GetDeviceResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetDeviceResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		d, err := s.h.Devices.Get(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetDeviceResponse{Device: deviceProto(d)}, nil
	})
}

// ListDevices lists an app's devices, newest first.
func (s Device) ListDevices(ctx context.Context, req *connect.Request[pluxv1.ListDevicesRequest]) (*connect.Response[pluxv1.ListDevicesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListDevicesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "registered_at desc", Filters: map[string]func(Item) string{
			"platform":        func(m Item) string { return m.(*pluxv1.Device).GetPlatform() },
			"runtime_version": func(m Item) string { return m.(*pluxv1.Device).GetRuntimeVersion() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		ds, err := s.h.Devices.List(ctx, p, req.Msg.GetAppId(), req.Msg.GetEnvironmentId(), after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListDevicesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, d := range ds {
			last = storage.Cursor{Time: d.RegisteredAt, ID: d.ID}
			if item := deviceProto(d); q.Keep(clauses, item) {
				out.Devices = append(out.Devices, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(ds), size, last)
		return out, nil
	})
}

// ReportInstalled records the release a device activated.
func (s Device) ReportInstalled(ctx context.Context, req *connect.Request[pluxv1.ReportInstalledRequest]) (*connect.Response[pluxv1.ReportInstalledResponse], error) {
	d, err := deviceOf(ctx)
	if err != nil {
		return nil, err
	}
	if id := req.Msg.GetDeviceId(); id != "" && id != d.DeviceID {
		return nil, plxerr.New(plxerr.PermissionDenied, "a device reports only for itself")
	}
	if err := s.h.Devices.ReportInstalled(ctx, d, req.Msg.GetReleaseSequence(), nil); err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.ReportInstalledResponse{}), nil
}

// evidenceOf maps the evidence of a request.
func evidenceOf(e *pluxv1.AttestationEvidence) device.Evidence {
	var out device.Evidence
	if a := e.GetAndroid(); a != nil {
		out.Android = &device.AndroidEvidence{KeyAttestationChain: a.GetKeyAttestationChain(), PlayIntegrityToken: a.GetPlayIntegrityToken()}
	}
	if i := e.GetIos(); i != nil {
		out.IOS = &device.IOSEvidence{KeyID: i.GetAppAttestKeyId(), AttestationObject: i.GetAttestationObject()}
	}
	if d := e.GetDevelopment(); d != nil {
		out.Development = &device.DevelopmentEvidence{BuildID: d.GetBuildId()}
	}
	return out
}

// keyStorages pairs the wire and the domain values of a key storage.
var keyStorages = []struct {
	wire   pluxv1.KeyStorage
	domain device.KeyStorage
}{
	{pluxv1.KeyStorage_KEY_STORAGE_SOFTWARE, device.KeyStorageSoftware},
	{pluxv1.KeyStorage_KEY_STORAGE_TEE, device.KeyStorageTEE},
	{pluxv1.KeyStorage_KEY_STORAGE_STRONGBOX, device.KeyStorageStrongBox},
	{pluxv1.KeyStorage_KEY_STORAGE_SECURE_ENCLAVE, device.KeyStorageSecureEnclave},
}

// keyStorageOf maps a claimed key storage.
func keyStorageOf(k pluxv1.KeyStorage) device.KeyStorage {
	for _, p := range keyStorages {
		if p.wire == k {
			return p.domain
		}
	}
	return device.KeyStorageUnspecified
}

// keyStorageProto maps a recorded key storage.
func keyStorageProto(k device.KeyStorage) pluxv1.KeyStorage {
	for _, p := range keyStorages {
		if p.domain == k {
			return p.wire
		}
	}
	return pluxv1.KeyStorage_KEY_STORAGE_UNSPECIFIED
}

func deviceProto(d device.Device) *pluxv1.Device {
	out := &pluxv1.Device{
		Id: d.ID, AppId: d.AppID, EnvironmentId: d.EnvironmentID, Platform: d.Platform, OsVersion: d.OSVersion,
		RuntimeVersion: d.RuntimeVersion, HostBuild: d.Build, AssuranceLevel: d.AssuranceLevel,
		InstalledSequence: d.InstalledSequence, RegisteredAt: ts(d.RegisteredAt), LastSeenAt: ts(d.LastSeenAt),
		KeyStorage: keyStorageProto(d.KeyStorage), DpopJkt: d.DPoPJKT,
	}
	if !d.AttestedAt.IsZero() {
		out.Attestation = &pluxv1.AttestationSummary{
			Provider: string(d.Provider), VerifiedAt: ts(d.AttestedAt), Verdicts: d.Verdicts, RiskMetric: d.RiskMetric,
		}
	}
	if !d.RevokedAt.IsZero() {
		out.RevokedAt = ts(d.RevokedAt)
	}
	return out
}

// Token serves TokenService.
type Token struct {
	// Unimplemented answers ExchangeUserToken until its handler lands.
	pluxv1connect.UnimplementedTokenServiceHandler
	h *Handlers
}

var _ pluxv1connect.TokenServiceHandler = Token{}

// Token returns the TokenService handler.
func (h *Handlers) Token() Token { return Token{h: h} }

// IssueDeviceToken is refused: a device secret no longer earns a token;
// RefreshDeviceToken takes its place (SEC-025, PLX-6008).
func (Token) IssueDeviceToken(context.Context, *connect.Request[pluxv1.IssueDeviceTokenRequest]) (*connect.Response[pluxv1.IssueDeviceTokenResponse], error) {
	return nil, legacyRegistration()
}

// RefreshDeviceToken renews a device's access token. The device is
// authenticated by the DPoP proof of the call alone (SEC-025).
func (s Token) RefreshDeviceToken(ctx context.Context, req *connect.Request[pluxv1.RefreshDeviceTokenRequest]) (*connect.Response[pluxv1.RefreshDeviceTokenResponse], error) {
	proof, ok := ProofFrom(ctx)
	if !ok {
		return nil, plxerr.New(plxerr.AuthenticationRequired, "this call needs a DPoP proof")
	}
	m := req.Msg
	t, err := s.h.Devices.Refresh(ctx, device.RefreshRequest{
		DeviceID: m.GetDeviceId(), ProofJKT: proof.JKT, Proof: proof.Raw,
		AppAttestAssertion: m.GetAppAttestAssertion(), PlayIntegrityToken: m.GetPlayIntegrityToken(),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.RefreshDeviceTokenResponse{AccessToken: t.Value, ExpiresAt: ts(t.ExpiresAt), TokenType: "DPoP"}), nil
}

// Manifest serves ManifestService.
type Manifest struct{ h *Handlers }

var _ pluxv1connect.ManifestServiceHandler = Manifest{}

// Manifest returns the ManifestService handler.
func (h *Handlers) Manifest() Manifest { return Manifest{h: h} }

// GetManifest returns the channel's signed manifest with this device's
// sync plan (REL-030–REL-033). The app and environment are the device's
// own; a request naming others is refused.
func (s Manifest) GetManifest(ctx context.Context, req *connect.Request[pluxv1.GetManifestRequest]) (*connect.Response[pluxv1.GetManifestResponse], error) {
	d, err := deviceOf(ctx)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if m.GetAppId() != "" && m.GetAppId() != d.AppID {
		return nil, plxerr.New(plxerr.PermissionDenied, "a device reads only its own app's manifest")
	}
	installed := make(map[string][]byte, len(m.GetInstalled()))
	var bundles []device.Installed
	for _, b := range m.GetInstalled() {
		sum, err := hex.DecodeString(strings.TrimPrefix(b.GetSha256(), "sha256:"))
		if err != nil || len(sum) != 32 {
			return nil, plxerr.New(plxerr.InvalidFormat, "installed bundle %q has no valid SHA-256", b.GetKey())
		}
		installed[b.GetKey()] = sum
		bundles = append(bundles, device.Installed{Key: b.GetKey(), SHA256: sum})
	}
	if d := m.GetInstalledDigest(); len(d) != 0 && len(d) != 32 {
		return nil, plxerr.New(plxerr.InvalidFormat, "installed_digest is not a SHA-256")
	}
	if m.GetConfigVersion() < 0 {
		return nil, plxerr.New(plxerr.InvalidFormat, "config_version is not negative")
	}
	served, err := s.h.Releases.GetManifest(ctx, release.ManifestRequest{
		OrganizationID: d.OrganizationID, AppID: d.AppID, EnvironmentID: d.EnvironmentID, Channel: m.GetChannel(), HostBuild: d.HostBuild,
		InstalledSequence: m.GetInstalledSequence(), Installed: installed, IfNoneMatch: m.GetIfNoneMatch(),
		InstalledDigest: m.GetInstalledDigest(), ConfigVersion: m.GetConfigVersion(),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	res := connect.NewResponse(&pluxv1.GetManifestResponse{
		NotModified: served.NotModified, InstalledRequired: served.InstalledRequired, Etag: served.ETag,
	})
	res.Header().Set("ETag", served.ETag)
	if served.NotModified || served.InstalledRequired {
		return res, nil
	}
	if err := s.h.Devices.ReportInstalled(ctx, d, m.GetInstalledSequence(), bundles); err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	res.Msg.Manifest = manifestProto(d, served)
	res.Msg.ConfigPatch, res.Msg.ConfigFullRequired = served.ConfigPatch, served.ConfigFullRequired
	return res, nil
}

// GetRootKeys returns the public keys of an environment (SEC-051): to a
// device, its own environment's; to a person or CI, any environment of
// an app they can read.
func (s Manifest) GetRootKeys(ctx context.Context, req *connect.Request[pluxv1.GetRootKeysRequest]) (*connect.Response[pluxv1.GetRootKeysResponse], error) {
	var org, env string
	if d, ok := DeviceFrom(ctx); ok {
		org, env = d.OrganizationID, d.EnvironmentID
	} else {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		e, err := s.h.Tenancy.GetEnvironmentByKey(ctx, p, req.Msg.GetAppId(), req.Msg.GetEnvironment())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		org, env = p.OrganizationID, e.ID
	}
	keys, err := s.h.Releases.RootKeys(ctx, org, env)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	roots, err := s.h.Releases.RootChain(ctx, org, env, req.Msg.GetSinceRootVersion())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	out := &pluxv1.GetRootKeysResponse{Roots: roots}
	for _, k := range keys {
		out.Keys = append(out.Keys, &pluxv1.PublicKey{
			KeyId: k.KeyID, Algorithm: k.Algorithm, PublicKey: k.PublicKey, Role: k.Role, EnvironmentType: k.EnvironmentType,
		})
	}
	return connect.NewResponse(out), nil
}

// manifestProto renders a served manifest.
func manifestProto(d device.Identity, m release.ServedManifest) *pluxv1.Manifest {
	doc := m.Document
	out := &pluxv1.Manifest{
		AppId: d.AppID, Environment: doc.Environment, Channel: doc.Channel, ReleaseSequence: doc.ReleaseSequence,
		AppBundle: bundleProto(doc.AppBundle, m.URLs[""], m.Plan[""]),
		Control: &pluxv1.ControlFlags{
			KillSwitchPlugins: doc.Control.KillSwitches, Mandatory: doc.Control.Mandatory, Message: doc.Control.Message,
		},
		Signed: m.Signed,
	}
	if m.Metadata.Timestamp > 0 {
		out.Metadata = &pluxv1.UpdateMetadataRef{
			RootVersion: m.Metadata.Root, SnapshotVersion: m.Metadata.Snapshot, TimestampVersion: m.Metadata.Timestamp,
		}
	}
	if t, err := parseRFC3339(doc.IssuedAt); err == nil {
		out.IssuedAt = ts(t)
	}
	if t, err := parseRFC3339(doc.Expires); err == nil {
		out.ExpiresAt = ts(t)
	}
	for _, p := range doc.Plugins {
		out.Plugins = append(out.Plugins, &pluxv1.PluginDescriptor{
			Key: p.Key, Version: p.Version, Bundle: bundleProto(p.SignedBundle, m.URLs[p.Key], m.Plan[p.Key]),
		})
	}
	for _, e := range doc.Experiments {
		out.Experiments = append(out.Experiments, &pluxv1.ExperimentAssignment{Key: e.Key, Layer: e.Layer, Variant: e.Variant})
	}
	if c := doc.Config; c != nil {
		if sum, ok := hashOf(c.SHA256); ok {
			out.Config = &pluxv1.SecurityConfigRef{Version: c.Version, Sha256: sum}
		}
	}
	for _, sig := range m.Signatures {
		raw, err := decodeBase64(sig.Signature)
		if err != nil {
			continue
		}
		out.Signatures = append(out.Signatures, &pluxv1.Signature{KeyId: sig.KeyID, Algorithm: sig.Algorithm, Signature: raw})
	}
	return out
}

// hashOf reads a "sha256:<hex>" reference.
func hashOf(ref string) ([]byte, bool) {
	h, ok := strings.CutPrefix(ref, "sha256:")
	if !ok {
		return nil, false
	}
	sum, err := hex.DecodeString(h)
	return sum, err == nil && len(sum) == 32
}

func bundleProto(b release.SignedBundle, url string, step release.SyncStep) *pluxv1.BundleDescriptor {
	return &pluxv1.BundleDescriptor{
		Sha256: b.Hash, Size: b.Size, RequiredFeatures: b.RequiredFeatures, MinRuntime: b.MinRuntime, Url: url,
		Sync: &pluxv1.SyncStep{Action: step.Action, From: step.From, Url: step.URL, Size: step.Size},
	}
}

// Control serves ControlService.
type Control struct{ h *Handlers }

var _ pluxv1connect.ControlServiceHandler = Control{}

// Control returns the ControlService handler.
func (h *Handlers) Control() Control { return Control{h: h} }

// GetControl returns a channel's switches.
func (s Control) GetControl(ctx context.Context, req *connect.Request[pluxv1.GetControlRequest]) (*connect.Response[pluxv1.GetControlResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetControlResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		c, err := s.h.Releases.GetControl(ctx, p, req.Msg.GetAppId(), req.Msg.GetEnvironmentId(), req.Msg.GetChannelKey())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetControlResponse{Control: controlProto(c)}, nil
	})
}

// SetControl replaces a channel's switches (REL-030).
func (s Control) SetControl(ctx context.Context, req *connect.Request[pluxv1.SetControlRequest]) (*connect.Response[pluxv1.SetControlResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetControlResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		c, err := s.h.Releases.SetControl(ctx, p, release.Control{
			AppID: m.GetAppId(), EnvironmentID: m.GetEnvironmentId(), ChannelKey: m.GetChannelKey(),
			KillSwitchPlugins: m.GetKillSwitchPlugins(), AppKillSwitch: m.GetAppKillSwitch(),
			MandatoryUpdate: m.GetMandatoryUpdate(), Message: m.GetMessage(),
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetControlResponse{Control: controlProto(c)}, nil
	})
}

func controlProto(c release.Control) *pluxv1.Control {
	return &pluxv1.Control{
		AppId: c.AppID, EnvironmentId: c.EnvironmentID, ChannelKey: c.ChannelKey, KillSwitchPlugins: c.KillSwitchPlugins,
		AppKillSwitch: c.AppKillSwitch, MandatoryUpdate: c.MandatoryUpdate, Message: c.Message,
		UpdatedBy: actorProto(c.UpdatedBy), UpdatedAt: ts(c.UpdatedAt),
	}
}

// Telemetry serves TelemetryService.
type Telemetry struct{ h *Handlers }

var _ pluxv1connect.TelemetryServiceHandler = Telemetry{}

// Telemetry returns the TelemetryService handler.
func (h *Handlers) Telemetry() Telemetry { return Telemetry{h: h} }

// IngestEvents stores a device's batch of events.
func (s Telemetry) IngestEvents(ctx context.Context, req *connect.Request[pluxv1.IngestEventsRequest]) (*connect.Response[pluxv1.IngestEventsResponse], error) {
	d, err := deviceOf(ctx)
	if err != nil {
		return nil, err
	}
	if id := req.Msg.GetAppId(); id != "" && id != d.AppID {
		return nil, plxerr.New(plxerr.PermissionDenied, "a device reports only for its own app")
	}
	events := make([]telemetry.Event, 0, len(req.Msg.GetEvents()))
	for _, e := range req.Msg.GetEvents() {
		events = append(events, telemetry.Event{
			Name: e.GetName(), Time: e.GetTime().AsTime(), DeviceID: e.GetDeviceId(), ReleaseSequence: e.GetReleaseSequence(),
			PluginKey: e.GetPluginKey(), Route: e.GetRoute(), Fields: e.GetFields(),
		})
		if e.GetTime() == nil {
			events[len(events)-1].Time = zeroTime
		}
	}
	n, diags, err := s.h.Events.Ingest(ctx, telemetry.Source{
		OrganizationID: d.OrganizationID, AppID: d.AppID, EnvironmentID: d.EnvironmentID, DeviceID: d.DeviceID,
	}, events)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.IngestEventsResponse{
		Accepted: int32(n), Rejected: int32(len(diags)), Diagnostics: diagnosticProtos(diags), //nolint:gosec // G115: bounded by telemetry.eventsPerRequest.
	}), nil
}

// ListEvents lists an environment's events, newest first.
func (s Telemetry) ListEvents(ctx context.Context, req *connect.Request[pluxv1.ListEventsRequest]) (*connect.Response[pluxv1.ListEventsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListEventsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		env, err := s.h.Tenancy.GetEnvironmentByKey(ctx, p, m.GetAppId(), m.GetEnvironment())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		page := m.GetPage()
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		since := zeroTime
		if m.GetSince() != nil {
			since = m.GetSince().AsTime()
		}
		es, err := s.h.Events.List(ctx, p, m.GetAppId(), env.ID, m.GetName(), since, after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListEventsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, e := range es {
			last = storage.Cursor{Time: e.Time, ID: e.ID}
			out.Events = append(out.Events, &pluxv1.Event{
				Name: e.Name, Time: ts(e.Time), DeviceId: e.DeviceID, ReleaseSequence: e.ReleaseSequence,
				PluginKey: e.PluginKey, Route: e.Route, Fields: e.Fields,
			})
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(es), size, last)
		return out, nil
	})
}

// zeroTime is an absent time.
var zeroTime time.Time

func parseRFC3339(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err //nolint:wrapcheck // callers ignore it
	}
	return t, nil
}

func decodeBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers skip the value
	}
	return b, nil
}
