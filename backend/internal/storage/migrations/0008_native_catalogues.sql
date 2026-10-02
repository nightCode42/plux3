-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Host builds and their native catalogues (CLI-006, WGT-032, REL-080,
-- ADR-0041).

-- The native catalogue of one build of the host app: the native routes,
-- slots and custom actions it registers for plugins, uploaded by
-- `plux native sync`. A build's native code never changes after it ships,
-- so neither does its catalogue. It is stored canonicalised (RFC 8785,
-- ADR-0025) with its SHA-256. host_build is the string devices report,
-- such as 1.4.0+52.
CREATE TABLE native_catalogues (
    organization_id  uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id           uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    host_build       text        NOT NULL,
    catalogue        bytea       NOT NULL,
    sha256           bytea       NOT NULL,
    uploaded_by_kind text        NOT NULL,
    uploaded_by_id   text        NOT NULL,
    uploaded_by      text        NOT NULL,
    uploaded_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, host_build),
    CONSTRAINT native_catalogues_host_build CHECK (host_build <> '' AND length(host_build) <= 64)
);
CREATE INDEX native_catalogues_uploaded ON native_catalogues (app_id, uploaded_at DESC, host_build DESC);
SELECT plux_tenant_policy('native_catalogues');

-- The native entries a version's plugin, or a release's plugins, use,
-- each with the types it was compiled against (compiler.NativeUse): a
-- host build can run the release when its catalogue declares every one
-- the same way (REL-080).
ALTER TABLE plugin_versions ADD COLUMN native_uses jsonb NOT NULL DEFAULT '[]';
ALTER TABLE releases ADD COLUMN native_uses jsonb NOT NULL DEFAULT '[]';

-- A manifest for the devices of one host build that cannot run their
-- channel's release names the newest release they can (REL-080); '' is
-- the channel's own manifest. A build's manifest is signed with the
-- channel's own, which own_manifest_id names, and is served only while
-- that one is the channel's newest.
ALTER TABLE manifests ADD COLUMN host_build text NOT NULL DEFAULT '';
ALTER TABLE manifests ADD COLUMN own_manifest_id uuid REFERENCES manifests (id) ON DELETE CASCADE;
ALTER TABLE manifests ADD CONSTRAINT manifests_own CHECK ((host_build = '') = (own_manifest_id IS NULL));
CREATE INDEX manifests_own ON manifests (own_manifest_id, host_build) WHERE own_manifest_id IS NOT NULL;

-- Counting a build's devices.
CREATE INDEX devices_host_build ON devices (app_id, host_build);
