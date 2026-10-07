// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package playintegrity verifies Google Play Integrity standard-request
// tokens locally.
//
// The token is a compact JWE (alg A256KW, enc A256GCM) whose plaintext is a
// compact JWS (alg ES256) carrying the verdict JSON. The decryption key and
// the verification key are the ones the developer downloads from the Play
// Console; Google's decode API is never called, so no device or app
// identifier leaves the server (SEC-003, ADR-0012).
//
// Invariants:
//
//   - Only the algorithms named above are accepted; the allow-lists are
//     explicit and anything else, including "none", is rejected.
//   - A token is decrypted and its signature verified before any verdict
//     field is read or trusted.
//   - The verdict must name the expected package, carry the expected request
//     hash (the challenge bound to the DPoP key thumbprint, see RequestHash),
//     be fresh, and describe a Play-recognised app.
//   - Errors name the failed check only; they never contain keys, tokens or
//     verdict content (SEC-092).
//   - Verifier holds no mutable state and is safe for concurrent use.
//
// Mapping a verdict to an assurance level is the concern of the device-trust
// profile, not of this package.
package playintegrity
