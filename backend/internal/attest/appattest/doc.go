// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package appattest verifies Apple App Attest evidence (SEC-004,
// ADR-0012): the attestation a device produces once for a freshly
// generated key, and the assertions it produces for later requests.
//
// An attestation proves that a key lives in the Secure Enclave of a
// genuine Apple device running the expected app. VerifyAttestation checks
// the certificate chain against Apple's App Attestation root, the nonce
// that binds the attestation to the server's challenge and the DPoP key
// (ClientDataHash), the key identifier, the relying-party hash of the
// application identifier, the zero counter and the environment in the
// authenticator data. An assertion proves possession of the attested key
// for one request; VerifyAssertion checks its signature, application
// identifier and that its counter increased.
//
// Invariants: input is size-bounded before it is parsed; every failure is
// a plxerr.AttestationFailed naming the failed check, and never contains
// keys, receipts or attestation blobs (SEC-092); nonces are compared in
// constant time. A Verifier holds no mutable state and is safe for
// concurrent use. Counter storage, with compare-and-set, belongs to the
// caller.
package appattest
