-- name: CreateInstallationSecret :exec
INSERT INTO installation_secrets (name, sealed) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING;

-- name: GetInstallationSecret :one
SELECT sealed FROM installation_secrets WHERE name = $1;
