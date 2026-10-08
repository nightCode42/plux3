// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package pkcs11helper is the portable half of the PKCS#11 signing
// helper (SEC-120, ADR-0060): the key reference grammar and the server
// that answers pkcs11wire requests over a Unix socket. It holds no cgo
// and no key. The token itself is reached through the Token interface,
// which the plux-pkcs11-helper binary implements with the vendor module
// and which tests implement with a fake. Only this package, the binary
// and the signing package handle key material (L-3).
package pkcs11helper
