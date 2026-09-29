// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package icons

import (
	"bytes"
	"encoding/binary"
	"os"
	"slices"
	"strings"
	"testing"
)

// glyphs returns the glyf bytes of every glyph of font.
func glyphs(t *testing.T, font []byte) [][]byte {
	t.Helper()
	tables, err := readTables(font)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	loca, err := readLoca(tables["loca"], n, binary.BigEndian.Uint16(tables["head"][50:]) == 1)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, n)
	for g := range n {
		out[g] = bytes.TrimRight(tables["glyf"][loca[g]:loca[g+1]], "\x00")
	}
	return out
}

// gvarData returns the variation data of every glyph of font.
func gvarData(t *testing.T, gvar []byte) [][]byte {
	t.Helper()
	count, long := int(binary.BigEndian.Uint16(gvar[12:])), binary.BigEndian.Uint16(gvar[14:])&1 != 0
	data := int(binary.BigEndian.Uint32(gvar[16:]))
	at := func(i int) int {
		if long {
			return int(binary.BigEndian.Uint32(gvar[20+4*i:]))
		}
		return int(binary.BigEndian.Uint16(gvar[20+2*i:])) * 2
	}
	out := make([][]byte, count)
	for g := range count {
		out[g] = bytes.TrimRight(gvar[data+at(g):data+at(g+1)], "\x00")
	}
	return out
}

// Verifies: THM-005, CMP-032.
// A subset of Material Symbols keeps the chosen glyphs, outlines and
// variations byte for byte at their glyph IDs, empties every other one,
// maps only the chosen code points, drops the layout tables, and is a
// well-formed font; the same input gives the same bytes.
func TestSubsetKeepsTheChosenGlyphs(t *testing.T) {
	t.Parallel()
	font, err := os.ReadFile("fonts/MaterialSymbolsOutlined.ttf")
	if err != nil {
		t.Fatal(err)
	}
	var runes []rune
	for _, name := range []string{"home", "arrow_back", "search", "10k"} {
		g, ok := Lookup(Material, name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		runes = append(runes, g.CodePoint)
	}
	sub, err := Subset(font, runes, map[string][]byte{"Plux": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) > 100_000 {
		t.Errorf("the subset is %d bytes", len(sub))
	}
	reversed := slices.Clone(runes)
	slices.Reverse(reversed)
	again, _ := Subset(font, append(reversed, runes[0]), map[string][]byte{"Plux": []byte("x")})
	if !bytes.Equal(sub, again) {
		t.Error("the subset depends on the order of runes")
	}
	if checksum(sub) != 0xB1B0AFBA {
		t.Error("head.checkSumAdjustment is wrong")
	}
	tables, err := readTables(sub)
	if err != nil {
		t.Fatal(err)
	}
	for tag, data := range tables {
		if !kept[tag] && tag != "Plux" {
			t.Errorf("kept table %s", tag)
		}
		if tag != "head" && checksumOf(t, sub, tag) != checksum(data) {
			t.Errorf("table %s has a wrong checksum", tag)
		}
	}
	origTables, _ := readTables(font)
	origLookup, _ := cmapLookup(origTables["cmap"])
	lookup, err := cmapLookup(tables["cmap"])
	if err != nil {
		t.Fatal(err)
	}
	orig, got := glyphs(t, font), glyphs(t, sub)
	origVar, gotVar := gvarData(t, origTables["gvar"]), gvarData(t, tables["gvar"])
	want := map[uint16]bool{0: true}
	for _, r := range runes {
		og, _ := origLookup(r)
		g, ok := lookup(r)
		if !ok || g != og {
			t.Errorf("U+%04X maps to %d, want %d", r, g, og)
		}
		want[g] = true
	}
	for g := range orig {
		keep := want[uint16(g)]
		if keep != (len(got[g]) > 0) || keep && !bytes.Equal(got[g], orig[g]) {
			t.Errorf("glyph %d: kept %t, %d bytes of %d", g, keep, len(got[g]), len(orig[g]))
		}
		if keep != (len(gotVar[g]) > 0) || keep && !bytes.Equal(gotVar[g], origVar[g]) {
			t.Errorf("glyph %d variations: kept %t", g, keep)
		}
	}
	if _, ok := lookup('a'); ok {
		t.Error("the ligature letters are still mapped")
	}
	if binary.BigEndian.Uint32(tables["post"]) != 0x00030000 || len(tables["post"]) != 32 {
		t.Error("post keeps glyph names")
	}
	if !bytes.Equal(tables["hmtx"], origTables["hmtx"]) || !bytes.Equal(tables["fvar"], origTables["fvar"]) {
		t.Error("hmtx or fvar changed")
	}
}

// checksumOf reads table tag's checksum from font's table directory.
func checksumOf(t *testing.T, font []byte, tag string) uint32 {
	t.Helper()
	for i := range int(binary.BigEndian.Uint16(font[4:])) {
		rec := font[12+16*i:]
		if string(rec[:4]) == tag {
			return binary.BigEndian.Uint32(rec[4:])
		}
	}
	t.Fatalf("no %s", tag)
	return 0
}

// Verifies: THM-005.
// Every name of both sets maps to a glyph its font has.
func TestEveryNameHasAGlyph(t *testing.T) {
	t.Parallel()
	for set, file := range map[Set]string{Material: "fonts/MaterialSymbolsOutlined.ttf", Cupertino: "fonts/CupertinoIcons.ttf"} {
		font, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tables, _ := readTables(font)
		lookup, err := cmapLookup(tables["cmap"])
		if err != nil {
			t.Fatal(err)
		}
		table := materialGlyphs
		if set == Cupertino {
			table = cupertinoGlyphs
		}
		for _, g := range table {
			if _, ok := lookup(g.CodePoint); !ok {
				t.Errorf("%s %s: U+%04X has no glyph", set, g.Name, g.CodePoint)
			}
		}
	}
	if g, ok := Lookup(Cupertino, "left_chevron"); !ok || !g.Mirrored {
		t.Errorf("left_chevron: %+v", g)
	}
	if g, ok := Lookup(Material, "arrow_back"); !ok || !g.Mirrored {
		t.Errorf("arrow_back: %+v", g)
	}
	if g, ok := Lookup(Material, "home"); !ok || g.Mirrored {
		t.Errorf("home: %+v", g)
	}
	for _, miss := range []struct {
		set  Set
		name string
	}{{Material, "no_such_icon"}, {Cupertino, "home_filled_x"}, {"fontawesome", "home"}} {
		if _, ok := Lookup(miss.set, miss.name); ok {
			t.Errorf("%s %s found", miss.set, miss.name)
		}
	}
}

// tinyFont is a TrueType font of five glyphs: 1 simple, 2 a composite of
// 3 and 4 (4 itself a composite of 3), mapped from 'a' and 'b' by a
// format 4 cmap.
func tinyFont() []byte {
	simple := []byte{0, 1, 0, 0, 0, 0, 0, 10, 0, 10, 0, 0, 0, 0, 1, 0, 0}
	composite := func(parts ...uint16) []byte {
		out := []byte{0xFF, 0xFF, 0, 0, 0, 0, 0, 10, 0, 10}
		for i, p := range parts {
			flags := uint16(0x0001 | 0x0008) // word args, a scale
			if i < len(parts)-1 {
				flags |= 0x0020
			}
			out = binary.BigEndian.AppendUint16(out, flags)
			out = binary.BigEndian.AppendUint16(out, p)
			out = append(out, 0, 0, 0, 0, 0x40, 0)
		}
		return out
	}
	glyfs := [][]byte{simple, simple, composite(3, 4), simple, composite(3)}
	var glyf, loca []byte
	for _, g := range glyfs {
		loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf))) //nolint:gosec // a tiny font
		glyf = pad(append(glyf, g...), 4)
	}
	loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf))) //nolint:gosec // a tiny font
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[50:], 1)
	maxp := []byte{0, 0, 0x50, 0, 0, 5}
	cmap := writeCmap([]runeGlyph{{'a', 1}, {'b', 2}})
	cmap = cmap[:20+binary.BigEndian.Uint16(cmap[22:])] // the format 4 subtable only
	binary.BigEndian.PutUint16(cmap[2:], 1)
	return writeFont(0x00010000, map[string][]byte{
		"head": head, "maxp": maxp, "cmap": cmap, "glyf": glyf, "loca": loca,
		"hhea": make([]byte, 36), "hmtx": make([]byte, 20), "post": make([]byte, 32), "GSUB": {1},
	})
}

