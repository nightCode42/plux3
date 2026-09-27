// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Token sources.
const (
	sourcePAT = "pat"
	sourceCLI = "cli"
	sourceCI  = "ci"
)

// Token is a personal access token, a `plux login` token or one
// exchanged from a CI identity (SRV-064).
type Token struct {
	ID             string
	Name           string
	UserID         string
	OrganizationID string
	Prefix         string
	Scopes         []Permission
	Source         string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	LastUsedAt     time.Time
	RevokedAt      time.Time
}

// Minted is a new token with its secret, which is returned exactly once
// and stored only as a hash.
type Minted struct {
	Token  Token
	Secret string
}

// tokenScopes decides what a new token carries: the requested scopes,
// each of which the creator must hold, or when none are requested the
// creator's own permissions as they are now. Either way the token's
// scopes are fixed at creation.
func tokenScopes(p Principal, requested []string) ([]Permission, error) {
	if len(requested) == 0 {
		return p.Permissions, nil
	}
	scopes, err := ParseScopes(requested)
	if err != nil {
		return nil, err
	}
	if err := subset(p, scopes); err != nil {
		return nil, err
	}
	return scopes, nil
}

// mint writes a token in the caller's transaction.
func (s *Service) mint(ctx context.Context, tx pgx.Tx, org, user pgtype.UUID, name, source, subject string, scopes []Permission, ttl time.Duration) (Minted, error) {
	secret, err := NewSecret(PrefixToken, 32)
	if err != nil {
		return Minted{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Minted{}, err
	}
	row, err := dbgen.New(tx).CreateAccessToken(ctx, dbgen.CreateAccessTokenParams{
		ID:             storage.MustUUID(id),
		OrganizationID: org,
		UserID:         user,
		Name:           name,
		Prefix:         secret.Prefix,
		SecretHash:     secret.Hash,
		Scopes:         ScopeStrings(scopes),
		Source:         source,
		Subject:        subject,
		ExpiresAt:      storage.Timestamp(s.now().Add(ttl)),
	})
	if err != nil {
		return Minted{}, fmt.Errorf("auth: create a token: %w", err)
	}
	return Minted{Token: tokenOf(row), Secret: secret.Value}, nil
}

// CreateAccessToken issues a personal access token. The caller must be
// a person in a session that presented a second factor, because the
// token outlives the session and carries its capabilities (SRV-064).
func (s *Service) CreateAccessToken(ctx context.Context, p Principal, name string, scopes []string, ttl time.Duration) (Minted, error) {
	if p.Kind != KindUser {
		return Minted{}, plxerr.New(plxerr.PermissionDenied, "a personal access token is created in a browser session, not with another token")
	}
	if !p.SecondFactor {
		return Minted{}, plxerr.New(plxerr.MultiFactorRequired, "creating a token requires a second factor in this session")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Minted{}, plxerr.New(plxerr.InvalidFormat, "a token's name is 1 to 100 characters")
	}
	if ttl <= 0 || ttl > maxTokenTTL {
		return Minted{}, plxerr.New(plxerr.OutOfRange, "a token lasts between one second and 366 days")
	}
	granted, err := tokenScopes(p, scopes)
	if err != nil {
		return Minted{}, err
	}
	var out Minted
	err = s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		out, err = s.mint(ctx, tx, storage.MustUUID(p.OrganizationID), storage.MustUUID(p.UserID), name, sourcePAT, "", granted, ttl)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, audit.Entry{
			OrganizationID: p.OrganizationID, Actor: p.Actor(),
			Action: audit.TokenCreated, TargetKind: "token", TargetID: out.Token.ID,
		})
	})
	return out, err
}

