-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Installation → Organization → Teams, with apps owned by an
-- organisation and access granted to teams or users per app (GOV-001),
-- and apps owning their environments and channels (GOV-010).

-- Two settings widen what a transaction may see, each for one narrow
-- purpose and never on a caller's behalf:
--
--   plux.user_id  the signed-in user, who may read their own memberships
--                 in every organisation (to list them) but change none;
--   plux.scope    'authentication' while a credential is looked up by
--                 its hash, before its organisation is known, and
--                 'installation' for audit entries that belong to no
--                 organisation, such as a sign-in.
CREATE OR REPLACE FUNCTION plux_current_user() RETURNS uuid
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
        SELECT NULLIF(current_setting('plux.user_id', true), '')::uuid
    $$;

CREATE OR REPLACE FUNCTION plux_in_scope(wanted text) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
        SELECT coalesce(current_setting('plux.scope', true), '') = wanted
    $$;

CREATE TABLE organizations (
    id         uuid        PRIMARY KEY,
    key        text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);

COMMENT ON TABLE organizations IS
    'The tenant boundary. Not itself tenant data: a row is the tenant (GOV-001).';

CREATE TABLE teams (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    name            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key),
    CONSTRAINT teams_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);
SELECT plux_tenant_policy('teams');

-- Users belong to the installation; an organisation grants them access.
CREATE TABLE users (
    id            uuid        PRIMARY KEY,
    email         text        NOT NULL,
    display_name  text        NOT NULL,
    -- password_hash is an Argon2id PHC string; empty until an invited
    -- person accepts the invitation (SEC-100). Single sign-on arrives
    -- with GOV-004 in P9.
    password_hash text        NOT NULL DEFAULT '',
    -- installation_admin may create organisations. The first one is
    -- created by `plux-server bootstrap`.
    installation_admin boolean NOT NULL DEFAULT false,
    -- invitation_hash is the hash of a one-time invitation that lets the
    -- invited person set a password; NULL once it is accepted.
    invitation_hash       bytea UNIQUE,
    invitation_expires_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    disabled_at   timestamptz
);

CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Memberships bind a user to an organisation, optionally through a team,
-- with a role the authorisation layer resolves to permissions (SEC-102).
CREATE TABLE memberships (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    team_id         uuid        REFERENCES teams (id) ON DELETE CASCADE,
    role            text        NOT NULL,
    added_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX memberships_direct
    ON memberships (organization_id, user_id) WHERE team_id IS NULL;
CREATE UNIQUE INDEX memberships_team
    ON memberships (organization_id, user_id, team_id) WHERE team_id IS NOT NULL;
CREATE INDEX memberships_user ON memberships (user_id);

-- A user reads their own memberships in every organisation, which is how
-- the organisations they belong to are listed; changing one still needs
-- the organisation's scope.
ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON memberships
    USING (organization_id = plux_current_organization() OR user_id = plux_current_user())
    WITH CHECK (organization_id = plux_current_organization());

-- Second factors. A user with any confirmed factor must present one
-- before a session is issued for a capability that requires it
-- (SEC-100).
CREATE TABLE mfa_factors (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         text        NOT NULL,
    label        text        NOT NULL DEFAULT '',
    -- secret holds the TOTP secret, encrypted with envelope encryption
    -- (SEC-106). WebAuthn arrives with SEC-103 in P9 as a new kind.
    secret       bytea       NOT NULL,
    -- last_counter is the TOTP time step last accepted; a code for that
    -- step or an earlier one is refused, so a code works only once.
    last_counter bigint      NOT NULL DEFAULT 0,
    confirmed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    CONSTRAINT mfa_factors_kind CHECK (kind IN ('totp'))
);

-- Browser sessions. Only the hash of the secret is stored, so a database
-- copy cannot be replayed as a session (SEC-101).
CREATE TABLE sessions (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    secret_hash   bytea       NOT NULL UNIQUE,
    csrf_token    text        NOT NULL,
    -- mfa_at is when a second factor was last presented in this
    -- session; a capability that requires one checks it.
    mfa_at        timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz
);

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);

