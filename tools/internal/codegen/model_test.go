// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalCommon is a common schema with the definitions the tests use.
const minimalCommon = `{
  "$id": "https://plux.dev/schema/v1/common.schema.json",
  "$defs": {
    "id": {"type": "string", "pattern": "^x$"},
    "kind": {"description": "A kind.", "enum": ["a", "b/c"]},
    "item": {"type": "object", "description": "An item.", "properties": {"id": {"$ref": "#/$defs/id"}, "n": {"type": "integer"}}, "required": ["id"], "additionalProperties": false},
    "items": {"oneOf": [{"$ref": "#/$defs/item"}, {"type": "array", "items": {"$ref": "#/$defs/item"}}]},
    "raw": {"x-plux-raw": true}
  }
}`

// minimalDoc is a document schema using every supported construct.
const minimalDoc = `{
  "$id": "https://plux.dev/schema/v1/thing.schema.json",
  "title": "Thing", "description": "A thing.",
  "type": "object",
  "required": ["kind", "id"],
  "properties": {
    "$schema": {"type": "string"},
    "kind": {"const": "thing"},
    "id": {"$ref": "common.schema.json#/$defs/id"},
    "mode": {"$ref": "common.schema.json#/$defs/kind"},
    "count": {"type": "number"},
    "flag": {"type": "boolean"},
    "tags": {"type": "array", "items": {"type": "string"}},
    "byName": {"type": "object", "additionalProperties": {"$ref": "common.schema.json#/$defs/item"}},
    "slot": {"$ref": "common.schema.json#/$defs/items"},
    "extra": {"$ref": "common.schema.json#/$defs/raw"},
    "inline": {"type": "object", "x-plux-type": "Inline", "properties": {"$t": {"type": "string"}}, "additionalProperties": false},
    "value": {"x-plux-raw": true, "anyOf": [{"type": "string"}, {"type": "number"}]}
  },
  "patternProperties": {"^x-": true},
  "additionalProperties": false,
  "oneOf": [{"required": ["mode"]}, {"required": ["count"]}]
}`

// schemaDir writes schema files into a temporary directory.
func schemaDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLoadModelBuildsTypesFromTheProfile_SCH_001 checks every construct of
// the schema profile and the three languages' output.
// Verifies: SCH-001.
func TestLoadModelBuildsTypesFromTheProfile_SCH_001(t *testing.T) {
	t.Parallel()
	dir := schemaDir(t, map[string]string{"common.schema.json": minimalCommon, "thing.schema.json": minimalDoc})
	m, err := LoadModel(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Documents) != 1 || m.Documents[0].Kind != "thing" || m.Documents[0].TypeName != "ThingDocument" {
		t.Fatalf("documents = %+v", m.Documents)
	}
	thing := m.Type("ThingDocument")
	if thing == nil || len(thing.Fields) != 11 {
		t.Fatalf("ThingDocument = %+v", thing)
	}
	if m.Type("Kind").Kind != NamedEnum || m.Type("Items").Kind != NamedSingleOrList || m.Type("Inline") == nil {
		t.Error("named types missing")
	}
	schemas, err := Schemas(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ModelFiles(m, schemas)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"backend/internal/schema/model_gen.go": {
			"KindThing DocumentKind = \"thing\"", "Mode  Kind", "Count *float64", "ByName map[string]Item",
			"Slot   Items", "Extra  json.RawMessage", "KindBC  Kind = \"b/c\"", "Translation string `json:\"$t,omitempty\"`",
		},
		"packages/plux_flutter/lib/src/schema/document.g.dart": {
			"final class ThingDocument", "enum Kind", "bC('b/c')", "final Map<String, Item>? byName;", "Items.fromJson(", "final JsonValue? extra;",
		},
		"studio/packages/schema/src/document.gen.ts": {
			"export interface ThingDocument", `readonly kind: "thing";`, `export type Kind = "a" | "b/c";`, "export type Items = Item | readonly Item[];",
			"readonly $t?: string;", "readonly [extension: `x-${string}`]: unknown;",
		},
		"docs/reference/document-schema.md":                 {"| `thing` | [ThingDocument](#thingdocument)", "| `byName` | map of [Item](#item) |"},
		"backend/internal/schema/schemas/thing.schema.json": {`"x-plux-type": "Inline"`},
	}
	for _, f := range files {
		for _, fragment := range want[f.Path] {
			if !strings.Contains(strings.Join(strings.Fields(string(f.Content)), " "), strings.Join(strings.Fields(fragment), " ")) {
				t.Errorf("%s lacks %q", f.Path, fragment)
			}
		}
	}
}

