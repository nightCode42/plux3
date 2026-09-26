// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package bundle writes and reads Plux bundles (spec §9.5, Appendix B,
// ADR-0002): a fixed header, a section directory with a SHA-256 per
// section, and 8-byte-aligned sections, each an independent FlatBuffers
// buffer whose accessors flatc generates into package fbs.
//
// Read checks everything before a section is used: the header and its
// hash, the directory, every section hash, the FlatBuffers structure of
// every known section against layout tables generated from the section
// schemas, the absence of executable payloads, and the required features.
// Unknown section kinds are skipped (BND-018). Transport compression with
// zstd is separate from the container (BND-007).
//
// Reference: docs/reference/bundle-format.md.
package bundle
