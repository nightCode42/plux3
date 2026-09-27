-- name: CreateOrganization :one
INSERT INTO organizations (id, key, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = $1;

-- name: UpdateOrganization :one
UPDATE organizations SET name = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListOrganizationsForUser :many
-- Memberships are readable by their own user in every organisation, so
-- this runs without an organisation scope.
SELECT o.* FROM organizations o
 WHERE o.id IN (SELECT m.organization_id FROM memberships m WHERE m.user_id = $1)
   AND (o.key, o.id) > (sqlc.arg(after_key)::text, sqlc.arg(after_id)::uuid)
 ORDER BY o.key, o.id
 LIMIT sqlc.arg(page_size);

-- name: CreateTeam :one
INSERT INTO teams (id, organization_id, key, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;

-- name: ListTeams :many
SELECT * FROM teams
 WHERE organization_id = $1 AND (key, id) > (sqlc.arg(after_key)::text, sqlc.arg(after_id)::uuid)
 ORDER BY key, id
 LIMIT sqlc.arg(page_size);

-- name: UpdateTeam :one
UPDATE teams SET name = $2 WHERE id = $1 RETURNING *;

-- name: DeleteTeam :execrows
DELETE FROM teams WHERE id = $1;

-- name: AddMembership :one
INSERT INTO memberships (id, organization_id, user_id, team_id, role)
VALUES ($1, $2, $3, NULL, $4)
ON CONFLICT (organization_id, user_id) WHERE team_id IS NULL
DO UPDATE SET role = EXCLUDED.role
RETURNING *;

-- name: AddTeamMembership :one
INSERT INTO memberships (id, organization_id, user_id, team_id, role)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (organization_id, user_id, team_id) WHERE team_id IS NOT NULL
DO UPDATE SET role = EXCLUDED.role
RETURNING *;

-- name: RemoveMembership :execrows
DELETE FROM memberships
 WHERE organization_id = $1 AND user_id = $2
   AND team_id IS NOT DISTINCT FROM sqlc.narg(team_id)::uuid;

-- name: ListMembers :many
SELECT m.*, u.display_name, u.email
  FROM memberships m
  JOIN users u ON u.id = m.user_id
 WHERE m.organization_id = $1
   AND m.team_id IS NOT DISTINCT FROM sqlc.narg(team_id)::uuid
   AND (m.user_id, m.id) > (sqlc.arg(after_user)::uuid, sqlc.arg(after_id)::uuid)
 ORDER BY m.user_id, m.id
 LIMIT sqlc.arg(page_size);

-- name: ListMembershipsForUser :many
SELECT * FROM memberships WHERE organization_id = $1 AND user_id = $2 ORDER BY id;

-- name: ListAllMembershipsForUser :many
SELECT * FROM memberships WHERE user_id = $1 ORDER BY organization_id, id LIMIT sqlc.arg(page_size);

-- name: CountOwners :one
SELECT count(DISTINCT user_id) FROM memberships
 WHERE organization_id = $1 AND team_id IS NULL AND role = 'owner';

-- name: ListOrganizationIDs :many
-- For maintenance that visits every tenant in turn, such as purging
-- the trash; organisations are not tenant data.
SELECT id FROM organizations WHERE id > sqlc.arg(after_id)::uuid ORDER BY id LIMIT sqlc.arg(page_size);
