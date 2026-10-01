// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package fonts embeds the fonts of the built-in icon sets and builds the
// icon font a bundle carries (THM-005, ADR-0032 § Icons). Only the server
// imports it: the fonts add about 11 MB to a binary. import.sh imports
// them at the versions icons.lock pins.
package fonts

import (
	_ "embed" // the fonts
	"fmt"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/icons"
)

var (
	//go:embed MaterialSymbolsOutlined.ttf
	material []byte
	//go:embed CupertinoIcons.ttf
	cupertino []byte
)

// NamesTable is the tag of the table an icon font carries its names in,
// for the runtime to draw icons by name: one "<name> <hex code point>
// <mirrored: 0 or 1>" line per icon, sorted by name. Font engines ignore
// tables they do not know.
const NamesTable = "Plux"

// Build returns the font of set subset to the named icons, carrying their
// names; every name must be one of set's (icons.Lookup).
func Build(set icons.Set, names []string) ([]byte, error) {
	var font []byte
	switch set {
	case icons.Material:
		font = material
	case icons.Cupertino:
		font = cupertino
	default:
		return nil, fmt.Errorf("fonts: unknown icon set %q", set)
	}
	names = slices.Clone(names)
	slices.Sort(names)
	names = slices.Compact(names)
	var table strings.Builder
	runes := make([]rune, 0, len(names))
	for _, name := range names {
		g, ok := icons.Lookup(set, name)
		if !ok {
			return nil, fmt.Errorf("fonts: %s has no icon %q", set, name)
		}
		mirrored := 0
		if g.Mirrored {
			mirrored = 1
		}
		fmt.Fprintf(&table, "%s %x %d\n", g.Name, g.CodePoint, mirrored)
		runes = append(runes, g.CodePoint)
	}
	return icons.Subset(font, runes, map[string][]byte{NamesTable: []byte(table.String())}) //nolint:wrapcheck // icons' errors name the font
}
