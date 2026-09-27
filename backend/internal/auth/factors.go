// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Factor is an enrolled second factor.
type Factor struct {
	ID         string
	Kind       string
	Label      string
	Confirmed  bool
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// Enrollment is a new TOTP factor with its secret, which is returned
// exactly once.
type Enrollment struct {
	Factor     Factor
	Secret     string
	OTPAuthURL string
}

// sessionOnly refuses a credential other than a browser session: second
// factors belong to a person, and a token must not manage them.
func sessionOnly(id Identity) error {
	if id.Kind != KindUser || id.UserID == "" {
		return plxerr.New(plxerr.PermissionDenied, "second factors are managed in a browser session")
	}
	return nil
}

// EnrollTOTP adds an unconfirmed TOTP factor and returns its secret
// once, with the URL an authenticator application scans (SEC-100). The
// secret is sealed with envelope encryption at rest (SEC-106).
func (s *Service) EnrollTOTP(ctx context.Context, id Identity, label string) (Enrollment, error) {
	if err := sessionOnly(id); err != nil {
		return Enrollment{}, err
	}
	label = strings.TrimSpace(label)
	if len(label) > 100 {
		return Enrollment{}, plxerr.New(plxerr.InvalidFormat, "a factor's label is at most 100 characters")
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return Enrollment{}, err
	}
	factorID, err := s.newID()
	if err != nil {
		return Enrollment{}, err
	}
	sealed, err := signing.Seal(ctx, s.o.Crypter, []byte(secret), factorBinding(factorID))
	if err != nil {
		return Enrollment{}, fmt.Errorf("auth: %w", err)
	}
	var out Enrollment
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		user := storage.MustUUID(id.UserID)
		row, err := q.CreateFactor(ctx, dbgen.CreateFactorParams{
			ID: storage.MustUUID(factorID), UserID: user, Kind: "totp", Label: label, Secret: sealed.Encode(),
		})
		if err != nil {
			return fmt.Errorf("auth: enrol a factor: %w", err)
		}
		account, err := q.GetUser(ctx, user)
		if err != nil {
			return fmt.Errorf("auth: read a user: %w", err)
		}
		out = Enrollment{Factor: factorOf(row), Secret: secret, OTPAuthURL: TOTPURL(s.o.Issuer, account.Email, secret)}
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.FactorEnrolled, TargetKind: "factor", TargetID: factorID,
		})
	})
	return out, err
}

// factorBinding ties a sealed TOTP secret to its factor, so that one
// factor's ciphertext cannot be copied onto another.
func factorBinding(factorID string) []byte { return []byte("mfa-factor:" + factorID) }

// openFactor decrypts a factor's secret.
func (s *Service) openFactor(ctx context.Context, f dbgen.MfaFactor) ([]byte, error) {
	sealed, err := signing.DecodeSealed(f.Secret)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	secret, err := signing.Open(ctx, s.o.Crypter, sealed, factorBinding(storage.ID(f.ID)))
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return secret, nil
}

// ConfirmFactor checks a code against an enrolled factor and marks it
// usable. Until then it is not asked for at sign-in. Confirming proves
// possession of the factor, so the session counts as having presented
// one.
func (s *Service) ConfirmFactor(ctx context.Context, id Identity, factorID, code string) (Factor, error) {
	if err := sessionOnly(id); err != nil {
		return Factor{}, err
	}
	fid, err := parseID(factorID, "factor")
	if err != nil {
		return Factor{}, err
	}
	if err := s.throttled(ctx, id.UserID); err != nil {
		return Factor{}, err
	}
	var (
		out   Factor
		wrong bool
	)
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		user := storage.MustUUID(id.UserID)
		row, err := q.GetFactor(ctx, dbgen.GetFactorParams{ID: fid, UserID: user})
		if err != nil {
			return notFound(err, "factor")
		}
		if row.ConfirmedAt.Valid {
			return plxerr.New(plxerr.PreconditionFailed, "the factor is already confirmed")
		}
		secret, err := s.openFactor(ctx, row)
		if err != nil {
			return err
		}
		step, matched := matchCode(string(secret), code, s.now())
		if !matched {
			wrong = true
			return nil
		}
		confirmed, err := q.ConfirmFactor(ctx, dbgen.ConfirmFactorParams{ID: fid, UserID: user, LastCounter: step})
		if err != nil {
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
	if err != nil {
		return Factor{}, err
	}
	if wrong {
		s.failed(ctx, id.UserID)
		return Factor{}, plxerr.New(plxerr.AuthenticationRequired, "the code is wrong")
	}
	return out, nil
}

// ListFactors returns the caller's factors.
func (s *Service) ListFactors(ctx context.Context, id Identity) ([]Factor, error) {
	if err := sessionOnly(id); err != nil {
		return nil, err
	}
	var out []Factor
	err := s.inTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListFactors(ctx, storage.MustUUID(id.UserID))
		if err != nil {
			return fmt.Errorf("auth: list the factors: %w", err)
		}
		out = make([]Factor, len(rows))
		for i, row := range rows {
			out[i] = factorOf(row)
		}
		return nil
	})
	return out, err
}

// DeleteFactor removes one of the caller's factors. It needs a second
// factor in the session: otherwise whoever holds a stolen session could
// remove the factor that protects the account.
func (s *Service) DeleteFactor(ctx context.Context, id Identity, factorID string) error {
	if err := sessionOnly(id); err != nil {
		return err
	}
	if !id.SecondFactor {
		return plxerr.New(plxerr.MultiFactorRequired, "removing a second factor requires presenting one in this session")
	}
	fid, err := parseID(factorID, "factor")
	if err != nil {
		return err
	}
	return s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := dbgen.New(tx).DeleteFactor(ctx, dbgen.DeleteFactorParams{ID: fid, UserID: storage.MustUUID(id.UserID)})
		if err != nil {
			return fmt.Errorf("auth: delete a factor: %w", err)
		}
		if n == 0 {
			return plxerr.New(plxerr.ResourceNotFound, "no such factor")
		}
		return s.record(ctx, tx, audit.Entry{
			Actor: id.Actor(), Action: audit.FactorDeleted, TargetKind: "factor", TargetID: factorID,
		})
	})
}

// factorOf converts a stored factor.
func factorOf(row dbgen.MfaFactor) Factor {
	return Factor{
		ID:         storage.ID(row.ID),
		Kind:       row.Kind,
		Label:      row.Label,
		Confirmed:  row.ConfirmedAt.Valid,
		CreatedAt:  storage.Time(row.CreatedAt),
		LastUsedAt: storage.Time(row.LastUsedAt),
	}
}
