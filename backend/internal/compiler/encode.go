// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"slices"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
)

// out is one bundle being encoded: its sections and what its sections
// share — programs, styles, the strings section and the source map.
type out struct {
	kind     bundle.Kind
	pl       *plugin // nil for the app bundle
	id       [16]byte
	key      string
	sections []bundle.Section
	programs map[uint64]*pxl.Program
	styles   map[uint64]*style
	shared   *interner
	features map[string]bool
	assets   map[string]bool
	// iconFonts are the icon fonts the bundle indexes (THM-005).
	iconFonts []indexedAsset
	srcmap    *sourceMap
	// srcmapData is the encoded source map of a release bundle.
	srcmapData []byte
}

// style is a deduplicated value-type object (CMP-020).
type style struct {
	typ   uint32
	value *value
}

// newOut starts a bundle.
func newOut(kind bundle.Kind, pl *plugin, id [16]byte, key string) *out {
	return &out{
		kind: kind, pl: pl, id: id, key: key, programs: map[uint64]*pxl.Program{}, styles: map[uint64]*style{},
		shared: newInterner(), features: map[string]bool{}, assets: map[string]bool{}, srcmap: &sourceMap{},
	}
}

// add appends a finished section.
func (o *out) add(kind bundle.SectionKind, id [16]byte, data []byte) {
	o.sections = append(o.sections, bundle.Section{Kind: kind, ID: id, Data: data})
}

// interner builds a string table; index 0 is the empty string (CMP-020).
type interner struct {
	list []string
	idx  map[string]uint32
}

func newInterner() *interner {
	return &interner{list: []string{""}, idx: map[string]uint32{"": 0}}
}

// of returns the index of s, adding it on first use.
func (t *interner) of(s string) uint32 {
	if i, ok := t.idx[s]; ok {
		return i
	}
	i := uint32(len(t.list)) //nolint:gosec // G115: bounded by the section size limit.
	t.list = append(t.list, s)
	t.idx[s] = i
	return i
}

// vector writes the table as a vector of strings.
func (t *interner) vector(b *flatbuffers.Builder) flatbuffers.UOffsetT {
	return stringVector(b, t.list)
}

// contentID is the first eight bytes of the SHA-256 of an encoding, read
// as a little-endian integer (ADR-0002).
func contentID(data []byte) uint64 {
	sum := sha256.Sum256(data)
	return binary.LittleEndian.Uint64(sum[:8])
}

// program registers a program and returns its content ID; two programs
// with one ID are a collision (PLX-2202).
func (u *unit) program(o *out, p *pxl.Program) uint64 {
	data := p.Encode()
	id := contentID(data)
	if prev, ok := o.programs[id]; ok && !bytes.Equal(prev.Encode(), data) {
		u.report(plxerr.ContentIDCollision, "", "", "programs %x collide", id)
	}
	o.programs[id] = p
	for _, f := range p.Features {
		o.features[f] = true
	}
	o.features["pxl.v1"] = true
	return id
}

// styleID registers a value-type object and returns its content ID: the
// hash of its canonical form, so equal objects share one style.
func (u *unit) styleID(o *out, v *value) uint64 {
	c := canonical(v)
	id := contentID(c)
	if prev, ok := o.styles[id]; ok && !bytes.Equal(canonical(prev.value), c) {
		u.report(plxerr.ContentIDCollision, "", "", "styles %x collide", id)
	}
	o.styles[id] = &style{typ: v.valueType, value: v}
	return id
}

// canonForm is the canonical form of a value: JSON of a fixed struct,
// with strings inline and programs by content ID, independent of any
// string table.
type canonForm struct {
	K  fbs.ValueKind `json:"k"`
	I  int64         `json:"i,omitempty"`
	O  int16         `json:"o,omitempty"`
	D  float64       `json:"d,omitempty"`
	S  string        `json:"s,omitempty"`
	U  string        `json:"u,omitempty"`
	Sc uint32        `json:"sc,omitempty"`
	ID string        `json:"id,omitempty"`
	It []canonForm   `json:"it,omitempty"`
	En []canonEntry  `json:"en,omitempty"`
	P  string        `json:"p,omitempty"`
	VT uint32        `json:"vt,omitempty"`
}

type canonEntry struct {
	K  string    `json:"k,omitempty"`
	ID uint32    `json:"id,omitempty"`
	V  canonForm `json:"v"`
}

// canonical returns the canonical bytes of a value.
func canonical(v *value) []byte {
	data, err := json.Marshal(canonOf(v))
	if err != nil {
		return nil // unreachable: the form holds finite numbers and strings
	}
	return data
}

