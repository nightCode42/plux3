// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// fieldKind classifies how a field is stored.
type fieldKind uint8

// Field kinds of the generated layout tables.
const (
	fieldScalar fieldKind = iota + 1
	fieldStruct
	fieldString
	fieldTable
	fieldVectorScalar
	fieldVectorStruct
	fieldVectorString
	fieldVectorTable
)

// fieldLayout describes a table field: its vtable slot, how it is stored,
// the size and alignment of the inline value or vector element, and the
// index of a referenced table in tableLayouts.
type fieldLayout struct {
	name     string
	id       int
	kind     fieldKind
	size     int
	align    int
	table    int
	required bool
}

// tableLayout describes a table.
type tableLayout struct {
	name   string
	fields []fieldLayout
}

// errVerify is wrapped by every verification failure.
var errVerify = errors.New("invalid FlatBuffers buffer")

// verifier checks a buffer against the layout tables before any accessor
// reads it: every offset, size, alignment, vtable, string and vector,
// within a depth and a count of tables and vectors (ADR-0002).
type verifier struct {
	buf       []byte
	maxDepth  int
	maxVisits int64
	visits    int64
}

// verify checks that data is a well-formed section of kind k.
func verify(k SectionKind, data []byte, lim limits.Set) error {
	info := sectionInfo[k]
	root, ok := rootLayouts[info.ident]
	if !ok {
		return fmt.Errorf("%w: no layout for %s", errVerify, k)
	}
	if len(data) < 8 || len(data) > math.MaxInt32 {
		return fmt.Errorf("%w: size %d", errVerify, len(data))
	}
	if string(data[4:8]) != info.ident {
		return fmt.Errorf("%w: file identifier %q, want %q", errVerify, data[4:8], info.ident)
	}
	v := verifier{buf: data, maxDepth: int(lim.Get(limits.BundleVerifierDepth)), maxVisits: lim.Get(limits.BundleVerifierTables)}
	return v.table(v.u32(0), root, 1)
}

func (*verifier) fail(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errVerify, fmt.Sprintf(format, args...))
}

// u32 reads a uint32 at pos; callers check bounds first.
func (v *verifier) u32(pos int) int { return int(binary.LittleEndian.Uint32(v.buf[pos:])) }

func (v *verifier) u16(pos int) int { return int(binary.LittleEndian.Uint16(v.buf[pos:])) }

// inBounds reports whether n bytes at pos lie in the buffer.
func (v *verifier) inBounds(pos, n int) bool {
	return pos >= 0 && n >= 0 && int64(pos)+int64(n) <= int64(len(v.buf))
}

// visit counts a table or vector against the limit.
func (v *verifier) visit() error {
	v.visits++
	if v.visits > v.maxVisits {
		return v.fail("more than %d tables and vectors", v.maxVisits)
	}
	return nil
}

// deref follows the uoffset at pos.
func (v *verifier) deref(pos int) (int, error) {
	if pos%4 != 0 || !v.inBounds(pos, 4) {
		return 0, v.fail("offset at %d is misaligned or out of bounds", pos)
	}
	off := v.u32(pos)
	if off == 0 || off > math.MaxInt32 || !v.inBounds(pos+off, 4) {
		return 0, v.fail("offset at %d points outside the buffer", pos)
	}
	return pos + off, nil
}

// table checks the table at pos against layout t.
func (v *verifier) table(pos, t, depth int) error {
	if depth > v.maxDepth {
		return v.fail("tables nested deeper than %d", v.maxDepth)
	}
	if err := v.visit(); err != nil {
		return err
	}
	if pos%4 != 0 || !v.inBounds(pos, 4) {
		return v.fail("table at %d is misaligned or out of bounds", pos)
	}
	vt := int64(pos) - int64(int32(binary.LittleEndian.Uint32(v.buf[pos:]))) //nolint:gosec // G115: soffset is signed.
	if vt < 0 || vt%2 != 0 || vt+4 > int64(len(v.buf)) {
		return v.fail("vtable of the table at %d is misaligned or out of bounds", pos)
	}
	vtPos := int(vt)
	vtSize, tableSize := v.u16(vtPos), v.u16(vtPos+2)
	if vtSize < 4 || vtSize%2 != 0 || !v.inBounds(vtPos, vtSize) || tableSize < 4 || !v.inBounds(pos, tableSize) {
		return v.fail("table at %d has an invalid vtable", pos)
	}
	layout := &tableLayouts[t]
	for i := range layout.fields {
		f := &layout.fields[i]
		off := 0
		if slot := 4 + 2*f.id; slot+2 <= vtSize {
			off = v.u16(vtPos + slot)
		}
		if off == 0 {
			if f.required {
				return v.fail("%s.%s is required", layout.name, f.name)
			}
			continue
		}
		if err := v.field(pos, tableSize, off, f, depth); err != nil {
			return err
		}
	}
	return nil
}

// field checks one present field at offset off of the table at pos.
func (v *verifier) field(pos, tableSize, off int, f *fieldLayout, depth int) error {
	size, align := f.size, f.align
	if f.kind != fieldScalar && f.kind != fieldStruct {
		size, align = 4, 4
	}
	at := pos + off
	if off < 4 || off+size > tableSize || at%align != 0 {
		return v.fail("field %s at %d is misplaced", f.name, at)
	}
	if f.kind == fieldScalar || f.kind == fieldStruct {
		return nil
	}
	target, err := v.deref(at)
	if err != nil {
		return err
	}
	switch f.kind {
	case fieldString:
		return v.string(target)
	case fieldTable:
		return v.table(target, f.table, depth+1)
	default:
		return v.vector(target, f, depth)
	}
}

// string checks a length-prefixed, NUL-terminated UTF-8 string.
func (v *verifier) string(pos int) error {
	if err := v.visit(); err != nil {
		return err
	}
	n := v.u32(pos)
	if !v.inBounds(pos+4, n+1) || n > math.MaxInt32 || v.buf[pos+4+n] != 0 {
		return v.fail("string at %d is out of bounds or not terminated", pos)
	}
	if !utf8.Valid(v.buf[pos+4 : pos+4+n]) {
		return v.fail("string at %d is not valid UTF-8", pos)
	}
	return nil
}

// vector checks a vector and, for strings and tables, every element.
func (v *verifier) vector(pos int, f *fieldLayout, depth int) error {
	if err := v.visit(); err != nil {
		return err
	}
	n := int64(v.u32(pos))
	elem := int64(f.size)
	if f.kind == fieldVectorString || f.kind == fieldVectorTable {
		elem = 4
	}
	if n*elem > int64(len(v.buf)-pos-4) {
		return v.fail("vector %s at %d is out of bounds", f.name, pos)
	}
	if f.kind == fieldVectorScalar || f.kind == fieldVectorStruct {
		return nil
	}
	for i := range int(n) {
		target, err := v.deref(pos + 4 + 4*i)
		if err != nil {
			return err
		}
		if f.kind == fieldVectorString {
			err = v.string(target)
		} else {
			err = v.table(target, f.table, depth+1)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