-- A sign-in waiting for its second factor. The caller holds the
-- challenge's secret; only its hash is stored, and a challenge accepts
-- a bounded number of wrong codes.
CREATE TABLE mfa_challenges (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    secret_hash bytea       NOT NULL UNIQUE,
    attempts    integer     NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

-- Personal access tokens and tokens exchanged from a CI identity
-- (SRV-064). Only the hash is stored.
CREATE TABLE access_tokens (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid        REFERENCES users (id) ON DELETE CASCADE,
    name            text        NOT NULL,
    prefix          text        NOT NULL,
    secret_hash     bytea       NOT NULL UNIQUE,
    scopes          text[]      NOT NULL DEFAULT '{}',
    -- source is 'pat', 'cli' (from the device authorization grant) or
    -- 'ci' (exchanged from a workload identity, with no user).
    source          text        NOT NULL DEFAULT 'pat',
    -- subject records the CI workload a 'ci' token was minted for.
    subject         text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    CONSTRAINT access_tokens_source CHECK (source IN ('pat', 'cli', 'ci')),
    CONSTRAINT access_tokens_owner CHECK ((source = 'ci') = (user_id IS NULL))
);

CREATE INDEX access_tokens_org ON access_tokens (organization_id);

-- A token is found by its hash before its organisation is known, so the
-- authentication scope may read it; nothing else about the policy
-- differs from plux_tenant_policy.
ALTER TABLE access_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON access_tokens
    USING (organization_id = plux_current_organization() OR plux_in_scope('authentication'))
    WITH CHECK (organization_id = plux_current_organization() OR plux_in_scope('authentication'));

-- CI workloads an organisation trusts: an identity token from the issuer,
-- for the audience, whose subject matches the pattern, may be exchanged
-- for a short-lived token with exactly these scopes (SRV-064).
CREATE TABLE workload_identities (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    issuer          text        NOT NULL,
    audience        text        NOT NULL,
    subject_pattern text        NOT NULL,
    scopes          text[]      NOT NULL,
    created_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workload_identities_issuer ON workload_identities (organization_id, issuer);
SELECT plux_tenant_policy('workload_identities');

-- The OAuth 2.0 device authorization grant `plux login` uses (CLI-002).
CREATE TABLE device_authorizations (
    id           uuid        PRIMARY KEY,
    device_code  bytea       NOT NULL UNIQUE,
    user_code    text        NOT NULL UNIQUE,
    client       text        NOT NULL,
    scopes       text[]      NOT NULL DEFAULT '{}',
    -- state is 'pending', 'approved', 'denied', or 'consumed' once the
    -- approved token has been handed to the polling client.
    state        text        NOT NULL DEFAULT 'pending',
    last_polled_at timestamptz,
    approved_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- The organisation is recorded on approval. It is deliberately not
    -- named organization_id: the row is anonymous until then, and is
    -- found by its unguessable device code rather than by tenant.
    approved_organization_id uuid REFERENCES organizations (id) ON DELETE CASCADE,
    token_id     uuid        REFERENCES access_tokens (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    CONSTRAINT device_authorizations_state CHECK (state IN ('pending', 'approved', 'denied', 'consumed'))
);

CREATE TABLE apps (
    id                 uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    key                text        NOT NULL,
    name               text        NOT NULL,
    default_plugin_key text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    deleted_at         timestamptz,
    CONSTRAINT apps_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);

CREATE UNIQUE INDEX apps_key ON apps (organization_id, key) WHERE deleted_at IS NULL;
SELECT plux_tenant_policy('apps');

-- Roles on one app, held by a team or a user (GOV-001). Organisation-wide
-- roles in memberships apply to every app already.
CREATE TABLE app_access (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id          uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    team_id         uuid        REFERENCES teams (id) ON DELETE CASCADE,
    user_id         uuid        REFERENCES users (id) ON DELETE CASCADE,
    role            text        NOT NULL,
    granted_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_access_holder CHECK ((team_id IS NULL) <> (user_id IS NULL))
);

CREATE UNIQUE INDEX app_access_team ON app_access (app_id, team_id) WHERE team_id IS NOT NULL;
CREATE UNIQUE INDEX app_access_user ON app_access (app_id, user_id) WHERE user_id IS NOT NULL;
SELECT plux_tenant_policy('app_access');

CREATE TABLE environments (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id          uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    name            text        NOT NULL,
    production      boolean     NOT NULL DEFAULT false,
    signing_key_ref text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, key),
    CONSTRAINT environments_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);
SELECT plux_tenant_policy('environments');

CREATE TABLE channels (
    id               uuid        PRIMARY KEY,
    organization_id  uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    environment_id   uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    key              text        NOT NULL,
    release_sequence bigint      NOT NULL DEFAULT 0,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment_id, key),
    CONSTRAINT channels_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);
SELECT plux_tenant_policy('channels');

CREATE TABLE environment_variables (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    value           text        NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, key)
);
SELECT plux_tenant_policy('environment_variables');

-- Secrets are encrypted with envelope encryption under a KMS key and are
-- never returned after creation (SEC-106).
CREATE TABLE environment_secrets (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    ciphertext      bytea       NOT NULL,
    wrapped_key     bytea       NOT NULL,
    key_id          text        NOT NULL,
    hint            text        NOT NULL DEFAULT '',
    updated_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, key)
);
SELECT plux_tenant_policy('environment_secrets');

