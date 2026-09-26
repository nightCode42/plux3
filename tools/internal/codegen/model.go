// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SchemaDir is the directory of the document schemas, relative to the root.
const SchemaDir = "schema/json"

// commonSchema is the file of shared definitions.
const commonSchema = "common.schema.json"

// TypeKind classifies a field type.
type TypeKind int

// Field type kinds.
const (
	KindString TypeKind = iota + 1
	KindInt
	KindNumber
	KindBool
	KindRaw   // any JSON value, interpreted by the compiler
	KindArray // Elem
	KindMap   // string keys, Elem values
	KindNamed // Name
)

// TypeRef is the type of a field.
type TypeRef struct {
	Kind TypeKind
	Elem *TypeRef
	Name string
}

// NamedKind classifies a named type.
type NamedKind int

// Named type kinds.
const (
	NamedStruct NamedKind = iota + 1
	NamedEnum
	NamedSingleOrList // one Elem value or a non-empty list of them
)

// NamedType is a generated type.
type NamedType struct {
	Name   string
	Doc    string
	Kind   NamedKind
	Fields []Field  // NamedStruct
	Values []string // NamedEnum
	Elem   string   // NamedSingleOrList: the element type's name
	Source string   // file#pointer of the defining schema
}

// Field is one property of a struct type.
type Field struct {
	JSONName string
	Doc      string
	Type     TypeRef
	Required bool
	Const    string // fixed string value, e.g. a document kind
}

// Document is a top-level document schema.
type Document struct {
	Kind     string // value of the `kind` property
	TypeName string
	File     string
	Title    string
	Doc      string
}

// Model is the language-neutral view of the document schemas.
type Model struct {
	Documents []Document
	Types     []*NamedType // sorted by name
	byName    map[string]*NamedType
}

// Type returns the named type called name.
func (m *Model) Type(name string) *NamedType { return m.byName[name] }

// modelBuilder converts schemas into a Model.
type modelBuilder struct {
	files   map[string]*schemaNode
	types   map[string]*NamedType
	origins map[string]string // type name → source, to detect clashes
}

// LoadModel parses every *.schema.json in dir except the limits registry
// schema and builds the model.
func LoadModel(dir string) (*Model, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("codegen.LoadModel: %w", err)
	}
	b := &modelBuilder{files: map[string]*schemaNode{}, types: map[string]*NamedType{}, origins: map[string]string{}}
	var docFiles []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".schema.json") || name == "limits.schema.json" {
			continue
		}
		n, err := parseSchemaFile(dir, name)
		if err != nil {
			return nil, err
		}
		b.files[name] = n
		if name != commonSchema {
			docFiles = append(docFiles, name)
		}
	}
	if b.files[commonSchema] == nil {
		return nil, fmt.Errorf("codegen.LoadModel: %s missing in %s", commonSchema, dir)
	}
	slices.Sort(docFiles)
	m := &Model{}
	for _, f := range docFiles {
		doc, err := b.document(f)
		if err != nil {
			return nil, err
		}
		m.Documents = append(m.Documents, doc)
	}
	for _, t := range b.types {
		m.Types = append(m.Types, t)
	}
	slices.SortFunc(m.Types, func(a, b *NamedType) int { return strings.Compare(a.Name, b.Name) })
	m.byName = b.types
	return m, nil
}

// document converts one top-level document schema.
func (b *modelBuilder) document(file string) (Document, error) {
	n := b.files[file]
	name := GoName(strings.TrimSuffix(file, ".schema.json")) + "Document"
	var kind string
	for _, p := range n.properties {
		if p.name == "kind" && p.schema.constValue != nil {
			kind = *p.schema.constValue
		}
	}
	if kind == "" {
		return Document{}, n.errorf("document schema without a constant `kind`")
	}
	if _, err := b.named(name, n); err != nil {
		return Document{}, err
	}
	return Document{Kind: kind, TypeName: name, File: file, Title: n.title, Doc: n.desc}, nil
}

// resolve follows a $ref to its target node and definition name.
func (b *modelBuilder) resolve(from *schemaNode) (*schemaNode, string, error) {
	file, pointer, ok := strings.Cut(from.ref, "#")
	if !ok || !strings.HasPrefix(pointer, "/$defs/") {
		return nil, "", from.errorf("$ref %q must point at a definition", from.ref)
	}
	if file == "" {
		file = from.file
	}
	root := b.files[file]
	if root == nil {
		return nil, "", from.errorf("$ref %q: unknown file %q", from.ref, file)
	}
	name := strings.TrimPrefix(pointer, "/$defs/")
	target := root.def(name)
	if target == nil {
		return nil, "", from.errorf("$ref %q: no definition %q", from.ref, name)
	}
	return target, name, nil
}

