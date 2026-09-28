// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// expired is the refusal of a credential that is unknown, expired or
// revoked; the three are not told apart.
func expired() error {
	return plxerr.New(plxerr.AuthenticationRequired, "the credential is not valid; sign in again")
}

// AuthenticateSession resolves a session cookie's value (SEC-101).
func (s *Service) AuthenticateSession(ctx context.Context, secret string) (Identity, error) {
	if !strings.HasPrefix(secret, PrefixSession+"_") {
		return Identity{}, expired()
	}
	var id Identity
	err := s.inTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		row, err := dbgen.New(tx).GetSessionByHash(ctx, HashSecret(secret))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return expired()
			}
			return fmt.Errorf("auth: read a session: %w", err)
		}
		id = Identity{
			Kind: KindUser, ID: storage.ID(row.UserID), UserID: storage.ID(row.UserID),
			Display: row.DisplayName, SessionID: storage.ID(row.ID), CSRFToken: row.CsrfToken,
			SecondFactor: row.MfaAt.Valid, InstallationAdmin: row.InstallationAdmin,
		}
		return nil
	})
	return id, err
}

// AuthenticateToken resolves a bearer token: a personal access token, a
// `plux login` token or a CI token (SRV-064). The token is found by its
// hash before its organisation is known, which is the one thing the
// authentication scope is for.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (Identity, error) {
	if !strings.HasPrefix(secret, PrefixToken+"_") {
		return Identity{}, expired()
	}
	var id Identity
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeAuthentication}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetAccessTokenByHash(ctx, HashSecret(secret))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return expired()
			}
			return fmt.Errorf("auth: read a token: %w", err)
		}
		if err := q.TouchAccessToken(ctx, row.ID); err != nil {
			return fmt.Errorf("auth: record a token's use: %w", err)
		}
		id = tokenIdentity(row)
		return nil
	})
	return id, err
}

// tokenIdentity describes a stored token's holder.
func tokenIdentity(row dbgen.GetAccessTokenByHashRow) Identity {
	// A scope that has left the catalogue grants nothing; the rest still
	// apply, and the slice is never nil, because a token is always
	// restricted by its scopes.
	scopes := make([]Permission, 0, len(row.Scopes))
	for _, v := range row.Scopes {
		if p, err := ParseScopes([]string{v}); err == nil {
			scopes = append(scopes, p...)
		}
	}
	id := Identity{
		Kind: KindToken, ID: storage.ID(row.ID), UserID: storage.ID(row.UserID),
		Display: row.UserDisplayName, OrganizationID: storage.ID(row.OrganizationID),
		Scopes: scopes, SecondFactor: true,
	}
	if row.Source == sourceCI {
		id.Kind, id.Display = KindCI, "ci: "+row.Subject
	}
	return id
}

// Resolve binds an identity to the organisation a call acts in and
// computes what it may do there (SEC-102). A token is bound to its own
// organisation and cannot name another; a session may name any
// organisation its user belongs to. An organisation the caller has no
// part in is reported as not found, not as forbidden, so that its
// existence is not disclosed.
func (s *Service) Resolve(ctx context.Context, id Identity, organizationID string) (Principal, error) {
	if id.OrganizationID != "" {
		if organizationID != "" && organizationID != id.OrganizationID {
			return Principal{}, plxerr.New(plxerr.PermissionDenied, "this token acts only in its own organisation")
		}
		organizationID = id.OrganizationID
	}
	if organizationID == "" {
		return Principal{}, plxerr.New(plxerr.InvalidFormat,
			"this call acts in an organisation; name it with the X-Plux-Organization header or the request")
	}
	org, err := parseID(organizationID, "organisation")
	if err != nil {
		return Principal{}, err
	}
	if id.Kind == KindCI {
		return resolve(id, organizationID, nil, nil, id.Scopes), nil
	}
	roles, appRoles, err := s.roles(ctx, organizationID, org, storage.MustUUID(id.UserID))
	if err != nil {
		return Principal{}, err
	}
	return resolve(id, organizationID, roles, appRoles, nil), nil
}

// roles reads a person's organisation-wide roles and their roles on
// single apps, directly or through a team (GOV-001). A person with
// neither is not part of the organisation, which is reported as not
// found.
func (s *Service) roles(ctx context.Context, organizationID string, org, user pgtype.UUID) ([]Role, map[string][]Role, error) {
	var (
		roles    []Role
		appRoles = map[string][]Role{}
	)
	err := s.inTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		memberships, err := q.ListMembershipsForUser(ctx, dbgen.ListMembershipsForUserParams{OrganizationID: org, UserID: user})
		if err != nil {
			return fmt.Errorf("auth: read memberships: %w", err)
		}
		for _, m := range memberships {
			// A team membership grants the team's roles on apps, through
			// app_access; the organisation-wide role is the direct one.
			if !m.TeamID.Valid {
				roles = append(roles, Role(m.Role))
			}
		}
		grants, err := q.ListAppRolesForUser(ctx, dbgen.ListAppRolesForUserParams{OrganizationID: org, UserID: user})
		if err != nil {
			return fmt.Errorf("auth: read app access: %w", err)
		}
		for _, g := range grants {
			app := storage.ID(g.AppID)
			appRoles[app] = append(appRoles[app], Role(g.Role))
		}
		if len(memberships) == 0 && len(grants) == 0 {
			return plxerr.New(plxerr.ResourceNotFound, "no such organisation")
		}
		return nil
	})
	return roles, appRoles, err
}

// CurrentUser returns the signed-in person with their memberships.
func (s *Service) CurrentUser(ctx context.Context, id Identity) (User, []dbgen.Membership, error) {
	if id.UserID == "" {
		return User{}, nil, plxerr.New(plxerr.PreconditionFailed, "this credential belongs to no person")
	}
	var (
		user        User
		memberships []dbgen.Membership
	)
	err := s.inTx(ctx, storage.Tenant{UserID: id.UserID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		uid := storage.MustUUID(id.UserID)
		row, err := q.GetUser(ctx, uid)
		if err != nil {
			return notFound(err, "user")
		}
		n, err := q.CountConfirmedFactors(ctx, uid)
		if err != nil {
			return fmt.Errorf("auth: read the factors: %w", err)
		}
		user = userOf(row, n > 0)
		memberships, err = q.ListAllMembershipsForUser(ctx, dbgen.ListAllMembershipsForUserParams{UserID: uid, PageSize: 1000})
		if err != nil {
			return fmt.Errorf("auth: read memberships: %w", err)
		}
		if id.OrganizationID != "" {
			// A token sees only its own organisation.
			kept := memberships[:0]
			for _, m := range memberships {
				if storage.ID(m.OrganizationID) == id.OrganizationID {
					kept = append(kept, m)
				}
			}
			memberships = kept
		}
		return nil
	})
	return user, memberships, err
}
