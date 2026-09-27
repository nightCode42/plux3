// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// IdentityPublic are the IdentityService procedures that take no
// credential: each authenticates by what it carries.
var IdentityPublic = map[string]bool{
	pluxv1connect.IdentityServiceAcceptInvitationProcedure:         true,
	pluxv1connect.IdentityServiceStartPasswordLoginProcedure:       true,
	pluxv1connect.IdentityServiceCompleteMfaProcedure:              true,
	pluxv1connect.IdentityServiceStartDeviceAuthorizationProcedure: true,
	pluxv1connect.IdentityServicePollDeviceAuthorizationProcedure:  true,
	pluxv1connect.IdentityServiceExchangeWorkloadIdentityProcedure: true,
}

// Identity serves IdentityService.
type Identity struct{ h *Handlers }

var _ pluxv1connect.IdentityServiceHandler = Identity{}

// Identity returns the IdentityService handler.
func (h *Handlers) Identity() Identity { return Identity{h: h} }

// GetCurrentUser returns the caller, their memberships and, when the
// call names an organisation, their permissions in it.
func (s Identity) GetCurrentUser(ctx context.Context, req *connect.Request[pluxv1.GetCurrentUserRequest]) (*connect.Response[pluxv1.GetCurrentUserResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetCurrentUserResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		user, memberships, err := s.h.Auth.CurrentUser(ctx, id)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.GetCurrentUserResponse{User: userProto(user)}
		for _, m := range memberships {
			out.Memberships = append(out.Memberships, &pluxv1.Member{
				UserId: storage.ID(m.UserID), OrganizationId: storage.ID(m.OrganizationID),
				TeamId: storage.ID(m.TeamID), Role: m.Role, AddedAt: ts(storage.Time(m.AddedAt)),
			})
		}
		if req.Header().Get(OrganizationHeader) != "" || id.OrganizationID != "" {
			p, err := s.h.principal(ctx, req.Header(), "")
			if err != nil {
				return nil, err
			}
			out.Permissions = auth.ScopeStrings(p.Permissions)
		}
		return out, nil
	})
}

// AcceptInvitation sets the password of an invited account.
func (s Identity) AcceptInvitation(ctx context.Context, req *connect.Request[pluxv1.AcceptInvitationRequest]) (*connect.Response[pluxv1.AcceptInvitationResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.AcceptInvitationResponse, error) {
		user, err := s.h.Auth.AcceptInvitation(ctx, req.Msg.GetInvitation(), req.Msg.GetDisplayName(), req.Msg.GetPassword())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.AcceptInvitationResponse{User: userProto(user)}, nil
	})
}

// StartPasswordLogin signs in with a password. A session is returned as
// a cookie, never in the body; an account with a second factor gets a
// challenge instead (SEC-100, SEC-101).
func (s Identity) StartPasswordLogin(ctx context.Context, req *connect.Request[pluxv1.StartPasswordLoginRequest]) (*connect.Response[pluxv1.StartPasswordLoginResponse], error) {
	session, challenge, err := s.h.Auth.StartPasswordLogin(ctx, req.Msg.GetEmail(), req.Msg.GetPassword())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	out := connect.NewResponse(&pluxv1.StartPasswordLoginResponse{})
	if challenge.Secret != "" {
		out.Msg.Challenge = &pluxv1.MfaChallenge{Id: challenge.Secret, Kinds: challenge.Kinds, ExpiresAt: ts(challenge.ExpiresAt)}
		return out, nil
	}
	out.Msg.Session = sessionProto(session)
	out.Header().Add("Set-Cookie", SessionCookieHeader(session.Secret, session.ExpiresAt, s.h.now()))
	return out, nil
}

// CompleteMfa answers a sign-in's challenge and sets the session cookie.
func (s Identity) CompleteMfa(ctx context.Context, req *connect.Request[pluxv1.CompleteMfaRequest]) (*connect.Response[pluxv1.CompleteMfaResponse], error) {
	session, err := s.h.Auth.CompleteMFA(ctx, req.Msg.GetChallengeId(), req.Msg.GetCode())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	out := connect.NewResponse(&pluxv1.CompleteMfaResponse{Session: sessionProto(session)})
	out.Header().Add("Set-Cookie", SessionCookieHeader(session.Secret, session.ExpiresAt, s.h.now()))
	return out, nil
}

