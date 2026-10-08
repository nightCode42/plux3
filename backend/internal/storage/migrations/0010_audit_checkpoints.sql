-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Signed checkpoints over each organisation's audit chain (SEC-141). A
-- checkpoint commits to the chain's entry hash at one sequence number
-- with a key the database never holds, so rewriting the whole chain
-- from some entry onwards is detected: the rewritten hashes no longer
-- match the signed one, and a checkpoint cannot be re-signed without
-- the key.
CREATE TABLE audit_checkpoints (
    id              uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (id),
    -- sequence is the sequence number of the last entry covered.
    sequence        bigint      NOT NULL CHECK (sequence > 0),
    -- entry_hash is that entry's hash, in hexadecimal.
    entry_hash      text        NOT NULL,
    key_id          text        NOT NULL,
    algorithm       text        NOT NULL,
    signature       bytea       NOT NULL,
    created_at      timestamptz NOT NULL,
    CONSTRAINT audit_checkpoints_sequence UNIQUE (organization_id, sequence)
);
SELECT plux_tenant_policy('audit_checkpoints');

-- Append-only like the log it vouches for.
CREATE TRIGGER audit_checkpoints_append_only
    BEFORE UPDATE OR DELETE ON audit_checkpoints
    FOR EACH ROW EXECUTE FUNCTION plux_audit_append_only();
CREATE TRIGGER audit_checkpoints_no_truncate
    BEFORE TRUNCATE ON audit_checkpoints
    FOR EACH STATEMENT EXECUTE FUNCTION plux_audit_append_only();
