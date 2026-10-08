-- name: BumpTargetsVersion :one
-- The next version of the targets role; the row lock orders concurrent
-- signings of one environment.
UPDATE environments SET targets_version = targets_version + 1 WHERE id = $1
RETURNING targets_version;

-- name: InsertMetadata :exec
INSERT INTO update_metadata (environment_id, organization_id, role, version, document, sha256, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: LatestMetadata :one
SELECT * FROM update_metadata WHERE environment_id = $1 AND role = $2
 ORDER BY version DESC LIMIT 1;

-- name: GetMetadata :one
SELECT * FROM update_metadata WHERE environment_id = $1 AND role = $2 AND version = $3;

-- name: ListRootsSince :many
SELECT * FROM update_metadata
 WHERE environment_id = $1 AND role = 'root' AND version > $2
 ORDER BY version;

-- name: LatestMetadataVersions :one
-- The newest version of each role, 0 for a role with no file.
SELECT COALESCE(max(version) FILTER (WHERE role = 'root'), 0)::bigint AS root_version,
       COALESCE(max(version) FILTER (WHERE role = 'snapshot'), 0)::bigint AS snapshot_version,
       COALESCE(max(version) FILTER (WHERE role = 'timestamp'), 0)::bigint AS timestamp_version
  FROM update_metadata WHERE environment_id = $1;

-- name: ListEnvironmentsWithRoot :many
-- The environments an operator has enrolled in update metadata.
SELECT e.* FROM environments e
 WHERE EXISTS (SELECT 1 FROM update_metadata m WHERE m.environment_id = e.id AND m.role = 'root')
 ORDER BY e.id;

-- name: ListChannelsOfEnvironment :many
SELECT * FROM channels WHERE environment_id = $1 ORDER BY id;

-- name: PurgeMetadata :execrows
-- Expired snapshots and timestamps other than the newest of each; roots
-- are kept for ever, so that a device can follow the whole chain.
DELETE FROM update_metadata m
 WHERE m.role <> 'root' AND m.expires_at < $1
   AND m.version < (SELECT max(n.version) FROM update_metadata n
                     WHERE n.environment_id = m.environment_id AND n.role = m.role);

-- name: LockEnvironment :one
SELECT * FROM environments WHERE id = $1 FOR UPDATE;
