// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// This file registers WebAuthn security keys and passkeys as second
// factors and answers sign-in challenges with them (SEC-100). The
// options returned are the JSON a browser passes to
// navigator.credentials.create() and .get(), with binary values in
// base64url as WebAuthn Level 3's JSON serialisation has them.

// registrationTTL is how long a registration may take.
const registrationTTL = 5 * time.Minute

// webAuthnTimeout is the ceremony timeout the browser is given, in ms.
const webAuthnTimeout = 300000

// credentialDescriptor names a credential in options.
type credentialDescriptor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// b64 encodes bytes the way WebAuthn's JSON serialisation does.
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// webAuthnRequired refuses when no relying party is configured.
func (s *Service) webAuthnRequired() error {
	if s.o.WebAuthn.RPID == "" || len(s.o.WebAuthn.Origins) == 0 {
		return plxerr.New(plxerr.PreconditionFailed, "security keys are not configured on this installation")
	}
	return nil
}

// newChallenge returns 32 random bytes.
func newChallenge() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24)
	return b
}

// credentials lists a user's confirmed security keys.
func credentials(ctx context.Context, q *dbgen.Queries, user dbgen.User) ([]credentialDescriptor, error) {
	rows, err := q.ListFactors(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: list the factors: %w", err)
	}
	var out []credentialDescriptor
	for _, f := range rows {
		if f.Kind == "webauthn" && f.ConfirmedAt.Valid {
			out = append(out, credentialDescriptor{Type: "public-key", ID: b64(f.CredentialID)})
		}
	}
	return out, nil
}

// BeginWebAuthnRegistration adds an unconfirmed security-key factor and
// returns it with the creation options for the browser.
func (s *Service) BeginWebAuthnRegistration(ctx context.Context, id Identity, label string) (Factor, []byte, error) {
	if err := sessionOnly(id); err != nil {
		return Factor{}, nil, err
	}
	if err := s.webAuthnRequired(); err != nil {
		return Factor{}, nil, err
	}
	label = strings.TrimSpace(label)
	if len(label) > 100 {
		return Factor{}, nil, plxerr.New(plxerr.InvalidFormat, "a factor's label is at most 100 characters")
	}
	factorID, err := s.newID()
	if err != nil {
		return Factor{}, nil, err
	}
	challenge := newChallenge()
	var (
		out     Factor
		options []byte
	)
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		user, err := q.GetUser(ctx, storage.MustUUID(id.UserID))
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		row, err := q.CreateWebAuthnFactor(ctx, dbgen.CreateWebAuthnFactorParams{
			ID: storage.MustUUID(factorID), UserID: user.ID, Label: label, Secret: challenge,
		})
		if err != nil {
			return fmt.Errorf("auth: enrol a factor: %w", err)
		}
		exclude, err := credentials(ctx, q, user)
		if err != nil {
			return err
		}
		options, err = s.creationOptions(user, challenge, exclude)
		if err != nil {
			return err
		}
		out = factorOf(row)
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.FactorEnrolled, TargetKind: "factor", TargetID: factorID,
		})
	})
	return out, options, err
}

// creationOptions are PublicKeyCredentialCreationOptions as JSON.
func (s *Service) creationOptions(user dbgen.User, challenge []byte, exclude []credentialDescriptor) ([]byte, error) {
	type param struct {
		Type string `json:"type"`
		Alg  int64  `json:"alg"`
	}
	params := make([]param, 0, len(webAuthnAlgorithms))
	for _, alg := range webAuthnAlgorithms {
		params = append(params, param{Type: "public-key", Alg: alg})
	}
	name := s.o.WebAuthn.RPName
	if name == "" {
		name = s.o.Issuer
	}
	out, err := json.Marshal(map[string]any{
		"challenge": b64(challenge),
		"rp":        map[string]string{"id": s.o.WebAuthn.RPID, "name": name},
		"user": map[string]string{
			"id": b64(user.ID.Bytes[:]), "name": user.Email, "displayName": user.DisplayName,
		},
		"pubKeyCredParams":       params,
		"timeout":                webAuthnTimeout,
		"attestation":            "none",
		"excludeCredentials":     nonNil(exclude),
		"authenticatorSelection": map[string]string{"residentKey": "discouraged", "userVerification": "preferred"},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return out, nil
}

// nonNil keeps an empty list a JSON array.
func nonNil(c []credentialDescriptor) []credentialDescriptor {
	if c == nil {
		return []credentialDescriptor{}
	}
	return c
}

// FinishWebAuthnRegistration verifies the browser's response and
// confirms the factor. Like confirming a TOTP factor, it counts as
// presenting one in the session.
func (s *Service) FinishWebAuthnRegistration(ctx context.Context, id Identity, factorID string, clientDataJSON, attestationObject []byte) (Factor, error) {
	if err := sessionOnly(id); err != nil {
		return Factor{}, err
	}
	if err := s.webAuthnRequired(); err != nil {
		return Factor{}, err
	}
	fid, err := parseID(factorID, "factor")
	if err != nil {
		return Factor{}, err
	}
	var out Factor
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		user := storage.MustUUID(id.UserID)
		row, err := s.pendingRegistration(ctx, q, fid, user)
		if err != nil {
			return err
		}
		cred, err := s.o.WebAuthn.verifyRegistration(row.Secret, clientDataJSON, attestationObject)
		if err != nil {
			return plxerr.Wrap(plxerr.AuthenticationRequired, err, "the security key's registration does not verify")
		}
		confirmed, err := q.ConfirmWebAuthnFactor(ctx, dbgen.ConfirmWebAuthnFactorParams{
			ID: fid, UserID: user, CredentialID: cred.ID, PublicKey: cred.PublicKey, LastCounter: int64(cred.SignCount),
		})
		if err != nil {
			if uniqueViolation(err) {
				return plxerr.New(plxerr.ResourceExists, "this security key is already registered")
			}
			return fmt.Errorf("auth: confirm a factor: %w", err)
		}
		if id.SessionID != "" {
			if _, err := q.RecordSessionMFA(ctx, storage.MustUUID(id.SessionID)); err != nil {
				return fmt.Errorf("auth: record the second factor: %w", err)
			}
		}
		out = factorOf(confirmed)
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.FactorConfirmed, TargetKind: "factor", TargetID: factorID,
		})
	})
	return out, err
}

