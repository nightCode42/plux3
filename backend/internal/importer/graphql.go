// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Source is a named input file.
type Source struct {
	File string
	Data []byte
}

// ImportGraphQL reads a GraphQL schema (SDL) and the operations of the given
// documents, validates every operation against the schema, and returns one
// GraphQL data source with a typed operation each (DAT-002). Operations are
// never derived from the schema: a GraphQL operation is a choice of fields,
// which only its author can make.
func ImportGraphQL(schemaSrc Source, docs []Source) Result {
	sch, err := gqlparser.LoadSchema(&ast.Source{Name: schemaSrc.File, Input: string(schemaSrc.Data)})
	if err != nil {
		return Result{Diagnostics: plxerr.Diagnostics{gqlDiagnostic(plxerr.ImportDocumentInvalid, schemaSrc.File, err)}}
	}
	g := &gqlConv{schema: sch, types: newTypeSet()}
	var diags plxerr.Diagnostics
	ops := map[string]any{}
	for _, src := range docs {
		doc, err := parser.ParseQuery(&ast.Source{Name: src.File, Input: string(src.Data)})
		if err != nil {
			diags = append(diags, gqlDiagnostic(plxerr.ImportDocumentInvalid, src.File, err))
			continue
		}
		failed := map[*ast.OperationDefinition]bool{}
		for _, e := range validator.ValidateWithRules(sch, doc, nil) {
			diags = append(diags, gqlDiagnostic(plxerr.ImportOperationInvalid, src.File, e))
			for _, op := range affected(doc, e) {
				failed[op] = true
			}
		}
		for _, op := range doc.Operations {
			if failed[op] {
				continue
			}
			name, cfg, err := g.operation(src.File, doc, op)
			if err == nil && ops[name] != nil {
				err = gqlProblem(plxerr.ImportOperationInvalid, op, "the operation name %q is already used by another operation", name)
			}
			if err != nil {
				diags = append(diags, gqlOpDiagnostic(src.File, op, err))
				continue
			}
			ops[name] = cfg
		}
	}
	srcName := lowerCamel(strings.TrimSuffix(path.Base(schemaSrc.File), path.Ext(schemaSrc.File)))
	variable := srcName + "BaseUrl"
	read := firstRead(ops, func(cfg map[string]any) bool {
		q, _ := cfg["query"].(string)
		return strings.HasPrefix(q, "query") && g.types.optionalInput(cfg["input"])
	})
	if read == "" {
		diags = append(diags, plxerr.NewDiagnostic(plxerr.ImportConstructUnsupported, plxerr.Location{File: schemaSrc.File},
			"no read operation to bind: a source needs a query without required variables; no source is written"))
		return finish(map[string]any{"dataSources": []any{}}, diags, schemaSrc.File)
	}
	rc := ops[read].(map[string]any)
	delete(ops, read)
	config := map[string]any{"baseUrl": variable, "path": "/graphql", "query": rc["query"], "select": "data"}
	if len(ops) > 0 {
		config["operations"] = ops
	}
	frag := map[string]any{
		"dataSources": []any{map[string]any{
			"id":     identifier("graphql", srcName),
			"name":   srcName,
			"kind":   "graphql",
			"type":   rc["output"],
			"mock":   g.types.zeroValue(rc["output"].(string), 0),
			"config": config,
		}},
		"types":     g.types.list(),
		"variables": []any{field(variable, "string", "Base URL of the "+srcName+" GraphQL endpoint.", false)},
	}
	return finish(frag, diags, schemaSrc.File)
}

// gqlDiagnostic converts a parser or validator error to a diagnostic. The
// message carries file:line:column, the path the stable location.
func gqlDiagnostic(code plxerr.Code, file string, err error) plxerr.Diagnostic {
	var ge *gqlerror.Error
	loc := plxerr.Location{File: file}
	msg := err.Error()
	if errors.As(err, &ge) {
		msg = ge.Message
		if len(ge.Locations) > 0 {
			msg = fmt.Sprintf("%s:%d:%d: %s", file, ge.Locations[0].Line, ge.Locations[0].Column, ge.Message)
		}
	}
	return plxerr.NewDiagnostic(code, loc, "%s", msg)
}

