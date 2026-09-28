-- name: LockAuditChain :exec
-- Serialises appends to one chain for the rest of the transaction, so
-- two writers cannot both extend the same entry (SEC-140).
SELECT pg_advisory_xact_lock(hashtextextended('plux.audit:' || coalesce(sqlc.narg(organization_id)::uuid::text, 'installation'), 0));

-- name: LastAuditEntry :one
SELECT * FROM audit_log
 WHERE organization_id IS NOT DISTINCT FROM sqlc.narg(organization_id)::uuid
 ORDER BY sequence DESC
 LIMIT 1;

-- name: AppendAuditEntry :one
INSERT INTO audit_log (
    id, organization_id, sequence, occurred_at, actor_kind, actor_id, actor_display,
    action, target_kind, target_id, source_ip, user_agent, request_id,
    before_hash, after_hash, previous_hash, entry_hash, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
RETURNING *;

-- name: ListAuditEntries :many
SELECT * FROM audit_log
 WHERE organization_id IS NOT DISTINCT FROM sqlc.narg(organization_id)::uuid
   AND sequence > sqlc.arg(after_sequence)
 ORDER BY sequence
 LIMIT sqlc.arg(page_size);

-- name: GetAuditEntry :one
SELECT * FROM audit_log
 WHERE organization_id IS NOT DISTINCT FROM sqlc.narg(organization_id)::uuid
   AND sequence = $1;
