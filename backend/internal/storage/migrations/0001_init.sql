-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- The foundation every later migration builds on: the setting that
-- carries the caller's organisation through a transaction, and the
-- function every row-level security policy compares against (SRV-022).

-- plux.organization_id is set per transaction by the pool, from the
-- authenticated principal. An unset or empty setting matches nothing, so
-- a query that forgets to set it returns no tenant rows rather than all
-- of them.
CREATE OR REPLACE FUNCTION plux_current_organization() RETURNS uuid
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
        SELECT NULLIF(current_setting('plux.organization_id', true), '')::uuid
    $$;

COMMENT ON FUNCTION plux_current_organization() IS
    'The organisation of the current transaction; NULL when unset (SRV-022).';

-- plux_tenant_policy enables row-level security on a table that holds
-- tenant data and adds the policy that binds it to the setting above.
-- Every migration that creates such a table calls it, so the guarantee
-- cannot be lost by forgetting a statement.
CREATE OR REPLACE FUNCTION plux_tenant_policy(target regclass) RETURNS void
    LANGUAGE plpgsql
    AS $$
    BEGIN
        EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', target);
        EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', target);
        EXECUTE format(
            'CREATE POLICY plux_tenant ON %s USING (organization_id = plux_current_organization())',
            target);
    END;
    $$;

COMMENT ON FUNCTION plux_tenant_policy(regclass) IS
    'Enables row-level security on a tenant table and binds it to plux_current_organization() (SRV-022).';
