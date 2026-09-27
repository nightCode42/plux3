// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

// pxlDart renders packages/plux_flutter/lib/src/pxl/tables.g.dart: the
// tables the Dart VM shares with Go, and the Unicode data PXL's string
// functions need, taken from Go's unicode package so both runtimes use
// one Unicode version.
func pxlDart(p *PXL) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangDart, PXLSource))
	b.WriteString("/// Tables of the PXL bytecode and standard library (ADR-0009).\nlibrary;\n\n")
	writeDartBytecode(&b, &p.Bytecode)
	writeDartStdlib(&b, p)
	writeCaseTable(&b, "upperCase", "simple uppercase", unicode.ToUpper)
	writeCaseTable(&b, "lowerCase", "simple lowercase", unicode.ToLower)
	writeWhiteSpace(&b)
	return b.Bytes()
}

// writeDartBytecode writes the opcodes, operand widths, comparison kinds,
// constant tags and error kinds.
func writeDartBytecode(b *bytes.Buffer, bc *pxlBytecode) {
	fmt.Fprintf(b, "/// The version of the program encoding.\nconst int bytecodeVersion = %d;\n\n", bc.Version)
	b.WriteString("/// Opcodes.\nabstract final class Op {\n")
	for _, op := range bc.Opcodes {
		fmt.Fprintf(b, "  /// %s\n  static const int %s = %d;\n\n", op.Description, dartName(strings.ToLower(op.Name)), op.Code)
	}
	b.WriteString("}\n\n/// Operand widths in bytes, indexed by opcode; null for unused codes.\nconst List<List<int>?> operandWidths = [\n  null,\n")
	for _, op := range bc.Opcodes {
		widths := make([]string, len(op.Operands))
		for i, o := range op.Operands {
			widths[i] = fmt.Sprint(operandKinds[o])
		}
		fmt.Fprintf(b, "  [%s], // %s\n", strings.Join(widths, ", "), op.Name)
	}
	b.WriteString("];\n\n/// Comparison kinds of SORT_BY.\nabstract final class CmpKind {\n")
	for _, c := range bc.Comparisons {
		fmt.Fprintf(b, "  /// Compares %s values.\n  static const int of%s = %d;\n\n", c.Name, strings.ToUpper(c.Name[:1])+c.Name[1:], c.Code)
	}
	b.WriteString("}\n\n/// Constant tags of the program encoding.\nabstract final class ConstTag {\n")
	for _, c := range bc.Constants {
		fmt.Fprintf(b, "  /// %s\n  static const int of%s = %d;\n\n", c.Description, strings.ToUpper(c.Name[:1])+c.Name[1:], c.Tag)
	}
	b.WriteString("}\n\n/// Kinds of run-time errors.\nenum PxlErrorKind {\n")
	for i, e := range bc.Errors {
		sep := ","
		if i == len(bc.Errors)-1 {
			sep = ";"
		}
		fmt.Fprintf(b, "  /// %s\n  %s(%d)%s\n", e.Description, e.Name, e.Code, sep)
	}
	b.WriteString("\n  const PxlErrorKind(this.code);\n\n  /// The permanent code.\n  final int code;\n}\n\n")
}

// writeDartStdlib writes the overloads, group features and currencies.
func writeDartStdlib(b *bytes.Buffer, p *PXL) {
	b.WriteString("/// A standard-library overload: name, group and parameter types.\nfinal class OverloadDef {\n  /// Creates a definition.\n  const OverloadDef(this.name, this.group, this.params, {this.variadic = false});\n\n  /// The function name.\n  final String name;\n\n  /// The feature group.\n  final String group;\n\n  /// The parameter types as type expressions.\n  final List<String> params;\n\n  /// Whether the last parameter repeats.\n  final bool variadic;\n}\n\n")
	b.WriteString("/// The standard-library overloads, indexed by ID - 1.\nconst List<OverloadDef> overloads = [\n")
	for _, f := range p.Stdlib.Functions {
		for _, o := range f.Overloads {
			params := make([]string, len(o.Params))
			for i, pr := range o.Params {
				params[i] = quoteDart(pr.Type)
			}
			variadic := ""
			if o.Variadic {
				variadic = ", variadic: true"
			}
			fmt.Fprintf(b, "  OverloadDef(%s, %s, [%s]%s), // %d\n", quoteDart(f.Name), quoteDart(f.Group), strings.Join(params, ", "), variadic, o.ID)
		}
	}
	b.WriteString("];\n\n/// The features of the standard-library groups.\nconst Map<String, String> groupFeatures = {\n")
	for _, g := range p.Stdlib.Groups {
		fmt.Fprintf(b, "  %s: %s,\n", quoteDart(g.Name), quoteDart(g.Feature))
	}
	b.WriteString("};\n\n/// ISO 4217 minor units by currency code.\nconst Map<String, int> minorUnits = {\n")
	for _, c := range p.Currencies.Currencies {
		fmt.Fprintf(b, "  %s: %d,\n", quoteDart(c.Code), c.MinorUnits)
	}
	b.WriteString("};\n\n")
}

// writeWhiteSpace writes the ranges of the White_Space property.
func writeWhiteSpace(b *bytes.Buffer) {
	var ranges []string
	for _, r := range unicode.White_Space.R16 {
		if r.Stride == 1 {
			ranges = append(ranges, fmt.Sprintf("0x%X, 0x%X", r.Lo, r.Hi))
			continue
		}
		for c := uint32(r.Lo); c <= uint32(r.Hi); c += uint32(r.Stride) {
			ranges = append(ranges, fmt.Sprintf("0x%X, 0x%X", c, c))
		}
	}
	for _, r := range unicode.White_Space.R32 {
		ranges = append(ranges, fmt.Sprintf("0x%X, 0x%X", r.Lo, r.Hi))
	}
	b.WriteString("/// Ranges of the Unicode White_Space property as inclusive pairs.\nconst List<int> whiteSpace = [" + strings.Join(ranges, ", ") + "];\n")
}

// writeCaseTable writes the code points a mapping changes, as pairs.
func writeCaseTable(b *bytes.Buffer, name, what string, mapping func(rune) rune) {
	fmt.Fprintf(b, "/// The %s mapping as pairs of code points, sorted, for every code\n/// point it changes (Unicode %s).\nconst List<int> %s = [\n", what, unicode.Version, name)
	var line []string
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if m := mapping(r); m != r {
			line = append(line, fmt.Sprintf("0x%X, 0x%X,", r, m))
		}
		if len(line) == 6 || (r == unicode.MaxRune && len(line) > 0) {
			b.WriteString("  " + strings.Join(line, " ") + "\n")
			line = line[:0]
		}
	}
	b.WriteString("];\n\n")
}
