-- name: CreateApp :one
INSERT INTO apps (id, organization_id, key, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetApp :one
SELECT * FROM apps WHERE id = $1 AND deleted_at IS NULL;

-- name: ListApps :many
-- only_ids, when not NULL, restricts the list to the apps a caller was
-- granted one by one (GOV-001).
SELECT * FROM apps
 WHERE organization_id = $1 AND deleted_at IS NULL
   AND (sqlc.narg(only_ids)::uuid[] IS NULL OR id = ANY (sqlc.narg(only_ids)::uuid[]))
   AND (key, id) > (sqlc.arg(after_key)::text, sqlc.arg(after_id)::uuid)
 ORDER BY key, id
 LIMIT sqlc.arg(page_size);

-- name: GetAppIncludingDeleted :one
SELECT * FROM apps WHERE id = $1;

-- name: UpdateApp :one
UPDATE apps
   SET name = $2, default_plugin_key = $3, updated_at = now()
 WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteApp :one
UPDATE apps SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: RestoreApp :one
UPDATE apps SET deleted_at = NULL, updated_at = now() WHERE id = $1 RETURNING *;

-- name: PurgeApp :execrows
DELETE FROM apps WHERE id = $1 AND deleted_at IS NOT NULL;

-- name: CreateEnvironment :one
INSERT INTO environments (id, organization_id, app_id, key, name, production, signing_key_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetEnvironment :one
SELECT * FROM environments WHERE id = $1;

-- name: GetEnvironmentByKey :one
SELECT * FROM environments WHERE app_id = $1 AND key = $2;

-- name: ListEnvironments :many
SELECT * FROM environments WHERE app_id = $1 AND key > sqlc.arg(after_key)::text ORDER BY key LIMIT sqlc.arg(page_size);

-- name: UpdateEnvironment :one
UPDATE environments SET name = $2 WHERE id = $1 RETURNING *;

-- name: DeleteEnvironment :execrows
DELETE FROM environments WHERE id = $1;

-- name: CreateChannel :one
INSERT INTO channels (id, organization_id, environment_id, key)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListChannels :many
-- With the release of each channel's newest signed manifest.
SELECT sqlc.embed(c),
       COALESCE((SELECT m.release_sequence FROM manifests m
                  WHERE m.channel_id = c.id AND m.host_build = ''
                  ORDER BY m.issued_at DESC, m.id DESC LIMIT 1), 0)::bigint AS signed_release_sequence
  FROM channels c
 WHERE c.environment_id = $1 AND c.key > sqlc.arg(after_key)::text
 ORDER BY c.key LIMIT sqlc.arg(page_size);

-- name: GetChannelByID :one
SELECT * FROM channels WHERE id = $1;

-- name: GetChannelByIDForUpdate :one
SELECT * FROM channels WHERE id = $1 FOR UPDATE;

-- name: GetChannel :one
SELECT * FROM channels WHERE environment_id = $1 AND key = $2;

-- name: SetChannelRelease :one
UPDATE channels SET release_sequence = $3, updated_at = now()
 WHERE environment_id = $1 AND key = $2
RETURNING *;

-- name: DeleteChannel :execrows
DELETE FROM channels WHERE id = $1;

-- name: SetVariable :one
INSERT INTO environment_variables (organization_id, environment_id, key, value)
VALUES ($1, $2, $3, $4)
ON CONFLICT (environment_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: ListVariables :many
SELECT * FROM environment_variables WHERE environment_id = $1 AND key > sqlc.arg(after_key)::text ORDER BY key LIMIT sqlc.arg(page_size);

-- name: DeleteVariable :execrows
DELETE FROM environment_variables WHERE environment_id = $1 AND key = $2;

-- name: SetSecret :one
INSERT INTO environment_secrets (organization_id, environment_id, key, ciphertext, wrapped_key, key_id, hint, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (environment_id, key) DO UPDATE
    SET ciphertext = EXCLUDED.ciphertext,
        wrapped_key = EXCLUDED.wrapped_key,
        key_id = EXCLUDED.key_id,
        hint = EXCLUDED.hint,
        updated_by = EXCLUDED.updated_by,
        updated_at = now()
RETURNING *;

-- name: GetSecret :one
SELECT * FROM environment_secrets WHERE environment_id = $1 AND key = $2;

-- name: ListSecrets :many
-- The ciphertext is not selected: a secret is never returned (SEC-106).
SELECT organization_id, environment_id, key, key_id, hint, updated_by, updated_at
  FROM environment_secrets WHERE environment_id = $1 AND key > sqlc.arg(after_key)::text ORDER BY key LIMIT sqlc.arg(page_size);

-- name: DeleteSecret :execrows
DELETE FROM environment_secrets WHERE environment_id = $1 AND key = $2;

-- name: SetLimitOverride :one
INSERT INTO limit_overrides (organization_id, scope, scope_id, limit_key, value)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (scope, scope_id, limit_key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: ListLimitOverrides :many
SELECT * FROM limit_overrides
 WHERE scope = $1 AND scope_id = $2
 ORDER BY limit_key;

-- name: DeleteLimitOverride :execrows
DELETE FROM limit_overrides WHERE scope = $1 AND scope_id = $2 AND limit_key = $3;

-- name: CreateTrashItem :one
INSERT INTO trash (id, organization_id, kind, target_id, app_id, name, deleted_by, purge_after)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetTrashItem :one
SELECT * FROM trash WHERE id = $1;

-- name: GrantAppAccess :one
INSERT INTO app_access (id, organization_id, app_id, team_id, user_id, role)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateAppAccess :one
UPDATE app_access SET role = $2, granted_at = now() WHERE id = $1 RETURNING *;

-- name: FindAppAccess :one
SELECT * FROM app_access
 WHERE app_id = $1
   AND team_id IS NOT DISTINCT FROM sqlc.narg(team_id)::uuid
   AND user_id IS NOT DISTINCT FROM sqlc.narg(user_id)::uuid;

-- name: RevokeAppAccess :execrows
DELETE FROM app_access WHERE id = $1;

-- name: ListAppAccess :many
SELECT * FROM app_access WHERE app_id = $1 AND id > sqlc.arg(after_id)::uuid ORDER BY id LIMIT sqlc.arg(page_size);

-- name: ListAppRolesForUser :many
-- The app roles a user holds directly or through a team (GOV-001).
SELECT a.app_id, a.role FROM app_access a
 WHERE a.organization_id = $1
   AND (a.user_id = sqlc.arg(user_id)::uuid
        OR a.team_id IN (SELECT m.team_id FROM memberships m
                          WHERE m.organization_id = $1 AND m.user_id = sqlc.arg(user_id)::uuid
                            AND m.team_id IS NOT NULL));

-- name: ListTrash :many
SELECT * FROM trash
 WHERE organization_id = $1 AND restored_at IS NULL
   AND (sqlc.narg(app_id)::uuid IS NULL OR app_id = sqlc.narg(app_id)::uuid)
   AND (deleted_at, id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid)
 ORDER BY deleted_at, id
 LIMIT sqlc.arg(page_size);

-- name: MarkTrashRestored :execrows
UPDATE trash SET restored_at = now() WHERE id = $1 AND restored_at IS NULL;

-- name: DeleteTrashItem :execrows
DELETE FROM trash WHERE id = $1;

-- name: ListExpiredTrash :many
SELECT * FROM trash WHERE restored_at IS NULL AND purge_after < now() ORDER BY purge_after LIMIT $1;

-- name: GetTrashItemForUpdate :one
SELECT * FROM trash WHERE id = $1 AND restored_at IS NULL FOR UPDATE;
