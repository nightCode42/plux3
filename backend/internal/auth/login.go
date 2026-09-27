// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// User is an account.
type User struct {
	ID                string
	Email             string
	DisplayName       string
	MFAEnrolled       bool
	InstallationAdmin bool
	CreatedAt         time.Time
	LastLoginAt       time.Time
}

// Session is a browser session. Secret is returned exactly once, when
// the session is created; the edge sets it as a cookie (SEC-101).
type Session struct {
	ID           string
	UserID       string
	Secret       string
	CSRFToken    string
	SecondFactor bool
	ExpiresAt    time.Time
}

// Challenge is a sign-in waiting for its second factor. Secret is what
// the caller answers it with.
type Challenge struct {
	Secret    string
	Kinds     []string
	ExpiresAt time.Time
}

// signInRefused is the one refusal a failed sign-in gets, whatever went
// wrong, so the API does not say which addresses have accounts.
func signInRefused() error {
	return plxerr.New(plxerr.AuthenticationRequired, "the email address or password is wrong")
}

// normaliseEmail validates an address and returns it as stored.
func normaliseEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", plxerr.New(plxerr.InvalidFormat, "%q is not an email address", email)
	}
	return email, nil
}

// Bootstrap creates the first installation administrator and returns
// the invitation they accept to set a password. It refuses once an
// administrator exists, so it cannot be used to add a second one
// behind the audit log's back.
func (s *Service) Bootstrap(ctx context.Context, email string) (User, string, error) {
	var (
		user       User
		invitation string
	)
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		n, err := q.CountInstallationAdmins(ctx)
		if err != nil {
			return fmt.Errorf("auth: count administrators: %w", err)
		}
		if n > 0 {
			return plxerr.New(plxerr.PreconditionFailed, "the installation already has an administrator")
		}
		user, invitation, err = s.createInvited(ctx, tx, email, true)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, audit.Entry{
			Actor:  audit.Actor{Kind: KindSystem, ID: "bootstrap", Display: "plux-server bootstrap"},
			Action: audit.UserInvited, TargetKind: "user", TargetID: user.ID,
		})
	})
	return user, invitation, err
}

// Invite returns the account with an email address, creating it with a
// one-time invitation when there is none (GOV-001). It runs in the
// caller's transaction, so the membership that motivated it commits
// with it; invitation is "" for an existing account.
func (s *Service) Invite(ctx context.Context, tx pgx.Tx, email string) (User, string, error) {
	email, err := normaliseEmail(email)
	if err != nil {
		return User{}, "", err
	}
	row, err := dbgen.New(tx).GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		return userOf(row, false), "", nil
	case errors.Is(err, pgx.ErrNoRows):
		return s.createInvited(ctx, tx, email, false)
	default:
		return User{}, "", fmt.Errorf("auth: read a user: %w", err)
	}
}

// createInvited adds an account without a password and returns its
// invitation.
func (s *Service) createInvited(ctx context.Context, tx pgx.Tx, email string, admin bool) (User, string, error) {
	email, err := normaliseEmail(email)
	if err != nil {
		return User{}, "", err
	}
	secret, err := NewSecret(PrefixInvitation, 32)
	if err != nil {
		return User{}, "", err
	}
	id, err := s.newID()
	if err != nil {
		return User{}, "", err
	}
	display, _, _ := strings.Cut(email, "@")
	row, err := dbgen.New(tx).CreateUser(ctx, dbgen.CreateUserParams{
		ID:                  storage.MustUUID(id),
		Email:               email,
		DisplayName:         display,
		InstallationAdmin:   admin,
		InvitationHash:      secret.Hash,
		InvitationExpiresAt: storage.Timestamp(s.now().Add(invitationTTL)),
	})
	if err != nil {
		if uniqueViolation(err) {
			return User{}, "", plxerr.New(plxerr.ResourceExists, "an account with this email address exists")
		}
		return User{}, "", fmt.Errorf("auth: create a user: %w", err)
	}
	return userOf(row, false), secret.Value, nil
}

