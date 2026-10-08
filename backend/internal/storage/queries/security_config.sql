-- name: LockEnvironmentForConfig :one
SELECT id FROM environments WHERE id = $1 FOR NO KEY UPDATE;

-- name: LatestSecurityConfig :one
SELECT * FROM security_config_versions
 WHERE environment_id = $1
 ORDER BY version DESC LIMIT 1;

-- name: GetSecurityConfig :one
SELECT * FROM security_config_versions WHERE environment_id = $1 AND version = $2;

-- name: InsertSecurityConfig :one
INSERT INTO security_config_versions
    (organization_id, app_id, environment_id, version, profile, overrides,
     created_by_kind, created_by_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: PruneSecurityConfigs :execrows
DELETE FROM security_config_versions WHERE environment_id = $1 AND version <= $2;

-- name: ListEnvironmentChannelIDs :many
SELECT id FROM channels WHERE environment_id = $1 ORDER BY key;