// typeOf returns the field type of a schema node.
func (b *modelBuilder) typeOf(n *schemaNode) (TypeRef, error) {
	switch {
	case n.raw || n.boolean != nil:
		return TypeRef{Kind: KindRaw}, nil
	case n.ref != "":
		return b.refType(n)
	case len(n.enum) > 0:
		return TypeRef{}, n.errorf("enums must be named definitions")
	case n.constValue != nil:
		return TypeRef{Kind: KindString}, nil
	case len(n.anyOf) > 0:
		return TypeRef{}, n.errorf("anyOf is only allowed on x-plux-raw values")
	}
	if len(n.types) != 1 {
		return TypeRef{}, n.errorf("exactly one type is required outside x-plux-raw values, got %v", n.types)
	}
	switch n.types[0] {
	case "string":
		return TypeRef{Kind: KindString}, nil
	case "integer":
		return TypeRef{Kind: KindInt}, nil
	case "number":
		return TypeRef{Kind: KindNumber}, nil
	case "boolean":
		return TypeRef{Kind: KindBool}, nil
	case "array":
		if n.items == nil {
			return TypeRef{}, n.errorf("array without items")
		}
		elem, err := b.typeOf(n.items)
		return TypeRef{Kind: KindArray, Elem: &elem}, err
	case "object":
		return b.objectType(n)
	}
	return TypeRef{}, n.errorf("unsupported type %q", n.types[0])
}

// refType returns the type a $ref denotes: the primitive of a primitive
// definition, or a named type.
func (b *modelBuilder) refType(n *schemaNode) (TypeRef, error) {
	target, defName, err := b.resolve(n)
	if err != nil {
		return TypeRef{}, err
	}
	if target.raw {
		return TypeRef{Kind: KindRaw}, nil
	}
	isNamed := len(target.enum) > 0 || len(target.properties) > 0 || len(target.oneOf) > 0 && len(target.properties) == 0
	if !isNamed {
		return b.typeOf(target)
	}
	name := target.typeName
	if name == "" {
		name = GoName(defName)
	}
	return b.named(name, target)
}

// objectType returns a map type or the named type of an inline object.
func (b *modelBuilder) objectType(n *schemaNode) (TypeRef, error) {
	if len(n.properties) == 0 && n.additional != nil {
		elem, err := b.typeOf(n.additional)
		return TypeRef{Kind: KindMap, Elem: &elem}, err
	}
	if n.typeName == "" {
		return TypeRef{}, n.errorf("inline object types need x-plux-type")
	}
	return b.named(n.typeName, n)
}

// named registers (once) and returns the named type defined by n.
func (b *modelBuilder) named(name string, n *schemaNode) (TypeRef, error) {
	source := n.file + "#" + n.path
	if prev, ok := b.origins[name]; ok {
		if prev != source {
			return TypeRef{}, n.errorf("type name %s already defined at %s", name, prev)
		}
		return TypeRef{Kind: KindNamed, Name: name}, nil
	}
	b.origins[name] = source
	t := &NamedType{Name: name, Doc: n.desc, Source: source}
	b.types[name] = t
	var err error
	switch {
	case len(n.enum) > 0:
		t.Kind, t.Values = NamedEnum, n.enum
	case len(n.properties) > 0:
		t.Kind = NamedStruct
		err = b.fillStruct(t, n)
	default:
		t.Kind = NamedSingleOrList
		t.Elem, err = b.singleOrList(n)
	}
	if err != nil {
		return TypeRef{}, err
	}
	return TypeRef{Kind: KindNamed, Name: name}, nil
}

// fillStruct converts the properties of an object schema into fields.
func (b *modelBuilder) fillStruct(t *NamedType, n *schemaNode) error {
	if !n.closed {
		return n.errorf("object types must set additionalProperties to false (SCH-004)")
	}
	for _, branch := range n.oneOf {
		if !branch.isValidationOnly() {
			return n.errorf("oneOf on an object type may only constrain required properties")
		}
	}
	for _, p := range n.properties {
		if p.name == "$schema" {
			continue // an editor hint, not part of the model
		}
		ft, err := b.typeOf(p.schema)
		if err != nil {
			return err
		}
		f := Field{JSONName: p.name, Type: ft, Required: slices.Contains(n.required, p.name)}
		var target *schemaNode
		if p.schema.ref != "" {
			target, _, _ = b.resolve(p.schema)
		}
		f.Doc = p.schema.resolvedDoc(target)
		if p.schema.constValue != nil {
			f.Const = *p.schema.constValue
		}
		t.Fields = append(t.Fields, f)
	}
	return nil
}

// singleOrList checks the one union shape the profile allows,
// oneOf [X, array of X], and returns X's type name.
func (b *modelBuilder) singleOrList(n *schemaNode) (string, error) {
	if len(n.oneOf) != 2 || n.oneOf[0].ref == "" || n.oneOf[1].items == nil || n.oneOf[1].items.ref != n.oneOf[0].ref {
		return "", n.errorf("the only union allowed outside x-plux-raw values is oneOf [X, array of X]")
	}
	elem, err := b.typeOf(n.oneOf[0])
	if err != nil {
		return "", err
	}
	if elem.Kind != KindNamed {
		return "", n.errorf("single-or-list elements must be named types")
	}
	return elem.Name, nil
}

// Schemas returns the raw bytes of every schema file, for embedding.
func Schemas(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("codegen.Schemas: %w", err)
	}
	var files []File
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".schema.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("codegen.Schemas: %w", err)
		}
		files = append(files, File{Path: "backend/internal/schema/schemas/" + e.Name(), Content: data})
	}
	return files, nil
}