// AcceptInvitation sets the password of an invited account (SEC-100).
func (s *Service) AcceptInvitation(ctx context.Context, invitation, displayName, password string) (User, error) {
	if err := CheckPassword(password); err != nil {
		return User{}, plxerr.Wrap(plxerr.InvalidFormat, err, "the password is not acceptable: %s", strings.TrimPrefix(err.Error(), "auth: "))
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len(displayName) > 200 || strings.Contains(displayName, "@") {
		return User{}, plxerr.New(plxerr.InvalidFormat, "a display name is 1 to 200 characters and is not an email address")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("auth: %w", err)
	}
	var user User
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetUserByInvitation(ctx, HashSecret(invitation))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return plxerr.New(plxerr.PreconditionFailed, "the invitation is not valid or has expired; ask for a new one")
			}
			return fmt.Errorf("auth: read an invitation: %w", err)
		}
		accepted, err := q.AcceptInvitation(ctx, dbgen.AcceptInvitationParams{
			ID: row.ID, PasswordHash: hash, DisplayName: displayName,
		})
		if err != nil {
			return fmt.Errorf("auth: accept an invitation: %w", err)
		}
		user = userOf(accepted, false)
		return s.record(ctx, tx, audit.Entry{
			Actor:  audit.Actor{Kind: KindUser, ID: user.ID, Display: user.DisplayName},
			Action: audit.InvitationAccepted, TargetKind: "user", TargetID: user.ID,
		})
	})
	return user, err
}

// StartPasswordLogin verifies a password and either issues a session or
// returns the challenge the user must answer (SEC-100).
func (s *Service) StartPasswordLogin(ctx context.Context, email, password string) (Session, Challenge, error) {
	if err := s.throttled(ctx, email); err != nil {
		return Session{}, Challenge{}, err
	}
	var (
		session   Session
		challenge Challenge
		refused   bool
	)
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetUserByEmail(ctx, strings.TrimSpace(email))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			burn(password)
			refused = true
			return nil
		case err != nil:
			return fmt.Errorf("auth: read a user: %w", err)
		}
		if row.DisabledAt.Valid || row.PasswordHash == "" {
			burn(password)
			refused = true
			return s.refusal(ctx, tx, row)
		}
		if !passwordMatches(row.PasswordHash, password) {
			refused = true
			return s.refusal(ctx, tx, row)
		}
		factors, err := q.CountConfirmedFactors(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("auth: read the factors: %w", err)
		}
		if factors > 0 {
			challenge, err = s.challenge(ctx, tx, row.ID)
			return err
		}
		session, err = s.issueSession(ctx, tx, row, false)
		return err
	})
	if err != nil {
		return Session{}, Challenge{}, err
	}
	if refused {
		s.failed(ctx, email)
		return Session{}, Challenge{}, signInRefused()
	}
	if session.ID != "" {
		s.succeeded(ctx, email)
	}
	return session, challenge, nil
}

// refusal records a refused sign-in for an existing account.
func (s *Service) refusal(ctx context.Context, tx pgx.Tx, row dbgen.User) error {
	return s.record(ctx, tx, audit.Entry{
		Actor:  Anonymous,
		Action: audit.UserSignInRefused, TargetKind: "user", TargetID: storage.ID(row.ID),
	})
}

// challenge opens a second-factor challenge for a user.
func (s *Service) challenge(ctx context.Context, tx pgx.Tx, userID pgtype.UUID) (Challenge, error) {
	secret, err := NewSecret(PrefixChallenge, 32)
	if err != nil {
		return Challenge{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Challenge{}, err
	}
	expires := s.now().Add(challengeTTL)
	if _, err := dbgen.New(tx).CreateChallenge(ctx, dbgen.CreateChallengeParams{
		ID: storage.MustUUID(id), UserID: userID, SecretHash: secret.Hash, ExpiresAt: storage.Timestamp(expires),
	}); err != nil {
		return Challenge{}, fmt.Errorf("auth: create a challenge: %w", err)
	}
	return Challenge{Secret: secret.Value, Kinds: []string{"totp"}, ExpiresAt: expires}, nil
}

// CompleteMFA answers a challenge with a one-time code and issues the
// session. A challenge accepts a few wrong codes and then closes.
func (s *Service) CompleteMFA(ctx context.Context, challenge, code string) (Session, error) {
	var (
		session Session
		account string
		wrong   bool
	)
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		c, err := q.GetChallengeForUpdate(ctx, HashSecret(challenge))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return plxerr.New(plxerr.AuthenticationRequired, "the sign-in has expired; sign in again")
			}
			return fmt.Errorf("auth: read a challenge: %w", err)
		}
		account = storage.ID(c.UserID)
		if err := s.throttled(ctx, account); err != nil {
			return err
		}
		row, err := q.GetUser(ctx, c.UserID)
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		ok, err := s.presentCode(ctx, tx, c.UserID, code)
		if err != nil {
			return err
		}
		if !ok {
			wrong = true
			return s.wrongAnswer(ctx, tx, c, row)
		}
		if err := q.DeleteChallenge(ctx, c.ID); err != nil {
			return fmt.Errorf("auth: close a challenge: %w", err)
		}
		session, err = s.issueSession(ctx, tx, row, true)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if wrong {
		s.failed(ctx, account)
		return Session{}, plxerr.New(plxerr.AuthenticationRequired, "the code is wrong")
	}
	s.succeeded(ctx, account)
	return session, nil
}

