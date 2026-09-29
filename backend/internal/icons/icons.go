// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package icons names the icons of the two built-in sets, Material
// Symbols and Cupertino icons (THM-005, ADR-0032 § Icons), and subsets
// their fonts to the glyphs a bundle uses. The name tables are generated
// from the fonts' name lists in fonts/; the fonts themselves are embedded
// by package fonts, which only the server imports.
package icons

//go:generate go run ./internal/gennames -dir fonts -out names_gen.go

import "sort"

// Set is an icon set, as the IconSet enum names it.
type Set string

// The icon sets.
const (
	Material  Set = "material"
	Cupertino Set = "cupertino"
)

// Sets lists the icon sets in a fixed order.
func Sets() []Set { return []Set{Material, Cupertino} }

// Glyph is an icon of a set.
type Glyph struct {
	// Name is the icon's name, e.g. "arrow_back".
	Name string
	// CodePoint is the character its set's font draws it for.
	CodePoint rune
	// Mirrored is true for an icon drawn mirrored in right-to-left text,
	// as Flutter's own icon tables mark it (matchTextDirection).
	Mirrored bool
}

// Lookup returns the icon name of set; ok is false when the set has none.
func Lookup(set Set, name string) (g Glyph, ok bool) {
	var table []Glyph
	switch set {
	case Material:
		table = materialGlyphs
	case Cupertino:
		table = cupertinoGlyphs
	default:
		return Glyph{}, false
	}
	i := sort.Search(len(table), func(i int) bool { return table[i].Name >= name })
	if i < len(table) && table[i].Name == name {
		return table[i], true
	}
	return Glyph{}, false
}
