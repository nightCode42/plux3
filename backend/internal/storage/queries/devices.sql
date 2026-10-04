-- name: FindAppForRegistration :one
-- Runs in the registration scope: the organisation is not known yet.
SELECT a.organization_id, e.id AS environment_id
  FROM apps a JOIN environments e ON e.app_id = a.id
 WHERE a.id = $1 AND e.key = $2 AND a.deleted_at IS NULL;

-- name: InsertDevice :one
INSERT INTO devices (id, organization_id, app_id, environment_id, platform, os_version, runtime_version, host_build, secret_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetDevice :one
SELECT * FROM devices WHERE id = $1;

-- name: ListDevices :many
SELECT * FROM devices
 WHERE app_id = $1
   AND (sqlc.narg(environment_id)::uuid IS NULL OR environment_id = sqlc.narg(environment_id)::uuid)
   AND (registered_at, id) < (sqlc.arg(before_time)::timestamptz, sqlc.arg(before_id)::uuid)
 ORDER BY registered_at DESC, id DESC
 LIMIT sqlc.arg(page_size);

-- name: TouchDevice :exec
UPDATE devices SET last_seen_at = now() WHERE id = $1;

-- name: SetDeviceSequence :exec
UPDATE devices SET installed_sequence = $2, last_seen_at = now() WHERE id = $1;

-- name: CountDevices :one
SELECT count(*) FROM devices WHERE organization_id = $1;

-- name: UpsertDeviceBundle :exec
INSERT INTO device_bundles (device_id, organization_id, plugin_key, bundle_sha256)
VALUES ($1, $2, $3, $4)
ON CONFLICT (device_id, plugin_key) DO UPDATE SET bundle_sha256 = excluded.bundle_sha256;

-- name: DeleteDeviceBundles :exec
DELETE FROM device_bundles WHERE device_id = $1 AND NOT (plugin_key = ANY (sqlc.arg(keep)::text[]));

-- name: PopularBundles :many
-- The bundles of one plugin held by the most devices (REL-022).
SELECT b.bundle_sha256, count(*) AS devices
  FROM device_bundles b JOIN devices d ON d.id = b.device_id
 WHERE d.app_id = $1 AND b.plugin_key = $2
 GROUP BY b.bundle_sha256
 ORDER BY devices DESC, b.bundle_sha256
 LIMIT sqlc.arg(n);

-- name: CountIncompatibleDevices :one
-- Devices of an app that cannot use a release (REL-080): their runtime
-- is older than it needs, or they report a host build that cannot run it.
-- A release may be promoted to any environment. Versions compare
-- numerically by component.
SELECT count(*) FROM devices
 WHERE app_id = $1
   AND ((sqlc.arg(min_runtime)::text <> ''
         AND string_to_array(NULLIF(substring(runtime_version FROM '^[0-9]+(?:\.[0-9]+)*'), ''), '.')::int[]
             < string_to_array(sqlc.arg(min_runtime)::text, '.')::int[])
        OR host_build = ANY(sqlc.arg(host_builds)::text[]));

-- name: InsertDeviceToken :exec
INSERT INTO device_tokens (id, organization_id, device_id, secret_hash, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: FindDeviceToken :one
-- Runs in the authentication scope.
SELECT t.id, t.organization_id, t.device_id, t.expires_at, d.app_id, d.environment_id, d.host_build
  FROM device_tokens t JOIN devices d ON d.id = t.device_id
 WHERE t.secret_hash = $1;

-- name: FindDeviceSecret :one
-- Runs in the authentication scope.
SELECT id, organization_id, secret_hash FROM devices WHERE id = $1;

-- name: ExpireDeviceTokens :execrows
DELETE FROM device_tokens WHERE expires_at < $1;

-- name: GetDelta :one
SELECT * FROM deltas WHERE from_sha256 = $1 AND to_sha256 = $2;

-- name: InsertDelta :exec
INSERT INTO deltas (organization_id, from_sha256, to_sha256, delta_sha256, size, full_size)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: GetChannelControl :one
SELECT * FROM channel_controls WHERE channel_id = $1;

-- name: UpsertChannelControl :one
INSERT INTO channel_controls (channel_id, organization_id, kill_switch_plugins, app_kill_switch, mandatory_update, message,
                              updated_by_kind, updated_by_id, updated_by, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (channel_id) DO UPDATE
   SET kill_switch_plugins = excluded.kill_switch_plugins, app_kill_switch = excluded.app_kill_switch,
       mandatory_update = excluded.mandatory_update, message = excluded.message,
       updated_by_kind = excluded.updated_by_kind, updated_by_id = excluded.updated_by_id,
       updated_by = excluded.updated_by, updated_at = now()
RETURNING *;

-- name: InsertManifest :one
INSERT INTO manifests (id, organization_id, channel_id, release_sequence, signed, signatures, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: LatestManifest :one
-- The channel's own manifest, the one devices of any build without one
-- of their own receive.
SELECT * FROM manifests WHERE channel_id = $1 AND host_build = '' ORDER BY issued_at DESC, id DESC LIMIT 1;

-- name: ExpiringChannels :many
-- Channels pointing at a release whose newest manifest expires before
-- the given time, or that have none.
SELECT c.* FROM channels c
 WHERE c.release_sequence > 0
   AND NOT EXISTS (SELECT 1 FROM manifests m WHERE m.channel_id = c.id AND m.host_build = '' AND m.expires_at >= sqlc.arg(before)::timestamptz);

-- name: PurgeManifests :execrows
-- Keeps each channel's newest manifest; the host builds' manifests
-- signed with an older one go with it.
DELETE FROM manifests m
 WHERE m.expires_at < $1
   AND m.host_build = ''
   AND m.id <> (SELECT n.id FROM manifests n WHERE n.channel_id = m.channel_id AND n.host_build = ''
                 ORDER BY n.issued_at DESC, n.id DESC LIMIT 1);

-- name: UpsertEnvironmentKey :exec
INSERT INTO environment_keys (environment_id, organization_id, key_id, algorithm, public_key)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: ListEnvironmentKeys :many
SELECT * FROM environment_keys WHERE environment_id = $1 ORDER BY created_at, key_id;

-- name: InsertTelemetryEvent :exec
INSERT INTO telemetry_events (id, organization_id, app_id, environment_id, device_id, name, time, release_sequence,
                              plugin_key, route, fields)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListTelemetryEvents :many
SELECT * FROM telemetry_events
 WHERE app_id = $1 AND environment_id = $2
   AND (sqlc.arg(name)::text = '' OR name = sqlc.arg(name)::text)
   AND time >= sqlc.arg(since)::timestamptz
   AND (time, id) < (sqlc.arg(before_time)::timestamptz, sqlc.arg(before_id)::uuid)
 ORDER BY time DESC, id DESC
 LIMIT sqlc.arg(page_size);

-- name: PurgeTelemetry :execrows
DELETE FROM telemetry_events WHERE received_at < $1;

-- name: RecentVersionBundles :many
-- The bundles of a plugin's newest versions (REL-022); plugin_key is ''
-- for the app bundle.
SELECT bundle_sha256 FROM plugin_versions
 WHERE app_id = $1 AND plugin_key = $2
 ORDER BY version DESC
 LIMIT sqlc.arg(n);

-- name: CountVersionsWithBundle :one
SELECT count(*) FROM plugin_versions WHERE bundle_sha256 = $1;

-- name: UpdateDeviceVersions :exec
-- What a device's latest session_start reports it runs (REL-080).
UPDATE devices SET runtime_version = $2, host_build = $3, os_version = $4 WHERE id = $1;