// TestLoadModelEnforcesTheProfile_SCH_001 checks that schemas outside the
// profile are rejected with their location.
// Verifies: SCH-001.
func TestLoadModelEnforcesTheProfile_SCH_001(t *testing.T) {
	t.Parallel()
	doc := func(props string) string {
		return `{"type":"object","properties":{"kind":{"const":"thing"},` + props + `},"additionalProperties":false}`
	}
	tests := map[string]struct{ common, doc, msg string }{
		"unknown keyword":        {minimalCommon, `{"type":"object","if":{},"properties":{"kind":{"const":"thing"}},"additionalProperties":false}`, `keyword "if"`},
		"inline enum":            {minimalCommon, doc(`"m":{"enum":["a"]}`), "enums must be named"},
		"unnamed inline object":  {minimalCommon, doc(`"o":{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`), "x-plux-type"},
		"open object":            {minimalCommon, `{"type":"object","properties":{"kind":{"const":"thing"}}}`, "additionalProperties"},
		"anyOf without raw":      {minimalCommon, doc(`"v":{"anyOf":[{"type":"string"}]}`), "anyOf"},
		"two types":              {minimalCommon, doc(`"v":{"type":["string","number"]}`), "exactly one type"},
		"array without items":    {minimalCommon, doc(`"v":{"type":"array"}`), "without items"},
		"unsupported type":       {minimalCommon, doc(`"v":{"type":"null"}`), "unsupported type"},
		"no kind":                {minimalCommon, `{"type":"object","properties":{},"additionalProperties":false}`, "constant `kind`"},
		"ref outside defs":       {minimalCommon, doc(`"v":{"$ref":"#/properties/kind"}`), "must point at a definition"},
		"ref to unknown file":    {minimalCommon, doc(`"v":{"$ref":"other.schema.json#/$defs/x"}`), "unknown file"},
		"ref to unknown def":     {minimalCommon, doc(`"v":{"$ref":"common.schema.json#/$defs/nope"}`), "no definition"},
		"general union":          {`{"$defs":{"u":{"oneOf":[{"type":"string"},{"type":"number"}]}}}`, doc(`"v":{"$ref":"common.schema.json#/$defs/u"}`), "only union"},
		"oneOf with types":       {minimalCommon, `{"type":"object","properties":{"kind":{"const":"thing"}},"additionalProperties":false,"oneOf":[{"type":"object"}]}`, "may only constrain"},
		"clashing type names":    {`{"$defs":{"a":{"type":"object","x-plux-type":"Same","properties":{"x":{"type":"string"}},"additionalProperties":false},"b":{"type":"object","x-plux-type":"Same","properties":{"y":{"type":"string"}},"additionalProperties":false}}}`, doc(`"a":{"$ref":"common.schema.json#/$defs/a"},"b":{"$ref":"common.schema.json#/$defs/b"}`), "already defined"},
		"malformed JSON":         {minimalCommon, `{`, "thing.schema.json"},
		"single-or-list of text": {`{"$defs":{"s":{"oneOf":[{"$ref":"#/$defs/t"},{"type":"array","items":{"$ref":"#/$defs/t"}}]},"t":{"type":"string"}}}`, doc(`"v":{"$ref":"common.schema.json#/$defs/s"}`), "must be named types"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := schemaDir(t, map[string]string{"common.schema.json": tt.common, "thing.schema.json": tt.doc})
			_, err := LoadModel(dir)
			if err == nil || !strings.Contains(err.Error(), tt.msg) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.msg)
			}
		})
	}
	if _, err := LoadModel(schemaDir(t, map[string]string{"thing.schema.json": minimalDoc})); err == nil {
		t.Error("missing common schema accepted")
	}
	if _, err := LoadModel(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory accepted")
	}
}

// TestRealSchemasBuildAModel checks the repository's schemas against the
// profile; `make gen-check` verifies the committed output.
func TestRealSchemasBuildAModel(t *testing.T) {
	t.Parallel()
	m, err := LoadModel(filepath.Join("..", "..", "..", "schema", "json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Documents) != 11 || m.Type("Node") == nil || m.Type("SlotFill").Kind != NamedSingleOrList {
		t.Errorf("unexpected model: %d documents", len(m.Documents))
	}
}
