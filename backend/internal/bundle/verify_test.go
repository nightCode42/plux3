// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// wasmSection builds a wasm section.
func wasmSection() []byte {
	b := flatbuffers.NewBuilder(0)
	mod := b.CreateByteVector([]byte("\x00asm\x01\x00\x00\x00"))
	fbs.WasmModuleStart(b)
	fbs.WasmModuleAddModule(b, mod)
	Finish(b, fbs.WasmModuleEnd(b), SectionWasm)
	return b.FinishedBytes()
}

// walkValue reads every field of a value, as a runtime would.
func walkValue(v *fbs.Value, depth int) {
	if v == nil || depth > 100 {
		return
	}
	_, _, _, _ = v.Kind(), v.I(), v.D(), v.S()
	_ = v.UnscaledBytes()
	v.Uuid(nil)
	var item fbs.Value
	for i := range v.ItemsLength() {
		if v.Items(&item, i) {
			walkValue(&item, depth+1)
		}
	}
	var e fbs.Entry
	for i := range v.EntriesLength() {
		if v.Entries(&e, i) {
			_ = e.Key()
			walkValue(e.Value(nil), depth+1)
		}
	}
}

// walkPage reads every field of a page section the tests build.
func walkPage(buf []byte) {
	p := fbs.GetRootAsPage(buf, 0)
	p.Id(nil)
	_, _ = p.Key(), p.Kind()
	walkValue(p.Title(nil), 0)
	for i := range p.StringsLength() {
		_ = p.Strings(i)
	}
	var n fbs.Node
	for i := range p.NodesLength() {
		if !p.Nodes(&n, i) {
			continue
		}
		n.Id(nil)
		_, _ = n.Widget(), n.TestId()
		var prop fbs.Prop
		for j := range n.PropsLength() {
			if n.Props(&prop, j) {
				walkValue(prop.Value(nil), 0)
			}
		}
		var h fbs.Handler
		for j := range n.HandlersLength() {
			if n.Handlers(&h, j) {
				h.Graph(nil)
			}
		}
		var s fbs.SlotFill
		for j := range n.SlotsLength() {
			if n.Slots(&s, j) {
				for k := range s.NodesLength() {
					_ = s.Nodes(k)
				}
			}
		}
		var o fbs.Override
		for j := range n.OverridesLength() {
			if n.Overrides(&o, j) {
				for k := range o.PropsLength() {
					if o.Props(&prop, k) {
						walkValue(prop.Value(nil), 0)
					}
				}
			}
		}
		for j := range n.ChildrenLength() {
			_ = n.Children(j)
		}
	}
}

// Verifies: BND-006, QA-004.
func TestVerifierAcceptsWellFormedSections(t *testing.T) {
	t.Parallel()
	lim := limits.Defaults()
	for kind, data := range map[SectionKind][]byte{
		SectionMeta: metaSection(fbs.BundleKindPlugin, "pxl.v1"), SectionPage: pageSection(),
		SectionStrings: stringsSection("", "x"), SectionWasm: wasmSection(),
	} {
		if err := verify(kind, data, lim); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	// Every section kind has a root layout.
	for kind := range sectionInfo {
		if _, ok := rootLayouts[sectionInfo[kind].ident]; !ok {
			t.Errorf("no layout for %s", kind)
		}
	}
}

// Verifies: BND-006, QA-004.
func TestVerifierRejectsMalformedBuffers(t *testing.T) {
	t.Parallel()
	lim := limits.Defaults()
	page := pageSection()
	edit := func(f func(d []byte)) []byte { d := bytes.Clone(page); f(d); return d }
	root := int(binary.LittleEndian.Uint32(page))
	tests := map[string][]byte{
		"short":            page[:7],
		"identifier":       edit(func(d []byte) { d[4] = 'X' }),
		"root offset":      edit(func(d []byte) { copy(d, le32(len(d))) }),
		"misaligned root":  edit(func(d []byte) { copy(d, le32(root+1)) }),
		"vtable offset":    edit(func(d []byte) { binary.LittleEndian.PutUint32(d[root:], 0x7fffffff) }),
		"truncated buffer": page[:len(page)-8],
	}
	for name, data := range tests {
		if err := verify(SectionPage, data, lim); !errors.Is(err, errVerify) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A string that is not UTF-8, found by its content.
	bad := edit(func(d []byte) {
		i := bytes.Index(d, []byte("Loan"))
		d[i] = 0xff
	})
	if err := verify(SectionPage, bad, lim); !errors.Is(err, errVerify) {
		t.Errorf("invalid UTF-8: %v", err)
	}
	// Depth and count limits.
	tight, err := lim.Tighten(limits.BundleVerifierDepth, limits.ScopeInstallation, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(SectionPage, page, tight); !errors.Is(err, errVerify) {
		t.Errorf("depth limit: %v", err)
	}
	if tight, err = lim.Tighten(limits.BundleVerifierTables, limits.ScopeInstallation, 5); err != nil {
		t.Fatal(err)
	}
	if err := verify(SectionPage, page, tight); !errors.Is(err, errVerify) {
		t.Errorf("table limit: %v", err)
	}
}

// Verifies: BND-006, QA-004.
// Every buffer the verifier accepts can be read with the generated
// accessors without a panic: 20,000 random mutations of a page section.
func TestVerifiedMutationsAreSafeToRead(t *testing.T) {
	t.Parallel()
	lim := limits.Defaults()
	page := pageSection()
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a reproducible sequence, not a secret.
	accepted := 0
	for range 20000 {
		d := bytes.Clone(page)
		for range 1 + r.IntN(3) {
			d[r.IntN(len(d))] = byte(r.Uint32()) //nolint:gosec // G115: truncation to a random byte is intended.
		}
		if verify(SectionPage, d, lim) != nil {
			continue
		}
		accepted++
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("accepted buffer panics when read: %v\n% x", p, d)
				}
			}()
			walkPage(d)
		}()
	}
	if accepted == 0 {
		t.Error("no mutation was accepted; the test proves nothing")
	}
}

// FuzzVerifyPage checks that an accepted page section is safe to read.
func FuzzVerifyPage(f *testing.F) {
	f.Add(pageSection())
	f.Add(metaSection(fbs.BundleKindPlugin, "pxl.v1"))
	lim := limits.Defaults()
	f.Fuzz(func(t *testing.T, data []byte) {
		if verify(SectionPage, data, lim) != nil {
			return
		}
		walkPage(data)
	})
}

// fixHashes recomputes the section hashes and the header hash of a
// container that fits, so mutated inputs reach the checks behind them.
func fixHashes(data []byte) {
	if len(data) < headerSize {
		return
	}
	n := int(binary.LittleEndian.Uint32(data[12:]))
	if n > (len(data)-headerSize)/entrySize {
		return
	}
	for i := range n {
		e := data[headerSize+entrySize*i:]
		off, length := binary.LittleEndian.Uint64(e[20:]), binary.LittleEndian.Uint64(e[28:])
		if off <= uint64(len(data)) && length <= uint64(len(data))-off {
			sum := sha256.Sum256(data[off : off+length])
			copy(e[36:68], sum[:])
		}
	}
	h := headerHash(data, n)
	copy(data[16:headerSize], h[:])
}

// FuzzRead checks that Read never panics; hashes are repaired first.
func FuzzRead(f *testing.F) {
	good, err := Encode(KindPlugin, sampleSections())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add(good[:100])
	opts := defaultOptions()
	f.Fuzz(func(t *testing.T, data []byte) {
		fixHashes(data)
		b, err := Read(data, opts)
		if err == nil && b.Meta == nil {
			t.Fatal("read without meta")
		}
	})
}