// problem is an error with its own diagnostic code.
type problem struct {
	code plxerr.Code
	msg  string
	line int
	col  int
}

func (p *problem) Error() string { return p.msg }

func gqlProblem(code plxerr.Code, op *ast.OperationDefinition, format string, args ...any) *problem {
	p := &problem{code: code, msg: fmt.Sprintf(format, args...)}
	if op.Position != nil {
		p.line, p.col = op.Position.Line, op.Position.Column
	}
	return p
}

func gqlOpDiagnostic(file string, op *ast.OperationDefinition, err error) plxerr.Diagnostic {
	code := plxerr.ImportConstructUnsupported
	msg := err.Error()
	var p *problem
	if errors.As(err, &p) {
		code = p.code
		if p.line > 0 {
			msg = fmt.Sprintf("%s:%d:%d: %s", file, p.line, p.col, p.msg)
		}
	}
	if code == plxerr.ImportConstructUnsupported {
		msg += "; the operation is left out"
	}
	return plxerr.NewDiagnostic(code, plxerr.Location{File: file, Path: plxerr.Pointer("operations", op.Name)}, "%s", msg)
}

// affected lists the operations a validation error belongs to: the one whose
// text contains its line, those that use the fragment that contains it, or
// every operation when it has no location.
func affected(doc *ast.QueryDocument, e *gqlerror.Error) []*ast.OperationDefinition {
	if len(e.Locations) == 0 {
		return doc.Operations
	}
	line := e.Locations[0].Line
	var out []*ast.OperationDefinition
	for _, op := range doc.Operations {
		if op.Position != nil && spanContains(doc, op.Position.Line, line) {
			out = append(out, op)
		}
	}
	for _, f := range doc.Fragments {
		if f.Position != nil && spanContains(doc, f.Position.Line, line) {
			for _, op := range doc.Operations {
				if slices.ContainsFunc(usedFragments(doc, op.SelectionSet), func(u *ast.FragmentDefinition) bool { return u == f }) {
					out = append(out, op)
				}
			}
		}
	}
	return out
}

// spanContains reports whether line lies in the definition starting at start,
// which ends where the next definition begins.
func spanContains(doc *ast.QueryDocument, start, line int) bool {
	next := int(^uint(0) >> 1)
	for _, p := range definitionLines(doc) {
		if p > start && p < next {
			next = p
		}
	}
	return line >= start && line < next
}

func definitionLines(doc *ast.QueryDocument) []int {
	var out []int
	for _, o := range doc.Operations {
		if o.Position != nil {
			out = append(out, o.Position.Line)
		}
	}
	for _, f := range doc.Fragments {
		if f.Position != nil {
			out = append(out, f.Position.Line)
		}
	}
	return out
}