// VerifySecondFactor presents a code within the caller's session.
func (s Identity) VerifySecondFactor(ctx context.Context, req *connect.Request[pluxv1.VerifySecondFactorRequest]) (*connect.Response[pluxv1.VerifySecondFactorResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.VerifySecondFactorResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		session, err := s.h.Auth.VerifySecondFactor(ctx, id, req.Msg.GetCode())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.VerifySecondFactorResponse{Session: sessionProto(session)}, nil
	})
}

// ChangePassword replaces the caller's password.
func (s Identity) ChangePassword(ctx context.Context, req *connect.Request[pluxv1.ChangePasswordRequest]) (*connect.Response[pluxv1.ChangePasswordResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.ChangePasswordResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.ChangePassword(ctx, id, req.Msg.GetCurrentPassword(), req.Msg.GetNewPassword()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ChangePasswordResponse{}, nil
	})
}

// Logout ends the caller's session and clears the cookie.
func (s Identity) Logout(ctx context.Context, _ *connect.Request[pluxv1.LogoutRequest]) (*connect.Response[pluxv1.LogoutResponse], error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.h.Auth.Logout(ctx, id); err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	out := connect.NewResponse(&pluxv1.LogoutResponse{})
	out.Header().Add("Set-Cookie", ClearSessionCookieHeader())
	return out, nil
}

// EnrollTotp adds an unconfirmed TOTP factor.
func (s Identity) EnrollTotp(ctx context.Context, req *connect.Request[pluxv1.EnrollTotpRequest]) (*connect.Response[pluxv1.EnrollTotpResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.EnrollTotpResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		e, err := s.h.Auth.EnrollTOTP(ctx, id, req.Msg.GetLabel())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.EnrollTotpResponse{Factor: factorProto(e.Factor), Secret: e.Secret, OtpauthUrl: e.OTPAuthURL}, nil
	})
}

// ConfirmFactor confirms an enrolled factor with a code.
func (s Identity) ConfirmFactor(ctx context.Context, req *connect.Request[pluxv1.ConfirmFactorRequest]) (*connect.Response[pluxv1.ConfirmFactorResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.ConfirmFactorResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		f, err := s.h.Auth.ConfirmFactor(ctx, id, req.Msg.GetFactorId(), req.Msg.GetCode())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ConfirmFactorResponse{Factor: factorProto(f)}, nil
	})
}

// ListFactors lists the caller's factors. A person has only a handful,
// so the list is one page.
func (s Identity) ListFactors(ctx context.Context, req *connect.Request[pluxv1.ListFactorsRequest]) (*connect.Response[pluxv1.ListFactorsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListFactorsResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := (Query{}).Parse(req.Msg.GetPage()); err != nil {
			return nil, err
		}
		factors, err := s.h.Auth.ListFactors(ctx, id)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListFactorsResponse{Page: &pluxv1.PageResult{}}
		for _, f := range factors {
			out.Factors = append(out.Factors, factorProto(f))
		}
		return out, Mask(req.Msg.GetPage().GetReadMask(), out.Factors)
	})
}

// DeleteFactor removes one of the caller's factors.
func (s Identity) DeleteFactor(ctx context.Context, req *connect.Request[pluxv1.DeleteFactorRequest]) (*connect.Response[pluxv1.DeleteFactorResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteFactorResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.DeleteFactor(ctx, id, req.Msg.GetFactorId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteFactorResponse{}, nil
	})
}

