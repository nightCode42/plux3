-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- OpenID Connect sign-in and WebAuthn security keys (SEC-100,
-- ADR-0026 as revised on 2026-09-28).

-- A WebAuthn factor stores the credential's identifier and COSE public
-- key; last_counter holds the authenticator's signature counter. While
-- the factor is unconfirmed, secret holds the registration challenge,
-- and it is emptied once the registration verifies.
ALTER TABLE mfa_factors DROP CONSTRAINT mfa_factors_kind;
ALTER TABLE mfa_factors ADD CONSTRAINT mfa_factors_kind CHECK (kind IN ('totp', 'webauthn'));
ALTER TABLE mfa_factors ADD COLUMN credential_id bytea UNIQUE;
ALTER TABLE mfa_factors ADD COLUMN public_key bytea;
ALTER TABLE mfa_factors ADD CONSTRAINT mfa_factors_webauthn
    CHECK (kind <> 'webauthn' OR confirmed_at IS NULL OR (credential_id IS NOT NULL AND public_key IS NOT NULL));

-- The WebAuthn challenge of a sign-in, issued when the browser asks for
-- one and consumed by the answer.
ALTER TABLE mfa_challenges ADD COLUMN webauthn_challenge bytea;

-- The OpenID Connect identities linked to an account: one per issuer
-- and subject. A person signs in with OIDC only once an account exists
-- for them — created by invitation — and the first sign-in links it by
-- the provider's verified address. Provisioning from the provider
-- arrives with GOV-004 in P9.
CREATE TABLE user_identities (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer     text        NOT NULL,
    subject    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);
CREATE INDEX user_identities_user ON user_identities (user_id);

-- A sign-in in progress with the provider: the hash of its state, and
-- the nonce and PKCE verifier sealed for the round trip.
CREATE TABLE oidc_logins (
    state_hash bytea       PRIMARY KEY,
    nonce      text        NOT NULL,
    verifier   bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
