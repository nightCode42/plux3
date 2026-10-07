-- SPDX-FileCopyrightText: 2026 Plux contributors
-- SPDX-License-Identifier: AGPL-3.0-only

-- Device trust: a device registers with a hardware-bound DPoP key and
-- platform attestation instead of a shared secret (SEC-001, SEC-003,
-- SEC-005–SEC-007, ADR-0012). The secret flow of P2 stays until the
-- runtime switches over, so a device registered with it has no key and a
-- device registered with a key has an empty secret hash, which no secret
-- matches. The column is dropped in the release after the secret flow
-- goes (DEP-030).

-- dpop_jkt is the RFC 7638 thumbprint every token of the device is bound
-- to; dpop_public_key is the JWK as the device sent it.
ALTER TABLE devices ADD COLUMN dpop_jkt text;
ALTER TABLE devices ADD COLUMN dpop_public_key bytea;

-- key_storage is where the attestation proved the private key lives
-- (SEC-002). A device without a key has none to report.
ALTER TABLE devices ADD COLUMN key_storage text NOT NULL DEFAULT 'unspecified';
ALTER TABLE devices ADD CONSTRAINT devices_key_storage
    CHECK (key_storage IN ('unspecified', 'software', 'tee', 'strongbox', 'secure_enclave'));

-- What the server keeps of the last verified attestation (SEC-003): the
-- verdicts it accepted, never the raw evidence.
ALTER TABLE devices ADD COLUMN attestation_provider text;
ALTER TABLE devices ADD COLUMN attestation_verdicts text[] NOT NULL DEFAULT '{}';
ALTER TABLE devices ADD COLUMN attestation_risk_metric integer NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN attested_at timestamptz;

-- The App Attest key of an iOS device, its signature counter and the
-- receipt Apple issued for it. The receipt is sensitive and is read only
-- by the fraud-metric check.
ALTER TABLE devices ADD COLUMN app_attest_key_id bytea;
ALTER TABLE devices ADD COLUMN app_attest_public_key bytea;
ALTER TABLE devices ADD COLUMN app_attest_counter bigint NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN app_attest_receipt bytea;

-- A revoked device keeps its row, so the audit trail still points at it
-- (SEC-006).
ALTER TABLE devices ADD COLUMN revoked_at timestamptz;
ALTER TABLE devices ADD COLUMN revoked_reason text NOT NULL DEFAULT '';

-- One key belongs to one device.
CREATE UNIQUE INDEX devices_dpop_jkt ON devices (dpop_jkt) WHERE dpop_jkt IS NOT NULL;
