// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import "embed"

// schemaFiles holds copies of schema/json written by `make gen`, so the
// validator needs no file system access.
//
//go:embed schemas/*.schema.json
var schemaFiles embed.FS

// CurrentVersion is the schema version documents are migrated to.
const CurrentVersion = "1.0.0"

// schemaBase is the base URI of the schema files' $id.
const schemaBase = "https://plux.dev/schema/v1/"
