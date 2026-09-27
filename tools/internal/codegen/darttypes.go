// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// dartKeywords are reserved words that cannot name enum values or fields.
var dartKeywords = []string{
	"assert", "break", "case", "catch", "class", "const", "continue", "default", "do", "else", "enum",
	"extends", "false", "final", "finally", "for", "if", "in", "is", "new", "null", "rethrow", "return",
	"super", "switch", "this", "throw", "true", "try", "var", "void", "while", "with",
}

// dartName returns a Dart identifier for a JSON name or enum value.
func dartName(v string) string {
	n := LowerCamel(sanitise(trimDollar(v)))
	if v == "$t" {
		n = "translation"
	}
	if slices.Contains(dartKeywords, n) {
		n += "Value"
	}
	return n
}

// dartType returns the Dart type of a field type.
func dartType(t TypeRef) string {
	switch t.Kind {
	case KindString:
		return "String"
	case KindInt:
		return "int"
	case KindNumber:
		return "double"
	case KindBool:
		return "bool"
	case KindRaw:
		return "Object?"
	case KindArray:
		return "List<" + dartType(*t.Elem) + ">"
	case KindMap:
		return "Map<String, " + dartType(*t.Elem) + ">"
	default:
		return t.Name
	}
}

// dartDecode returns the expression decoding the JSON value expr, which is
// known to be non-null, into type t.
func dartDecode(t TypeRef, expr string) string {
	switch t.Kind {
	case KindString:
		return expr + " as String"
	case KindInt:
		return "(" + expr + " as num).toInt()"
	case KindNumber:
		return "(" + expr + " as num).toDouble()"
	case KindBool:
		return expr + " as bool"
	case KindRaw:
		return expr
	case KindArray:
		return "[for (final e in " + expr + " as List<Object?>) " + dartDecode(*t.Elem, nonNull(*t.Elem, "e")) + "]"
	case KindMap:
		return "{for (final e in (" + expr + " as Map<String, Object?>).entries) e.key: " + dartDecode(*t.Elem, nonNull(*t.Elem, "e.value")) + "}"
	default:
		return t.Name + ".fromJson(" + expr + ")"
	}
}

// nonNull asserts that a JSON value is present, except for raw values,
// where null is a valid value.
func nonNull(t TypeRef, expr string) string {
	if t.Kind == KindRaw {
		return expr
	}
	return expr + "!"
}

// dartEncode returns the expression encoding value of type t as JSON.
func dartEncode(t TypeRef, value string) string {
	switch t.Kind {
	case KindArray:
		return "[for (final e in " + value + ") " + dartEncode(*t.Elem, "e") + "]"
	case KindMap:
		return "{for (final e in " + value + ".entries) e.key: " + dartEncode(*t.Elem, "e.value") + "}"
	case KindNamed:
		return value + ".toJson()"
	default:
		return value
	}
}

// modelDart renders packages/plux_flutter/lib/src/schema/document.g.dart.
func modelDart(m *Model) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangDart, DocumentTypesSource))
	b.WriteString("/// Plux document types (SCH-001, ADR-0025).\n///\n/// Decoding assumes a document that passed structural validation.\nlibrary;\n\n")
	b.WriteString("// ignore_for_file: public_member_api_docs, sort_constructors_first\n\n")
	b.WriteString("/// A JSON value that is present in a document, possibly `null`; used for\n/// optional raw values, where absence and `null` differ.\nfinal class JsonValue {\n  const JsonValue(this.value);\n\n  /// The decoded JSON value.\n  final Object? value;\n}\n")
	for _, t := range m.Types {
		b.WriteString("\n")
		writeDartType(&b, t)
	}
	return b.Bytes()
}

