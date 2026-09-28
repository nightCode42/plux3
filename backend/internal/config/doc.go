// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads and validates the server's configuration
// (SRV-008, spec Appendix H).
//
// Configuration comes from one YAML file plus environment variables. The
// file is decoded strictly: an unknown key is an error, so a typo can
// never be ignored, and a section belonging to a later phase is refused
// with the phase it arrives in rather than silently accepted. Every
// section is validated before the server starts, and the same code runs
// offline in `plux-server config validate`.
//
// Values are never mutated after loading: Load returns a Config that the
// rest of the server treats as read-only, so there is no global state and
// no reload path to reason about.
package config
