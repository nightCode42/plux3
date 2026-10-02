-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Host builds and their native catalogues (ADR-0041).

-- name: GetNativeCatalogue :one
SELECT * FROM native_catalogues WHERE app_id = $1 AND host_build = $2;

-- name: InsertNativeCatalogue :one
-- A concurrent upload of the same build loses: the caller reads the
-- stored catalogue again and compares.
INSERT INTO native_catalogues (organization_id, app_id, host_build, catalogue, sha256,
                               uploaded_by_kind, uploaded_by_id, uploaded_by, uploaded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (app_id, host_build) DO NOTHING
RETURNING *;

-- name: ListNativeCatalogues :many
-- An app's host builds, newest upload first, after a cursor.
SELECT * FROM native_catalogues
 WHERE app_id = $1
   AND (uploaded_at, host_build) < (sqlc.arg(before_time)::timestamptz, sqlc.arg(before_build)::text)
 ORDER BY uploaded_at DESC, host_build DESC
 LIMIT sqlc.arg(page_size);

-- name: ListAllNativeCatalogues :many
SELECT * FROM native_catalogues WHERE app_id = $1 ORDER BY host_build;

-- name: CountDevicesByHostBuild :many
-- How many of an app's devices report each host build.
SELECT host_build, count(*) AS devices FROM devices WHERE app_id = $1 GROUP BY host_build;

-- name: SetVersionNativeUses :exec
UPDATE plugin_versions SET native_uses = $2 WHERE id = $1;

-- name: SetReleaseNativeUses :exec
UPDATE releases SET native_uses = $2 WHERE id = $1;

-- name: InsertBuildManifest :one
INSERT INTO manifests (id, organization_id, channel_id, release_sequence, signed, signatures, issued_at, expires_at,
                       host_build, own_manifest_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: LatestManifestForBuild :one
-- The manifest a device of a host build receives: the build's own when
-- the channel's newest manifest was signed with one, else the channel's.
WITH own AS (
    SELECT o.id FROM manifests o WHERE o.channel_id = $1 AND o.host_build = '' ORDER BY o.issued_at DESC, o.id DESC LIMIT 1
)
SELECT m.* FROM manifests m, own
 WHERE m.id = own.id OR (m.own_manifest_id = own.id AND m.host_build = sqlc.arg(host_build)::text)
 ORDER BY (m.host_build <> '') DESC
 LIMIT 1;

-- name: ListAppChannels :many
-- Every channel of every environment of an app.
SELECT c.* FROM channels c JOIN environments e ON e.id = c.environment_id
 WHERE e.app_id = $1 ORDER BY c.id;
