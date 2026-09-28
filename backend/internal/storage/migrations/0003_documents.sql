-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Plugins, their drafts and the app's own draft; documents with
-- optimistic concurrency; an append-only history of snapshots; and the
-- exclusive editing locks (SRV-030, SRV-031, SRV-040–SRV-042, ADR-0015).

CREATE TABLE plugins (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id          uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    name            text        NOT NULL,
    -- latest_version is the highest published version (SRV-051).
    latest_version  bigint      NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    CONSTRAINT plugins_key_form CHECK (key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')
);

CREATE UNIQUE INDEX plugins_key ON plugins (app_id, key) WHERE deleted_at IS NULL;
SELECT plux_tenant_policy('plugins');

-- A draft is the current state of one plugin's documents, or of the
-- app-level documents when plugin_id is NULL (SRV-042). Its revision
-- counts accepted writes; its snapshot count numbers its history.
CREATE TABLE drafts (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id          uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    plugin_id       uuid        REFERENCES plugins (id) ON DELETE CASCADE,
    revision        bigint      NOT NULL DEFAULT 0,
    snapshots       bigint      NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT drafts_one_per_owner UNIQUE NULLS NOT DISTINCT (app_id, plugin_id)
);
SELECT plux_tenant_policy('drafts');

-- Document content, compressed and addressed by the SHA-256 of its
-- canonical form, so an unchanged document costs one reference in every
-- snapshot that records it (SRV-031). Blobs are per organisation, so the
-- existence of a document never leaks across tenants.
CREATE TABLE blobs (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    sha256          bytea       NOT NULL,
    size            bigint      NOT NULL,
    content         bytea       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, sha256),
    CONSTRAINT blobs_sha256 CHECK (length(sha256) = 32)
);
SELECT plux_tenant_policy('blobs');

-- The documents of every draft. revision is per document: a write
-- carries the revision it read and is refused when it no longer matches
-- (SRV-030). A deleted page keeps its row until the trash purges it
-- (GOV-031).
CREATE TABLE documents (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    draft_id        uuid        NOT NULL REFERENCES drafts (id) ON DELETE CASCADE,
    path            text        NOT NULL,
    kind            text        NOT NULL,
    -- entity_id and entity_key are the document's own "id" and "key",
    -- for looking up a component or template by its identifier.
    entity_id       uuid,
    entity_key      text        NOT NULL DEFAULT '',
    sha256          bytea       NOT NULL,
    revision        bigint      NOT NULL,
    updated_by_kind text        NOT NULL,
    updated_by_id   text        NOT NULL,
    updated_by      text        NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    FOREIGN KEY (organization_id, sha256) REFERENCES blobs (organization_id, sha256)
);

CREATE UNIQUE INDEX documents_path ON documents (draft_id, path) WHERE deleted_at IS NULL;
CREATE INDEX documents_entity ON documents (entity_id) WHERE entity_id IS NOT NULL;
SELECT plux_tenant_policy('documents');

-- Every accepted write appends a snapshot recording the documents it
-- touched (SRV-031). A snapshot's documents are the paths it changed;
-- the state of the draft at a snapshot is, for each path, its latest
-- entry at or before it. Restoring writes old content forward as a new
-- snapshot; nothing here is ever rewritten.
--
-- reason is 'write', 'delete', 'restore', 'import', 'lock_takeover',
-- 'publish', or 'preserved' for the unsaved work of a holder whose lock
-- was taken over, which is kept but was never applied to the draft
-- (SRV-041). keep marks the source of a published version, retained for
-- ever.
CREATE TABLE snapshots (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    draft_id        uuid        NOT NULL REFERENCES drafts (id) ON DELETE CASCADE,
    sequence        bigint      NOT NULL,
    revision        bigint      NOT NULL,
    reason          text        NOT NULL,
    applied         boolean     NOT NULL,
    actor_kind      text        NOT NULL,
    actor_id        text        NOT NULL,
    actor_display   text        NOT NULL DEFAULT '',
    keep            boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (draft_id, sequence),
    CONSTRAINT snapshots_reason CHECK (reason IN
        ('write', 'delete', 'restore', 'import', 'lock_takeover', 'publish', 'preserved')),
    CONSTRAINT snapshots_preserved CHECK ((reason = 'preserved') = (NOT applied))
);

CREATE INDEX snapshots_retention ON snapshots (created_at) WHERE NOT keep;
SELECT plux_tenant_policy('snapshots');

-- The documents a snapshot recorded. sha256 is NULL for a deletion.
-- carried marks an entry copied into the oldest retained snapshot when
-- older history is purged, so the state at every retained snapshot can
-- still be rebuilt; it is not one the snapshot changed.
CREATE TABLE snapshot_documents (
    snapshot_id     uuid        NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    path            text        NOT NULL,
    kind            text        NOT NULL DEFAULT '',
    sha256          bytea,
    carried         boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (snapshot_id, path),
    FOREIGN KEY (organization_id, sha256) REFERENCES blobs (organization_id, sha256)
);
SELECT plux_tenant_policy('snapshot_documents');

-- The exclusive editing lock of a draft (SRV-040). It expires two
-- minutes after the last heartbeat; requested_by lists who asked for it
-- since it was taken (SRV-041), which the holder sees on its heartbeat.
-- previous_session is the session a takeover displaced, whose late
-- writes are preserved rather than refused outright.
CREATE TABLE locks (
    draft_id         uuid        PRIMARY KEY REFERENCES drafts (id) ON DELETE CASCADE,
    organization_id  uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    holder_kind      text        NOT NULL,
    holder_id        text        NOT NULL,
    holder_display   text        NOT NULL DEFAULT '',
    session          text        NOT NULL,
    acquired_at      timestamptz NOT NULL DEFAULT now(),
    heartbeat_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    previous_session text        NOT NULL DEFAULT '',
    previous_holder  text        NOT NULL DEFAULT '',
    requested_by     jsonb       NOT NULL DEFAULT '[]'
);
SELECT plux_tenant_policy('locks');

-- An audit entry may say more about its action than the fixed fields
-- hold, such as whose lock a takeover displaced (SRV-041). It is hashed
-- with the entry when present, so entries without one keep their hashes.
ALTER TABLE audit_log ADD COLUMN detail text NOT NULL DEFAULT '';
