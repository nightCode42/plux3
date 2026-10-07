// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package devtoken issues and verifies device access tokens: RFC 9068
// JWTs signed with ES256 that are bound to a device's DPoP key
// (SEC-020, ADR-0012 "Tokens"). It builds the compact serialisation
// itself and hands only the signing input to signing.TokenSigner, so
// the private key stays in signing/ (L-3).
//
// Invariants:
//
//   - Only ES256 is accepted; none, HS*, RS* and every other algorithm
//     are refused before a key is looked at (SEC-020).
//   - A token lives at most fifteen minutes; the issuer refuses a longer
//     lifetime and the verifier refuses a longer token (SEC-020).
//   - The token's key class must match the environment verifying it: a
//     development key never vouches for a production environment, nor a
//     production key for a development one (SEC-056).
//   - The key identifier is in the protected header and must name a key
//     the verifier was given; a token cannot bring its own key.
//   - The token carries the DPoP key thumbprint in cnf.jkt, so a stolen
//     token is useless without the device key (SEC-020).
//   - Tokens, signatures and keys never appear in errors or logs
//     (SEC-092); a failed check names the check, not the value.
//
// Issuer and Verifier take their clock and randomness as fields; nothing
// here reads the wall clock or the system random source unless the
// caller left them unset.
package devtoken
