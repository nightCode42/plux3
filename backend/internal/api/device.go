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

// DeviceProcedures are the procedures a device calls with its access
// token.
var DeviceProcedures = map[string]bool{
	pluxv1connect.ManifestServiceGetManifestProcedure:   true,
	pluxv1connect.DeviceServiceReportInstalledProcedure: true,
	pluxv1connect.TelemetryServiceIngestEventsProcedure: true,
	pluxv1connect.ManifestServiceGetRootKeysProcedure:   true,
}

// DevicePublic are the device procedures that take no credential: each
// authenticates by what it carries.
var DevicePublic = map[string]bool{
	pluxv1connect.DeviceServiceRegisterDeviceProcedure:  true,
	pluxv1connect.TokenServiceIssueDeviceTokenProcedure: true,
}

// deviceKey carries an authenticated device in a context.
type deviceKey struct{}

// DeviceFrom returns the authenticated device, when there is one.
func DeviceFrom(ctx context.Context) (device.Identity, bool) {
	d, ok := ctx.Value(deviceKey{}).(device.Identity)
	return d, ok
}

// DeviceAuthentication authenticates device calls and hands every other
// call to people, the authentication of people and CI. A device token is
// accepted only by device procedures, and a device is rate limited on
// its own allowance (SRV-065). GetRootKeys accepts either kind of
// caller: devices and `plux pull` both need the keys.
func DeviceAuthentication(devices *device.Service, limiter RateLimiter, perDevice int64, people Around) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		bearer, _ := bearerToken(c.Header)
		isDevice := device.IsToken(bearer)
		switch {
		case DevicePublic[c.Procedure] && bearer == "":
			return next(ctx)
		case isDevice && DeviceProcedures[c.Procedure]:
			d, err := devices.Authenticate(ctx, bearer)
			if err != nil {
				return err //nolint:wrapcheck // a domain error
			}
			if limiter.Count != nil {
				retry, err := limiter.Allow(ctx, "rate:device:"+d.DeviceID, perDevice)
				if err != nil {
					if retry > 0 {
						c.ResponseHeader.Set("Retry-After", retryAfterHeader(retry))
					}
					return err
				}
			}
			return next(context.WithValue(ctx, deviceKey{}, d))
		case isDevice:
			return plxerr.New(plxerr.PermissionDenied, "a device token cannot call %s", c.Procedure)
		case DeviceProcedures[c.Procedure] && c.Procedure != pluxv1connect.ManifestServiceGetRootKeysProcedure:
			return plxerr.New(plxerr.AuthenticationRequired, "this call needs a device token")
		default:
			return people(ctx, c, next)
		}
	}
}

// device returns the authenticated device.
func deviceOf(ctx context.Context) (device.Identity, error) {
	d, ok := DeviceFrom(ctx)
	if !ok {
		return device.Identity{}, plxerr.New(plxerr.AuthenticationRequired, "this call needs a device token")
	}
	return d, nil
}

// Device serves DeviceService.
type Device struct {
	// Unimplemented answers the P6 RPCs until their handlers land.
	pluxv1connect.UnimplementedDeviceServiceHandler
	h *Handlers
}

var _ pluxv1connect.DeviceServiceHandler = Device{}

// Device returns the DeviceService handler.
func (h *Handlers) Device() Device { return Device{h: h} }

// RegisterDevice registers an installation and returns its credential
// once (GOV-010).
func (s Device) RegisterDevice(ctx context.Context, req *connect.Request[pluxv1.RegisterDeviceRequest]) (*connect.Response[pluxv1.RegisterDeviceResponse], error) {
	m := req.Msg
	d, secret, err := s.h.Devices.Register(ctx, device.Registration{
		AppID: m.GetAppId(), Environment: m.GetEnvironment(), Platform: m.GetPlatform(),
		OSVersion: m.GetOsVersion(), RuntimeVersion: m.GetRuntimeVersion(), Build: m.GetHostBuild(),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.RegisterDeviceResponse{Device: deviceProto(d), DeviceSecret: secret}), nil
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

func deviceProto(d device.Device) *pluxv1.Device {
	return &pluxv1.Device{
		Id: d.ID, AppId: d.AppID, EnvironmentId: d.EnvironmentID, Platform: d.Platform, OsVersion: d.OSVersion,
		RuntimeVersion: d.RuntimeVersion, HostBuild: d.Build, AssuranceLevel: d.AssuranceLevel,
		InstalledSequence: d.InstalledSequence, RegisteredAt: ts(d.RegisteredAt), LastSeenAt: ts(d.LastSeenAt),
	}
}

// Token serves TokenService.
type Token struct {
	// Unimplemented answers the P6 RPCs until their handlers land.
	pluxv1connect.UnimplementedTokenServiceHandler
	h *Handlers
}

var _ pluxv1connect.TokenServiceHandler = Token{}

// Token returns the TokenService handler.
func (h *Handlers) Token() Token { return Token{h: h} }

// IssueDeviceToken exchanges a device's credential for a short-lived
// access token.
func (s Token) IssueDeviceToken(ctx context.Context, req *connect.Request[pluxv1.IssueDeviceTokenRequest]) (*connect.Response[pluxv1.IssueDeviceTokenResponse], error) {
	t, err := s.h.Devices.IssueToken(ctx, req.Msg.GetDeviceId(), req.Msg.GetDeviceSecret())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.IssueDeviceTokenResponse{AccessToken: t.Value, ExpiresAt: ts(t.ExpiresAt)}), nil
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
	served, err := s.h.Releases.GetManifest(ctx, release.ManifestRequest{
		OrganizationID: d.OrganizationID, AppID: d.AppID, EnvironmentID: d.EnvironmentID, Channel: m.GetChannel(), HostBuild: d.HostBuild,
		InstalledSequence: m.GetInstalledSequence(), Installed: installed, IfNoneMatch: m.GetIfNoneMatch(),
		InstalledDigest: m.GetInstalledDigest(),
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
	out := &pluxv1.GetRootKeysResponse{}
	for _, k := range keys {
		out.Keys = append(out.Keys, &pluxv1.PublicKey{KeyId: k.KeyID, Algorithm: k.Algorithm, PublicKey: k.PublicKey, Role: k.Role})
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
	for _, sig := range m.Signatures {
		raw, err := decodeBase64(sig.Signature)
		if err != nil {
			continue
		}
		out.Signatures = append(out.Signatures, &pluxv1.Signature{KeyId: sig.KeyID, Algorithm: sig.Algorithm, Signature: raw})
	}
	return out
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