// ListAccessTokens lists the tokens of an organisation: every token for
// a principal who manages tokens, and otherwise the caller's own.
func (s *Service) ListAccessTokens(ctx context.Context, p Principal, after storage.Cursor, limit int32) ([]Token, error) {
	var user pgtype.UUID
	if !p.Holds(TokensManage) {
		if p.UserID == "" {
			return nil, plxerr.New(plxerr.PermissionDenied, "%s is required", TokensManage)
		}
		user = storage.MustUUID(p.UserID)
	}
	var out []Token
	err := s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListAccessTokens(ctx, dbgen.ListAccessTokensParams{
			OrganizationID: storage.MustUUID(p.OrganizationID),
			UserID:         user,
			AfterTime:      after.AfterTime(),
			AfterID:        after.AfterID(),
			PageSize:       limit,
		})
		if err != nil {
			return fmt.Errorf("auth: list tokens: %w", err)
		}
		out = make([]Token, len(rows))
		for i, row := range rows {
			out[i] = tokenOf(row)
		}
		return nil
	})
	return out, err
}

// RevokeAccessToken revokes a token: one's own, or any in the
// organisation for a principal who manages tokens.
func (s *Service) RevokeAccessToken(ctx context.Context, p Principal, tokenID string) error {
	tid, err := parseID(tokenID, "token")
	if err != nil {
		return err
	}
	return s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetAccessToken(ctx, tid)
		if err != nil {
			return notFound(err, "token")
		}
		own := p.UserID != "" && storage.ID(row.UserID) == p.UserID
		if !own {
			if err := p.Authorize(TokensManage); err != nil {
				return err
			}
		}
		if _, err := q.RevokeAccessToken(ctx, tid); err != nil {
			return fmt.Errorf("auth: revoke a token: %w", err)
		}
		return s.record(ctx, tx, audit.Entry{
			OrganizationID: p.OrganizationID, Actor: p.Actor(),
			Action: audit.TokenRevoked, TargetKind: "token", TargetID: tokenID,
		})
	})
}

// DeviceGrant is the start of `plux login` (CLI-002, RFC 8628).
type DeviceGrant struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                time.Duration
	ExpiresAt               time.Time
}

// Poll statuses (RFC 8628 §3.5).
const (
	PollPending  = "pending"
	PollSlowDown = "slow_down"
	PollApproved = "approved"
	PollDenied   = "denied"
)

// StartDeviceAuthorization begins the device authorization grant. It
// needs no credential; the scopes are checked when a person approves.
func (s *Service) StartDeviceAuthorization(ctx context.Context, client string, scopes []string) (DeviceGrant, error) {
	client = strings.TrimSpace(client)
	if client == "" || len(client) > 100 {
		return DeviceGrant{}, plxerr.New(plxerr.InvalidFormat, "the client name is 1 to 100 characters")
	}
	requested, err := ParseScopes(scopes)
	if err != nil {
		return DeviceGrant{}, err
	}
	if s.o.VerificationURI == "" {
		return DeviceGrant{}, plxerr.New(plxerr.PreconditionFailed, "the installation has no public base URL, so `plux login` cannot be approved")
	}
	secret, err := NewSecret(PrefixDeviceCode, 32)
	if err != nil {
		return DeviceGrant{}, err
	}
	var grant DeviceGrant
	err = s.inTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		// A user code collision is unlikely but possible; a second draw
		// settles it.
		for range 3 {
			userCode, err := NewUserCode()
			if err != nil {
				return err
			}
			id, err := s.newID()
			if err != nil {
				return err
			}
			expires := s.now().Add(deviceCodeTTL)
			_, err = dbgen.New(tx).CreateDeviceAuthorization(ctx, dbgen.CreateDeviceAuthorizationParams{
				ID: storage.MustUUID(id), DeviceCode: secret.Hash, UserCode: userCode,
				Client: client, Scopes: ScopeStrings(requested), ExpiresAt: storage.Timestamp(expires),
			})
			if uniqueViolation(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("auth: start a device authorization: %w", err)
			}
			grant = DeviceGrant{
				DeviceCode: secret.Value, UserCode: userCode,
				VerificationURI:         s.o.VerificationURI,
				VerificationURIComplete: s.o.VerificationURI + "?user_code=" + userCode,
				Interval:                devicePollInterval, ExpiresAt: expires,
			}
			return nil
		}
		return errors.New("auth: could not draw a free user code")
	})
	return grant, err
}

