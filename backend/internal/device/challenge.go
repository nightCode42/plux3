// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// challengeBytes is the entropy of a challenge.
const challengeBytes = 32

// Cache keys: a challenge is stored under the hash of its bytes, so the
// cache never holds the value a device must present.
const (
	challengePrefix     = "regchallenge:"
	challengeUsedPrefix = "regchallenge:used:"
)

// CreateChallenge issues a single-use challenge bound to an app and an
// environment, which a registration or a re-attestation must present
// within the registrationChallengeTtl setting of its environment
// (SEC-005). It returns the challenge and when it stops being accepted.
func (s *Service) CreateChallenge(ctx context.Context, appID, environment string) ([]byte, time.Time, error) {
	if s.cache == nil {
		return nil, time.Time{}, plxerr.New(plxerr.AttestationUnavailable, "the challenge store is not configured")
	}
	found, err := s.findEnvironment(ctx, appID, environment)
	if err != nil {
		return nil, time.Time{}, err
	}
	conf, err := s.settingsFor(ctx, canonicalID(appID), storage.ID(found.EnvironmentID))
	if err != nil {
		return nil, time.Time{}, err
	}
	challenge := make([]byte, challengeBytes)
	if _, err := io.ReadFull(s.random, challenge); err != nil {
		return nil, time.Time{}, fmt.Errorf("device: challenge: %w", err)
	}
	value := challengeValue(canonicalID(appID), storage.ID(found.EnvironmentID))
	if err := s.cache.Set(ctx, challengeKey(challengePrefix, challenge), value, conf.RegistrationChallengeTTL); err != nil {
		return nil, time.Time{}, plxerr.Wrap(plxerr.AttestationUnavailable, err, "the challenge store is unavailable")
	}
	return challenge, s.now().Add(conf.RegistrationChallengeTTL).UTC().Truncate(time.Second), nil
}

// consumeChallenge accepts a challenge exactly once, and only for the
// app and environment it was issued for, and remembers it as spent for
// ttl. A challenge presented for another app is refused without being
// spent.
func (s *Service) consumeChallenge(ctx context.Context, challenge []byte, appID, envID string, ttl time.Duration) error {
	if s.cache == nil {
		return plxerr.New(plxerr.AttestationUnavailable, "the challenge store is not configured")
	}
	if len(challenge) != challengeBytes {
		return plxerr.New(plxerr.RegistrationChallengeInvalid, "the challenge is not one this server issued")
	}
	issued, found, err := s.cache.Get(ctx, challengeKey(challengePrefix, challenge))
	if err != nil {
		return plxerr.Wrap(plxerr.AttestationUnavailable, err, "the challenge store is unavailable")
	}
	if !found {
		return plxerr.New(plxerr.RegistrationChallengeInvalid, "the challenge is unknown or has expired")
	}
	if subtle.ConstantTimeCompare(issued, challengeValue(appID, envID)) != 1 {
		return plxerr.New(plxerr.RegistrationChallengeInvalid, "the challenge was issued for another app or environment")
	}
	first, err := s.cache.SetNX(ctx, challengeKey(challengeUsedPrefix, challenge), []byte{1}, ttl)
	if err != nil {
		return plxerr.Wrap(plxerr.AttestationUnavailable, err, "the challenge store is unavailable")
	}
	if !first {
		return plxerr.New(plxerr.RegistrationChallengeInvalid, "the challenge was already used")
	}
	return nil
}

// canonicalID writes an identifier the validated way, so the same app
// compares equal however a device spelled it.
func canonicalID(id string) string {
	return storage.ID(storage.MustUUID(id))
}

// challengeKey is the cache key of a challenge: the prefix and the
// base64url SHA-256 of its bytes.
func challengeKey(prefix string, challenge []byte) string {
	sum := sha256.Sum256(challenge)
	return prefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

// challengeValue is what the cache stores for a challenge.
func challengeValue(appID, envID string) []byte {
	return []byte(appID + "\x00" + envID)
}

// findEnvironment resolves the app and environment a device names, in the
// registration scope.
func (s *Service) findEnvironment(ctx context.Context, appID, environment string) (dbgen.FindEnvironmentForRegistrationRow, error) {
	app, err := storage.UUID(appID)
	if err != nil {
		return dbgen.FindEnvironmentForRegistrationRow{}, plxerr.New(plxerr.InvalidFormat, "the app identifier is not valid")
	}
	var found dbgen.FindEnvironmentForRegistrationRow
	err = s.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeRegistration}, func(ctx context.Context, tx pgx.Tx) error {
		found, err = dbgen.New(tx).FindEnvironmentForRegistration(ctx, dbgen.FindEnvironmentForRegistrationParams{ID: app, Key: environment})
		return err //nolint:wrapcheck // translated below
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return found, plxerr.New(plxerr.ResourceNotFound, "no such app or environment")
	}
	if err != nil {
		return found, fmt.Errorf("device: %w", err)
	}
	return found, nil
}
