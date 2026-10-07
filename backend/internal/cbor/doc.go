// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package cbor decodes the small subset of CBOR (RFC 8949) that device
// and authenticator evidence uses: WebAuthn attestation objects and COSE
// keys (SEC-100) and Apple App Attest objects (SEC-004).
//
// The decoder accepts definite lengths only, refuses tags, floats and
// indefinite items, bounds nesting depth and the number of items, and
// rejects repeated map keys. It never panics on any input and never
// reads beyond its argument. It has no state; every call is independent
// and safe for concurrent use.
package cbor
