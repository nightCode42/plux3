// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package seccfg stores and serves the security configuration of an
// environment: a profile and the operator's overrides, in numbered
// versions (SEC-180, SEC-182, ADR-0053).
//
// The registry in security/settings defines every setting, its bounds and
// the preset of each profile. An operator may only tighten a preset; the
// effective configuration is the preset with the overrides applied. Each
// change adds a version, and the server keeps the newest HistoryKept of
// them so that it can compute the RFC 7396 merge patch from a version a
// device still holds. Version 0 is the built-in default and has no row.
//
// The device does not receive the whole configuration: it receives the
// device document, the profile and the overrides of the settings that
// travel to it, as canonical JSON. The signed manifest pins that
// document's version and SHA-256, so a device can verify what a patch
// produced.
package seccfg
