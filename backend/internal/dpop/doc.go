// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package dpop verifies DPoP proofs (RFC 9449) and holds the state they
// need: server-issued nonces and the replay cache. It contains the pieces
// the HTTP middleware calls; it does no HTTP itself.
//
// Invariants:
//
//   - Only ES256 over P-256 is accepted; none, HS* and RS* never are
//     (SEC-021). The proof's key travels in its own header and must be
//     public.
//   - A proof is bound to one method, one URL, one moment and, at the
//     resource server, one access token through its ath claim (SEC-022).
//   - A proof identifier is accepted once per key within the window. The
//     shared cache decides; when it is unavailable the configured policy
//     either refuses (fail closed) or falls back to a bounded per-replica
//     store with a narrower time window (SEC-023, ADR-0012).
//   - Nonces are stateless HMACs over a rotating epoch, so every replica
//     accepts what any replica issued (SEC-024).
//   - Proofs, tokens, keys and identifiers never appear in errors or logs
//     (SEC-092).
//
// Verification takes its clock as an argument; nothing here reads the wall
// clock unless the caller left the clock unset.
package dpop
