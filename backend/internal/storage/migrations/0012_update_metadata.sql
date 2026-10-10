-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Update metadata: the root, snapshot and timestamp roles beside the
-- targets role, which is the manifest (SEC-050, SEC-051, ADR-0054). The
-- worker signs snapshot and timestamp; an operator uploads each root,
-- signed offline, and the server never holds a root key.

-- Each file as it is served, byte for byte: the timestamp hashes the
-- snapshot's bytes, so they are stored, not rebuilt. Versions only grow
-- within an environment and role (the primary key refuses a repeat).
CREATE TABLE update_metadata (
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    role            text        NOT NULL,
    version         bigint      NOT NULL,
    document        bytea       NOT NULL,
    sha256          bytea       NOT NULL,
    issued_at       timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (environment_id, role, version),
    CONSTRAINT update_metadata_role CHECK (role IN ('root', 'snapshot', 'timestamp')),
    CONSTRAINT update_metadata_version CHECK (version >= 1)
);
CREATE INDEX update_metadata_expiry ON update_metadata (expires_at);
SELECT plux_tenant_policy('update_metadata');
-- The files are signed and public: the metadata scope reads them by
-- environment, and nothing else.
CREATE POLICY plux_metadata ON update_metadata FOR SELECT USING (plux_in_scope('metadata'));

-- The targets role has no file of its own: its version is the counter
-- below, stamped into every manifest signed together and pinned by the
-- snapshot. Zero until the environment has a root.
ALTER TABLE environments ADD COLUMN targets_version bigint NOT NULL DEFAULT 0;

-- A recorded key names the role it signs for and the type of environment
-- it belongs to, so that a production runtime can refuse a development
-- key (SEC-056).
ALTER TABLE environment_keys ADD COLUMN role text NOT NULL DEFAULT 'targets';
ALTER TABLE environment_keys ADD COLUMN environment_type text NOT NULL DEFAULT 'development';
ALTER TABLE environment_keys ADD CONSTRAINT environment_keys_role
    CHECK (role IN ('targets', 'snapshot', 'timestamp'));
ALTER TABLE environment_keys ADD CONSTRAINT environment_keys_type
    CHECK (environment_type IN ('production', 'development'));
UPDATE environment_keys k SET environment_type = 'production'
  FROM environments e WHERE e.id = k.environment_id AND e.production;
