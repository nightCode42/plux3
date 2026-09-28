-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Plugin versions, app releases and publish jobs (SRV-050–SRV-052,
-- REL-001–REL-007, ADR-0020).

-- An immutable compiled bundle: a plugin's, or the app's own when
-- plugin_id is NULL. The bundle and its source map are objects in object
-- storage; the signature covers the bundle hash (SRV-052).
CREATE TABLE plugin_versions (
    id                  uuid        PRIMARY KEY,
    organization_id     uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id              uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    plugin_id           uuid        REFERENCES plugins (id) ON DELETE CASCADE,
    plugin_key          text        NOT NULL,
    version             bigint      NOT NULL,
    label               text        NOT NULL DEFAULT '',
    notes               text        NOT NULL DEFAULT '',
    bundle_sha256       bytea       NOT NULL,
    bundle_size         bigint      NOT NULL,
    source_map_sha256   bytea,
    required_features   text[]      NOT NULL DEFAULT '{}',
    min_runtime         text        NOT NULL DEFAULT '',
    signature           bytea       NOT NULL,
    key_id              text        NOT NULL,
    algorithm           text        NOT NULL,
    environment_id      uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    -- source_snapshot_id is this draft's snapshot; sources are every
    -- snapshot compiled with it, kept while the version exists.
    source_snapshot_id  uuid        NOT NULL,
    sources             uuid[]      NOT NULL,
    published_by_kind   text        NOT NULL,
    published_by_id     text        NOT NULL,
    published_by        text        NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (app_id, plugin_id, version)
);
CREATE INDEX plugin_versions_bundle ON plugin_versions (organization_id, bundle_sha256);
SELECT plux_tenant_policy('plugin_versions');

-- An immutable set of one version per active plugin and one app bundle
-- (REL-002). production records that it was ever promoted to a
-- production environment, which keeps it for ever (REL-007).
CREATE TABLE releases (
    id                uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id            uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    environment_id    uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    sequence          bigint      NOT NULL,
    app_version_id    uuid        NOT NULL REFERENCES plugin_versions (id),
    rollback_of       bigint      NOT NULL DEFAULT 0,
    notes             text        NOT NULL DEFAULT '',
    min_runtime       text        NOT NULL DEFAULT '',
    required_features text[]      NOT NULL DEFAULT '{}',
    size              bigint      NOT NULL,
    production        boolean     NOT NULL DEFAULT false,
    created_by_kind   text        NOT NULL,
    created_by_id     text        NOT NULL,
    created_by        text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, sequence)
);
SELECT plux_tenant_policy('releases');

CREATE TABLE release_versions (
    release_id        uuid NOT NULL REFERENCES releases (id) ON DELETE CASCADE,
    organization_id   uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plugin_version_id uuid NOT NULL REFERENCES plugin_versions (id),
    PRIMARY KEY (release_id, plugin_version_id)
);
CREATE INDEX release_versions_version ON release_versions (plugin_version_id);
SELECT plux_tenant_policy('release_versions');

-- A publish: durable, resumable, and without side effects until it
-- records its version (SRV-050, SRV-051).
CREATE TABLE publish_jobs (
    id                   uuid        PRIMARY KEY,
    organization_id      uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id               uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    plugin_id            uuid        REFERENCES plugins (id) ON DELETE CASCADE,
    environment_id       uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    revision             bigint      NOT NULL,
    snapshot_id          uuid        NOT NULL,
    state                text        NOT NULL,
    stage                text        NOT NULL DEFAULT '',
    percent              integer     NOT NULL DEFAULT 0,
    label                text        NOT NULL DEFAULT '',
    notes                text        NOT NULL DEFAULT '',
    acknowledge_warnings boolean     NOT NULL DEFAULT false,
    version              bigint      NOT NULL DEFAULT 0,
    diagnostics          jsonb       NOT NULL DEFAULT '[]',
    actor_kind           text        NOT NULL,
    actor_id             text        NOT NULL,
    actor_display        text        NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    finished_at          timestamptz,
    CONSTRAINT publish_jobs_state CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled'))
);
CREATE INDEX publish_jobs_app ON publish_jobs (app_id, created_at DESC);
SELECT plux_tenant_policy('publish_jobs');