// StartDeviceAuthorization begins `plux login` (CLI-002).
func (s Identity) StartDeviceAuthorization(ctx context.Context, req *connect.Request[pluxv1.StartDeviceAuthorizationRequest]) (*connect.Response[pluxv1.StartDeviceAuthorizationResponse], error) {
	g, err := s.h.Auth.StartDeviceAuthorization(ctx, req.Msg.GetClient(), req.Msg.GetScopes())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.StartDeviceAuthorizationResponse{
		DeviceCode: g.DeviceCode, UserCode: g.UserCode,
		VerificationUri: g.VerificationURI, VerificationUriComplete: g.VerificationURIComplete,
		IntervalSeconds: int32(g.Interval / time.Second), //nolint:gosec // a few seconds
		ExpiresAt:       ts(g.ExpiresAt),
	}), nil
}

// PollDeviceAuthorization reports a grant's state and, once, its token.
func (s Identity) PollDeviceAuthorization(ctx context.Context, req *connect.Request[pluxv1.PollDeviceAuthorizationRequest]) (*connect.Response[pluxv1.PollDeviceAuthorizationResponse], error) {
	status, minted, err := s.h.Auth.PollDeviceAuthorization(ctx, req.Msg.GetDeviceCode())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	out := &pluxv1.PollDeviceAuthorizationResponse{Status: status}
	if minted.Secret != "" {
		out.Token, out.Secret = tokenProto(minted.Token), minted.Secret
	}
	return connect.NewResponse(out), nil
}

// ApproveDeviceAuthorization approves a pending `plux login` for an
// organisation.
func (s Identity) ApproveDeviceAuthorization(ctx context.Context, req *connect.Request[pluxv1.ApproveDeviceAuthorizationRequest]) (*connect.Response[pluxv1.ApproveDeviceAuthorizationResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.ApproveDeviceAuthorizationResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.ApproveDeviceAuthorization(ctx, p, req.Msg.GetUserCode()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ApproveDeviceAuthorizationResponse{}, nil
	})
}

// DenyDeviceAuthorization refuses a pending `plux login`.
func (s Identity) DenyDeviceAuthorization(ctx context.Context, req *connect.Request[pluxv1.DenyDeviceAuthorizationRequest]) (*connect.Response[pluxv1.DenyDeviceAuthorizationResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DenyDeviceAuthorizationResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.DenyDeviceAuthorization(ctx, id, req.Msg.GetUserCode()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DenyDeviceAuthorizationResponse{}, nil
	})
}

// CreateAccessToken issues a personal access token (SRV-064).
func (s Identity) CreateAccessToken(ctx context.Context, req *connect.Request[pluxv1.CreateAccessTokenRequest]) (*connect.Response[pluxv1.CreateAccessTokenResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateAccessTokenResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		ttl := time.Duration(req.Msg.GetTtlSeconds()) * time.Second
		minted, err := s.h.Auth.CreateAccessToken(ctx, p, req.Msg.GetName(), req.Msg.GetScopes(), ttl)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateAccessTokenResponse{Token: tokenProto(minted.Token), Secret: minted.Secret}, nil
	})
}