// writeDartType renders one named type.
func writeDartType(b *bytes.Buffer, t *NamedType) {
	if t.Doc != "" {
		b.WriteString(wrapComment("/// ", t.Doc, 80))
	}
	switch t.Kind {
	case NamedEnum:
		fmt.Fprintf(b, "enum %s {\n", t.Name)
		for i, v := range t.Values {
			sep := ","
			if i == len(t.Values)-1 {
				sep = ";"
			}
			fmt.Fprintf(b, "  %s(%s)%s\n", dartName(v), quoteDart(v), sep)
		}
		fmt.Fprintf(b, "\n  const %s(this.json);\n\n  /// Decodes a JSON value.\n  factory %s.fromJson(Object json) =>\n      values.firstWhere((v) => v.json == json, orElse: () => throw FormatException('unknown %s', json));\n\n  /// The JSON value.\n  final String json;\n\n  /// Encodes the JSON value.\n  String toJson() => json;\n}\n", t.Name, t.Name, t.Name)
	case NamedSingleOrList:
		fmt.Fprintf(b, "final class %s {\n  const %s.one(%s this.one) : many = null;\n  const %s.many(List<%s> this.many) : one = null;\n\n", t.Name, t.Name, t.Elem, t.Name, t.Elem)
		fmt.Fprintf(b, "  /// Decodes a single value or a list.\n  factory %s.fromJson(Object json) => json is List<Object?>\n      ? %s.many([for (final e in json) %s.fromJson(e!)])\n      : %s.one(%s.fromJson(json));\n\n", t.Name, t.Name, t.Elem, t.Name, t.Elem)
		fmt.Fprintf(b, "  final %s? one;\n  final List<%s>? many;\n\n  /// Encodes the single value or the list.\n  Object? toJson() => many == null ? one!.toJson() : [for (final e in many!) e.toJson()];\n}\n", t.Elem, t.Elem)
	default:
		writeDartStruct(b, t)
	}
}

// writeDartStruct renders an immutable class with fromJson and toJson.
func writeDartStruct(b *bytes.Buffer, t *NamedType) {
	fmt.Fprintf(b, "final class %s {\n  const %s({", t.Name, t.Name)
	params := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		params[i] = "this." + dartName(f.JSONName)
		if f.Required {
			params[i] = "required " + params[i]
		}
	}
	b.WriteString(strings.Join(params, ", "))
	b.WriteString("});\n\n")
	fmt.Fprintf(b, "  /// Decodes a JSON object.\n  factory %s.fromJson(Object json) {\n    final m = json as Map<String, Object?>;\n    return %s(\n", t.Name, t.Name)
	for _, f := range t.Fields {
		access := "m[" + quoteDart(f.JSONName) + "]"
		switch {
		case f.Required:
			fmt.Fprintf(b, "      %s: %s,\n", dartName(f.JSONName), dartDecode(f.Type, nonNull(f.Type, access)))
		case f.Type.Kind == KindRaw:
			fmt.Fprintf(b, "      %s: m.containsKey(%s) ? JsonValue(%s) : null,\n", dartName(f.JSONName), quoteDart(f.JSONName), access)
		default:
			fmt.Fprintf(b, "      %s: %s == null ? null : %s,\n", dartName(f.JSONName), access, dartDecode(f.Type, access+"!"))
		}
	}
	b.WriteString("    );\n  }\n\n")
	for _, f := range t.Fields {
		if f.Doc != "" {
			b.WriteString(wrapComment("  /// ", f.Doc, 80))
		}
		typ := dartType(f.Type)
		switch {
		case !f.Required && f.Type.Kind == KindRaw:
			typ = "JsonValue?"
		case !f.Required:
			typ += "?"
		}
		fmt.Fprintf(b, "  final %s %s;\n", typ, dartName(f.JSONName))
	}
	b.WriteString("\n  /// Encodes a JSON object.\n  Map<String, Object?> toJson() => {\n")
	for _, f := range t.Fields {
		name := dartName(f.JSONName)
		switch {
		case f.Required:
			fmt.Fprintf(b, "        %s: %s,\n", quoteDart(f.JSONName), dartEncode(f.Type, name))
		case f.Type.Kind == KindRaw:
			fmt.Fprintf(b, "        if (%s != null) %s: %s!.value,\n", name, quoteDart(f.JSONName), name)
		default:
			fmt.Fprintf(b, "        if (%s != null) %s: %s,\n", name, quoteDart(f.JSONName), dartEncode(f.Type, name+"!"))
		}
	}
	b.WriteString("      };\n}\n")
}
