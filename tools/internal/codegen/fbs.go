// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FBSLayoutPath is the Go file the bundle verifier interprets (ADR-0002).
const FBSLayoutPath = "backend/internal/bundle/layout_gen.go"

// FBSSource names the schemas the layout tables come from.
const FBSSource = "schema/fbs"

// FBS is the layout of the bundle section schemas, read from the binary
// schemas (.bfbs) flatc writes for schema/fbs/*.fbs.
type FBS struct {
	// Tables are every table of every schema, sorted by name.
	Tables []FBSTable
	// Roots maps each file identifier to the name of its root table.
	Roots map[string]string
}

// FBSTable is a table and the fields the verifier checks.
type FBSTable struct {
	Name   string
	Fields []FBSField
}

// FBSFieldKind classifies how a field is stored.
type FBSFieldKind int

// Field kinds: inline scalars and structs, or offsets to strings, tables
// and vectors.
const (
	FieldScalar FBSFieldKind = iota + 1
	FieldStruct
	FieldString
	FieldTable
	FieldVectorScalar
	FieldVectorStruct
	FieldVectorString
	FieldVectorTable
)

// FBSField is a field of a table. Size and Align describe the inline value
// or the vector element; Table names the referenced table.
type FBSField struct {
	Name     string
	ID       int
	Kind     FBSFieldKind
	Size     int
	Align    int
	Table    string
	Required bool
}

// Base types of reflection.fbs.
const (
	bfbsUType  = 1
	bfbsBool   = 2
	bfbsDouble = 12
	bfbsString = 13
	bfbsVector = 14
	bfbsObj    = 15
)

// bfbsObject is an object of a binary schema.
type bfbsObject struct {
	name     string
	isStruct bool
	minAlign int
	byteSize int
	fields   []bfbsField
}

// bfbsField is a field of a binary-schema object.
type bfbsField struct {
	name                            string
	id                              int
	baseType, element, index        int
	baseSize, elementSize, required int
	deprecated                      bool
}

// LoadFBS reads every .bfbs file of dir.
func LoadFBS(dir string) (*FBS, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.bfbs"))
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("codegen.LoadFBS: no binary schemas in %s", dir)
	}
	slices.Sort(paths)
	out := &FBS{Roots: map[string]string{}}
	tables := map[string]FBSTable{}
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // G304: flatc output under the build directory.
		if err != nil {
			return nil, fmt.Errorf("codegen.LoadFBS: %w", err)
		}
		if err := loadSchema(data, out, tables); err != nil {
			return nil, fmt.Errorf("codegen.LoadFBS: %s: %w", filepath.Base(p), err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		out.Tables = append(out.Tables, tables[name])
	}
	return out, nil
}

// loadSchema adds the tables and root of one binary schema.
func loadSchema(data []byte, out *FBS, tables map[string]FBSTable) error {
	r := fbReader{data}
	if len(data) < 8 || string(data[4:8]) != "BFBS" {
		return errors.New("not a binary schema")
	}
	schema := r.root()
	var objects []bfbsObject
	objs := r.tables(schema, 0)
	if len(objs) == 0 {
		return errors.New("no objects")
	}
	for _, o := range objs {
		obj := bfbsObject{
			name: r.str(o, 0), isStruct: r.scalar(o, 2, 1) != 0,
			minAlign: r.scalar(o, 3, 4), byteSize: r.scalar(o, 4, 4),
		}
		for _, f := range r.tables(o, 1) {
			typ := r.table(f, 1)
			obj.fields = append(obj.fields, bfbsField{
				name: r.str(f, 0), id: r.scalar(f, 2, 2), deprecated: r.scalar(f, 6, 1) != 0, required: r.scalar(f, 7, 1),
				baseType: r.scalar(typ, 0, 1), element: r.scalar(typ, 1, 1), index: r.scalarOr(typ, 2, 4, -1),
				baseSize: r.scalarOr(typ, 4, 4, 4), elementSize: r.scalar(typ, 5, 4),
			})
		}
		objects = append(objects, obj)
	}
	for _, o := range objects {
		if o.isStruct {
			continue
		}
		t, err := tableLayout(o, objects)
		if err != nil {
			return fmt.Errorf("table %s: %w", o.name, err)
		}
		if prev, seen := tables[t.Name]; seen && !slices.Equal(prev.Fields, t.Fields) {
			return fmt.Errorf("table %s differs between schemas", t.Name)
		}
		tables[t.Name] = t
	}
	if ident := r.str(schema, 2); ident != "" {
		root := r.table(schema, 4)
		if root == 0 {
			return fmt.Errorf("file identifier %q without a root table", ident)
		}
		out.Roots[ident] = shortName(r.str(root, 0))
	}
	return nil
}

