// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// PXLSource names the PXL sources in generated headers.
const PXLSource = "schema/pxl"

// pxlGoPath is the generated Go table file.
const pxlGoPath = "backend/internal/pxl/tables_gen.go"

// PXL describes the bytecode, standard library and currency sources.
type PXL struct {
	Bytecode   pxlBytecode
	Stdlib     pxlStdlib
	Currencies pxlCurrencies
}

type pxlBytecode struct {
	Schema      string   `json:"$schema"`
	Version     int      `json:"version"`
	Description string   `json:"description"`
	Opcodes     []pxlOp  `json:"opcodes"`
	Comparisons []pxlTag `json:"comparisons"`
	Constants   []pxlTag `json:"constants"`
	Errors      []pxlTag `json:"errors"`
}

type pxlOp struct {
	Name        string   `json:"name"`
	Code        int      `json:"code"`
	Operands    []string `json:"operands"`
	Description string   `json:"description"`
}

type pxlTag struct {
	Name        string `json:"name"`
	Code        int    `json:"code"`
	Tag         int    `json:"tag"`
	Description string `json:"description"`
}

type pxlStdlib struct {
	Schema      string        `json:"$schema"`
	Description string        `json:"description"`
	Groups      []pxlGroup    `json:"groups"`
	Enums       []pxlEnum     `json:"enums"`
	Macros      []pxlMacro    `json:"macros"`
	Functions   []pxlFunction `json:"functions"`
}

type pxlGroup struct {
	Name        string `json:"name"`
	Feature     string `json:"feature"`
	Phase       string `json:"phase"`
	Description string `json:"description"`
}

type pxlEnum struct {
	Name        string   `json:"name"`
	Values      []string `json:"values"`
	Description string   `json:"description"`
}

type pxlMacro struct {
	Name        string `json:"name"`
	ID          int    `json:"id"`
	Description string `json:"description"`
}

type pxlFunction struct {
	Name        string        `json:"name"`
	Category    string        `json:"category"`
	Group       string        `json:"group"`
	Description string        `json:"description"`
	Overloads   []pxlOverload `json:"overloads"`
}

type pxlOverload struct {
	ID       int        `json:"id"`
	Params   []pxlParam `json:"params"`
	Result   string     `json:"result"`
	Variadic bool       `json:"variadic,omitempty"`
}

type pxlParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type pxlCurrencies struct {
	Schema      string        `json:"$schema"`
	Description string        `json:"description"`
	Currencies  []pxlCurrency `json:"currencies"`
}

type pxlCurrency struct {
	Code       string `json:"code"`
	MinorUnits int    `json:"minorUnits"`
}

var (
	opName       = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	lowerName    = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	functionName = regexp.MustCompile(`^([a-z][a-zA-Z0-9]*\.)?[a-z][a-zA-Z0-9]*$`)
	currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
	operandKinds = map[string]int{"u8": 1, "u16": 2, "u32": 4}
)