// pendingRegistration reads a security key waiting for its
// registration, refusing one that has expired.
func (s *Service) pendingRegistration(ctx context.Context, q *dbgen.Queries, fid, user pgtype.UUID) (dbgen.MfaFactor, error) {
	row, err := q.GetFactor(ctx, dbgen.GetFactorParams{ID: fid, UserID: user})
	if err != nil {
		return dbgen.MfaFactor{}, notFound(err, "factor")
	}
	if row.Kind != "webauthn" || row.ConfirmedAt.Valid {
		return dbgen.MfaFactor{}, plxerr.New(plxerr.PreconditionFailed, "the factor is not a security key waiting for registration")
	}
	if s.now().After(storage.Time(row.CreatedAt).Add(registrationTTL)) {
		return dbgen.MfaFactor{}, plxerr.New(plxerr.PreconditionFailed, "the registration has expired; start it again")
	}
	return row, nil
}

// openChallenge reads a sign-in challenge for update.
func openChallenge(ctx context.Context, q *dbgen.Queries, secret string) (dbgen.MfaChallenge, error) {
	c, err := q.GetChallengeForUpdate(ctx, HashSecret(secret))
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.MfaChallenge{}, plxerr.New(plxerr.AuthenticationRequired, "the sign-in has expired; sign in again")
	}
	if err != nil {
		return dbgen.MfaChallenge{}, fmt.Errorf("auth: read a challenge: %w", err)
	}
	return c, nil
}

// BeginWebAuthnLogin returns the request options that answer a sign-in
// challenge with a security key.
func (s *Service) BeginWebAuthnLogin(ctx context.Context, challengeSecret string) ([]byte, error) {
	if err := s.webAuthnRequired(); err != nil {
		return nil, err
	}
	var options []byte
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		c, err := openChallenge(ctx, q, challengeSecret)
		if err != nil {
			return err
		}
		user, err := q.GetUser(ctx, c.UserID)
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		allow, err := credentials(ctx, q, user)
		if err != nil {
			return err
		}
		if len(allow) == 0 {
			return plxerr.New(plxerr.PreconditionFailed, "the account has no security key; answer with a code")
		}
		challenge := newChallenge()
		if err := q.SetChallengeWebAuthn(ctx, dbgen.SetChallengeWebAuthnParams{ID: c.ID, WebauthnChallenge: challenge}); err != nil {
			return fmt.Errorf("auth: record the challenge: %w", err)
		}
		options, err = json.Marshal(map[string]any{
			"challenge": b64(challenge), "rpId": s.o.WebAuthn.RPID, "timeout": webAuthnTimeout,
			"allowCredentials": allow, "userVerification": "preferred",
		})
		return err //nolint:wrapcheck // encoding a map of strings cannot fail
	})
	return options, err
}

// WebAuthnAssertion is a browser's answer to a sign-in challenge.
type WebAuthnAssertion struct {
	CredentialID      []byte
	ClientDataJSON    []byte
	AuthenticatorData []byte
	Signature         []byte
}

// CompleteWebAuthnLogin answers a sign-in challenge with a security key
// and issues the session.
func (s *Service) CompleteWebAuthnLogin(ctx context.Context, challengeSecret string, a WebAuthnAssertion) (Session, error) {
	if err := s.webAuthnRequired(); err != nil {
		return Session{}, err
	}
	var (
		session Session
		account string
		wrong   bool
	)
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		c, err := openChallenge(ctx, q, challengeSecret)
		if err != nil {
			return err
		}
		account = storage.ID(c.UserID)
		if err := s.throttled(ctx, account); err != nil {
			return err
		}
		if c.WebauthnChallenge == nil {
			return plxerr.New(plxerr.PreconditionFailed, "ask for the security key's options first")
		}
		row, err := q.GetUser(ctx, c.UserID)
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		if !s.assertionMatches(ctx, q, c, a) {
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
		return Session{}, plxerr.New(plxerr.AuthenticationRequired, "the security key's answer does not verify")
	}
	s.succeeded(ctx, account)
	return session, nil
}

// assertionMatches verifies an assertion against the user's key and
// advances its counter; the challenge is used once either way.
func (s *Service) assertionMatches(ctx context.Context, q *dbgen.Queries, c dbgen.MfaChallenge, a WebAuthnAssertion) bool {
	defer func() { _ = q.SetChallengeWebAuthn(ctx, dbgen.SetChallengeWebAuthnParams{ID: c.ID}) }()
	f, err := q.GetFactorByCredentialForUpdate(ctx, dbgen.GetFactorByCredentialForUpdateParams{CredentialID: a.CredentialID, UserID: c.UserID})
	if err != nil {
		return false
	}
	stored := uint32(min(max(f.LastCounter, 0), int64(^uint32(0)))) //nolint:gosec // clamped to uint32
	count, err := s.o.WebAuthn.verifyAssertion(c.WebauthnChallenge, f.PublicKey, stored, a.ClientDataJSON, a.AuthenticatorData, a.Signature)
	if err != nil {
		return false
	}
	return q.UseWebAuthnFactor(ctx, dbgen.UseWebAuthnFactorParams{ID: f.ID, LastCounter: int64(count)}) == nil
}
