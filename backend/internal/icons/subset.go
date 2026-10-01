// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package icons

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"slices"
)

// kept are the tables a subset keeps: what TrueType outlines, their
// metrics and their font variations need. Layout tables (GSUB and the
// rest) are dropped: an icon is drawn by its code point, never shaped
// from its name.
var kept = map[string]bool{
	"head": true, "hhea": true, "maxp": true, "OS/2": true, "name": true,
	"post": true, "cmap": true, "glyf": true, "loca": true, "hmtx": true,
	"gvar": true, "fvar": true, "avar": true, "STAT": true, "HVAR": true,
	"gasp": true, "prep": true, "fpgm": true,
	"cvt ": true, //nolint:gocritic // the control value table's tag ends in a space
}

// Subset returns a TrueType font reduced to the glyphs of runes, and of
// the glyphs those are composed of, with the extra tables added (THM-005,
// ADR-0032 § Icons). Glyph IDs are kept: every other glyph is emptied in
// glyf and gvar, so hmtx, HVAR and the rest stay valid as they are. The
// cmap maps runes only, and post keeps no glyph names. The output depends
// on its inputs only.
func Subset(font []byte, runes []rune, extra map[string][]byte) ([]byte, error) {
	tables, err := readTables(font)
	if err != nil {
		return nil, err
	}
	for _, tag := range []string{"head", "maxp", "cmap", "loca", "glyf", "hhea", "hmtx", "post"} {
		if tables[tag] == nil {
			return nil, fmt.Errorf("icons: the font has no %s table", tag)
		}
	}
	if len(tables["head"]) < 54 || len(tables["maxp"]) < 6 || len(tables["post"]) < 32 {
		return nil, errors.New("icons: the font's head, maxp or post table is truncated")
	}
	numGlyphs := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	loca, err := readLoca(tables["loca"], numGlyphs, binary.BigEndian.Uint16(tables["head"][50:]) == 1)
	if err != nil {
		return nil, err
	}
	glyf := tables["glyf"]
	if int(loca[numGlyphs]) > len(glyf) {
		return nil, errors.New("icons: loca points past glyf")
	}
	mapping, keep, err := choose(tables["cmap"], glyf, loca, runes)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for tag, data := range tables {
		if kept[tag] {
			out[tag] = data
		}
	}
	head := slices.Clone(tables["head"])
	out["glyf"], out["loca"] = subsetGlyf(glyf, loca, keep, head)
	out["head"] = head
	if gvar := tables["gvar"]; gvar != nil {
		if out["gvar"], err = subsetGvar(gvar, numGlyphs, keep); err != nil {
			return nil, err
		}
	}
	out["cmap"] = writeCmap(mapping)
	post := slices.Clone(tables["post"][:32])
	binary.BigEndian.PutUint32(post, 0x00030000)
	out["post"] = post
	for tag, data := range extra {
		if len(tag) != 4 || out[tag] != nil {
			return nil, fmt.Errorf("icons: bad extra table %q", tag)
		}
		out[tag] = data
	}
	return writeFont(binary.BigEndian.Uint32(font), out), nil
}

// choose maps runes, sorted and without duplicates, to their glyphs, and
// returns the glyphs to keep: .notdef, theirs and their components.
func choose(cmap, glyf []byte, loca []uint32, runes []rune) ([]runeGlyph, map[uint16]bool, error) {
	lookup, err := cmapLookup(cmap)
	if err != nil {
		return nil, nil, err
	}
	runes = slices.Clone(runes)
	slices.Sort(runes)
	runes = slices.Compact(runes)
	mapping := make([]runeGlyph, 0, len(runes))
	keep := map[uint16]bool{}
	if err := closure(glyf, loca, 0, keep); err != nil { // .notdef
		return nil, nil, err
	}
	for _, r := range runes {
		g, ok := lookup(r)
		if !ok || int(g)+1 >= len(loca) {
			return nil, nil, fmt.Errorf("icons: the font has no glyph for U+%04X", r)
		}
		mapping = append(mapping, runeGlyph{r, g})
		if err := closure(glyf, loca, g, keep); err != nil {
			return nil, nil, err
		}
	}
	return mapping, keep, nil
}

