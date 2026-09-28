// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package delta

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"pgregory.net/rapid"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const maxSize = 1 << 24

// genSections draws a set of sections with distinct (kind, ID).
func genSections(t *rapid.T, label string) []bundle.Section {
	n := rapid.IntRange(0, 12).Draw(t, label+".n")
	seen := map[[18]byte]bool{}
	var out []bundle.Section
	for range n {
		var s bundle.Section
		s.Kind = bundle.SectionKind(rapid.IntRange(1, 12).Draw(t, label+".kind")) //nolint:gosec // G115: 1–12.
		s.ID[0] = rapid.Byte().Draw(t, label+".id") % 4
		key := [18]byte{byte(s.Kind)} //nolint:gosec // G115: 1–12.
		copy(key[2:], s.ID[:])
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Data = rapid.SliceOfN(rapid.Byte(), 0, 512).Draw(t, label+".data")
		out = append(out, s)
	}
	return out
}

// mutate derives new sections from old ones: kept, grown, shrunk, edited,
// removed and added, so every operation is exercised.
func mutate(t *rapid.T, old []bundle.Section) []bundle.Section {
	var out []bundle.Section
	for _, s := range old {
		switch rapid.IntRange(0, 4).Draw(t, "change") {
		case 0: // removed
		case 1: // kept
			out = append(out, s)
		case 2: // edited in place
			d := bytes.Clone(s.Data)
			if len(d) > 0 {
				d[rapid.IntRange(0, len(d)-1).Draw(t, "at")] ^= 0x5a
			}
			out = append(out, bundle.Section{Kind: s.Kind, ID: s.ID, Data: d})
		case 3: // grown
			tail := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "tail")
			out = append(out, bundle.Section{Kind: s.Kind, ID: s.ID, Data: append(bytes.Clone(s.Data), tail...)})
		case 4: // shrunk
			out = append(out, bundle.Section{Kind: s.Kind, ID: s.ID, Data: s.Data[:len(s.Data)/2]})
		}
	}
	for _, s := range genSections(t, "added") {
		dup := false
		for _, o := range out {
			dup = dup || o.Kind == s.Kind && o.ID == s.ID
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

func encode(t interface{ Fatal(...any) }, sections []bundle.Section) []byte {
	b, err := bundle.Encode(bundle.KindPlugin, sections)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Verifies: REL-021, REL-025, QA-002.
// apply(delta(a, b), a) == b byte for byte for generated bundle pairs.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		oldSections := genSections(t, "old")
		a := encode(t, oldSections)
		b := encode(t, mutate(t, oldSections))
		d, err := Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Apply(a, d, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, b) {
			t.Fatal("the rebuilt bundle differs")
		}
		d2, err := Diff(a, b)
		if err != nil || !bytes.Equal(d, d2) {
			t.Fatal("Diff is not deterministic")
		}
	})
}

// Verifies: REL-021.
// Unchanged sections are referenced, and an edit to one section costs a
// small patch rather than the section.
func TestOperations(t *testing.T) {
	t.Parallel()
	page := make([]byte, 2800)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: test data.
	for i := range page {
		page[i] = byte(rng.Uint32() & 0xff)
	}
	edited := bytes.Clone(page)
	copy(edited[500:], "Yearly")
	fresh := []byte("a brand new section")
	var id1, id2 bundle.ID
	id2[0] = 1
	a := encode(t, []bundle.Section{{Kind: bundle.SectionPage, ID: id1, Data: page}, {Kind: bundle.SectionStrings, ID: id1, Data: []byte("kept")}})
	b := encode(t, []bundle.Section{{Kind: bundle.SectionPage, ID: id1, Data: edited}, {Kind: bundle.SectionStrings, ID: id1, Data: []byte("kept")}, {Kind: bundle.SectionPage, ID: id2, Data: fresh}})
	d, err := Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	var ops []byte
	rest := d[headerSize:]
	for len(rest) > 0 {
		ops = append(ops, rest[18])
		n := int(rest[28]) | int(rest[29])<<8
		rest = rest[instrSize+n:]
	}
	if !bytes.Equal(ops, []byte{opPatch, opWhole, opReuse}) {
		t.Errorf("operations %v", ops)
	}
	if len(d) > 400 {
		t.Errorf("the delta has %d bytes", len(d))
	}
	h, err := ReadHeader(d)
	if err != nil || h.Sections != 3 || h.Kind != bundle.KindPlugin {
		t.Errorf("header %+v, %v", h, err)
	}
}

// Verifies: REL-023.
func TestWorthwhile(t *testing.T) {
	t.Parallel()
	if !Worthwhile(60, 100) || Worthwhile(61, 100) {
		t.Error("the 60 % threshold is wrong")
	}
}

// Verifies: SEC-052.
// A delta applied to another base, or tampered with, is refused.
func TestRefusals(t *testing.T) {
	t.Parallel()
	var id bundle.ID
	a := encode(t, []bundle.Section{{Kind: bundle.SectionPage, ID: id, Data: []byte("one")}})
	b := encode(t, []bundle.Section{{Kind: bundle.SectionPage, ID: id, Data: []byte("two")}})
	c := encode(t, []bundle.Section{{Kind: bundle.SectionPage, ID: id, Data: []byte("three")}})
	d, err := Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(c, d, maxSize); codeOf(err) != plxerr.PatchHashMismatch {
		t.Errorf("another base: %v", err)
	}
	bad := bytes.Clone(d)
	bad[50] ^= 1 // the new bundle hash
	if _, err := Apply(a, bad, maxSize); codeOf(err) != plxerr.PatchHashMismatch {
		t.Errorf("tampered hash: %v", err)
	}
	if _, err := Apply(a, d, 1); codeOf(err) != plxerr.DeltaMalformed {
		t.Errorf("over the size limit: %v", err)
	}
	for _, x := range [][]byte{nil, []byte("PXDL"), append(bytes.Clone(d), 0), d[:len(d)-1]} {
		if _, err := Apply(a, x, maxSize); err == nil {
			t.Errorf("%d bytes accepted", len(x))
		}
	}
}

// FuzzApply: the applier never panics and never returns a bundle other
// than the one the delta names.
func FuzzApply(f *testing.F) {
	var id bundle.ID
	a := encode(f, []bundle.Section{{Kind: bundle.SectionPage, ID: id, Data: bytes.Repeat([]byte("abc"), 50)}})
	b := encode(f, []bundle.Section{{Kind: bundle.SectionPage, ID: id, Data: bytes.Repeat([]byte("abd"), 50)}, {Kind: bundle.SectionStrings, ID: id, Data: []byte("x")}})
	d, err := Diff(a, b)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(d)
	f.Fuzz(func(t *testing.T, d []byte) {
		out, err := Apply(a, d, 1<<16)
		if err != nil {
			return
		}
		got, err := bundle.ReadStructure(out)
		if err != nil {
			t.Fatal(err)
		}
		h, _ := ReadHeader(d)
		if got.Hash != h.New {
			t.Fatal("a bundle other than the named one was returned")
		}
	})
}

func codeOf(err error) plxerr.Code {
	c, _ := plxerr.CodeOf(err)
	return c
}
