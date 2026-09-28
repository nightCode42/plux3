// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package observability builds the logger, the metric registry and the
// tracer a server process uses (OBS-001, OBS-002, OBS-003).
//
// Everything here is constructed and handed to the components that need
// it: there is no package-level logger, registry or tracer, so a test can
// build its own and nothing writes to a global by accident.
//
// Logs are structured JSON with the request ID, the trace and span IDs
// and the organisation and app IDs where they are known, and they never
// carry a secret: attributes whose key is in the redaction list, and
// values that declare themselves sensitive, are replaced before the
// handler sees them (OBS-003, SEC-092).
//
// Metrics are exactly those catalogued in Appendix G. Label cardinality
// is bounded by construction: the helpers here take the labels the
// catalogue names and nothing else.
package observability