// ListAccessTokens lists an organisation's tokens.
func (s Identity) ListAccessTokens(ctx context.Context, req *connect.Request[pluxv1.ListAccessTokensRequest]) (*connect.Response[pluxv1.ListAccessTokensResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAccessTokensResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "created_at", Filters: map[string]func(m Item) string{
			"source": func(m Item) string { return m.(*pluxv1.AccessToken).GetSource() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		tokens, err := s.h.Auth.ListAccessTokens(ctx, p, after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListAccessTokensResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, t := range tokens {
			last = storage.Cursor{Time: t.CreatedAt, ID: t.ID}
			if item := tokenProto(t); q.Keep(clauses, item) {
				out.Tokens = append(out.Tokens, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(tokens), size, last)
		return out, Mask(page.GetReadMask(), out.Tokens)
	})
}

// RevokeAccessToken revokes a token.
func (s Identity) RevokeAccessToken(ctx context.Context, req *connect.Request[pluxv1.RevokeAccessTokenRequest]) (*connect.Response[pluxv1.RevokeAccessTokenResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RevokeAccessTokenResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.RevokeAccessToken(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RevokeAccessTokenResponse{}, nil
	})
}

// CreateWorkloadIdentity trusts CI workloads of an issuer (SRV-064).
func (s Identity) CreateWorkloadIdentity(ctx context.Context, req *connect.Request[pluxv1.CreateWorkloadIdentityRequest]) (*connect.Response[pluxv1.CreateWorkloadIdentityResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateWorkloadIdentityResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		w, err := s.h.Auth.CreateWorkloadIdentity(ctx, p, auth.WorkloadIdentity{
			Issuer: req.Msg.GetIssuer(), Audience: req.Msg.GetAudience(), SubjectPattern: req.Msg.GetSubjectPattern(),
		}, req.Msg.GetScopes())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreateWorkloadIdentityResponse{Identity: workloadProto(w)}, nil
	})
}

// ListWorkloadIdentities lists an organisation's workload identities.
// An organisation trusts a handful, so the list is one page.
func (s Identity) ListWorkloadIdentities(ctx context.Context, req *connect.Request[pluxv1.ListWorkloadIdentitiesRequest]) (*connect.Response[pluxv1.ListWorkloadIdentitiesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListWorkloadIdentitiesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
		if err != nil {
			return nil, err
		}
		if _, err := (Query{}).Parse(req.Msg.GetPage()); err != nil {
			return nil, err
		}
		ws, err := s.h.Auth.ListWorkloadIdentities(ctx, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListWorkloadIdentitiesResponse{Page: &pluxv1.PageResult{}}
		for _, w := range ws {
			out.Identities = append(out.Identities, workloadProto(w))
		}
		return out, Mask(req.Msg.GetPage().GetReadMask(), out.Identities)
	})
}

// DeleteWorkloadIdentity stops trusting a workload.
func (s Identity) DeleteWorkloadIdentity(ctx context.Context, req *connect.Request[pluxv1.DeleteWorkloadIdentityRequest]) (*connect.Response[pluxv1.DeleteWorkloadIdentityResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteWorkloadIdentityResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Auth.DeleteWorkloadIdentity(ctx, p, req.Msg.GetId()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteWorkloadIdentityResponse{}, nil
	})
}

// ExchangeWorkloadIdentity trades a CI identity token for a short-lived
// token (SRV-064).
func (s Identity) ExchangeWorkloadIdentity(ctx context.Context, req *connect.Request[pluxv1.ExchangeWorkloadIdentityRequest]) (*connect.Response[pluxv1.ExchangeWorkloadIdentityResponse], error) {
	minted, err := s.h.Auth.ExchangeWorkloadIdentity(ctx, req.Msg.GetIdentityToken(), req.Msg.GetOrganizationId())
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.ExchangeWorkloadIdentityResponse{Token: tokenProto(minted.Token), Secret: minted.Secret}), nil
}

// ListAuditEntries lists an organisation's audit log, or the
// installation's for an administrator who names no organisation.
func (s Identity) ListAuditEntries(ctx context.Context, req *connect.Request[pluxv1.ListAuditEntriesRequest]) (*connect.Response[pluxv1.ListAuditEntriesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAuditEntriesResponse, error) {
		page := req.Msg.GetPage()
		q := Query{OrderBy: "sequence", Filters: map[string]func(m Item) string{
			"action":      func(m Item) string { return m.(*pluxv1.AuditEntry).GetAction() },
			"target_kind": func(m Item) string { return m.(*pluxv1.AuditEntry).GetTargetKind() },
			"target_id":   func(m Item) string { return m.(*pluxv1.AuditEntry).GetTargetId() },
		}}
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		scope, entries, size, err := s.auditPage(ctx, req)
		if err != nil {
			return nil, err
		}
		out := &pluxv1.ListAuditEntriesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, e := range entries {
			last = storage.Cursor{Sequence: e.Sequence}
			if item := auditProto(e); q.Keep(clauses, item) {
				out.Entries = append(out.Entries, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, scope, page, len(entries), size, last)
		return out, Mask(page.GetReadMask(), out.Entries)
	})
}

// auditPage reads one page of the audit log the request names: an
// organisation's, or the installation's for an administrator who names
// none. It returns the page's scope for its token.
func (s Identity) auditPage(ctx context.Context, req *connect.Request[pluxv1.ListAuditEntriesRequest]) (string, []audit.Entry, int32, error) {
	id, err := identity(ctx)
	if err != nil {
		return "", nil, 0, err
	}
	page := req.Msg.GetPage()
	if req.Msg.GetOrganizationId() == "" && req.Header().Get(OrganizationHeader) == "" && id.OrganizationID == "" {
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, "installation", page)
		if err != nil {
			return "", nil, 0, err
		}
		entries, err := s.h.Tenancy.InstallationAuditEntries(ctx, id, after.Sequence, size)
		return "installation", entries, size, err //nolint:wrapcheck // a domain error
	}
	p, err := s.h.principal(ctx, req.Header(), req.Msg.GetOrganizationId())
	if err != nil {
		return "", nil, 0, err
	}
	after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
	if err != nil {
		return "", nil, 0, err
	}
	entries, err := s.h.Tenancy.AuditEntries(ctx, p, after.Sequence, size)
	return p.OrganizationID, entries, size, err //nolint:wrapcheck // a domain error
}

// userProto converts an account.
func userProto(u auth.User) *pluxv1.User {
	return &pluxv1.User{
		Id: u.ID, DisplayName: u.DisplayName, Email: u.Email, MfaEnrolled: u.MFAEnrolled,
		CreatedAt: ts(u.CreatedAt), LastLoginAt: ts(u.LastLoginAt),
	}
}

// sessionProto converts a session; the secret never goes in a body.
func sessionProto(s auth.Session) *pluxv1.Session {
	return &pluxv1.Session{
		Id: s.ID, UserId: s.UserID, CsrfToken: s.CSRFToken, ExpiresAt: ts(s.ExpiresAt), SecondFactor: s.SecondFactor,
	}
}

// factorProto converts a factor.
func factorProto(f auth.Factor) *pluxv1.Factor {
	return &pluxv1.Factor{
		Id: f.ID, Kind: f.Kind, Label: f.Label, Confirmed: f.Confirmed,
		CreatedAt: ts(f.CreatedAt), LastUsedAt: ts(f.LastUsedAt),
	}
}

// tokenProto converts a token; its secret is added by the caller once.
func tokenProto(t auth.Token) *pluxv1.AccessToken {
	return &pluxv1.AccessToken{
		Id: t.ID, Name: t.Name, UserId: t.UserID, OrganizationId: t.OrganizationID,
		Scopes: auth.ScopeStrings(t.Scopes), Prefix: t.Prefix, Source: t.Source,
		CreatedAt: ts(t.CreatedAt), ExpiresAt: ts(t.ExpiresAt), LastUsedAt: ts(t.LastUsedAt), RevokedAt: ts(t.RevokedAt),
	}
}

// workloadProto converts a workload identity.
func workloadProto(w auth.WorkloadIdentity) *pluxv1.WorkloadIdentity {
	return &pluxv1.WorkloadIdentity{
		Id: w.ID, OrganizationId: w.OrganizationID, Issuer: w.Issuer, Audience: w.Audience,
		SubjectPattern: w.SubjectPattern, Scopes: auth.ScopeStrings(w.Scopes), CreatedAt: ts(w.CreatedAt),
	}
}

// auditProto converts an audit entry.
func auditProto(e audit.Entry) *pluxv1.AuditEntry {
	return &pluxv1.AuditEntry{
		Id: e.ID, Time: ts(e.At), Actor: &pluxv1.Actor{Kind: e.Actor.Kind, Id: e.Actor.ID, Display: e.Actor.Display},
		Action: string(e.Action), TargetKind: e.TargetKind, TargetId: e.TargetID, SourceIp: e.SourceIP,
		UserAgent: e.UserAgent, RequestId: e.RequestID, BeforeHash: e.BeforeHash, AfterHash: e.AfterHash,
	}
}