// tableLayout converts a binary-schema table.
func tableLayout(o bfbsObject, objects []bfbsObject) (FBSTable, error) {
	t := FBSTable{Name: shortName(o.name)}
	for _, f := range o.fields {
		if f.deprecated {
			continue
		}
		field, err := fieldLayout(f, objects)
		if err != nil {
			return t, fmt.Errorf("field %s: %w", f.name, err)
		}
		t.Fields = append(t.Fields, field)
	}
	slices.SortFunc(t.Fields, func(a, b FBSField) int { return a.ID - b.ID })
	return t, nil
}

// fieldLayout classifies one field.
func fieldLayout(f bfbsField, objects []bfbsObject) (FBSField, error) {
	out := FBSField{Name: f.name, ID: f.id, Required: f.required != 0}
	object := func() (bfbsObject, error) {
		if f.index < 0 || f.index >= len(objects) {
			return bfbsObject{}, errors.New("object index out of range")
		}
		return objects[f.index], nil
	}
	switch {
	case f.baseType >= bfbsUType && f.baseType <= bfbsDouble && f.baseType != bfbsUType:
		out.Kind, out.Size, out.Align = FieldScalar, f.baseSize, f.baseSize
	case f.baseType == bfbsString:
		out.Kind = FieldString
	case f.baseType == bfbsObj:
		o, err := object()
		if err != nil {
			return out, err
		}
		if o.isStruct {
			out.Kind, out.Size, out.Align = FieldStruct, o.byteSize, o.minAlign
		} else {
			out.Kind, out.Table = FieldTable, shortName(o.name)
		}
	case f.baseType == bfbsVector:
		return vectorLayout(out, f, object)
	default:
		return out, fmt.Errorf("unsupported base type %d (unions, arrays and 64-bit vectors are not used)", f.baseType)
	}
	return out, nil
}

// vectorLayout classifies a vector field.
func vectorLayout(out FBSField, f bfbsField, object func() (bfbsObject, error)) (FBSField, error) {
	switch {
	case f.element >= bfbsBool && f.element <= bfbsDouble:
		out.Kind, out.Size, out.Align = FieldVectorScalar, f.elementSize, f.elementSize
	case f.element == bfbsString:
		out.Kind = FieldVectorString
	case f.element == bfbsObj:
		o, err := object()
		if err != nil {
			return out, err
		}
		if o.isStruct {
			out.Kind, out.Size, out.Align = FieldVectorStruct, o.byteSize, o.minAlign
		} else {
			out.Kind, out.Table = FieldVectorTable, shortName(o.name)
		}
	default:
		return out, fmt.Errorf("unsupported vector element type %d", f.element)
	}
	return out, nil
}

// shortName drops the namespace of a qualified name.
func shortName(qualified string) string {
	return qualified[strings.LastIndexByte(qualified, '.')+1:]
}

