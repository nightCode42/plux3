// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package registry loads and checks the widget and action registries: the
// widget descriptors, value types and enums under schema/widgets/ and the
// action descriptors under schema/actions/ (WGT-001, BND-011, ADR-0010).
//
// Structure is defined by the JSON Schemas in schema/json/registry/ and
// checked by the backend's tests; this package decodes strictly and checks
// what a JSON Schema cannot: identity and permanence of IDs, resolution of
// type expressions, defaults against their types, revisions, and coverage
// of the Flutter counterparts recorded in schema/widgets/flutter-api.json
// (WGT-003). Only the standard library is used (tools policy).
package registry