// wrongAnswer counts a wrong code against a challenge, closing it after
// challengeAttempts, and records the refusal.
func (s *Service) wrongAnswer(ctx context.Context, tx pgx.Tx, c dbgen.MfaChallenge, user dbgen.User) error {
	q := dbgen.New(tx)
	attempts, err := q.CountChallengeAttempt(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("auth: count an attempt: %w", err)
	}
	if attempts >= challengeAttempts {
		if err := q.DeleteChallenge(ctx, c.ID); err != nil {
			return fmt.Errorf("auth: close a challenge: %w", err)
		}
	}
	return s.refusal(ctx, tx, user)
}

// presentCode checks a one-time code against a user's confirmed factors
// and consumes its time step, so the same code cannot be used twice.
func (s *Service) presentCode(ctx context.Context, tx pgx.Tx, userID pgtype.UUID, code string) (bool, error) {
	q := dbgen.New(tx)
	factors, err := q.ListConfirmedFactorsForUpdate(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("auth: read the factors: %w", err)
	}
	for _, f := range factors {
		secret, err := s.openFactor(ctx, f)
		if err != nil {
			return false, err
		}
		step, err := MatchTOTP(string(secret), code, s.now())
		if err != nil || step <= f.LastCounter {
			continue
		}
		n, err := q.UseFactor(ctx, dbgen.UseFactorParams{ID: f.ID, Counter: step})
		if err != nil {
			return false, fmt.Errorf("auth: record the factor's use: %w", err)
		}
		return n == 1, nil
	}
	return false, nil
}

// VerifySecondFactor presents a one-time code within a session, which
// capabilities that require one then accept (SEC-100).
func (s *Service) VerifySecondFactor(ctx context.Context, id Identity, code string) (Session, error) {
	if id.Kind != KindUser || id.SessionID == "" {
		return Session{}, plxerr.New(plxerr.PreconditionFailed, "a second factor is presented in a browser session")
	}
	if err := s.throttled(ctx, id.UserID); err != nil {
		return Session{}, err
	}
	var (
		session Session
		wrong   bool
	)
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		ok, err := s.presentCode(ctx, tx, storage.MustUUID(id.UserID), code)
		if err != nil {
			return err
		}
		if !ok {
			wrong = true
			return nil
		}
		row, err := dbgen.New(tx).RecordSessionMFA(ctx, storage.MustUUID(id.SessionID))
		if err != nil {
			return fmt.Errorf("auth: record the second factor: %w", err)
		}
		session = sessionOf(row, "")
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.SecondFactorVerified, TargetKind: "session", TargetID: id.SessionID,
		})
	})
	if err != nil {
		return Session{}, err
	}
	if wrong {
		s.failed(ctx, id.UserID)
		return Session{}, plxerr.New(plxerr.AuthenticationRequired, "the code is wrong")
	}
	s.succeeded(ctx, id.UserID)
	return session, nil
}

