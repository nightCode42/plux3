// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"strconv"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// App serves AppService.
type App struct{ h *Handlers }

var _ pluxv1connect.AppServiceHandler = App{}

// App returns the AppService handler.
func (h *Handlers) App() App { return App{h: h} }

// keyQuery is the query of a list ordered by key and filterable by it.
func keyQuery(key func(Item) string) Query {
	return Query{OrderBy: "key", Filters: map[string]func(Item) string{"key": key}}
}

// CreateApp creates an app with its default environments.
func (s App) CreateApp(ctx context.Context, req *connect.Request[pluxv1.CreateAppRequest]) (*connect.Response[pluxv1.CreateAppResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateAppResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		a, err := s.h.Tenancy.CreateApp(ctx, p, req.Msg.GetKey(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateAppResponse{App: appProto(a)}, nil
	})
}

// GetApp returns an app.
func (s App) GetApp(ctx context.Context, req *connect.Request[pluxv1.GetAppRequest]) (*connect.Response[pluxv1.GetAppResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetAppResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		a, err := s.h.Tenancy.GetApp(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetAppResponse{App: appProto(a)}, nil
	})
}

// ListApps lists the apps the caller may read.
func (s App) ListApps(ctx context.Context, req *connect.Request[pluxv1.ListAppsRequest]) (*connect.Response[pluxv1.ListAppsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAppsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.App).GetKey() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		apps, err := s.h.Tenancy.ListApps(ctx, p, tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListAppsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, a := range apps {
			last = storage.Cursor{Key: a.Key, ID: a.ID}
			if item := appProto(a); q.Keep(clauses, item) {
				out.Apps = append(out.Apps, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(apps), size, last)
		return out, Mask(page.GetReadMask(), out.Apps)
	})
}

// UpdateApp renames an app or sets its default plugin.
func (s App) UpdateApp(ctx context.Context, req *connect.Request[pluxv1.UpdateAppRequest]) (*connect.Response[pluxv1.UpdateAppResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UpdateAppResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		a, err := s.h.Tenancy.UpdateApp(ctx, p, req.Msg.GetId(), req.Msg.GetName(), req.Msg.GetDefaultPluginKey())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UpdateAppResponse{App: appProto(a)}, nil
	})
}

// DeleteApp moves an app to the trash (GOV-031).
func (s App) DeleteApp(ctx context.Context, req *connect.Request[pluxv1.DeleteAppRequest]) (*connect.Response[pluxv1.DeleteAppResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteAppResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		item, err := s.h.Tenancy.DeleteApp(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteAppResponse{Trash: trashProto(item)}, nil
	})
}

// CreateEnvironment adds an environment (GOV-010).
func (s App) CreateEnvironment(ctx context.Context, req *connect.Request[pluxv1.CreateEnvironmentRequest]) (*connect.Response[pluxv1.CreateEnvironmentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateEnvironmentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		e, err := s.h.Tenancy.CreateEnvironment(ctx, p, req.Msg.GetAppId(), req.Msg.GetKey(), req.Msg.GetName(), req.Msg.GetProduction())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateEnvironmentResponse{Environment: environmentProto(e)}, nil
	})
}

