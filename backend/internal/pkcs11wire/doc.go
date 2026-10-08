// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package pkcs11wire is the framing of the protocol between the server
// and the PKCS#11 helper (SEC-120, ADR-0060); the messages are in
// pkcs11pb. Both ends import it, and it imports nothing of the server.
//
// A client connects to the helper's Unix socket, writes one framed
// Request, reads one framed Response and closes. A connection carries
// exactly one request, so there is no multiplexing to get wrong and a
// slow or stuck client holds one connection, not the token. A frame is a
// 4-byte big-endian length followed by that many bytes of protobuf, at
// most MaxMessageSize. The helper gives each connection a deadline, the
// per-request timeout, for reading the request and for the token's work,
// and a short grace to write the answer, a timeout included. A
// connection that closes before sending anything is a liveness probe and
// is not an error.
package pkcs11wire
