-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Devices, their access tokens, deltas, channel controls, signed
-- manifests and telemetry (GOV-010, REL-020–REL-033, SRV-065, ADR-0003,
-- ADR-0004).

-- A device registering names its app before its organisation is known,
-- so the registration scope may read apps and environments, and nothing
-- else about their policies differs.
CREATE POLICY plux_registration ON apps FOR SELECT USING (plux_in_scope('registration'));
CREATE POLICY plux_registration ON environments FOR SELECT USING (plux_in_scope('registration'));

-- A device installation of an app in one environment (GOV-010). The
-- credential is stored only as a hash; from P6 it is replaced by a
-- hardware-bound key (SEC-020).
CREATE TABLE devices (
    id                 uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id             uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    environment_id     uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    platform           text        NOT NULL,
    os_version         text        NOT NULL DEFAULT '',
    runtime_version    text        NOT NULL DEFAULT '',
    host_build         text        NOT NULL DEFAULT '',
    assurance_level    text        NOT NULL DEFAULT 'none',
    secret_hash        bytea       NOT NULL,
    installed_sequence bigint      NOT NULL DEFAULT 0,
    registered_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT devices_platform CHECK (platform IN ('android', 'ios', 'web', 'macos', 'windows', 'linux'))
);
CREATE INDEX devices_app ON devices (app_id, environment_id, registered_at DESC, id DESC);
ALTER TABLE devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE devices FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON devices
    USING (organization_id = plux_current_organization() OR plux_in_scope('authentication'))
    WITH CHECK (organization_id = plux_current_organization() OR plux_in_scope('authentication'));

-- The bundles a device reported holding, which choose the deltas worth
-- precomputing (REL-022) and count devices a release would strand
-- (REL-080). plugin_key is '' for the app bundle.
CREATE TABLE device_bundles (
    device_id       uuid  NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    organization_id uuid  NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plugin_key      text  NOT NULL,
    bundle_sha256   bytea NOT NULL,
    PRIMARY KEY (device_id, plugin_key)
);
CREATE INDEX device_bundles_bundle ON device_bundles (organization_id, bundle_sha256);
SELECT plux_tenant_policy('device_bundles');

-- Short-lived device access tokens; only the hash is stored.
CREATE TABLE device_tokens (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    device_id       uuid        NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    secret_hash     bytea       NOT NULL UNIQUE,
    expires_at      timestamptz NOT NULL
);
CREATE INDEX device_tokens_expiry ON device_tokens (expires_at);
ALTER TABLE device_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON device_tokens
    USING (organization_id = plux_current_organization() OR plux_in_scope('authentication'))
    WITH CHECK (organization_id = plux_current_organization() OR plux_in_scope('authentication'));

-- A computed delta between two bundles, stored in object storage under
-- the hash of its own bytes (REL-024).
CREATE TABLE deltas (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    from_sha256     bytea       NOT NULL,
    to_sha256       bytea       NOT NULL,
    delta_sha256    bytea       NOT NULL,
    size            bigint      NOT NULL,
    -- full_size is the new bundle's zstd-compressed size, the other
    -- side of the 60 % rule (REL-023).
    full_size       bigint      NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, from_sha256, to_sha256)
);
SELECT plux_tenant_policy('deltas');

-- The switches of one channel (REL-030).
CREATE TABLE channel_controls (
    channel_id          uuid        PRIMARY KEY REFERENCES channels (id) ON DELETE CASCADE,
    organization_id     uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kill_switch_plugins text[]      NOT NULL DEFAULT '{}',
    app_kill_switch     boolean     NOT NULL DEFAULT false,
    mandatory_update    boolean     NOT NULL DEFAULT false,
    message             text        NOT NULL DEFAULT '',
    updated_by_kind     text        NOT NULL,
    updated_by_id       text        NOT NULL,
    updated_by          text        NOT NULL,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
SELECT plux_tenant_policy('channel_controls');

-- A signed manifest of a channel. The worker signs one whenever the
-- channel's release or controls change, or the newest nears its expiry;
-- the api role serves the newest (SRV-052, REL-031).
CREATE TABLE manifests (
    id               uuid        PRIMARY KEY,
    organization_id  uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    channel_id       uuid        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    release_sequence bigint      NOT NULL,
    signed           bytea       NOT NULL,
    signatures       jsonb       NOT NULL,
    issued_at        timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL
);
CREATE INDEX manifests_channel ON manifests (channel_id, issued_at DESC, id DESC);
SELECT plux_tenant_policy('manifests');

-- The public keys an environment has signed with, for GetRootKeys
-- (SEC-051). The worker records a key the first time it signs with it.
CREATE TABLE environment_keys (
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    key_id          text        NOT NULL,
    algorithm       text        NOT NULL,
    public_key      bytea       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, key_id)
);
SELECT plux_tenant_policy('environment_keys');

-- Runtime telemetry events (Appendix G.2). Fields are the event's
-- declared, non-sensitive properties only.
CREATE TABLE telemetry_events (
    id               uuid        PRIMARY KEY,
    organization_id  uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id           uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    environment_id   uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    device_id        uuid        NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    name             text        NOT NULL,
    time             timestamptz NOT NULL,
    release_sequence bigint      NOT NULL DEFAULT 0,
    plugin_key       text        NOT NULL DEFAULT '',
    route            text        NOT NULL DEFAULT '',
    fields           jsonb       NOT NULL DEFAULT '{}',
    received_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telemetry_events_app ON telemetry_events (app_id, environment_id, time DESC, id DESC);
CREATE INDEX telemetry_events_received ON telemetry_events (received_at);
SELECT plux_tenant_policy('telemetry_events');
