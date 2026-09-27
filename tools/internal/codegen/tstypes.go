// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"strings"
)

// tsType returns the TypeScript type of a field type.
func tsType(t TypeRef) string {
	switch t.Kind {
	case KindString:
		return "string"
	case KindInt, KindNumber:
		return "number"
	case KindBool:
		return "boolean"
	case KindRaw:
		return "JsonValue"
	case KindArray:
		return "readonly " + tsElem(*t.Elem) + "[]"
	case KindMap:
		return "{ readonly [key: string]: " + tsType(*t.Elem) + " }"
	default:
		return t.Name
	}
}

// tsElem wraps element types that need parentheses inside T[].
func tsElem(t TypeRef) string {
	s := tsType(t)
	if strings.ContainsAny(s, " |") {
		return "(" + s + ")"
	}
	return s
}

// tsProperty quotes property names that are not identifiers.
func tsProperty(name string) string {
	if strings.HasPrefix(name, "$") && !strings.ContainsAny(name[1:], "-.") {
		return name
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Sprintf("%q", name)
		}
	}
	return name
}

// modelTS renders studio/packages/schema/src/document.gen.ts.
func modelTS(m *Model) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangTS, DocumentTypesSource))
	b.WriteString("/** Any JSON value; interpreted by the compiler against a declared type. */\n")
	b.WriteString("export type JsonValue = null | boolean | number | string | readonly JsonValue[] | { readonly [key: string]: JsonValue };\n\n")
	b.WriteString("/** The `kind` of every document type (SCH-006). */\nexport type DocumentKind = ")
	kinds := make([]string, len(m.Documents))
	for i, d := range m.Documents {
		kinds[i] = fmt.Sprintf("%q", d.Kind)
	}
	b.WriteString(strings.Join(kinds, " | ") + ";\n")
	for _, t := range m.Types {
		b.WriteString("\n")
		writeTSType(&b, t)
	}
	return b.Bytes()
}

// writeTSType renders one named type.
func writeTSType(b *bytes.Buffer, t *NamedType) {
	if t.Doc != "" {
		b.WriteString("/** " + t.Doc + " */\n")
	}
	switch t.Kind {
	case NamedEnum:
		vals := make([]string, len(t.Values))
		for i, v := range t.Values {
			vals[i] = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(b, "export type %s = %s;\n", t.Name, strings.Join(vals, " | "))
	case NamedSingleOrList:
		fmt.Fprintf(b, "export type %s = %s | readonly %s[];\n", t.Name, t.Elem, t.Elem)
	default:
		fmt.Fprintf(b, "export interface %s {\n", t.Name)
		for _, f := range t.Fields {
			if f.Doc != "" {
				b.WriteString("  /** " + f.Doc + " */\n")
			}
			typ := tsType(f.Type)
			if f.Const != "" {
				typ = fmt.Sprintf("%q", f.Const)
			}
			opt := "?"
			if f.Required {
				opt = ""
			}
			fmt.Fprintf(b, "  readonly %s%s: %s;\n", tsProperty(f.JSONName), opt, typ)
		}
		b.WriteString("  /** Extension properties are preserved and ignored by the compiler (SCH-004). */\n")
		b.WriteString("  readonly [extension: `x-${string}`]: unknown;\n}\n")
	}
}

// modelMarkdown renders docs/reference/document-schema.md.
func modelMarkdown(m *Model) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, DocumentTypesSource))
	b.WriteString("# Document Schema Reference\n\n")
	b.WriteString("Every Plux document is JSON validated by the JSON Schema 2020-12 files in `schema/json/` (`SCH-001`, [ADR-0025](../adr/0025-document-schema-toolchain.md)). ")
	b.WriteString("Unknown properties are rejected, except extension properties prefixed with `x-`, which are preserved and ignored (`SCH-004`). ")
	b.WriteString("The layout of a project and the validation rules beyond structure are described in [document-model.md](document-model.md).\n\n")
	b.WriteString("## Documents\n\n| Kind | Type | Schema | Description |\n|---|---|---|---|\n")
	for _, d := range m.Documents {
		fmt.Fprintf(&b, "| `%s` | [%s](#%s) | `schema/json/%s` | %s |\n", d.Kind, d.TypeName, strings.ToLower(d.TypeName), d.File, d.Doc)
	}
	b.WriteString("\n## Types\n")
	for _, t := range m.Types {
		fmt.Fprintf(&b, "\n### %s\n\n", t.Name)
		if t.Doc != "" {
			b.WriteString(t.Doc + "\n\n")
		}
		switch t.Kind {
		case NamedEnum:
			vals := make([]string, len(t.Values))
			for i, v := range t.Values {
				vals[i] = "`" + v + "`"
			}
			b.WriteString("One of " + strings.Join(vals, ", ") + ".\n")
		case NamedSingleOrList:
			fmt.Fprintf(&b, "A [%s](#%s) or a non-empty list of them.\n", t.Elem, strings.ToLower(t.Elem))
		default:
			b.WriteString("| Property | Type | Required | Description |\n|---|---|---|---|\n")
			for _, f := range t.Fields {
				req := ""
				if f.Required {
					req = "yes"
				}
				typ := mdType(f.Type)
				if f.Const != "" {
					typ = "`\"" + f.Const + "\"`"
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f.JSONName, typ, req, strings.ReplaceAll(f.Doc, "|", `\|`))
			}
		}
	}
	return b.Bytes()
}

// mdType renders a field type for the reference, linking named types.
func mdType(t TypeRef) string {
	switch t.Kind {
	case KindString:
		return "string"
	case KindInt:
		return "integer"
	case KindNumber:
		return "number"
	case KindBool:
		return "boolean"
	case KindRaw:
		return "JSON value"
	case KindArray:
		return "list of " + mdType(*t.Elem)
	case KindMap:
		return "map of " + mdType(*t.Elem)
	default:
		return fmt.Sprintf("[%s](#%s)", t.Name, strings.ToLower(t.Name))
	}
}