// PollDeviceAuthorization reports a grant's state. The first poll after
// approval mints the token and returns it; the grant is then consumed,
// so the token is handed out exactly once (RFC 8628 §3.5).
func (s *Service) PollDeviceAuthorization(ctx context.Context, deviceCode string) (string, Minted, error) {
	var (
		status string
		out    Minted
	)
	err := s.inTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetDeviceAuthorizationByCodeForUpdate(ctx, HashSecret(deviceCode))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return plxerr.New(plxerr.AuthenticationRequired, "the device code has expired; run `plux login` again")
			}
			return fmt.Errorf("auth: read a device authorization: %w", err)
		}
		tooSoon := row.LastPolledAt.Valid && s.now().Sub(row.LastPolledAt.Time) < devicePollInterval
		if err := q.RecordDevicePoll(ctx, row.ID); err != nil {
			return fmt.Errorf("auth: record a poll: %w", err)
		}
		switch row.State {
		case "denied":
			status = PollDenied
			return nil
		case "consumed":
			return plxerr.New(plxerr.AuthenticationRequired, "the device code was already used; run `plux login` again")
		case "pending":
			status = PollPending
			if tooSoon {
				status = PollSlowDown
			}
			return nil
		}
		status = PollApproved
		out, err = s.consumeGrant(ctx, tx, row)
		return err
	})
	return status, out, err
}

// consumeGrant mints the token of an approved grant, in the organisation
// the approver chose, and marks the grant used.
func (s *Service) consumeGrant(ctx context.Context, tx pgx.Tx, row dbgen.DeviceAuthorization) (Minted, error) {
	org := storage.ID(row.ApprovedOrganizationID)
	if err := storage.Rescope(ctx, tx, storage.Tenant{OrganizationID: org}); err != nil {
		return Minted{}, fmt.Errorf("auth: %w", err)
	}
	scopes, err := ParseScopes(row.Scopes)
	if err != nil {
		return Minted{}, err
	}
	out, err := s.mint(ctx, tx, row.ApprovedOrganizationID, row.ApprovedBy, row.Client, sourceCLI, "", scopes, s.o.CLITokenTTL)
	if err != nil {
		return Minted{}, err
	}
	if _, err := dbgen.New(tx).ConsumeDeviceAuthorization(ctx, dbgen.ConsumeDeviceAuthorizationParams{
		ID: row.ID, TokenID: storage.MustUUID(out.Token.ID),
	}); err != nil {
		return Minted{}, fmt.Errorf("auth: consume a device authorization: %w", err)
	}
	return out, s.record(ctx, tx, audit.Entry{
		OrganizationID: org,
		Actor:          audit.Actor{Kind: KindUser, ID: storage.ID(row.ApprovedBy)},
		Action:         audit.TokenCreated, TargetKind: "token", TargetID: out.Token.ID,
	})
}

// ApproveDeviceAuthorization is called by a person who read the user
// code from their terminal. Like creating a token, it needs a second
// factor in the session, and the token cannot do more than the approver
// can.
func (s *Service) ApproveDeviceAuthorization(ctx context.Context, p Principal, userCode string) error {
	if p.Kind != KindUser {
		return plxerr.New(plxerr.PermissionDenied, "`plux login` is approved in a browser session")
	}
	if !p.SecondFactor {
		return plxerr.New(plxerr.MultiFactorRequired, "approving `plux login` requires a second factor in this session")
	}
	return s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetDeviceAuthorizationByUserCodeForUpdate(ctx, NormaliseUserCode(userCode))
		if err != nil {
			return notFound(err, "pending login with this code")
		}
		granted, err := tokenScopes(p, row.Scopes)
		if err != nil {
			return err
		}
		if _, err := q.ApproveDeviceAuthorization(ctx, dbgen.ApproveDeviceAuthorizationParams{
			ID: row.ID, ApprovedBy: storage.MustUUID(p.UserID),
			ApprovedOrganizationID: storage.MustUUID(p.OrganizationID), Scopes: ScopeStrings(granted),
		}); err != nil {
			return fmt.Errorf("auth: approve a device authorization: %w", err)
		}
		return s.record(ctx, tx, audit.Entry{
			OrganizationID: p.OrganizationID, Actor: p.Actor(),
			Action: audit.DeviceAuthApproved, TargetKind: "device_authorization", TargetID: storage.ID(row.ID),
		})
	})
}

