// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package fonts

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/icons"
)

// table returns table tag of font.
func table(t *testing.T, font []byte, tag string) []byte {
	t.Helper()
	for i := range int(binary.BigEndian.Uint16(font[4:])) {
		rec := font[12+16*i:]
		if string(rec[:4]) == tag {
			off, n := binary.BigEndian.Uint32(rec[8:]), binary.BigEndian.Uint32(rec[12:])
			return font[off : off+n]
		}
	}
	t.Fatalf("no %s table", tag)
	return nil
}

// Verifies: THM-005.
// An icon font carries the names of its icons, sorted and once each, with
// their code points and whether they mirror; unknown names and sets are
// refused.
func TestBuildNamesTheIcons(t *testing.T) {
	t.Parallel()
	font, err := Build(icons.Cupertino, []string{"left_chevron", "add", "left_chevron"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(table(t, font, NamesTable)); got != "add f489 0\nleft_chevron f3d2 1\n" {
		t.Errorf("names %q", got)
	}
	again, _ := Build(icons.Cupertino, []string{"add", "left_chevron"})
	if !bytes.Equal(font, again) {
		t.Error("the font depends on the order of names")
	}
	if _, err := Build(icons.Material, []string{"home"}); err != nil {
		t.Error(err)
	}
	if _, err := Build(icons.Material, []string{"left_chevron"}); err == nil {
		t.Error("a Cupertino name built a Material font")
	}
	if _, err := Build("fontawesome", nil); err == nil {
		t.Error("an unknown set built a font")
	}
}