// ListEnvironments lists an app's environments.
func (s App) ListEnvironments(ctx context.Context, req *connect.Request[pluxv1.ListEnvironmentsRequest]) (*connect.Response[pluxv1.ListEnvironmentsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListEnvironmentsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "key", Filters: map[string]func(Item) string{
			"key":        func(m Item) string { return m.(*pluxv1.Environment).GetKey() },
			"production": func(m Item) string { return strconv.FormatBool(m.(*pluxv1.Environment).GetProduction()) },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, req.Msg.GetAppId(), page)
		if err != nil {
			return nil, err
		}
		envs, err := s.h.Tenancy.ListEnvironments(ctx, p, req.Msg.GetAppId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListEnvironmentsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, e := range envs {
			last = storage.Cursor{Key: e.Key}
			if item := environmentProto(e); q.Keep(clauses, item) {
				out.Environments = append(out.Environments, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, req.Msg.GetAppId(), page, len(envs), size, last)
		return out, Mask(page.GetReadMask(), out.Environments)
	})
}

// UpdateEnvironment renames an environment.
func (s App) UpdateEnvironment(ctx context.Context, req *connect.Request[pluxv1.UpdateEnvironmentRequest]) (*connect.Response[pluxv1.UpdateEnvironmentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UpdateEnvironmentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		e, err := s.h.Tenancy.UpdateEnvironment(ctx, p, req.Msg.GetId(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UpdateEnvironmentResponse{Environment: environmentProto(e)}, nil
	})
}

// DeleteEnvironment removes a non-production environment.
func (s App) DeleteEnvironment(ctx context.Context, req *connect.Request[pluxv1.DeleteEnvironmentRequest]) (*connect.Response[pluxv1.DeleteEnvironmentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteEnvironmentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.DeleteEnvironment(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteEnvironmentResponse{}, nil
	})
}

// SetVariable sets a non-secret variable.
func (s App) SetVariable(ctx context.Context, req *connect.Request[pluxv1.SetVariableRequest]) (*connect.Response[pluxv1.SetVariableResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetVariableResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		v, err := s.h.Tenancy.SetVariable(ctx, p, req.Msg.GetEnvironmentId(), req.Msg.GetKey(), req.Msg.GetValue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetVariableResponse{Variable: &pluxv1.Variable{Key: v.Key, Value: v.Value}}, nil
	})
}

// ListVariables lists an environment's variables.
func (s App) ListVariables(ctx context.Context, req *connect.Request[pluxv1.ListVariablesRequest]) (*connect.Response[pluxv1.ListVariablesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListVariablesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.Variable).GetKey() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page)
		if err != nil {
			return nil, err
		}
		vars, err := s.h.Tenancy.ListVariables(ctx, p, req.Msg.GetEnvironmentId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListVariablesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, v := range vars {
			last = storage.Cursor{Key: v.Key}
			if item := (&pluxv1.Variable{Key: v.Key, Value: v.Value}); q.Keep(clauses, item) {
				out.Variables = append(out.Variables, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page, len(vars), size, last)
		return out, Mask(page.GetReadMask(), out.Variables)
	})
}

// SetSecret stores a secret; its value is never returned (SEC-106).
func (s App) SetSecret(ctx context.Context, req *connect.Request[pluxv1.SetSecretRequest]) (*connect.Response[pluxv1.SetSecretResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetSecretResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		sec, err := s.h.Tenancy.SetSecret(ctx, p, req.Msg.GetEnvironmentId(), req.Msg.GetKey(), req.Msg.GetValue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetSecretResponse{Secret: secretProto(sec)}, nil
	})
}

// ListSecrets lists an environment's secrets without their values.
func (s App) ListSecrets(ctx context.Context, req *connect.Request[pluxv1.ListSecretsRequest]) (*connect.Response[pluxv1.ListSecretsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListSecretsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.Secret).GetKey() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page)
		if err != nil {
			return nil, err
		}
		secrets, err := s.h.Tenancy.ListSecrets(ctx, p, req.Msg.GetEnvironmentId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListSecretsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, sec := range secrets {
			last = storage.Cursor{Key: sec.Key}
			if item := secretProto(sec); q.Keep(clauses, item) {
				out.Secrets = append(out.Secrets, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page, len(secrets), size, last)
		return out, Mask(page.GetReadMask(), out.Secrets)
	})
}

// DeleteSecret removes a secret.
func (s App) DeleteSecret(ctx context.Context, req *connect.Request[pluxv1.DeleteSecretRequest]) (*connect.Response[pluxv1.DeleteSecretResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteSecretResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.DeleteSecret(ctx, p, req.Msg.GetEnvironmentId(), req.Msg.GetKey()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteSecretResponse{}, nil
	})
}

// CreateChannel adds a channel (REL-005).
func (s App) CreateChannel(ctx context.Context, req *connect.Request[pluxv1.CreateChannelRequest]) (*connect.Response[pluxv1.CreateChannelResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateChannelResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		c, err := s.h.Tenancy.CreateChannel(ctx, p, req.Msg.GetEnvironmentId(), req.Msg.GetKey())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateChannelResponse{Channel: channelProto(c)}, nil
	})
}

// ListChannels lists an environment's channels.
func (s App) ListChannels(ctx context.Context, req *connect.Request[pluxv1.ListChannelsRequest]) (*connect.Response[pluxv1.ListChannelsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListChannelsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.Channel).GetKey() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page)
		if err != nil {
			return nil, err
		}
		channels, err := s.h.Tenancy.ListChannels(ctx, p, req.Msg.GetEnvironmentId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListChannelsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, c := range channels {
			last = storage.Cursor{Key: c.Key}
			if item := channelProto(c); q.Keep(clauses, item) {
				out.Channels = append(out.Channels, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, req.Msg.GetEnvironmentId(), page, len(channels), size, last)
		return out, Mask(page.GetReadMask(), out.Channels)
	})
}

// DeleteChannel removes a channel.
func (s App) DeleteChannel(ctx context.Context, req *connect.Request[pluxv1.DeleteChannelRequest]) (*connect.Response[pluxv1.DeleteChannelResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteChannelResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.DeleteChannel(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteChannelResponse{}, nil
	})
}

// GrantAccess gives a team or a user a role on one app (GOV-001).
func (s App) GrantAccess(ctx context.Context, req *connect.Request[pluxv1.GrantAccessRequest]) (*connect.Response[pluxv1.GrantAccessResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.GrantAccessResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		g, err := s.h.Tenancy.GrantAccess(ctx, p, req.Msg.GetAppId(), req.Msg.GetTeamId(), req.Msg.GetUserId(), req.Msg.GetRole())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GrantAccessResponse{Grant: grantProto(g)}, nil
	})
}

// RevokeAccess removes a grant.
func (s App) RevokeAccess(ctx context.Context, req *connect.Request[pluxv1.RevokeAccessRequest]) (*connect.Response[pluxv1.RevokeAccessResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RevokeAccessResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.RevokeAccess(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RevokeAccessResponse{}, nil
	})
}

// ListAccess lists the grants on an app.
func (s App) ListAccess(ctx context.Context, req *connect.Request[pluxv1.ListAccessRequest]) (*connect.Response[pluxv1.ListAccessResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAccessResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "id", Filters: map[string]func(Item) string{
			"role": func(m Item) string { return m.(*pluxv1.AccessGrant).GetRole() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, req.Msg.GetAppId(), page)
		if err != nil {
			return nil, err
		}
		grants, err := s.h.Tenancy.ListAccess(ctx, p, req.Msg.GetAppId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListAccessResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, g := range grants {
			last = storage.Cursor{ID: g.ID}
			if item := grantProto(g); q.Keep(clauses, item) {
				out.Grants = append(out.Grants, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, req.Msg.GetAppId(), page, len(grants), size, last)
		return out, Mask(page.GetReadMask(), out.Grants)
	})
}

// ListTrash lists what waits in the trash (GOV-031).
func (s App) ListTrash(ctx context.Context, req *connect.Request[pluxv1.ListTrashRequest]) (*connect.Response[pluxv1.ListTrashResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListTrashResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "deleted_at", Filters: map[string]func(Item) string{
			"kind": func(m Item) string { return m.(*pluxv1.TrashItem).GetKind() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		scope := p.OrganizationID + "/" + req.Msg.GetAppId()
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, scope, page)
		if err != nil {
			return nil, err
		}
		items, err := s.h.Tenancy.ListTrash(ctx, p, req.Msg.GetAppId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListTrashResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, item := range items {
			last = storage.Cursor{Time: item.DeletedAt, ID: item.ID}
			if msg := trashProto(item); q.Keep(clauses, msg) {
				out.Items = append(out.Items, msg)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, scope, page, len(items), size, last)
		return out, Mask(page.GetReadMask(), out.Items)
	})
}

// RestoreFromTrash restores an item.
func (s App) RestoreFromTrash(ctx context.Context, req *connect.Request[pluxv1.RestoreFromTrashRequest]) (*connect.Response[pluxv1.RestoreFromTrashResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RestoreFromTrashResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.RestoreFromTrash(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RestoreFromTrashResponse{}, nil
	})
}

// PurgeFromTrash deletes an item for good.
func (s App) PurgeFromTrash(ctx context.Context, req *connect.Request[pluxv1.PurgeFromTrashRequest]) (*connect.Response[pluxv1.PurgeFromTrashResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.PurgeFromTrashResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.PurgeFromTrash(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.PurgeFromTrashResponse{}, nil
	})
}

// ListAppLimits reports an app's limits.
func (s App) ListAppLimits(ctx context.Context, req *connect.Request[pluxv1.ListAppLimitsRequest]) (*connect.Response[pluxv1.ListAppLimitsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAppLimitsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		usages, err := s.h.Tenancy.ListAppLimits(ctx, p, req.Msg.GetAppId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ListAppLimitsResponse{Limits: limitProtos(usages)}, nil
	})
}

// SetAppLimit tightens a limit for one app.
func (s App) SetAppLimit(ctx context.Context, req *connect.Request[pluxv1.SetAppLimitRequest]) (*connect.Response[pluxv1.SetAppLimitResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetAppLimitResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		u, err := s.h.Tenancy.SetAppLimit(ctx, p, req.Msg.GetAppId(), req.Msg.GetKey(), req.Msg.GetValue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetAppLimitResponse{Limit: limitProto(u)}, nil
	})
}

// appProto converts an app.
func appProto(a tenancy.App) *pluxv1.App {
	return &pluxv1.App{
		Id: a.ID, OrganizationId: a.OrganizationID, Key: a.Key, Name: a.Name,
		DefaultPluginKey: a.DefaultPluginKey, CreatedAt: ts(a.CreatedAt), UpdatedAt: ts(a.UpdatedAt),
	}
}

// environmentProto converts an environment.
func environmentProto(e tenancy.Environment) *pluxv1.Environment {
	return &pluxv1.Environment{
		Id: e.ID, AppId: e.AppID, Key: e.Key, Name: e.Name, Production: e.Production,
		SigningKeyRef: e.SigningKeyRef, CreatedAt: ts(e.CreatedAt),
	}
}

// channelProto converts a channel.
func channelProto(c tenancy.Channel) *pluxv1.Channel {
	return &pluxv1.Channel{
		Id: c.ID, EnvironmentId: c.EnvironmentID, Key: c.Key, ReleaseSequence: c.ReleaseSequence, UpdatedAt: ts(c.UpdatedAt),
		SignedReleaseSequence: c.SignedReleaseSequence,
	}
}

// secretProto converts a secret's description.
func secretProto(s tenancy.Secret) *pluxv1.Secret {
	out := &pluxv1.Secret{Key: s.Key, Hint: s.Hint, UpdatedAt: ts(s.UpdatedAt)}
	if s.UpdatedBy != "" {
		out.UpdatedBy = &pluxv1.Actor{Kind: "user", Id: s.UpdatedBy}
	}
	return out
}

// grantProto converts an access grant.
func grantProto(g tenancy.AccessGrant) *pluxv1.AccessGrant {
	return &pluxv1.AccessGrant{
		Id: g.ID, AppId: g.AppID, TeamId: g.TeamID, UserId: g.UserID, Role: g.Role, GrantedAt: ts(g.GrantedAt),
	}
}

// trashProto converts a trash item.
func trashProto(t tenancy.TrashItem) *pluxv1.TrashItem {
	out := &pluxv1.TrashItem{
		Id: t.ID, Kind: t.Kind, Name: t.Name, DeletedAt: ts(t.DeletedAt), PurgeAfter: ts(t.PurgeAfter),
	}
	if t.DeletedBy != "" {
		out.DeletedBy = &pluxv1.Actor{Kind: "user", Id: t.DeletedBy}
	}
	return out
}
