-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Uploaded asset files (SRV-060, CMP-030). The file itself is an object
-- in object storage, addressed by its SHA-256 after metadata is stripped;
-- the app-level draft's assets/index.json lists it. A row is replaced,
-- not changed, when a file is uploaded again under the same name.

CREATE TABLE assets (
    id                uuid        PRIMARY KEY,
    -- asset_id is the asset's identifier in assets/index.json, which
    -- stays when the file is uploaded again.
    asset_id          uuid        NOT NULL,
    organization_id   uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id            uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    -- file is the path under assets/ the index lists.
    file              text        NOT NULL,
    media_type        text        NOT NULL,
    sha256            bytea       NOT NULL,
    size              bigint      NOT NULL,
    width             integer     NOT NULL DEFAULT 0,
    height            integer     NOT NULL DEFAULT 0,
    -- processing is 'pending' until a raster image's variants are made.
    processing        text        NOT NULL,
    -- variants lists the transcoded forms: media type, density, size in
    -- pixels, SHA-256 and bytes (CMP-030).
    variants          jsonb       NOT NULL DEFAULT '[]',
    diagnostics       jsonb       NOT NULL DEFAULT '[]',
    uploaded_by_kind  text        NOT NULL,
    uploaded_by_id    text        NOT NULL,
    uploaded_by       text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    CONSTRAINT assets_processing CHECK (processing IN ('pending', 'ready', 'failed'))
);
CREATE UNIQUE INDEX assets_file ON assets (app_id, file) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX assets_current ON assets (app_id, asset_id) WHERE deleted_at IS NULL;
CREATE INDEX assets_content ON assets (organization_id, sha256);
SELECT plux_tenant_policy('assets');
