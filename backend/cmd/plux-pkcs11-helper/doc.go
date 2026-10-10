// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

// Command plux-pkcs11-helper holds the session with a PKCS#11 token and
// signs for the server over a Unix socket (SEC-120, ADR-0060). It is the
// only code that imports the PKCS#11 binding, which is why it is a binary
// of its own: plux-server stays free of cgo and of the vendor's library.
//
//	plux-pkcs11-helper --module /usr/lib/softhsm/libsofthsm2.so \
//	    --token-label plux --socket /run/plux/pkcs11.sock --pin-file /etc/plux/pin
//
// The token PIN is read from the environment variable PLUX_PKCS11_PIN or
// from the file named by --pin-file, which must not be readable by group
// or others; it is never a flag value, so it never appears in a process
// listing. The socket is created with mode 0600. The wire protocol is
// described in package pkcs11wire.
package main