// issueSession creates a session for a user and records the sign-in.
func (s *Service) issueSession(ctx context.Context, tx pgx.Tx, user dbgen.User, secondFactor bool) (Session, error) {
	secret, err := NewSecret(PrefixSession, 32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := NewSecret(PrefixCSRF, 32)
	if err != nil {
		return Session{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Session{}, err
	}
	var mfaAt time.Time
	if secondFactor {
		mfaAt = s.now()
	}
	q := dbgen.New(tx)
	row, err := q.CreateSession(ctx, dbgen.CreateSessionParams{
		ID:         storage.MustUUID(id),
		UserID:     user.ID,
		SecretHash: secret.Hash,
		CsrfToken:  csrf.Value,
		MfaAt:      storage.Timestamp(mfaAt),
		ExpiresAt:  storage.Timestamp(s.now().Add(s.o.SessionTTL)),
	})
	if err != nil {
		return Session{}, fmt.Errorf("auth: create a session: %w", err)
	}
	if err := q.RecordLogin(ctx, user.ID); err != nil {
		return Session{}, fmt.Errorf("auth: record the sign-in: %w", err)
	}
	if err := s.record(ctx, tx, audit.Entry{
		Actor:  audit.Actor{Kind: KindUser, ID: storage.ID(user.ID), Display: user.DisplayName},
		Action: audit.UserSignedIn, TargetKind: "session", TargetID: id,
	}); err != nil {
		return Session{}, err
	}
	return sessionOf(row, secret.Value), nil
}

// ChangePassword replaces the caller's password and ends every other
// session, so a password change locks out whoever knew the old one.
func (s *Service) ChangePassword(ctx context.Context, id Identity, current, next string) error {
	if id.Kind != KindUser {
		return plxerr.New(plxerr.PreconditionFailed, "a password is changed in a browser session")
	}
	if err := CheckPassword(next); err != nil {
		return plxerr.Wrap(plxerr.InvalidFormat, err, "the new password is not acceptable: %s", strings.TrimPrefix(err.Error(), "auth: "))
	}
	if err := s.throttled(ctx, id.UserID); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	wrong := false
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		user := storage.MustUUID(id.UserID)
		row, err := q.GetUser(ctx, user)
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		if !passwordMatches(row.PasswordHash, current) {
			wrong = true
			return nil
		}
		if err := q.SetUserPassword(ctx, dbgen.SetUserPasswordParams{ID: user, PasswordHash: hash}); err != nil {
			return fmt.Errorf("auth: set the password: %w", err)
		}
		if _, err := q.RevokeUserSessions(ctx, dbgen.RevokeUserSessionsParams{UserID: user, Keep: storage.MustUUID(id.SessionID)}); err != nil {
			return fmt.Errorf("auth: end the other sessions: %w", err)
		}
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.PasswordChanged, TargetKind: "user", TargetID: id.UserID,
		})
	})
	if err != nil {
		return err
	}
	if wrong {
		s.failed(ctx, id.UserID)
		return plxerr.New(plxerr.AuthenticationRequired, "the current password is wrong")
	}
	return nil
}

// Logout revokes the caller's session.
func (s *Service) Logout(ctx context.Context, id Identity) error {
	if id.SessionID == "" {
		return nil
	}
	return s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := dbgen.New(tx).RevokeSession(ctx, storage.MustUUID(id.SessionID)); err != nil {
			return fmt.Errorf("auth: revoke a session: %w", err)
		}
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.UserSignedOut, TargetKind: "session", TargetID: id.SessionID,
		})
	})
}

// userOf converts a stored user.
func userOf(row dbgen.User, mfa bool) User {
	return User{
		ID:                storage.ID(row.ID),
		Email:             row.Email,
		DisplayName:       row.DisplayName,
		MFAEnrolled:       mfa,
		InstallationAdmin: row.InstallationAdmin,
		CreatedAt:         storage.Time(row.CreatedAt),
		LastLoginAt:       storage.Time(row.LastLoginAt),
	}
}

// sessionOf converts a stored session; secret is "" except when the
// session has just been created.
func sessionOf(row dbgen.Session, secret string) Session {
	return Session{
		ID:           storage.ID(row.ID),
		UserID:       storage.ID(row.UserID),
		Secret:       secret,
		CSRFToken:    row.CsrfToken,
		SecondFactor: row.MfaAt.Valid,
		ExpiresAt:    storage.Time(row.ExpiresAt),
	}
}
