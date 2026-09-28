-- name: InsertAsset :one
INSERT INTO assets (id, asset_id, organization_id, app_id, file, media_type, sha256, size, width, height, processing,
                    uploaded_by_kind, uploaded_by_id, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: RetireAssetFile :execrows
UPDATE assets SET deleted_at = now() WHERE app_id = $1 AND file = $2 AND deleted_at IS NULL;

-- name: GetAsset :one
SELECT * FROM assets WHERE asset_id = $1 AND deleted_at IS NULL ORDER BY created_at LIMIT 1;

-- name: GetAssetIncludingDeleted :one
SELECT * FROM assets WHERE id = $1;

-- name: GetAssetForUpdate :one
SELECT * FROM assets WHERE id = $1 FOR UPDATE;

-- name: GetAssetByFile :one
SELECT * FROM assets WHERE app_id = $1 AND file = $2 AND deleted_at IS NULL;

-- name: ListAssets :many
SELECT * FROM assets
 WHERE app_id = $1 AND deleted_at IS NULL AND file > sqlc.arg(after_file)::text
 ORDER BY file
 LIMIT sqlc.arg(page_size);

-- name: ListAllAssets :many
SELECT * FROM assets WHERE app_id = $1 AND deleted_at IS NULL ORDER BY file;

-- name: FindProcessedAsset :one
-- Another asset with the same content whose variants are made, so an
-- upload of the same file is not transcoded twice.
SELECT * FROM assets
 WHERE organization_id = $1 AND sha256 = $2 AND processing = 'ready' AND id <> $3
 ORDER BY created_at
 LIMIT 1;

-- name: CompleteAsset :one
UPDATE assets
   SET processing = $2, width = $3, height = $4, variants = $5, diagnostics = $6
 WHERE id = $1
RETURNING *;