// usedFragments returns the fragments a selection set uses, directly or
// through other fragments, in document order.
func usedFragments(doc *ast.QueryDocument, set ast.SelectionSet) []*ast.FragmentDefinition {
	seen := map[string]bool{}
	var visit func(ast.SelectionSet)
	visit = func(s ast.SelectionSet) {
		for _, sel := range s {
			switch n := sel.(type) {
			case *ast.Field:
				visit(n.SelectionSet)
			case *ast.InlineFragment:
				visit(n.SelectionSet)
			case *ast.FragmentSpread:
				if !seen[n.Name] {
					seen[n.Name] = true
					if f := doc.Fragments.ForName(n.Name); f != nil {
						visit(f.SelectionSet)
					}
				}
			}
		}
	}
	visit(set)
	var out []*ast.FragmentDefinition
	for _, f := range doc.Fragments {
		if seen[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

// gqlConv converts the operations of one schema.
type gqlConv struct {
	schema *ast.Schema
	types  *typeSet
}

// operation converts one validated operation to its configuration.
func (g *gqlConv) operation(file string, doc *ast.QueryDocument, op *ast.OperationDefinition) (string, map[string]any, error) {
	if op.Name == "" {
		return "", nil, gqlProblem(plxerr.ImportOperationInvalid, op, "an operation needs a name to be called by")
	}
	name := lowerCamel(op.Name)
	if op.Operation == ast.Subscription {
		return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "%s is a subscription; subscriptions are configured on the source (DAT-012)", op.Name)
	}
	if len(op.Directives) > 0 {
		return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "%s has directives, which have no Plux type", op.Name)
	}
	oc := &opCtx{name: upperCamel(op.Name), pending: map[string]map[string]any{}}
	cfg := map[string]any{"select": "data"}

	var fields []map[string]any
	for _, v := range op.VariableDefinitions {
		if !nameShapeOK(v.Variable) {
			return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "the variable $%s is not a valid Plux name (lowerCamelCase)", v.Variable)
		}
		t, err := g.inputType(oc, v.Type, nil)
		if err != nil {
			return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "the variable $%s: %v", v.Variable, err)
		}
		fields = append(fields, field(v.Variable, t, "", false))
	}
	if len(fields) > 0 {
		in := oc.name + "Input"
		oc.pending[in] = map[string]any{"name": in, "fields": toAny(fields)}
		cfg["input"] = in
	}

	root := g.schema.Query
	if op.Operation == ast.Mutation {
		root = g.schema.Mutation
	}
	out := oc.name + "Output"
	if err := g.selectionObject(oc, doc, op.SelectionSet, root, out, nil); err != nil {
		return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "%v", err)
	}
	cfg["output"] = out

	var buf strings.Builder
	formatter.NewFormatter(&buf, formatter.WithCompacted()).FormatQueryDocument(&ast.QueryDocument{
		Operations: ast.OperationList{op},
		Fragments:  usedFragments(doc, op.SelectionSet),
	})
	cfg["query"] = strings.TrimSpace(buf.String())

	if err := g.types.merge(oc.pending); err != nil {
		return "", nil, gqlProblem(plxerr.ImportConstructUnsupported, op, "%v", err)
	}
	return name, cfg, nil
}

// scalarTypes maps the built-in GraphQL scalars to Plux types.
var scalarTypes = map[string]string{"Int": "int", "Float": "double", "String": "string", "Boolean": "bool", "ID": "string"}

// namedType converts the named type of a schema type: a built-in scalar, an
// enum or, when objects is set, an input object.
func (g *gqlConv) namedType(oc *opCtx, name string, stack []string) (string, error) {
	if t, ok := scalarTypes[name]; ok {
		return t, nil
	}
	def := g.schema.Types[name]
	if def == nil {
		return "", fmt.Errorf("the type %s is not in the schema", name)
	}
	switch def.Kind {
	case ast.Enum:
		members := make([]any, 0, len(def.EnumValues))
		for _, v := range def.EnumValues {
			if !isEnumMember(v.Name) {
				return "", fmt.Errorf("the enum value %s of %s is not a Plux enum member (a letter, then letters and digits)", v.Name, name)
			}
			members = append(members, v.Name)
		}
		tn := upperCamel(name)
		oc.pending[tn] = map[string]any{"name": tn, "enum": members}
		return tn, nil
	case ast.InputObject:
		if slices.Contains(stack, name) {
			return "", fmt.Errorf("the input type %s refers to itself", name)
		}
		stack = append(slices.Clone(stack), name)
		var fields []map[string]any
		for _, f := range def.Fields {
			if !nameShapeOK(f.Name) {
				return "", fmt.Errorf("the field %s of %s is not a valid Plux name (lowerCamelCase)", f.Name, name)
			}
			t, err := g.inputType(oc, f.Type, stack)
			if err != nil {
				return "", err
			}
			fields = append(fields, field(f.Name, t, "", false))
		}
		if len(fields) == 0 {
			return "", fmt.Errorf("the input type %s has no fields", name)
		}
		tn := upperCamel(name)
		oc.pending[tn] = map[string]any{"name": tn, "fields": toAny(fields)}
		return tn, nil
	}
	return "", fmt.Errorf("the type %s has no Plux type", name)
}

// inputType converts a variable or input field type; GraphQL types are
// nullable unless marked non-null.
func (g *gqlConv) inputType(oc *opCtx, t *ast.Type, stack []string) (string, error) {
	var base string
	var err error
	if t.Elem != nil {
		var elem string
		if elem, err = g.inputType(oc, t.Elem, stack); err == nil {
			base = "list<" + elem + ">"
		}
	} else {
		base, err = g.namedType(oc, t.NamedType, stack)
	}
	if err != nil {
		return "", err
	}
	return optional(base, t.NonNull), nil
}