// LoadPXL reads and checks schema/pxl under root.
func LoadPXL(root string) (*PXL, error) {
	dir := filepath.Join(root, "schema", "pxl")
	var p PXL
	if err := decodeStrictFile(filepath.Join(dir, "bytecode.json"), &p.Bytecode); err != nil {
		return nil, err
	}
	if err := decodeStrictFile(filepath.Join(dir, "stdlib.json"), &p.Stdlib); err != nil {
		return nil, err
	}
	if err := decodeStrictFile(filepath.Join(dir, "currencies.json"), &p.Currencies); err != nil {
		return nil, err
	}
	var errs []string
	errs = append(errs, p.checkBytecode()...)
	errs = append(errs, p.checkStdlib()...)
	errs = append(errs, p.checkCurrencies()...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("codegen.LoadPXL: %d problems:\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return &p, nil
}

// checkBytecode checks names, sequential codes and operand kinds.
func (p *PXL) checkBytecode() []string {
	var errs []string
	b := p.Bytecode
	if b.Version < 1 {
		errs = append(errs, "bytecode: version must be at least 1")
	}
	for i, op := range b.Opcodes {
		if op.Code != i+1 || !opName.MatchString(op.Name) {
			errs = append(errs, fmt.Sprintf("bytecode: opcode %s must be named in upper snake case and numbered %d", op.Name, i+1))
		}
		for _, o := range op.Operands {
			if operandKinds[o] == 0 {
				errs = append(errs, fmt.Sprintf("bytecode: opcode %s: unknown operand %q", op.Name, o))
			}
		}
	}
	for what, list := range map[string][]pxlTag{"comparison": b.Comparisons, "constant": b.Constants, "error": b.Errors} {
		for i, t := range list {
			code := t.Code
			if what == "constant" {
				code = t.Tag
			}
			if code != i+1 || !lowerName.MatchString(t.Name) {
				errs = append(errs, fmt.Sprintf("bytecode: %s %s must be lowerCamelCase and numbered %d", what, t.Name, i+1))
			}
		}
	}
	return errs
}

// checkStdlib checks groups, IDs, names and signature types.
func (p *PXL) checkStdlib() []string {
	var errs []string
	s := p.Stdlib
	groups, enums := map[string]bool{}, map[string]bool{}
	for _, g := range s.Groups {
		groups[g.Name] = true
	}
	for _, e := range s.Enums {
		enums[e.Name] = true
	}
	for i, m := range s.Macros {
		if m.ID != i+1 || !lowerName.MatchString(m.Name) {
			errs = append(errs, fmt.Sprintf("stdlib: macro %s must be numbered %d", m.Name, i+1))
		}
	}
	next := 1
	seen := map[string]bool{}
	for _, f := range s.Functions {
		if seen[f.Name] || !functionName.MatchString(f.Name) {
			errs = append(errs, fmt.Sprintf("stdlib: function %q is malformed or repeated", f.Name))
		}
		seen[f.Name] = true
		if !groups[f.Group] {
			errs = append(errs, fmt.Sprintf("stdlib: %s: unknown group %q", f.Name, f.Group))
		}
		errs = append(errs, checkOverloads(f, enums, &next)...)
	}
	return errs
}

// checkOverloads checks the sequential IDs and signature types of one
// function's overloads.
func checkOverloads(f pxlFunction, enums map[string]bool, next *int) []string {
	var errs []string
	for _, o := range f.Overloads {
		if o.ID != *next {
			errs = append(errs, fmt.Sprintf("stdlib: %s: overload IDs must be sequential; want %d, got %d", f.Name, *next, o.ID))
		}
		*next = o.ID + 1
		for _, t := range append(paramTypes(o.Params), o.Result) {
			if err := checkSignatureType(t, enums); err != nil {
				errs = append(errs, fmt.Sprintf("stdlib: %s#%d: %v", f.Name, o.ID, err))
			}
		}
	}
	return errs
}

// paramTypes lists the parameter types.
func paramTypes(params []pxlParam) []string {
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = p.Type
	}
	return out
}

// checkSignatureType accepts a type expression over SCH-010 types, the
// built-in enums, type variables and the pseudo-type `enum`.
func checkSignatureType(expr string, enums map[string]bool) error {
	if expr == "enum" {
		return nil
	}
	t, err := registry.ParseType(expr)
	if err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	var bad error
	walkType(t, func(n registry.Type) {
		if n.Kind == registry.KindNamed && !enums[n.Name] {
			bad = fmt.Errorf("unknown type %s", n.Name)
		}
	})
	return bad
}

// walkType visits t and its element types.
func walkType(t registry.Type, fn func(registry.Type)) {
	fn(t)
	if t.Elem != nil {
		walkType(*t.Elem, fn)
	}
}

// checkCurrencies checks codes, order and minor units.
func (p *PXL) checkCurrencies() []string {
	var errs []string
	for i, c := range p.Currencies.Currencies {
		if !currencyCode.MatchString(c.Code) || c.MinorUnits < 0 || c.MinorUnits > 4 {
			errs = append(errs, fmt.Sprintf("currencies: %s is malformed", c.Code))
		}
		if i > 0 && p.Currencies.Currencies[i-1].Code >= c.Code {
			errs = append(errs, fmt.Sprintf("currencies: %s is out of order or repeated", c.Code))
		}
	}
	return errs
}

// decodeStrictFile decodes a JSON file into v, rejecting unknown fields and
// trailing data.
func decodeStrictFile(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: sources are fixed paths under the repository root.
	if err != nil {
		return fmt.Errorf("codegen: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("codegen: %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("codegen: %s: trailing data", path)
	}
	return nil
}

// PXLFiles renders the generated tables and the standard-library reference.
func PXLFiles(p *PXL) ([]File, error) {
	goSrc, err := goFile(pxlGoPath, pxlGo(p))
	if err != nil {
		return nil, err
	}
	return []File{goSrc, {Path: "docs/reference/pxl-stdlib.md", Content: pxlMarkdown(p)}}, nil
}

// pxlMarkdown renders docs/reference/pxl-stdlib.md.
func pxlMarkdown(p *PXL) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, PXLSource))
	b.WriteString("# PXL Standard Library\n\n")
	b.WriteString("Every built-in function of PXL (spec Appendix E.3), generated from `schema/pxl/stdlib.json`. The language is described in [pxl.md](pxl.md). ")
	b.WriteString("Each overload has a permanent ID the bytecode calls it by. Functions of a group other than `core` are type-checked everywhere, but a bundle that calls one requires the group's feature, so runtimes without it refuse the bundle (ADR-0009).\n\n")
	b.WriteString("## Groups\n\n| Group | Feature | Phase | Description |\n|---|---|---|---|\n")
	for _, g := range p.Stdlib.Groups {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", g.Name, g.Feature, g.Phase, g.Description)
	}
	b.WriteString("\n## Built-in enums\n\n| Enum | Values | Description |\n|---|---|---|\n")
	for _, e := range p.Stdlib.Enums {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", e.Name, "`"+strings.Join(e.Values, "`, `")+"`", e.Description)
	}
	b.WriteString("\n## Macros\n\n| Macro | ID | Description |\n|---|---|---|\n")
	for _, m := range p.Stdlib.Macros {
		fmt.Fprintf(&b, "| `%s` | %d | %s |\n", m.Name, m.ID, m.Description)
	}
	order, by := functionsByCategory(p.Stdlib.Functions)
	for _, cat := range order {
		fmt.Fprintf(&b, "\n## %s\n\n| Function | ID | Signature | Group | Description |\n|---|---|---|---|---|\n", strings.ToUpper(cat[:1])+cat[1:])
		for _, f := range by[cat] {
			for i, o := range f.Overloads {
				desc := ""
				if i == 0 {
					desc = mdCell(f.Description)
				}
				fmt.Fprintf(&b, "| `%s` | %d | `%s` | %s | %s |\n", f.Name, o.ID, mdCell(signatureText(f.Name, o)), f.Group, desc)
			}
		}
	}
	b.WriteString("\n## Currencies\n\n")
	var codes []string
	for _, c := range p.Currencies.Currencies {
		if c.MinorUnits != 2 {
			codes = append(codes, fmt.Sprintf("`%s` %d", c.Code, c.MinorUnits))
		}
	}
	fmt.Fprintf(&b, "Money values accept the %d ISO 4217 codes of `schema/pxl/currencies.json`. Their minor units, which `round(money)` and `div(money, …)` use, are 2 except: %s.\n", len(p.Currencies.Currencies), strings.Join(codes, ", "))
	return b.Bytes()
}