-- Limits tightened below the installation defaults (LIM-002).
CREATE TABLE limit_overrides (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- scope is 'organization', 'app' or 'plugin'; scope_id is the app or
    -- plugin, and the organisation itself for the organisation scope.
    scope           text        NOT NULL,
    scope_id        uuid        NOT NULL,
    limit_key       text        NOT NULL,
    value           bigint      NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, scope_id, limit_key),
    CONSTRAINT limit_overrides_scope CHECK (scope IN ('organization', 'app', 'plugin'))
);
SELECT plux_tenant_policy('limit_overrides');

-- Deleted apps, plugins and pages wait here for 30 days (GOV-031).
CREATE TABLE trash (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            text        NOT NULL,
    target_id       uuid        NOT NULL,
    -- app_id is the app the item belongs to, or is, so that an app's
    -- trash can be listed on its own.
    app_id          uuid        NOT NULL,
    name            text        NOT NULL,
    deleted_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    deleted_at      timestamptz NOT NULL DEFAULT now(),
    purge_after     timestamptz NOT NULL,
    restored_at     timestamptz,
    CONSTRAINT trash_kind CHECK (kind IN ('app', 'plugin', 'page'))
);

CREATE INDEX trash_purge ON trash (purge_after) WHERE restored_at IS NULL;
CREATE INDEX trash_app ON trash (app_id, deleted_at);
SELECT plux_tenant_policy('trash');

-- Every state-changing operation and every security-relevant event
-- (SEC-140). Each scope — an organisation, or the installation itself
-- for events that belong to no organisation, such as a sign-in — is its
-- own append-only chain: every entry commits to the previous one, so a
-- deletion or an edit is detectable.
CREATE TABLE audit_log (
    id              uuid        PRIMARY KEY,
    -- organization_id is NULL for an installation-scope entry. An
    -- organisation with audit entries cannot be deleted.
    organization_id uuid        REFERENCES organizations (id),
    sequence        bigint      NOT NULL,
    occurred_at     timestamptz NOT NULL,
    actor_kind      text        NOT NULL,
    actor_id        text        NOT NULL DEFAULT '',
    actor_display   text        NOT NULL DEFAULT '',
    action          text        NOT NULL,
    target_kind     text        NOT NULL DEFAULT '',
    target_id       text        NOT NULL DEFAULT '',
    source_ip       text        NOT NULL DEFAULT '',
    user_agent      text        NOT NULL DEFAULT '',
    request_id      text        NOT NULL DEFAULT '',
    before_hash     text        NOT NULL DEFAULT '',
    after_hash      text        NOT NULL DEFAULT '',
    previous_hash   text        NOT NULL,
    entry_hash      text        NOT NULL,
    CONSTRAINT audit_log_sequence UNIQUE NULLS NOT DISTINCT (organization_id, sequence)
);

-- The audit log is tenant data with one addition to the usual policy: an
-- installation-scope entry is visible, and writable, only in a
-- transaction that declared the installation scope. No request handler
-- declares it on a caller's behalf.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON audit_log USING (
    organization_id = plux_current_organization()
    OR (organization_id IS NULL AND plux_in_scope('installation'))
);

-- Append-only in the database too: an entry can be neither changed nor
-- removed, whatever the caller, so the chain is a check on the database
-- as well as on the application.
CREATE OR REPLACE FUNCTION plux_audit_append_only() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
    BEGIN
        RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'insufficient_privilege';
    END;
    $$;

CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION plux_audit_append_only();
CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION plux_audit_append_only();

-- A mutating call may be retried with the same key within 24 hours and
-- must return the original result (SRV-005). Keys belong to the
-- credential's subject — a user, a token, a device — not to an
-- organisation, because several calls that take a key act in none. Only
-- the same subject can reach an entry, and the stored response is
-- sealed with envelope encryption, since some responses carry a secret
-- that is returned exactly once (SEC-106).
CREATE TABLE idempotency_keys (
    subject      text        NOT NULL,
    key          text        NOT NULL,
    procedure    text        NOT NULL,
    request_hash bytea       NOT NULL,
    response     bytea,
    -- state is 'in_progress' or 'done'.
    state        text        NOT NULL DEFAULT 'in_progress',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (subject, key),
    CONSTRAINT idempotency_state CHECK (state IN ('in_progress', 'done'))
);

CREATE INDEX idempotency_expiry ON idempotency_keys (expires_at);

-- Keys the installation itself holds, such as the one that
-- authenticates page tokens (SRV-004). Each value is sealed with
-- envelope encryption by the signing backend before it is stored
-- (SEC-106, SEC-120), so a copy of the database does not reveal it.
CREATE TABLE installation_secrets (
    name       text        PRIMARY KEY,
    sealed     bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
