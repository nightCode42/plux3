-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- The security configuration of an environment: a profile and the
-- operator's overrides, one immutable row per version (SEC-180, SEC-182,
-- ADR-0053). Version 0 is the built-in default and has no row. A change
-- adds the next version; the server keeps the newest few so that it can
-- compute the merge patch from a version a device still holds, and
-- deletes the rest.
CREATE TABLE security_config_versions (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id          uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    version         bigint      NOT NULL,
    profile         text        NOT NULL,
    -- overrides maps a setting key to its value; only settings that differ
    -- from the profile's preset appear.
    overrides       jsonb       NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    created_by_kind text        NOT NULL,
    created_by_id   text        NOT NULL,
    created_by      text        NOT NULL,
    PRIMARY KEY (environment_id, version),
    CONSTRAINT security_config_versions_version CHECK (version >= 1),
    CONSTRAINT security_config_versions_profile CHECK (profile IN ('standard', 'strict', 'maximum')),
    CONSTRAINT security_config_versions_overrides CHECK (jsonb_typeof(overrides) = 'object')
);
ALTER TABLE security_config_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_config_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY plux_tenant ON security_config_versions
    USING (organization_id = plux_current_organization())
    WITH CHECK (organization_id = plux_current_organization());
-- A registering or authenticating device must apply its environment's
-- configuration before its organisation is known to the caller; both scopes
-- may read, neither may write.
CREATE POLICY plux_device_read ON security_config_versions FOR SELECT
    USING (plux_in_scope('registration') OR plux_in_scope('authentication'));