// runeGlyph maps a character to its glyph.
type runeGlyph struct {
	r rune
	g uint16
}

// readTables returns the tables of an sfnt font by tag.
func readTables(font []byte) (map[string][]byte, error) {
	if len(font) < 12 {
		return nil, errors.New("icons: not a font")
	}
	n := int(binary.BigEndian.Uint16(font[4:]))
	if len(font) < 12+16*n {
		return nil, errors.New("icons: the table directory is truncated")
	}
	out := make(map[string][]byte, n)
	for i := range n {
		rec := font[12+16*i:]
		off, length := uint64(binary.BigEndian.Uint32(rec[8:])), uint64(binary.BigEndian.Uint32(rec[12:]))
		if off+length > uint64(len(font)) {
			return nil, fmt.Errorf("icons: table %q is outside the font", rec[:4])
		}
		out[string(rec[:4])] = font[off : off+length]
	}
	return out, nil
}

// readLoca returns numGlyphs+1 glyph offsets into glyf.
func readLoca(loca []byte, numGlyphs int, long bool) ([]uint32, error) {
	size := 2
	if long {
		size = 4
	}
	if len(loca) < (numGlyphs+1)*size {
		return nil, errors.New("icons: loca is truncated")
	}
	out := make([]uint32, numGlyphs+1)
	for i := range out {
		if long {
			out[i] = binary.BigEndian.Uint32(loca[4*i:])
		} else {
			out[i] = uint32(binary.BigEndian.Uint16(loca[2*i:])) * 2
		}
		if i > 0 && out[i] < out[i-1] {
			return nil, errors.New("icons: loca is not ascending")
		}
	}
	return out, nil
}

// closure marks g and, for a composite glyph, its components.
func closure(glyf []byte, loca []uint32, g uint16, keep map[uint16]bool) error {
	if keep[g] {
		return nil
	}
	keep[g] = true
	data := glyf[loca[g]:loca[g+1]]
	if len(data) < 10 || int16(binary.BigEndian.Uint16(data)) >= 0 { //nolint:gosec // numberOfContours is signed
		return nil
	}
	const (
		argsAreWords   = 0x0001
		haveScale      = 0x0008
		moreComponents = 0x0020
		haveXYScale    = 0x0040
		haveTwoByTwo   = 0x0080
	)
	for p := 10; ; {
		if p+4 > len(data) {
			return fmt.Errorf("icons: composite glyph %d is truncated", g)
		}
		flags, comp := binary.BigEndian.Uint16(data[p:]), binary.BigEndian.Uint16(data[p+2:])
		if int(comp)+1 >= len(loca) {
			return fmt.Errorf("icons: glyph %d uses a glyph the font does not have", g)
		}
		if err := closure(glyf, loca, comp, keep); err != nil {
			return err
		}
		p += 4 + 2
		if flags&argsAreWords != 0 {
			p += 2
		}
		switch {
		case flags&haveScale != 0:
			p += 2
		case flags&haveXYScale != 0:
			p += 4
		case flags&haveTwoByTwo != 0:
			p += 8
		}
		if flags&moreComponents == 0 {
			return nil
		}
	}
}

// subsetGlyf copies the kept glyphs, each padded to four bytes, empties
// the rest, and returns the new glyf and loca; head's indexToLocFormat is
// set to the loca format written, the short one when it fits.
func subsetGlyf(glyf []byte, loca []uint32, keep map[uint16]bool, head []byte) (newGlyf, newLoca []byte) {
	offsets := make([]uint32, len(loca))
	for g := range len(loca) - 1 {
		offsets[g] = uint32(len(newGlyf)) //nolint:gosec // bounded by the input glyf
		if keep[uint16(g)] {              //nolint:gosec // numGlyphs fits uint16
			newGlyf = append(newGlyf, glyf[loca[g]:loca[g+1]]...)
			newGlyf = pad(newGlyf, 4)
		}
	}
	offsets[len(loca)-1] = uint32(len(newGlyf)) //nolint:gosec // bounded by the input glyf
	short := len(newGlyf) <= 0x1FFFE
	for _, o := range offsets {
		if short {
			newLoca = binary.BigEndian.AppendUint16(newLoca, uint16(o/2)) //nolint:gosec // checked to fit
		} else {
			newLoca = binary.BigEndian.AppendUint32(newLoca, o)
		}
	}
	format := uint16(1)
	if short {
		format = 0
	}
	binary.BigEndian.PutUint16(head[50:], format)
	return newGlyf, newLoca
}

