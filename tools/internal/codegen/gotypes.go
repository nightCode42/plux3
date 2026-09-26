// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"strings"
)

// DocumentTypesSource names the source of the generated document types.
const DocumentTypesSource = "schema/json"

// ModelFiles renders the document model for Go, Dart, TypeScript and the
// reference documentation, plus the schema copies the Go validator embeds.
func ModelFiles(m *Model, schemas []File) ([]File, error) {
	goSrc, err := goFile("backend/internal/schema/model_gen.go", modelGo(m))
	if err != nil {
		return nil, err
	}
	files := []File{
		goSrc,
		{Path: "packages/plux_flutter/lib/src/schema/document.g.dart", Content: modelDart(m)},
		{Path: "studio/packages/schema/src/document.gen.ts", Content: modelTS(m)},
		{Path: "docs/reference/document-schema.md", Content: modelMarkdown(m)},
	}
	return append(files, schemas...), nil
}

// goFieldName returns the Go name of a JSON property.
func goFieldName(jsonName string) string {
	if jsonName == "$t" {
		return "Translation"
	}
	return GoName(trimDollar(jsonName))
}

// goType returns the Go type expression of a field type.
func goType(t TypeRef) string {
	switch t.Kind {
	case KindString:
		return "string"
	case KindInt:
		return "int64"
	case KindNumber:
		return "float64"
	case KindBool:
		return "bool"
	case KindRaw:
		return "json.RawMessage"
	case KindArray:
		return "[]" + goType(*t.Elem)
	case KindMap:
		return "map[string]" + goType(*t.Elem)
	default:
		return t.Name
	}
}

// goFieldType returns the declared type of a struct field: optional
// numbers, booleans and structs are pointers, so absence is visible.
func goFieldType(m *Model, f Field) string {
	base := goType(f.Type)
	if f.Required {
		return base
	}
	switch f.Type.Kind {
	case KindInt, KindNumber, KindBool:
		return "*" + base
	case KindNamed:
		if m.Type(f.Type.Name).Kind == NamedStruct {
			return "*" + base
		}
	}
	return base
}

// modelGo renders backend/internal/schema/model_gen.go.
func modelGo(m *Model) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangGo, DocumentTypesSource))
	b.WriteString("package schema\n\nimport (\n\t\"bytes\"\n\t\"encoding/json\"\n)\n\n")
	b.WriteString("// DocumentKind is the value of a document's `kind` property.\ntype DocumentKind string\n\n")
	b.WriteString("// Document kinds and the schema file that validates each.\nconst (\n")
	for _, d := range m.Documents {
		fmt.Fprintf(&b, "\tKind%s DocumentKind = %q\n", GoName(d.Kind), d.Kind)
	}
	b.WriteString(")\n\n// documentSchemas maps every kind to its schema file in schemas/.\nvar documentSchemas = [...]struct {\n\tKind DocumentKind\n\tFile string\n}{\n")
	for _, d := range m.Documents {
		fmt.Fprintf(&b, "\t{Kind%s, %q},\n", GoName(d.Kind), d.File)
	}
	b.WriteString("}\n")
	for _, t := range m.Types {
		b.WriteString("\n")
		writeGoType(&b, m, t)
	}
	return b.Bytes()
}

// writeGoType renders one named type.
func writeGoType(b *bytes.Buffer, m *Model, t *NamedType) {
	doc := t.Doc
	if doc == "" {
		doc = "is generated from " + t.Source + "."
	}
	b.WriteString(wrapComment("// ", t.Name+" — "+doc, 78))
	switch t.Kind {
	case NamedEnum:
		fmt.Fprintf(b, "type %s string\n\n// Values of %s.\nconst (\n", t.Name, t.Name)
		for _, v := range t.Values {
			fmt.Fprintf(b, "\t%s%s %s = %q\n", t.Name, GoName(sanitise(v)), t.Name, v)
		}
		fmt.Fprintf(b, ")\n\n// Valid reports whether v is one of the values of %s.\nfunc (v %s) Valid() bool {\n\tswitch v {\n\tcase ", t.Name, t.Name)
		names := make([]string, len(t.Values))
		for i, v := range t.Values {
			names[i] = t.Name + GoName(sanitise(v))
		}
		b.WriteString(strings.Join(names, ", "))
		b.WriteString(":\n\t\treturn true\n\t}\n\treturn false\n}\n")
	case NamedSingleOrList:
		fmt.Fprintf(b, "// Exactly one of One and Many is set.\ntype %s struct {\n\tOne *%s\n\tMany []%s\n}\n\n", t.Name, t.Elem, t.Elem)
		fmt.Fprintf(b, "// MarshalJSON writes a single value or a list.\nfunc (s %s) MarshalJSON() ([]byte, error) {\n\tif s.Many != nil {\n\t\treturn json.Marshal(s.Many)\n\t}\n\treturn json.Marshal(s.One)\n}\n\n", t.Name)
		fmt.Fprintf(b, "// UnmarshalJSON reads a single value or a list.\nfunc (s *%s) UnmarshalJSON(data []byte) error {\n\tif trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {\n\t\ts.One = nil\n\t\treturn json.Unmarshal(data, &s.Many)\n\t}\n\ts.Many = nil\n\ts.One = new(%s)\n\treturn json.Unmarshal(data, s.One)\n}\n", t.Name, t.Elem)
	default:
		fmt.Fprintf(b, "type %s struct {\n", t.Name)
		for _, f := range t.Fields {
			if f.Doc != "" {
				b.WriteString(wrapComment("\t// ", goFieldName(f.JSONName)+": "+f.Doc, 78))
			}
			tag := f.JSONName
			if !f.Required {
				tag += ",omitempty"
			}
			fmt.Fprintf(b, "\t%s %s `json:%q`\n", goFieldName(f.JSONName), goFieldType(m, f), tag)
		}
		b.WriteString("}\n")
	}
}

// sanitise turns an enum value such as "image/svg+xml" into a key.
func sanitise(v string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, v)
}
