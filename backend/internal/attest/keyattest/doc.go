// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package keyattest verifies Android Key Attestation certificate chains
// (SEC-002, ADR-0012). A device generates its DPoP key in the Android
// Keystore and presents the attestation chain for it; Verifier proves that
// the chain leads to a Google attestation root, that no certificate on it is
// revoked, that the attested key is the P-256 key of the leaf, that the
// attestation challenge is the one the server issued, and that the key
// description satisfies a Policy (application identity, hardware backing,
// verified boot).
//
// Invariants:
//
//   - Deny by default: every failure returns an error and a zero Result; a
//     Result is only ever produced for a chain that passed every check.
//   - Nothing beyond the certificates is trusted. The key description is read
//     only from the leaf, and the root of trust only from the hardware-enforced
//     authorisation list.
//   - The package performs no I/O and reads no clock of its own. Time and the
//     revocation list are injected; fetching and caching Google's status list
//     is the caller's concern, this package only parses and applies it.
//   - Error messages name the failed check only; keys, certificates and
//     attestation blobs never appear in them (SEC-092).
//
// Failures are plxerr.AttestationFailed, except a key that is attested but
// not backed by a TEE or StrongBox when the policy requires it, which is
// plxerr.KeyNotHardwareBacked.
package keyattest