// subsetGvar keeps the variation data of the kept glyphs only.
func subsetGvar(gvar []byte, numGlyphs int, keep map[uint16]bool) ([]byte, error) {
	if len(gvar) < 20 {
		return nil, errors.New("icons: gvar is truncated")
	}
	axes, sharedCount := int(binary.BigEndian.Uint16(gvar[4:])), int(binary.BigEndian.Uint16(gvar[6:]))
	sharedOff := int(binary.BigEndian.Uint32(gvar[8:]))
	count, flags := int(binary.BigEndian.Uint16(gvar[12:])), binary.BigEndian.Uint16(gvar[14:])
	dataOff := int(binary.BigEndian.Uint32(gvar[16:]))
	if count != numGlyphs {
		return nil, errors.New("icons: gvar and maxp disagree on the number of glyphs")
	}
	offsets, err := gvarOffsets(gvar, count, flags&1 != 0)
	if err != nil {
		return nil, err
	}
	sharedLen := axes * 2 * sharedCount
	if sharedOff+sharedLen > len(gvar) || dataOff+offsets[count] > len(gvar) {
		return nil, errors.New("icons: gvar data is outside the table")
	}
	var data []byte
	newOffsets := make([]int, count+1)
	for g := range count {
		newOffsets[g] = len(data)
		if keep[uint16(g)] && offsets[g+1] > offsets[g] { //nolint:gosec // numGlyphs fits uint16
			data = append(data, gvar[dataOff+offsets[g]:dataOff+offsets[g+1]]...)
			data = pad(data, 2)
		}
	}
	newOffsets[count] = len(data)
	short := len(data) <= 0x1FFFE
	entry := 4
	if short {
		entry = 2
	}
	newSharedOff := 20 + entry*(count+1)
	newDataOff := newSharedOff + sharedLen
	out := make([]byte, 20, newDataOff+len(data))
	copy(out, gvar[:12])
	binary.BigEndian.PutUint32(out[8:], uint32(newSharedOff)) //nolint:gosec // small
	binary.BigEndian.PutUint16(out[12:], uint16(count))       //nolint:gosec // numGlyphs fits uint16
	binary.BigEndian.PutUint16(out[14:], boolFlag(!short))
	binary.BigEndian.PutUint32(out[16:], uint32(newDataOff)) //nolint:gosec // small
	for _, o := range newOffsets {
		if short {
			out = binary.BigEndian.AppendUint16(out, uint16(o/2)) //nolint:gosec // checked to fit
		} else {
			out = binary.BigEndian.AppendUint32(out, uint32(o)) //nolint:gosec // bounded by the input gvar
		}
	}
	out = append(out, gvar[sharedOff:sharedOff+sharedLen]...)
	return append(out, data...), nil
}

// gvarOffsets reads the count+1 offsets of gvar's variation data.
func gvarOffsets(gvar []byte, count int, long bool) ([]int, error) {
	size := 2
	if long {
		size = 4
	}
	if 20+size*(count+1) > len(gvar) {
		return nil, errors.New("icons: gvar offsets are truncated")
	}
	offsets := make([]int, count+1)
	for i := range offsets {
		if long {
			offsets[i] = int(binary.BigEndian.Uint32(gvar[20+4*i:]))
		} else {
			offsets[i] = int(binary.BigEndian.Uint16(gvar[20+2*i:])) * 2
		}
	}
	return offsets, nil
}

// boolFlag is 1 for true.
func boolFlag(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}