func canonOf(v *value) canonForm {
	if v == nil {
		return canonForm{}
	}
	c := canonForm{K: v.kind, I: v.i, O: v.offset, D: v.d, S: v.s, Sc: v.scale, VT: v.valueType}
	if len(v.unscaled) > 0 {
		c.U = hex.EncodeToString(v.unscaled)
	}
	if v.uuid != ([16]byte{}) {
		c.ID = hex.EncodeToString(v.uuid[:])
	}
	if v.prog != nil {
		c.P = hex.EncodeToString(v.prog.Encode())
	}
	for _, it := range v.items {
		c.It = append(c.It, canonOf(it))
	}
	for _, e := range v.entries {
		c.En = append(c.En, canonEntry{K: e.key, ID: e.id, V: canonOf(e.value)})
	}
	return c
}

// valueEnc encodes values into one section, interning strings into strs.
type valueEnc struct {
	u    *unit
	o    *out
	b    *flatbuffers.Builder
	strs *interner
	// inStyle is set while encoding a style, whose nested value-type
	// objects stay inline.
	inStyle bool
}

// value writes a value and returns its offset; nil writes nothing.
func (e *valueEnc) value(v *value) flatbuffers.UOffsetT {
	if v == nil {
		return 0
	}
	if v.kind == fbs.ValueKindObject && v.valueType != 0 && !e.inStyle {
		id := e.u.styleID(e.o, v)
		return e.table(&value{kind: fbs.ValueKindStyle, i: int64(id)}) //nolint:gosec // G115: the ID's bits are stored as they are.
	}
	if v.kind == fbs.ValueKindAsset {
		e.o.assets[uuidString(v.uuid)] = true
	}
	if v.kind == fbs.ValueKindExpr {
		id := e.u.program(e.o, v.prog)
		return e.table(&value{kind: fbs.ValueKindExpr, i: int64(id)}) //nolint:gosec // G115: as above.
	}
	return e.table(v)
}

// table writes the Value table of v, children first.
func (e *valueEnc) table(v *value) flatbuffers.UOffsetT {
	b := e.b
	var unscaled, items, entries flatbuffers.UOffsetT
	if len(v.unscaled) > 0 {
		unscaled = b.CreateByteVector(v.unscaled)
	}
	if v.kind == fbs.ValueKindList {
		offs := make([]flatbuffers.UOffsetT, len(v.items))
		for i, it := range v.items {
			offs[i] = e.value(it)
		}
		items = offsetVector(b, offs)
	}
	if len(v.entries) > 0 {
		offs := make([]flatbuffers.UOffsetT, len(v.entries))
		for i, en := range v.entries {
			val := e.value(en.value)
			key := en.id
			if en.key != "" {
				key = e.strs.of(en.key)
			}
			fbs.EntryStart(b)
			fbs.EntryAddKey(b, key)
			fbs.EntryAddValue(b, val)
			offs[i] = fbs.EntryEnd(b)
		}
		entries = offsetVector(b, offs)
	}
	var s uint32
	if v.s != "" {
		s = e.strs.of(v.s)
	}
	fbs.ValueStart(b)
	fbs.ValueAddKind(b, v.kind)
	if v.i != 0 {
		fbs.ValueAddI(b, v.i)
	}
	if v.offset != 0 {
		fbs.ValueAddOffset(b, v.offset)
	}
	if v.d != 0 {
		fbs.ValueAddD(b, v.d)
	}
	if s != 0 {
		fbs.ValueAddS(b, s)
	}
	if unscaled != 0 {
		fbs.ValueAddUnscaled(b, unscaled)
	}
	if v.scale != 0 {
		fbs.ValueAddScale(b, v.scale)
	}
	if v.uuid != ([16]byte{}) {
		hi, lo := uuidHalves(v.uuid)
		fbs.ValueAddUuid(b, fbs.CreateUuid(b, hi, lo))
	}
	if items != 0 {
		fbs.ValueAddItems(b, items)
	}
	if entries != 0 {
		fbs.ValueAddEntries(b, entries)
	}
	return fbs.ValueEnd(b)
}

// uuidHalves splits a UUID into its big-endian halves.
func uuidHalves(id [16]byte) (hi, lo uint64) {
	return binary.BigEndian.Uint64(id[:8]), binary.BigEndian.Uint64(id[8:])
}

// offsetVector writes a vector of offsets.
func offsetVector(b *flatbuffers.Builder, offs []flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	b.StartVector(4, len(offs), 4)
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	return b.EndVector(len(offs))
}

// u32Vector writes a vector of uint32.
func u32Vector(b *flatbuffers.Builder, xs []uint32) flatbuffers.UOffsetT {
	b.StartVector(4, len(xs), 4)
	for i := len(xs) - 1; i >= 0; i-- {
		b.PrependUint32(xs[i])
	}
	return b.EndVector(len(xs))
}

