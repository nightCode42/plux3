// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Org serves OrgService.
type Org struct{ h *Handlers }

var _ pluxv1connect.OrgServiceHandler = Org{}

// Org returns the OrgService handler.
func (h *Handlers) Org() Org { return Org{h: h} }

// CreateOrganization creates an organisation owned by its creator.
func (s Org) CreateOrganization(ctx context.Context, req *connect.Request[pluxv1.CreateOrganizationRequest]) (*connect.Response[pluxv1.CreateOrganizationResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateOrganizationResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		o, err := s.h.Tenancy.CreateOrganization(ctx, id, req.Msg.GetKey(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateOrganizationResponse{Organization: organizationProto(o)}, nil
	})
}

// GetOrganization returns an organisation the caller belongs to.
func (s Org) GetOrganization(ctx context.Context, req *connect.Request[pluxv1.GetOrganizationRequest]) (*connect.Response[pluxv1.GetOrganizationResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetOrganizationResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetId())
		if err != nil {
			return nil, err
		}
		o, err := s.h.Tenancy.GetOrganization(ctx, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetOrganizationResponse{Organization: organizationProto(o)}, nil
	})
}

// ListOrganizations lists the organisations the caller belongs to.
func (s Org) ListOrganizations(ctx context.Context, req *connect.Request[pluxv1.ListOrganizationsRequest]) (*connect.Response[pluxv1.ListOrganizationsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListOrganizationsResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "key", Filters: map[string]func(Item) string{
			"key": func(m Item) string { return m.(*pluxv1.Organization).GetKey() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		scope := id.Kind + ":" + id.ID
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, scope, page)
		if err != nil {
			return nil, err
		}
		orgs, err := s.h.Tenancy.ListOrganizations(ctx, id, tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListOrganizationsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, o := range orgs {
			last = storage.Cursor{Key: o.Key, ID: o.ID}
			if item := organizationProto(o); q.Keep(clauses, item) {
				out.Organizations = append(out.Organizations, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, scope, page, len(orgs), size, last)
		return out, Mask(page.GetReadMask(), out.Organizations)
	})
}

// UpdateOrganization renames an organisation.
func (s Org) UpdateOrganization(ctx context.Context, req *connect.Request[pluxv1.UpdateOrganizationRequest]) (*connect.Response[pluxv1.UpdateOrganizationResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UpdateOrganizationResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetId())
		if err != nil {
			return nil, err
		}
		o, err := s.h.Tenancy.UpdateOrganization(ctx, p, req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UpdateOrganizationResponse{Organization: organizationProto(o)}, nil
	})
}

// CreateTeam adds a team.
func (s Org) CreateTeam(ctx context.Context, req *connect.Request[pluxv1.CreateTeamRequest]) (*connect.Response[pluxv1.CreateTeamResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateTeamResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		t, err := s.h.Tenancy.CreateTeam(ctx, p, req.Msg.GetKey(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateTeamResponse{Team: teamProto(t)}, nil
	})
}

// ListTeams lists an organisation's teams.
func (s Org) ListTeams(ctx context.Context, req *connect.Request[pluxv1.ListTeamsRequest]) (*connect.Response[pluxv1.ListTeamsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListTeamsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "key", Filters: map[string]func(Item) string{
			"key": func(m Item) string { return m.(*pluxv1.Team).GetKey() },
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
		teams, err := s.h.Tenancy.ListTeams(ctx, p, tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListTeamsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, t := range teams {
			last = storage.Cursor{Key: t.Key, ID: t.ID}
			if item := teamProto(t); q.Keep(clauses, item) {
				out.Teams = append(out.Teams, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(teams), size, last)
		return out, Mask(page.GetReadMask(), out.Teams)
	})
}

// UpdateTeam renames a team.
func (s Org) UpdateTeam(ctx context.Context, req *connect.Request[pluxv1.UpdateTeamRequest]) (*connect.Response[pluxv1.UpdateTeamResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UpdateTeamResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		t, err := s.h.Tenancy.UpdateTeam(ctx, p, req.Msg.GetId(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UpdateTeamResponse{Team: teamProto(t)}, nil
	})
}

// DeleteTeam removes a team.
func (s Org) DeleteTeam(ctx context.Context, req *connect.Request[pluxv1.DeleteTeamRequest]) (*connect.Response[pluxv1.DeleteTeamResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteTeamResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.DeleteTeam(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteTeamResponse{}, nil
	})
}

// AddMember grants a role, inviting the person when they have no
// account yet.
func (s Org) AddMember(ctx context.Context, req *connect.Request[pluxv1.AddMemberRequest]) (*connect.Response[pluxv1.AddMemberResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.AddMemberResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		m, invitation, err := s.h.Tenancy.AddMember(ctx, p, req.Msg.GetTeamId(), req.Msg.GetEmail(), req.Msg.GetRole())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.AddMemberResponse{Member: memberProto(m), Invitation: invitation}, nil
	})
}

// RemoveMember removes a membership.
func (s Org) RemoveMember(ctx context.Context, req *connect.Request[pluxv1.RemoveMemberRequest]) (*connect.Response[pluxv1.RemoveMemberResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RemoveMemberResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		if err := s.h.Tenancy.RemoveMember(ctx, p, req.Msg.GetTeamId(), req.Msg.GetUserId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RemoveMemberResponse{}, nil
	})
}

// ListMembers lists an organisation's or a team's members.
func (s Org) ListMembers(ctx context.Context, req *connect.Request[pluxv1.ListMembersRequest]) (*connect.Response[pluxv1.ListMembersResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListMembersResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "user_id", Filters: map[string]func(Item) string{
			"role": func(m Item) string { return m.(*pluxv1.Member).GetRole() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		scope := p.OrganizationID + "/" + req.Msg.GetTeamId()
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, scope, page)
		if err != nil {
			return nil, err
		}
		members, err := s.h.Tenancy.ListMembers(ctx, p, req.Msg.GetTeamId(), tenancy.Page{After: after, Size: size})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListMembersResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, m := range members {
			last = storage.Cursor{ID: m.UserID, Key: m.MembershipID}
			if item := memberProto(m); q.Keep(clauses, item) {
				out.Members = append(out.Members, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, scope, page, len(members), size, last)
		return out, Mask(page.GetReadMask(), out.Members)
	})
}

// ListOrganizationLimits reports an organisation's limits.
func (s Org) ListOrganizationLimits(ctx context.Context, req *connect.Request[pluxv1.ListOrganizationLimitsRequest]) (*connect.Response[pluxv1.ListOrganizationLimitsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListOrganizationLimitsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		usages, err := s.h.Tenancy.ListOrganizationLimits(ctx, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ListOrganizationLimitsResponse{Limits: limitProtos(usages)}, nil
	})
}

// SetOrganizationLimit tightens a limit for an organisation.
func (s Org) SetOrganizationLimit(ctx context.Context, req *connect.Request[pluxv1.SetOrganizationLimitRequest]) (*connect.Response[pluxv1.SetOrganizationLimitResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetOrganizationLimitResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		u, err := s.h.Tenancy.SetOrganizationLimit(ctx, p, req.Msg.GetKey(), req.Msg.GetValue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetOrganizationLimitResponse{Limit: limitProto(u)}, nil
	})
}

// organizationProto converts an organisation.
func organizationProto(o tenancy.Organization) *pluxv1.Organization {
	return &pluxv1.Organization{Id: o.ID, Key: o.Key, Name: o.Name, CreatedAt: ts(o.CreatedAt), UpdatedAt: ts(o.UpdatedAt)}
}

// teamProto converts a team.
func teamProto(t tenancy.Team) *pluxv1.Team {
	return &pluxv1.Team{Id: t.ID, OrganizationId: t.OrganizationID, Key: t.Key, Name: t.Name, CreatedAt: ts(t.CreatedAt)}
}

// memberProto converts a membership.
func memberProto(m tenancy.Member) *pluxv1.Member {
	return &pluxv1.Member{
		UserId: m.UserID, OrganizationId: m.OrganizationID, TeamId: m.TeamID, Role: m.Role,
		AddedAt: ts(m.AddedAt), DisplayName: m.DisplayName, Email: m.Email,
	}
}

// limitProto converts a limit's usage.
func limitProto(u tenancy.LimitUsage) *pluxv1.LimitUsage {
	return &pluxv1.LimitUsage{
		Key: string(u.Key), Unit: u.Unit, Scope: u.Scope, Value: u.Value,
		Effective: u.Effective, HardMax: u.HardMax, Warning: u.Warning,
	}
}

// limitProtos converts a list of usages.
func limitProtos(us []tenancy.LimitUsage) []*pluxv1.LimitUsage {
	out := make([]*pluxv1.LimitUsage, len(us))
	for i, u := range us {
		out[i] = limitProto(u)
	}
	return out
}
