// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package storage is the only place that talks to PostgreSQL
// (SRV-020, ADR-0007). Domain packages depend on the interfaces they
// need; nothing outside this package writes SQL (L-2).
//
// Three rules hold everywhere here:
//
//   - Migrations are numbered SQL files applied on start under an
//     advisory lock, so several replicas converge without racing
//     (SRV-021).
//   - Every table holding tenant data carries organization_id and has
//     row-level security enabled. The pool sets the organisation per
//     transaction, and a test walks the catalogue to prove no tenant
//     table was added without it (SRV-022).
//   - A job is enqueued in the same transaction as the change that
//     causes it, so state and work never disagree (SRV-024).
package storage