// FBSFiles renders the verifier's layout tables.
func FBSFiles(s *FBS) ([]File, error) {
	index := map[string]int{}
	for i, t := range s.Tables {
		index[t.Name] = i
	}
	var b bytes.Buffer
	b.WriteString(header(LangGo, FBSSource))
	b.WriteString("package bundle\n\n")
	b.WriteString("// tableLayouts describes every table of the section schemas, sorted by\n// name, for the verifier (ADR-0002).\n")
	b.WriteString("var tableLayouts = [...]tableLayout{\n")
	kinds := map[FBSFieldKind]string{
		FieldScalar: "fieldScalar", FieldStruct: "fieldStruct", FieldString: "fieldString", FieldTable: "fieldTable",
		FieldVectorScalar: "fieldVectorScalar", FieldVectorStruct: "fieldVectorStruct",
		FieldVectorString: "fieldVectorString", FieldVectorTable: "fieldVectorTable",
	}
	for i, t := range s.Tables {
		fmt.Fprintf(&b, "\t%d: {name: %q, fields: []fieldLayout{\n", i, t.Name)
		for _, f := range t.Fields {
			table := -1
			if f.Table != "" {
				idx, ok := index[f.Table]
				if !ok {
					return nil, fmt.Errorf("codegen.FBSFiles: %s.%s refers to unknown table %s", t.Name, f.Name, f.Table)
				}
				table = idx
			}
			fmt.Fprintf(&b, "\t\t{name: %q, id: %d, kind: %s, size: %d, align: %d, table: %d, required: %t},\n",
				f.Name, f.ID, kinds[f.Kind], f.Size, f.Align, table, f.Required)
		}
		b.WriteString("\t}},\n")
	}
	b.WriteString("}\n\n// rootLayouts maps each section's file identifier to its root table.\nvar rootLayouts = map[string]int{\n")
	for _, ident := range slices.Sorted(maps.Keys(s.Roots)) {
		idx, ok := index[s.Roots[ident]]
		if !ok {
			return nil, fmt.Errorf("codegen.FBSFiles: root %s of %q is not a table", s.Roots[ident], ident)
		}
		fmt.Fprintf(&b, "\t%q: %d, // %s\n", ident, idx, s.Roots[ident])
	}
	b.WriteString("}\n")
	f, err := goFile(FBSLayoutPath, b.Bytes())
	if err != nil {
		return nil, err
	}
	return []File{f}, nil
}

// fbReader reads a FlatBuffers buffer without a schema-specific API. It
// reads flatc's own output, so a read out of bounds returns a zero value
// instead of an error.
type fbReader struct {
	data []byte
}

func (r *fbReader) u32(pos int) int {
	if pos < 0 || pos+4 > len(r.data) {
		return 0
	}
	return int(binary.LittleEndian.Uint32(r.data[pos:]))
}

// root returns the position of the root table.
func (r *fbReader) root() int { return r.u32(0) }

// field returns the position of field id of the table at pos, or 0.
func (r *fbReader) field(pos, id int) int {
	if pos <= 0 || pos+4 > len(r.data) {
		return 0
	}
	vt := pos - int(int32(binary.LittleEndian.Uint32(r.data[pos:]))) //nolint:gosec // G115: soffset is signed by definition.
	if vt < 0 || vt+4 > len(r.data) {
		return 0
	}
	vtSize := int(binary.LittleEndian.Uint16(r.data[vt:]))
	slot := 4 + 2*id
	if slot+2 > vtSize || vt+slot+2 > len(r.data) {
		return 0
	}
	off := int(binary.LittleEndian.Uint16(r.data[vt+slot:]))
	if off == 0 {
		return 0
	}
	return pos + off
}

// scalarOr reads an unsigned scalar of size bytes, or def when absent;
// sizes 1, 2 and 4 are sign-extended when def is negative.
func (r *fbReader) scalarOr(pos, id, size, def int) int {
	p := r.field(pos, id)
	if p == 0 || p+size > len(r.data) {
		return def
	}
	switch size {
	case 1:
		return int(r.data[p])
	case 2:
		return int(binary.LittleEndian.Uint16(r.data[p:]))
	default:
		v := binary.LittleEndian.Uint32(r.data[p:])
		if def < 0 {
			return int(int32(v)) //nolint:gosec // G115: the field is a signed int.
		}
		return int(v)
	}
}

func (r *fbReader) scalar(pos, id, size int) int { return r.scalarOr(pos, id, size, 0) }

// deref follows the offset stored at field id.
func (r *fbReader) deref(pos, id int) int {
	p := r.field(pos, id)
	if p == 0 {
		return 0
	}
	return p + r.u32(p)
}

func (r *fbReader) table(pos, id int) int { return r.deref(pos, id) }

func (r *fbReader) str(pos, id int) string {
	p := r.deref(pos, id)
	if p == 0 {
		return ""
	}
	n := r.u32(p)
	if p+4+n > len(r.data) {
		return ""
	}
	return string(r.data[p+4 : p+4+n])
}

// tables returns the positions of a vector of tables.
func (r *fbReader) tables(pos, id int) []int {
	p := r.deref(pos, id)
	if p == 0 {
		return nil
	}
	n := r.u32(p)
	if p+4+4*n > len(r.data) {
		return nil
	}
	out := make([]int, n)
	for i := range out {
		e := p + 4 + 4*i
		out[i] = e + r.u32(e)
	}
	return out
}