// cmapLookup returns a lookup over the font's best Unicode cmap: a full
// repertoire subtable (format 12) if it has one, else a BMP one (format 4).
func cmapLookup(cmap []byte) (func(rune) (uint16, bool), error) {
	if len(cmap) < 4 {
		return nil, errors.New("icons: cmap is truncated")
	}
	n := int(binary.BigEndian.Uint16(cmap[2:]))
	var f4, f12 []byte
	for i := range n {
		rec := 4 + 8*i
		if rec+8 > len(cmap) {
			return nil, errors.New("icons: cmap is truncated")
		}
		platform, encoding := binary.BigEndian.Uint16(cmap[rec:]), binary.BigEndian.Uint16(cmap[rec+2:])
		off := int(binary.BigEndian.Uint32(cmap[rec+4:]))
		unicode := platform == 0 || platform == 3 && (encoding == 1 || encoding == 10)
		if off+2 > len(cmap) || !unicode {
			continue
		}
		switch binary.BigEndian.Uint16(cmap[off:]) {
		case 4:
			f4 = cmap[off:]
		case 12:
			f12 = cmap[off:]
		}
	}
	switch {
	case f12 != nil:
		return format12(f12)
	case f4 != nil:
		return format4(f4)
	}
	return nil, errors.New("icons: the font has no Unicode cmap of format 4 or 12")
}

func format12(t []byte) (func(rune) (uint16, bool), error) {
	if len(t) < 16 {
		return nil, errors.New("icons: cmap format 12 is truncated")
	}
	groups := int(binary.BigEndian.Uint32(t[12:]))
	if len(t) < 16+12*groups {
		return nil, errors.New("icons: cmap format 12 is truncated")
	}
	return func(r rune) (uint16, bool) {
		for i := range groups {
			g := t[16+12*i:]
			start, end := binary.BigEndian.Uint32(g), binary.BigEndian.Uint32(g[4:])
			if uint32(r) >= start && uint32(r) <= end { //nolint:gosec // code points are non-negative
				return uint16(binary.BigEndian.Uint32(g[8:]) + uint32(r) - start), true //nolint:gosec // glyph IDs fit uint16
			}
		}
		return 0, false
	}, nil
}

func format4(t []byte) (func(rune) (uint16, bool), error) {
	if len(t) < 14 {
		return nil, errors.New("icons: cmap format 4 is truncated")
	}
	seg := int(binary.BigEndian.Uint16(t[6:])) / 2
	ends, starts, deltas, ranges := 14, 16+2*seg, 16+4*seg, 16+6*seg
	if len(t) < 16+8*seg {
		return nil, errors.New("icons: cmap format 4 is truncated")
	}
	return func(r rune) (uint16, bool) {
		if r > 0xFFFF {
			return 0, false
		}
		c := uint16(r) //nolint:gosec // r is in the BMP
		for i := range seg {
			end, start := binary.BigEndian.Uint16(t[ends+2*i:]), binary.BigEndian.Uint16(t[starts+2*i:])
			if c < start || c > end {
				continue
			}
			delta, ro := binary.BigEndian.Uint16(t[deltas+2*i:]), int(binary.BigEndian.Uint16(t[ranges+2*i:]))
			if ro == 0 {
				return c + delta, c+delta != 0
			}
			at := ranges + 2*i + ro + 2*int(c-start)
			if at+2 > len(t) {
				return 0, false
			}
			g := binary.BigEndian.Uint16(t[at:])
			if g == 0 {
				return 0, false
			}
			return g + delta, true
		}
		return 0, false
	}, nil
}