// stringVector writes a vector of strings.
func stringVector(b *flatbuffers.Builder, xs []string) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(xs))
	for i, s := range xs {
		offs[i] = b.CreateString(s)
	}
	return offsetVector(b, offs)
}

// uuidVector writes a vector of Uuid structs.
func uuidVector(b *flatbuffers.Builder, ids [][16]byte) flatbuffers.UOffsetT {
	b.StartVector(16, len(ids), 8)
	for i := len(ids) - 1; i >= 0; i-- {
		hi, lo := uuidHalves(ids[i])
		fbs.CreateUuid(b, hi, lo)
	}
	return b.EndVector(len(ids))
}

// finish completes a section buffer and returns a copy of its bytes.
func finish(b *flatbuffers.Builder, root flatbuffers.UOffsetT, k bundle.SectionKind) []byte {
	bundle.Finish(b, root, k)
	return slices.Clone(b.FinishedBytes())
}

// params writes a vector of parameters.
func (e *valueEnc) params(ps []*param) flatbuffers.UOffsetT {
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(ps))
	for i, p := range ps {
		def := e.value(p.def)
		name, typ := e.strs.of(p.name), e.strs.of(p.typ)
		fbs.ParamStart(b)
		if p.id != ([16]byte{}) {
			hi, lo := uuidHalves(p.id)
			fbs.ParamAddId(b, fbs.CreateUuid(b, hi, lo))
		}
		fbs.ParamAddName(b, name)
		fbs.ParamAddType(b, typ)
		fbs.ParamAddRequired(b, p.required)
		if def != 0 {
			fbs.ParamAddDefault(b, def)
		}
		fbs.ParamAddSensitive(b, p.sensitive)
		offs[i] = fbs.ParamEnd(b)
	}
	return offsetVector(b, offs)
}

// state writes a vector of state entries.
func (e *valueEnc) state(entries []*stateEntry) flatbuffers.UOffsetT {
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(entries))
	for i, s := range entries {
		def := e.value(s.def)
		var computed uint64
		if s.computed != nil && s.computed.prog != nil {
			computed = e.u.program(e.o, s.computed.prog)
		}
		name, typ := e.strs.of(s.name), e.strs.of(s.typ)
		fbs.StateEntryStart(b)
		hi, lo := uuidHalves(s.id)
		fbs.StateEntryAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.StateEntryAddName(b, name)
		fbs.StateEntryAddType(b, typ)
		if def != 0 {
			fbs.StateEntryAddDefault(b, def)
		}
		if computed != 0 {
			fbs.StateEntryAddComputed(b, computed)
		}
		fbs.StateEntryAddPersistence(b, s.persistence)
		fbs.StateEntryAddSensitive(b, s.sensitive)
		fbs.StateEntryAddExposed(b, s.exposed)
		offs[i] = fbs.StateEntryEnd(b)
	}
	return offsetVector(b, offs)
}

// sources writes a vector of data sources.
func (e *valueEnc) sources(ds []*dataSource) flatbuffers.UOffsetT {
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(ds))
	for i, d := range ds {
		config := e.value(d.config)
		name, typ := e.strs.of(d.name), e.strs.of(d.typ)
		fbs.DataSourceStart(b)
		hi, lo := uuidHalves(d.id)
		fbs.DataSourceAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.DataSourceAddName(b, name)
		fbs.DataSourceAddKind(b, d.kind)
		fbs.DataSourceAddType(b, typ)
		if config != 0 {
			fbs.DataSourceAddConfig(b, config)
		}
		offs[i] = fbs.DataSourceEnd(b)
	}
	return offsetVector(b, offs)
}

// handlers writes a vector of handlers.
func handlers(b *flatbuffers.Builder, hs []*handler) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(hs))
	for i, h := range hs {
		offs[i] = handlerTable(b, h)
	}
	return offsetVector(b, offs)
}

// handlerTable writes one handler.
func handlerTable(b *flatbuffers.Builder, h *handler) flatbuffers.UOffsetT {
	fbs.HandlerStart(b)
	fbs.HandlerAddEvent(b, h.event)
	hi, lo := uuidHalves(h.graph.id)
	fbs.HandlerAddGraph(b, fbs.CreateUuid(b, hi, lo))
	fbs.HandlerAddConcurrency(b, h.concurrency)
	if h.interval != 0 {
		fbs.HandlerAddIntervalMs(b, h.interval)
	}
	fbs.HandlerAddDetached(b, h.detached)
	return fbs.HandlerEnd(b)
}