// pxlGo renders backend/internal/pxl/tables_gen.go.
func pxlGo(p *PXL) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangGo, PXLSource))
	fmt.Fprintf(&b, "package pxl\n\n// BytecodeVersion is the version of the program encoding.\nconst BytecodeVersion = %d\n\n", p.Bytecode.Version)
	b.WriteString("// Opcodes.\nconst (\n")
	for _, op := range p.Bytecode.Opcodes {
		fmt.Fprintf(&b, "\t// %s\n\tOp%s Opcode = %d\n", op.Description, GoName(strings.ToLower(op.Name)), op.Code)
	}
	b.WriteString(")\n\n// opcodeInfo describes each opcode, indexed by code.\nvar opcodeInfo = [...]opcodeDef{\n")
	for _, op := range p.Bytecode.Opcodes {
		widths := make([]string, len(op.Operands))
		for i, o := range op.Operands {
			widths[i] = fmt.Sprint(operandKinds[o])
		}
		fmt.Fprintf(&b, "\tOp%s: {name: %q, operands: []int{%s}},\n", GoName(strings.ToLower(op.Name)), op.Name, strings.Join(widths, ", "))
	}
	b.WriteString("}\n\n// Comparison kinds of SORT_BY.\nconst (\n")
	for _, c := range p.Bytecode.Comparisons {
		fmt.Fprintf(&b, "\tCmp%s CmpKind = %d\n", GoName(c.Name), c.Code)
	}
	b.WriteString(")\n\n// Constant tags of the program encoding.\nconst (\n")
	for _, c := range p.Bytecode.Constants {
		fmt.Fprintf(&b, "\t// %s\n\ttag%s constTag = %d\n", c.Description, GoName(c.Name), c.Tag)
	}
	b.WriteString(")\n\n// Kinds of run-time errors.\nconst (\n")
	for _, e := range p.Bytecode.Errors {
		fmt.Fprintf(&b, "\t// %s\n\tError%s ErrorKind = %d\n", e.Description, GoName(e.Name), e.Code)
	}
	b.WriteString(")\n\n// errorKindNames are the names of the error kinds, indexed by kind.\nvar errorKindNames = [...]string{\n")
	for _, e := range p.Bytecode.Errors {
		fmt.Fprintf(&b, "\tError%s: %q,\n", GoName(e.Name), e.Name)
	}
	b.WriteString("}\n\n// stdGroups are the feature groups of the standard library.\nvar stdGroups = [...]groupDef{\n")
	for _, g := range p.Stdlib.Groups {
		fmt.Fprintf(&b, "\t{name: %q, feature: %q, phase: %q},\n", g.Name, g.Feature, g.Phase)
	}
	b.WriteString("}\n\n// stdEnums are the built-in enums.\nvar stdEnums = [...]enumDef{\n")
	for _, e := range p.Stdlib.Enums {
		fmt.Fprintf(&b, "\t{name: %q, values: %s},\n", e.Name, goStrings(e.Values))
	}
	b.WriteString("}\n\n// stdMacros are the receiver macros, indexed by ID - 1.\nvar stdMacros = [...]string{")
	names := make([]string, len(p.Stdlib.Macros))
	for i, m := range p.Stdlib.Macros {
		names[i] = fmt.Sprintf("%q", m.Name)
	}
	b.WriteString(strings.Join(names, ", ") + "}\n\n// stdOverloads are the standard-library overloads, indexed by ID - 1.\nvar stdOverloads = [...]overloadDef{\n")
	for _, f := range p.Stdlib.Functions {
		for _, o := range f.Overloads {
			params := make([]string, len(o.Params))
			for i, pr := range o.Params {
				params[i] = fmt.Sprintf("{%q, %q}", pr.Name, pr.Type)
			}
			fmt.Fprintf(&b, "\t{id: %d, name: %q, group: %q, params: []paramDef{%s}, result: %q, variadic: %t},\n",
				o.ID, f.Name, f.Group, strings.Join(params, ", "), o.Result, o.Variadic)
		}
	}
	b.WriteString("}\n\n// currencies are the ISO 4217 codes and their minor units, sorted by code.\nvar currencies = [...]currencyDef{\n")
	for _, c := range p.Currencies.Currencies {
		fmt.Fprintf(&b, "\t{%q, %d},\n", c.Code, c.MinorUnits)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// signatureText renders an overload as `name(p type, …) result`.
func signatureText(name string, o pxlOverload) string {
	params := make([]string, len(o.Params))
	for j, pr := range o.Params {
		params[j] = pr.Name + " " + pr.Type
	}
	sig := name + "(" + strings.Join(params, ", ")
	if o.Variadic {
		sig += ", …"
	}
	return sig + ") " + o.Result
}

// functionsByCategory groups functions for the reference, keeping order.
func functionsByCategory(fs []pxlFunction) ([]string, map[string][]pxlFunction) {
	var order []string
	by := map[string][]pxlFunction{}
	for _, f := range fs {
		if !slices.Contains(order, f.Category) {
			order = append(order, f.Category)
		}
		by[f.Category] = append(by[f.Category], f)
	}
	return order, by
}
