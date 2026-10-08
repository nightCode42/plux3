// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/seccfg"
)

// SecurityAdmin serves SecurityAdminService (SEC-182). The upstream and
// audit-checkpoint procedures arrive with the gateway and the audit
// signing, and answer Unimplemented until then.
type SecurityAdmin struct {
	pluxv1connect.UnimplementedSecurityAdminServiceHandler
	h *Handlers
}

var _ pluxv1connect.SecurityAdminServiceHandler = SecurityAdmin{}

// SecurityAdmin returns the SecurityAdminService handler.
func (h *Handlers) SecurityAdmin() SecurityAdmin { return SecurityAdmin{h: h} }

// SetSecurityConfig sets an environment's profile and overrides (SEC-180,
// SEC-182).
func (s SecurityAdmin) SetSecurityConfig(ctx context.Context, req *connect.Request[pluxv1.SetSecurityConfigRequest]) (*connect.Response[pluxv1.SetSecurityConfigResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetSecurityConfigResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		version, err := s.h.SecurityConfig.Set(ctx, p, seccfg.Change{
			AppID: m.GetAppId(), EnvironmentID: m.GetEnvironmentId(), Profile: m.GetProfile(),
			Overrides: m.GetOverridesJson(), ExpectedVersion: m.GetExpectedVersion(),
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetSecurityConfigResponse{Version: version}, nil
	})
}

// GetEffectiveSecurityConfig returns an environment's profile merged with
// its overrides, and the hash of the device document the manifest pins
// (SEC-180, SEC-182).
func (s SecurityAdmin) GetEffectiveSecurityConfig(ctx context.Context, req *connect.Request[pluxv1.GetEffectiveSecurityConfigRequest]) (*connect.Response[pluxv1.GetEffectiveSecurityConfigResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetEffectiveSecurityConfigResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		c, err := s.h.SecurityConfig.Get(ctx, p, req.Msg.GetAppId(), req.Msg.GetEnvironmentId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		effective, err := c.Values.EffectiveJSON()
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		_, sum, err := c.DeviceDocument()
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetEffectiveSecurityConfigResponse{
			Profile: string(c.Values.Profile()), Version: c.Version, EffectiveJson: effective, Sha256: sum[:],
		}, nil
	})
}