// writeCmap writes a cmap of a BMP subtable (3,1, format 4) and a full
// repertoire subtable (3,10, format 12) mapping exactly m, sorted by rune.
func writeCmap(m []runeGlyph) []byte {
	var f4 []byte
	var ends, starts, deltas []uint16
	for _, x := range m {
		if x.r <= 0xFFFF {
			c := uint16(x.r) //nolint:gosec // x.r is in the BMP
			ends, starts = append(ends, c), append(starts, c)
			deltas = append(deltas, x.g-c)
		}
	}
	ends, starts, deltas = append(ends, 0xFFFF), append(starts, 0xFFFF), append(deltas, 1)
	seg := len(ends)
	searchRange := 2 << (bits.Len(uint(seg)) - 1)
	f4 = binary.BigEndian.AppendUint16(f4, 4)
	f4 = binary.BigEndian.AppendUint16(f4, uint16(16+8*seg))              //nolint:gosec // few icons
	f4 = binary.BigEndian.AppendUint16(f4, 0)                             // language
	f4 = binary.BigEndian.AppendUint16(f4, uint16(2*seg))                 //nolint:gosec // few icons
	f4 = binary.BigEndian.AppendUint16(f4, uint16(searchRange))           //nolint:gosec // few icons
	f4 = binary.BigEndian.AppendUint16(f4, uint16(bits.Len(uint(seg))-1)) //nolint:gosec // few icons
	f4 = binary.BigEndian.AppendUint16(f4, uint16(2*seg-searchRange))     //nolint:gosec // few icons
	for _, list := range [][]uint16{ends, {0}, starts, deltas, make([]uint16, seg)} {
		for _, v := range list {
			f4 = binary.BigEndian.AppendUint16(f4, v)
		}
	}
	var groups [][3]uint32
	for _, x := range m {
		r, g := uint32(x.r), uint32(x.g) //nolint:gosec // code points are non-negative
		if n := len(groups); n > 0 && groups[n-1][1]+1 == r && groups[n-1][2]+r-groups[n-1][0] == g {
			groups[n-1][1] = r
			continue
		}
		groups = append(groups, [3]uint32{r, r, g})
	}
	var f12 []byte
	f12 = binary.BigEndian.AppendUint16(f12, 12)
	f12 = binary.BigEndian.AppendUint16(f12, 0)
	f12 = binary.BigEndian.AppendUint32(f12, uint32(16+12*len(groups))) //nolint:gosec // few icons
	f12 = binary.BigEndian.AppendUint32(f12, 0)                         // language
	f12 = binary.BigEndian.AppendUint32(f12, uint32(len(groups)))       //nolint:gosec // few icons
	for _, gr := range groups {
		f12 = binary.BigEndian.AppendUint32(f12, gr[0])
		f12 = binary.BigEndian.AppendUint32(f12, gr[1])
		f12 = binary.BigEndian.AppendUint32(f12, gr[2])
	}
	out := []byte{0, 0, 0, 2}
	out = append(out, 0, 3, 0, 1)
	out = binary.BigEndian.AppendUint32(out, 20)
	out = append(out, 0, 3, 0, 10)
	out = binary.BigEndian.AppendUint32(out, uint32(20+len(f4))) //nolint:gosec // few icons
	return append(append(out, f4...), f12...)
}

// writeFont writes an sfnt font of tables, in tag order, with every
// checksum and head's checkSumAdjustment set.
func writeFont(version uint32, tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	n := len(tags)
	searchRange := 16 << (bits.Len(uint(n)) - 1)
	out := binary.BigEndian.AppendUint32(nil, version)
	out = binary.BigEndian.AppendUint16(out, uint16(n))                   //nolint:gosec // few tables
	out = binary.BigEndian.AppendUint16(out, uint16(searchRange))         //nolint:gosec // few tables
	out = binary.BigEndian.AppendUint16(out, uint16(bits.Len(uint(n))-1)) //nolint:gosec // few tables
	out = binary.BigEndian.AppendUint16(out, uint16(16*n-searchRange))    //nolint:gosec // few tables
	dir := len(out)
	out = append(out, make([]byte, 16*n)...)
	headAt := 0
	for i, tag := range tags {
		data := tables[tag]
		if tag == "head" {
			data = slices.Clone(data)
			binary.BigEndian.PutUint32(data[8:], 0)
			headAt = len(out)
		}
		rec := out[dir+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], checksum(data))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(out)))   //nolint:gosec // a font is far below 4 GiB
		binary.BigEndian.PutUint32(rec[12:], uint32(len(data))) //nolint:gosec // a font is far below 4 GiB
		out = pad(append(out, data...), 4)
	}
	binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-checksum(out))
	return out
}

// checksum is the sfnt checksum: the sum of big-endian uint32s, the last
// one zero-padded.
func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}

// pad zero-pads b to a multiple of n bytes.
func pad(b []byte, n int) []byte {
	for len(b)%n != 0 {
		b = append(b, 0)
	}
	return b
}
