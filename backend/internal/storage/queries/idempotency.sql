-- name: BeginIdempotent :one
INSERT INTO idempotency_keys (subject, key, procedure, request_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (subject, key) DO NOTHING
RETURNING *;

-- name: GetIdempotent :one
SELECT * FROM idempotency_keys
 WHERE subject = $1 AND key = $2 AND expires_at > now();

-- name: CompleteIdempotent :execrows
UPDATE idempotency_keys
   SET response = $3, state = 'done'
 WHERE subject = $1 AND key = $2 AND state = 'in_progress';

-- name: AbandonIdempotent :execrows
DELETE FROM idempotency_keys WHERE subject = $1 AND key = $2 AND state = 'in_progress';

-- name: DeleteExpiredIdempotent :execrows
DELETE FROM idempotency_keys WHERE expires_at < now();

-- name: ReplaceExpiredIdempotent :execrows
DELETE FROM idempotency_keys WHERE subject = $1 AND key = $2 AND expires_at <= now();