// DenyDeviceAuthorization refuses a pending `plux login`.
func (s *Service) DenyDeviceAuthorization(ctx context.Context, id Identity, userCode string) error {
	if id.Kind != KindUser {
		return plxerr.New(plxerr.PermissionDenied, "`plux login` is denied in a browser session")
	}
	return s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetDeviceAuthorizationByUserCodeForUpdate(ctx, NormaliseUserCode(userCode))
		if err != nil {
			return notFound(err, "pending login with this code")
		}
		if _, err := q.DenyDeviceAuthorization(ctx, row.ID); err != nil {
			return fmt.Errorf("auth: deny a device authorization: %w", err)
		}
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.DeviceAuthDenied, TargetKind: "device_authorization", TargetID: storage.ID(row.ID),
		})
	})
}

// WorkloadIdentity lets CI workloads of an issuer act for an
// organisation with fixed scopes (SRV-064).
type WorkloadIdentity struct {
	ID             string
	OrganizationID string
	Issuer         string
	Audience       string
	SubjectPattern string
	Scopes         []Permission
	CreatedAt      time.Time
}

// CreateWorkloadIdentity trusts CI workloads matching an issuer,
// audience and subject pattern. The issuer must be one the installation
// trusts, the pattern must be within the installation's, and the scopes
// within the creator's own.
func (s *Service) CreateWorkloadIdentity(ctx context.Context, p Principal, w WorkloadIdentity, scopes []string) (WorkloadIdentity, error) {
	if err := p.Authorize(TokensManage); err != nil {
		return WorkloadIdentity{}, err
	}
	trusted, ok := s.o.Issuers[strings.TrimSuffix(w.Issuer, "/")]
	if !ok {
		return WorkloadIdentity{}, plxerr.New(plxerr.InvalidEnumValue, "%q is not an issuer this installation trusts", w.Issuer)
	}
	if w.Audience != trusted.Audience {
		return WorkloadIdentity{}, plxerr.New(plxerr.InvalidFormat, "the audience must be %q, the one this installation expects", trusted.Audience)
	}
	if w.SubjectPattern == "" || !patternWithin(w.SubjectPattern, trusted.SubjectPattern) {
		return WorkloadIdentity{}, plxerr.New(plxerr.InvalidFormat,
			"the subject pattern must be narrower than the installation's %q", trusted.SubjectPattern)
	}
	if _, err := path.Match(w.SubjectPattern, ""); err != nil {
		return WorkloadIdentity{}, plxerr.New(plxerr.InvalidFormat, "the subject pattern is not a valid glob")
	}
	parsed, err := ParseScopes(scopes)
	if err != nil {
		return WorkloadIdentity{}, err
	}
	if len(parsed) == 0 {
		return WorkloadIdentity{}, plxerr.New(plxerr.MissingProperty, "a workload identity needs at least one scope")
	}
	if err := subset(p, parsed); err != nil {
		return WorkloadIdentity{}, err
	}
	id, err := s.newID()
	if err != nil {
		return WorkloadIdentity{}, err
	}
	var creator pgtype.UUID
	if p.UserID != "" {
		creator = storage.MustUUID(p.UserID)
	}
	var out WorkloadIdentity
	err = s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		row, err := dbgen.New(tx).CreateWorkloadIdentity(ctx, dbgen.CreateWorkloadIdentityParams{
			ID: storage.MustUUID(id), OrganizationID: storage.MustUUID(p.OrganizationID),
			Issuer: strings.TrimSuffix(w.Issuer, "/"), Audience: w.Audience, SubjectPattern: w.SubjectPattern,
			Scopes: ScopeStrings(parsed), CreatedBy: creator,
		})
		if err != nil {
			return fmt.Errorf("auth: create a workload identity: %w", err)
		}
		out = workloadOf(row)
		return s.record(ctx, tx, audit.Entry{
			OrganizationID: p.OrganizationID, Actor: p.Actor(),
			Action: audit.WorkloadIdentityCreated, TargetKind: "workload_identity", TargetID: id,
		})
	})
	return out, err
}

