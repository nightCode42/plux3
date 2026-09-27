// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package jcs implements the JSON Canonicalization Scheme of RFC 8785
// (SCH-003, ADR-0025).
//
// Documents are canonicalised before hashing, diffing and storage, so equal
// content has equal bytes and equal hashes. Parse is a strict I-JSON parser:
// it rejects duplicate keys, invalid UTF-8, unpaired surrogates, integer
// literals that canonicalisation would change — doubles lose integer
// precision beyond 2^53 (RFC 7493 §2.2) — and nesting deeper than the
// caller's limit. It returns the
// tree in the shape encoding/json produces with UseNumber, so the result can
// be validated and decoded directly.
//
// Canonical output sorts object members by the UTF-16 code units of their
// names, writes numbers with the ECMAScript Number-to-String algorithm,
// escapes strings minimally and contains no insignificant whitespace. Format
// writes the same ordering and number form with two-space indentation for
// the Git layout (SCH-006), so exported files diff line by line and
// canonicalise to the same bytes.
//
// All functions are pure and safe for concurrent use.
package jcs