// selectionObject declares the object type name for a selection set on def.
func (g *gqlConv) selectionObject(oc *opCtx, doc *ast.QueryDocument, set ast.SelectionSet, def *ast.Definition, name string, stack []string) error {
	var order []string
	byKey := map[string][]*ast.Field{}
	if err := g.collect(doc, set, def, &order, byKey, nil); err != nil {
		return err
	}
	var fields []map[string]any
	for _, key := range order {
		fs := byKey[key]
		if !nameShapeOK(key) {
			return fmt.Errorf("the response field %q is not a valid Plux name (lowerCamelCase); alias it", key)
		}
		fd := fs[0].Definition
		if fd == nil {
			return fmt.Errorf("the field %q is not in the schema", key)
		}
		var sub ast.SelectionSet
		for _, f := range fs {
			if len(f.Directives) > 0 {
				return fmt.Errorf("the field %q has directives, which have no Plux type", key)
			}
			sub = append(sub, f.SelectionSet...)
		}
		t, err := g.outputType(oc, doc, fd.Type, sub, name+upperCamel(key), stack)
		if err != nil {
			return err
		}
		fields = append(fields, field(key, t, "", false))
	}
	if len(fields) == 0 {
		return fmt.Errorf("%s selects no fields", name)
	}
	oc.pending[name] = map[string]any{"name": name, "fields": toAny(fields)}
	return nil
}

// collect flattens a selection set into response keys, in order of first
// appearance. Fragments apply when their type condition is def itself.
func (g *gqlConv) collect(doc *ast.QueryDocument, set ast.SelectionSet, def *ast.Definition, order *[]string, byKey map[string][]*ast.Field, seen []string) error {
	for _, sel := range set {
		switch n := sel.(type) {
		case *ast.Field:
			if n.Name == "__typename" {
				continue
			}
			key := n.Alias
			if key == "" {
				key = n.Name
			}
			if _, ok := byKey[key]; !ok {
				*order = append(*order, key)
			}
			byKey[key] = append(byKey[key], n)
		case *ast.InlineFragment:
			if n.TypeCondition != "" && n.TypeCondition != def.Name {
				return fmt.Errorf("an inline fragment on %s inside %s selects a different type", n.TypeCondition, def.Name)
			}
			if err := g.collect(doc, n.SelectionSet, def, order, byKey, seen); err != nil {
				return err
			}
		case *ast.FragmentSpread:
			f := doc.Fragments.ForName(n.Name)
			if f == nil || slices.Contains(seen, n.Name) {
				return fmt.Errorf("the fragment %s is missing or refers to itself", n.Name)
			}
			if f.TypeCondition != def.Name {
				return fmt.Errorf("the fragment %s on %s inside %s selects a different type", n.Name, f.TypeCondition, def.Name)
			}
			if err := g.collect(doc, f.SelectionSet, def, order, byKey, append(slices.Clone(seen), n.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// outputType converts the type of a selected field.
func (g *gqlConv) outputType(oc *opCtx, doc *ast.QueryDocument, t *ast.Type, sub ast.SelectionSet, name string, stack []string) (string, error) {
	var base string
	if t.Elem != nil {
		elem, err := g.outputType(oc, doc, t.Elem, sub, name+"Item", stack)
		if err != nil {
			return "", err
		}
		base = "list<" + elem + ">"
	} else {
		def := g.schema.Types[t.NamedType]
		switch {
		case def == nil:
			return "", fmt.Errorf("the type %s is not in the schema", t.NamedType)
		case def.Kind == ast.Object:
			if slices.Contains(stack, def.Name+"/"+name) || len(stack) > 32 {
				return "", fmt.Errorf("the selection of %s is nested too deeply", def.Name)
			}
			if err := g.selectionObject(oc, doc, sub, def, name, append(slices.Clone(stack), def.Name+"/"+name)); err != nil {
				return "", err
			}
			base = name
		case def.Kind == ast.Interface || def.Kind == ast.Union:
			return "", fmt.Errorf("the %s type %s has no Plux type", strings.ToLower(string(def.Kind)), def.Name)
		default:
			var err error
			if base, err = g.namedType(oc, t.NamedType, nil); err != nil {
				return "", err
			}
		}
	}
	return optional(base, t.NonNull), nil
}
