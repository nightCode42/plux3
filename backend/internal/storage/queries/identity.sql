-- name: CreateUser :one
INSERT INTO users (id, email, display_name, password_hash, installation_admin, invitation_hash, invitation_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: GetUserByInvitation :one
SELECT * FROM users
 WHERE invitation_hash = $1 AND invitation_expires_at > now() AND disabled_at IS NULL;

-- name: AcceptInvitation :one
UPDATE users
   SET password_hash = $2, display_name = $3, invitation_hash = NULL, invitation_expires_at = NULL
 WHERE id = $1 AND invitation_hash IS NOT NULL
RETURNING *;

-- name: RenewInvitation :one
UPDATE users
   SET invitation_hash = $2, invitation_expires_at = $3
 WHERE id = $1 AND password_hash = ''
RETURNING *;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: RecordLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: CountInstallationAdmins :one
SELECT count(*) FROM users WHERE installation_admin AND disabled_at IS NULL;

-- name: CreateFactor :one
INSERT INTO mfa_factors (id, user_id, kind, label, secret)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ConfirmFactor :one
UPDATE mfa_factors
   SET confirmed_at = now(), last_counter = $3, last_used_at = now()
 WHERE id = $1 AND user_id = $2 AND confirmed_at IS NULL
RETURNING *;

-- name: GetFactor :one
SELECT * FROM mfa_factors WHERE id = $1 AND user_id = $2;

-- name: ListFactors :many
SELECT * FROM mfa_factors WHERE user_id = $1 ORDER BY created_at, id;

-- name: ListConfirmedFactorsForUpdate :many
SELECT * FROM mfa_factors
 WHERE user_id = $1 AND confirmed_at IS NOT NULL
 ORDER BY created_at, id
   FOR UPDATE;

-- name: CountConfirmedFactors :one
SELECT count(*) FROM mfa_factors WHERE user_id = $1 AND confirmed_at IS NOT NULL;

-- name: UseFactor :execrows
UPDATE mfa_factors
   SET last_counter = sqlc.arg(counter), last_used_at = now()
 WHERE id = $1 AND last_counter < sqlc.arg(counter);

-- name: DeleteFactor :execrows
DELETE FROM mfa_factors WHERE id = $1 AND user_id = $2;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, secret_hash, csrf_token, mfa_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByHash :one
SELECT s.*, u.display_name, u.installation_admin
  FROM sessions s
  JOIN users u ON u.id = s.user_id
 WHERE s.secret_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
   AND u.disabled_at IS NULL;

-- name: RecordSessionMFA :one
UPDATE sessions SET mfa_at = now() WHERE id = $1 RETURNING *;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
UPDATE sessions SET revoked_at = now()
 WHERE user_id = $1 AND revoked_at IS NULL AND id <> sqlc.arg(keep)::uuid;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() - interval '1 day';

-- name: CreateChallenge :one
INSERT INTO mfa_challenges (id, user_id, secret_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetChallengeForUpdate :one
SELECT * FROM mfa_challenges WHERE secret_hash = $1 AND expires_at > now() FOR UPDATE;

-- name: CountChallengeAttempt :one
UPDATE mfa_challenges SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts;

-- name: DeleteChallenge :exec
DELETE FROM mfa_challenges WHERE id = $1;

-- name: DeleteExpiredChallenges :execrows
DELETE FROM mfa_challenges WHERE expires_at < now();

-- name: CreateAccessToken :one
INSERT INTO access_tokens (id, organization_id, user_id, name, prefix, secret_hash, scopes, source, subject, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetAccessTokenByHash :one
SELECT t.*, coalesce(u.display_name, '')::text AS user_display_name
  FROM access_tokens t
  LEFT JOIN users u ON u.id = t.user_id
 WHERE t.secret_hash = $1 AND t.revoked_at IS NULL AND t.expires_at > now()
   AND (t.user_id IS NULL OR u.disabled_at IS NULL);

-- name: GetAccessToken :one
SELECT * FROM access_tokens WHERE id = $1;

-- name: TouchAccessToken :exec
UPDATE access_tokens SET last_used_at = now()
 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: RevokeAccessToken :execrows
UPDATE access_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: ListAccessTokens :many
SELECT * FROM access_tokens
 WHERE organization_id = $1
   AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
   AND (created_at, id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid)
 ORDER BY created_at, id
 LIMIT sqlc.arg(page_size);

-- name: CreateWorkloadIdentity :one
INSERT INTO workload_identities (id, organization_id, issuer, audience, subject_pattern, scopes, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWorkloadIdentities :many
SELECT * FROM workload_identities
 WHERE organization_id = $1
   AND (sqlc.narg(issuer)::text IS NULL OR issuer = sqlc.narg(issuer)::text)
 ORDER BY created_at, id
 LIMIT sqlc.arg(page_size);

-- name: DeleteWorkloadIdentity :execrows
DELETE FROM workload_identities WHERE id = $1;

-- name: CreateDeviceAuthorization :one
INSERT INTO device_authorizations (id, device_code, user_code, client, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetDeviceAuthorizationByCodeForUpdate :one
SELECT * FROM device_authorizations WHERE device_code = $1 AND expires_at > now() FOR UPDATE;

-- name: GetDeviceAuthorizationByUserCodeForUpdate :one
SELECT * FROM device_authorizations
 WHERE user_code = $1 AND expires_at > now() AND state = 'pending'
   FOR UPDATE;

-- name: RecordDevicePoll :exec
UPDATE device_authorizations SET last_polled_at = now() WHERE id = $1;

-- name: ApproveDeviceAuthorization :execrows
UPDATE device_authorizations
   SET state = 'approved', approved_by = $2, approved_organization_id = $3, scopes = $4
 WHERE id = $1 AND state = 'pending';

-- name: DenyDeviceAuthorization :execrows
UPDATE device_authorizations SET state = 'denied' WHERE id = $1 AND state = 'pending';

-- name: ConsumeDeviceAuthorization :execrows
UPDATE device_authorizations SET state = 'consumed', token_id = $2 WHERE id = $1 AND state = 'approved';

-- name: DeleteExpiredDeviceAuthorizations :execrows
DELETE FROM device_authorizations WHERE expires_at < now() - interval '1 day';

-- name: CreateWebAuthnFactor :one
INSERT INTO mfa_factors (id, user_id, kind, label, secret)
VALUES ($1, $2, 'webauthn', $3, $4)
RETURNING *;

-- name: ConfirmWebAuthnFactor :one
UPDATE mfa_factors
   SET confirmed_at = now(), last_used_at = now(), secret = '', credential_id = $3, public_key = $4, last_counter = $5
 WHERE id = $1 AND user_id = $2 AND kind = 'webauthn' AND confirmed_at IS NULL
RETURNING *;

-- name: GetFactorByCredentialForUpdate :one
SELECT * FROM mfa_factors
 WHERE credential_id = $1 AND user_id = $2 AND kind = 'webauthn' AND confirmed_at IS NOT NULL
   FOR UPDATE;

-- name: UseWebAuthnFactor :exec
UPDATE mfa_factors SET last_counter = $2, last_used_at = now() WHERE id = $1;

-- name: SetChallengeWebAuthn :exec
UPDATE mfa_challenges SET webauthn_challenge = $2 WHERE id = $1;

-- name: GetUserIdentity :one
SELECT * FROM user_identities WHERE issuer = $1 AND subject = $2;

-- name: LinkUserIdentity :one
INSERT INTO user_identities (id, user_id, issuer, subject)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateOIDCLogin :exec
INSERT INTO oidc_logins (state_hash, nonce, verifier, expires_at)
VALUES ($1, $2, $3, $4);

-- name: TakeOIDCLogin :one
DELETE FROM oidc_logins WHERE state_hash = $1 RETURNING *;

-- name: DeleteExpiredOIDCLogins :execrows
DELETE FROM oidc_logins WHERE expires_at < now();

-- name: AcceptInvitationExternally :one
UPDATE users SET invitation_hash = NULL, invitation_expires_at = NULL
 WHERE id = $1
RETURNING *;