// Verifies: THM-005.
// A composite glyph keeps the glyphs it is made of, transitively; a font
// with only a BMP cmap works too, and short loca offsets are written when
// they fit.
func TestSubsetFollowsComposites(t *testing.T) {
	t.Parallel()
	font := tinyFont()
	sub, err := Subset(font, []rune{'b'}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, orig := glyphs(t, sub), glyphs(t, font)
	for g, keep := range []bool{true, false, true, true, true} {
		if keep != (len(got[g]) > 0) || keep && !bytes.Equal(got[g], orig[g]) {
			t.Errorf("glyph %d: kept %t", g, keep)
		}
	}
	tables, _ := readTables(sub)
	if binary.BigEndian.Uint16(tables["head"][50:]) != 0 {
		t.Error("the loca is not short")
	}
	if _, err := Subset(font, []rune{'z'}, nil); err == nil || !strings.Contains(err.Error(), "U+007A") {
		t.Errorf("unmapped rune: %v", err)
	}
	if _, err := Subset(font, nil, map[string][]byte{"glyf": {}}); err == nil {
		t.Error("an extra table replaced glyf")
	}
}

// Verifies: THM-005.
// Damaged fonts are refused with an error, never a panic.
func TestSubsetRefusesDamagedFonts(t *testing.T) {
	t.Parallel()
	font := tinyFont()
	for name, f := range map[string][]byte{
		"empty":     nil,
		"directory": font[:20],
		"table":     font[:len(font)-40],
	} {
		if _, err := Subset(f, []rune{'a'}, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	tables, _ := readTables(font)
	delete(tables, "cmap")
	if _, err := Subset(writeFont(0x00010000, tables), nil, nil); err == nil {
		t.Error("no cmap: no error")
	}
}