// patternWithin reports whether every subject a pattern matches is also
// matched by the installation's bound. It is decided conservatively:
// the pattern must be the bound itself, or the bound must match it as a
// literal string with no wildcards of its own.
func patternWithin(pattern, bound string) bool {
	if bound == "" || pattern == bound {
		return true
	}
	if strings.ContainsAny(pattern, `*?[\`) {
		return false
	}
	ok, err := path.Match(bound, pattern)
	return err == nil && ok
}

// ListWorkloadIdentities lists an organisation's workload identities.
func (s *Service) ListWorkloadIdentities(ctx context.Context, p Principal) ([]WorkloadIdentity, error) {
	if err := p.Authorize(OrganizationRead); err != nil {
		return nil, err
	}
	var out []WorkloadIdentity
	err := s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListWorkloadIdentities(ctx, dbgen.ListWorkloadIdentitiesParams{
			OrganizationID: storage.MustUUID(p.OrganizationID), PageSize: 1000,
		})
		if err != nil {
			return fmt.Errorf("auth: list workload identities: %w", err)
		}
		out = make([]WorkloadIdentity, len(rows))
		for i, row := range rows {
			out[i] = workloadOf(row)
		}
		return nil
	})
	return out, err
}

// DeleteWorkloadIdentity stops trusting a workload. Tokens it already
// exchanged expire on their own within auth.ci.tokenTTL.
func (s *Service) DeleteWorkloadIdentity(ctx context.Context, p Principal, identityID string) error {
	if err := p.Authorize(TokensManage); err != nil {
		return err
	}
	wid, err := parseID(identityID, "workload identity")
	if err != nil {
		return err
	}
	return s.inTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := dbgen.New(tx).DeleteWorkloadIdentity(ctx, wid)
		if err != nil {
			return fmt.Errorf("auth: delete a workload identity: %w", err)
		}
		if n == 0 {
			return plxerr.New(plxerr.ResourceNotFound, "no such workload identity")
		}
		return s.record(ctx, tx, audit.Entry{
			OrganizationID: p.OrganizationID, Actor: p.Actor(),
			Action: audit.WorkloadIdentityDeleted, TargetKind: "workload_identity", TargetID: identityID,
		})
	})
}

// ExchangeWorkloadIdentity trades a CI provider's identity token for a
// short-lived Plux token of an organisation (SRV-064). The identity
// token is the only credential: it is verified against the issuer's
// published keys, and its subject must match one of the organisation's
// workload identities for that issuer.
func (s *Service) ExchangeWorkloadIdentity(ctx context.Context, identityToken, organizationID string) (Minted, error) {
	org, err := parseID(organizationID, "organisation")
	if err != nil {
		return Minted{}, err
	}
	issuer, err := unverifiedIssuer(identityToken)
	if err != nil {
		return Minted{}, plxerr.Wrap(plxerr.AuthenticationRequired, err, "the identity token is not valid")
	}
	trusted, ok := s.o.Issuers[issuer]
	if !ok {
		return Minted{}, plxerr.New(plxerr.AuthenticationRequired, "the identity token is not from an issuer this installation trusts")
	}
	claims, err := trusted.Verifier.Verify(ctx, identityToken, trusted.Audience)
	if err != nil {
		return Minted{}, plxerr.Wrap(plxerr.AuthenticationRequired, err, "the identity token is not valid")
	}
	if !subjectMatches(trusted.SubjectPattern, claims.Subject) {
		return Minted{}, plxerr.New(plxerr.AuthenticationRequired, "the workload is outside what this installation trusts")
	}
	var out Minted
	err = s.inTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListWorkloadIdentities(ctx, dbgen.ListWorkloadIdentitiesParams{
			OrganizationID: org, Issuer: &issuer, PageSize: 1000,
		})
		if err != nil {
			return fmt.Errorf("auth: read workload identities: %w", err)
		}
		for _, row := range rows {
			if row.Audience != trusted.Audience || !subjectMatches(row.SubjectPattern, claims.Subject) {
				continue
			}
			scopes, err := ParseScopes(row.Scopes)
			if err != nil {
				return err
			}
			out, err = s.mint(ctx, tx, org, pgtype.UUID{}, "ci: "+truncate(claims.Subject, 90),
				sourceCI, claims.Subject, scopes, s.o.CITokenTTL)
			if err != nil {
				return err
			}
			return s.record(ctx, tx, audit.Entry{
				OrganizationID: organizationID,
				Actor:          audit.Actor{Kind: KindCI, ID: storage.ID(row.ID), Display: "ci: " + truncate(claims.Subject, 90)},
				Action:         audit.WorkloadExchanged, TargetKind: "token", TargetID: out.Token.ID,
			})
		}
		// The same answer whether the organisation exists or not.
		return plxerr.New(plxerr.AuthenticationRequired, "no workload identity of this organisation matches the token")
	})
	return out, err
}

// subjectMatches matches a subject against a glob; an empty pattern
// matches nothing.
func subjectMatches(pattern, subject string) bool {
	if pattern == "" {
		return false
	}
	ok, err := path.Match(pattern, subject)
	return err == nil && ok
}

// truncate shortens a string for display.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// tokenOf converts a stored token.
func tokenOf(row dbgen.AccessToken) Token {
	scopes := make([]Permission, len(row.Scopes))
	for i, v := range row.Scopes {
		scopes[i] = Permission(v)
	}
	return Token{
		ID: storage.ID(row.ID), Name: row.Name, UserID: storage.ID(row.UserID),
		OrganizationID: storage.ID(row.OrganizationID), Prefix: row.Prefix, Scopes: scopes,
		Source: row.Source, CreatedAt: storage.Time(row.CreatedAt), ExpiresAt: storage.Time(row.ExpiresAt),
		LastUsedAt: storage.Time(row.LastUsedAt), RevokedAt: storage.Time(row.RevokedAt),
	}
}

// workloadOf converts a stored workload identity.
func workloadOf(row dbgen.WorkloadIdentity) WorkloadIdentity {
	scopes := make([]Permission, len(row.Scopes))
	for i, v := range row.Scopes {
		scopes[i] = Permission(v)
	}
	return WorkloadIdentity{
		ID: storage.ID(row.ID), OrganizationID: storage.ID(row.OrganizationID), Issuer: row.Issuer,
		Audience: row.Audience, SubjectPattern: row.SubjectPattern, Scopes: scopes,
		CreatedAt: storage.Time(row.CreatedAt),
	}
}

// Expire deletes sessions, challenges and device grants that expired
// more than a day ago, for the maintenance job. Tokens are kept after
// they expire, so that the audit log's references to them stay readable.
func (s *Service) Expire(ctx context.Context) (int64, error) {
	var n int64
	err := s.inTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		for _, del := range []func(context.Context) (int64, error){
			q.DeleteExpiredSessions, q.DeleteExpiredChallenges, q.DeleteExpiredDeviceAuthorizations,
		} {
			deleted, err := del(ctx)
			if err != nil {
				return fmt.Errorf("auth: expire credentials: %w", err)
			}
			n += deleted
		}
		return nil
	})
	return n, err
}
