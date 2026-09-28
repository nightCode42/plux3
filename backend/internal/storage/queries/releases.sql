-- name: LockApp :one
SELECT id FROM apps WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: CreatePublishJob :one
INSERT INTO publish_jobs (id, organization_id, app_id, plugin_id, environment_id, revision, snapshot_id, state,
                          label, notes, acknowledge_warnings, actor_kind, actor_id, actor_display)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetPublishJob :one
SELECT * FROM publish_jobs WHERE id = $1;

-- name: GetPublishJobForUpdate :one
SELECT * FROM publish_jobs WHERE id = $1 FOR UPDATE;

-- name: ListPublishJobs :many
SELECT * FROM publish_jobs
 WHERE app_id = $1
   AND (sqlc.narg(plugin_id)::uuid IS NULL OR plugin_id = sqlc.narg(plugin_id)::uuid)
   AND (created_at, id) < (sqlc.arg(before_time)::timestamptz, sqlc.arg(before_id)::uuid)
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(page_size);

-- name: ProgressPublishJob :one
UPDATE publish_jobs SET state = 'running', stage = $2, percent = $3
 WHERE id = $1 AND state IN ('queued', 'running')
RETURNING *;

-- name: FinishPublishJob :one
UPDATE publish_jobs
   SET state = $2, stage = $3, percent = $4, diagnostics = $5, version = $6, finished_at = now()
 WHERE id = $1 AND state IN ('queued', 'running')
RETURNING *;

-- name: CancelPublishJob :one
UPDATE publish_jobs SET state = 'cancelled', finished_at = now()
 WHERE id = $1 AND state IN ('queued', 'running')
RETURNING *;

-- name: NextVersion :one
SELECT (COALESCE(MAX(version), 0) + 1)::bigint FROM plugin_versions
 WHERE app_id = $1 AND plugin_id IS NOT DISTINCT FROM sqlc.narg(plugin_id)::uuid;

-- name: InsertPluginVersion :one
INSERT INTO plugin_versions (id, organization_id, app_id, plugin_id, plugin_key, version, label, notes,
                             bundle_sha256, bundle_size, source_map_sha256, required_features, min_runtime,
                             signature, key_id, algorithm, environment_id, source_snapshot_id, sources,
                             published_by_kind, published_by_id, published_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
RETURNING *;

-- name: GetVersion :one
SELECT * FROM plugin_versions
 WHERE app_id = $1 AND plugin_id IS NOT DISTINCT FROM sqlc.narg(plugin_id)::uuid AND version = $2;

-- name: GetVersionByID :one
SELECT * FROM plugin_versions WHERE id = $1;

-- name: LatestVersion :one
SELECT * FROM plugin_versions
 WHERE app_id = $1 AND plugin_id IS NOT DISTINCT FROM sqlc.narg(plugin_id)::uuid
 ORDER BY version DESC
 LIMIT 1;

-- name: ListVersions :many
SELECT * FROM plugin_versions
 WHERE app_id = $1 AND plugin_id IS NOT DISTINCT FROM sqlc.narg(plugin_id)::uuid
   AND version < sqlc.arg(before_version)::bigint
 ORDER BY version DESC
 LIMIT sqlc.arg(page_size);

-- name: NextReleaseSequence :one
SELECT (COALESCE(MAX(sequence), 0) + 1)::bigint FROM releases WHERE app_id = $1;

-- name: InsertRelease :one
INSERT INTO releases (id, organization_id, app_id, environment_id, sequence, app_version_id, rollback_of, notes,
                      min_runtime, required_features, size, created_by_kind, created_by_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: InsertReleaseVersion :exec
INSERT INTO release_versions (release_id, organization_id, plugin_version_id) VALUES ($1, $2, $3);

-- name: GetRelease :one
SELECT * FROM releases WHERE app_id = $1 AND sequence = $2;

-- name: ListReleases :many
SELECT * FROM releases
 WHERE app_id = $1
   AND (sqlc.narg(environment_id)::uuid IS NULL OR environment_id = sqlc.narg(environment_id)::uuid)
   AND sequence < sqlc.arg(before_sequence)::bigint
 ORDER BY sequence DESC
 LIMIT sqlc.arg(page_size);

-- name: ListAllReleases :many
SELECT * FROM releases WHERE app_id = $1 ORDER BY sequence;

-- name: ListReleaseVersions :many
SELECT v.* FROM plugin_versions v
  JOIN release_versions rv ON rv.plugin_version_id = v.id
 WHERE rv.release_id = $1
 ORDER BY v.plugin_key;

-- name: LatestReleaseInEnvironment :one
SELECT * FROM releases WHERE app_id = $1 AND environment_id = $2 ORDER BY sequence DESC LIMIT 1;

-- name: MarkReleaseProduction :exec
UPDATE releases SET production = true WHERE id = $1;

-- name: GetChannelForUpdate :one
SELECT * FROM channels WHERE environment_id = $1 AND key = $2 FOR UPDATE;

-- name: PointChannel :one
UPDATE channels SET release_sequence = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListPurgeableReleases :many
-- Development releases past their retention: never promoted to a
-- production environment, current on no channel, and not the app's
-- newest (REL-007).
SELECT r.* FROM releases r
 WHERE NOT r.production
   AND r.created_at < sqlc.arg(before)::timestamptz
   AND r.sequence < (SELECT MAX(sequence) FROM releases n WHERE n.app_id = r.app_id)
   AND NOT EXISTS (
       SELECT 1 FROM channels c JOIN environments e ON e.id = c.environment_id
        WHERE e.app_id = r.app_id AND c.release_sequence = r.sequence)
 LIMIT sqlc.arg(page_size);

-- name: DeleteRelease :exec
DELETE FROM releases WHERE id = $1;

-- name: DeleteUnreferencedVersions :many
-- Versions no retained release holds, published before the window, and
-- not their plugin's newest.
DELETE FROM plugin_versions v
 WHERE v.created_at < sqlc.arg(before)::timestamptz
   AND NOT EXISTS (SELECT 1 FROM release_versions rv WHERE rv.plugin_version_id = v.id)
   AND NOT EXISTS (SELECT 1 FROM releases r WHERE r.app_version_id = v.id)
   AND v.version < (SELECT MAX(version) FROM plugin_versions n
                     WHERE n.app_id = v.app_id AND n.plugin_id IS NOT DISTINCT FROM v.plugin_id)
RETURNING v.id;

-- name: ReleaseUnusedSnapshots :execrows
-- Snapshots kept for versions that no longer exist go back to ordinary
-- retention (SRV-031).
UPDATE snapshots s SET keep = false
 WHERE s.keep AND s.organization_id = $1
   AND NOT EXISTS (SELECT 1 FROM plugin_versions v WHERE s.id = ANY (v.sources))
   AND NOT EXISTS (SELECT 1 FROM publish_jobs j WHERE j.snapshot_id = s.id AND j.state IN ('queued', 'running'));

-- name: LatestReleaseSize :one
SELECT COALESCE((SELECT size FROM releases WHERE app_id = $1 ORDER BY sequence DESC LIMIT 1), 0)::bigint;

-- name: CountPages :one
SELECT count(*) FROM documents d JOIN drafts dr ON dr.id = d.draft_id
 WHERE dr.app_id = $1 AND dr.plugin_id = $2 AND d.kind = 'page' AND d.deleted_at IS NULL;
