// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"errors"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/icons"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: THM-005.
// An icon names one of its set's glyphs, literally: an unknown name, one
// of the other set, or a computed name or icon is refused (PLX-1123).
func TestIconsAreCheckedAtCompileTime(t *testing.T) {
	t.Parallel()
	decoration := "/" + amountNode + "/props/decoration"
	for _, tc := range []invalidCase{
		{name: "unknown material icon", edit: setProp(amountNode, "decoration", `{"icon": {"name": "no_such_icon"}}`), ptr: decoration + "/icon/name"},
		{name: "icon of the other set", edit: setProp(amountNode, "decoration", `{"icon": {"name": "left_chevron", "set": "material"}}`), ptr: decoration + "/icon/name"},
		{name: "unknown cupertino icon", edit: setProp(amountNode, "decoration", `{"icon": {"name": "home_rounded", "set": "cupertino"}}`), ptr: decoration + "/icon/name"},
		{name: "computed name", edit: setProp(amountNode, "decoration", `{"icon": {"name": {"$expr": "'home'"}}}`), ptr: decoration + "/icon"},
		{name: "computed icon", edit: setProp(amountNode, "decoration", `{"icon": {"$expr": "page.amount"}}`), ptr: decoration + "/icon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := fixture(t)
			tc.edit(t, m)
			wantDiag(t, compileFS(m), plxerr.UnknownIcon, calculatorPage, tc.ptr)
		})
	}
}

// Verifies: THM-005, CMP-032.
// A bundle indexes one font per icon set its documents use, subset to
// their icons by the server's IconFont, under a reserved key; the files
// are returned with the bundles. Without IconFont, no font is indexed.
func TestIconFontsAreIndexed(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	setProp(amountNode, "decoration", `{"icon": {"name": "home"}, "suffixIcon": {"name": "left_chevron", "set": "cupertino"}, "prefixIcon": {"name": "search"}}`)(t, m)
	opts := DefaultOptions()
	asked := map[icons.Set][]string{}
	opts.IconFont = func(set icons.Set, names []string) ([]byte, error) {
		asked[set] = names
		return []byte("font of " + string(set)), nil
	}
	res := Compile(m, opts)
	if res.Diagnostics.HasErrors() {
		t.Fatal(list(res.Diagnostics))
	}
	if !slices.Equal(asked[icons.Material], []string{"home", "search"}) || !slices.Equal(asked[icons.Cupertino], []string{"left_chevron"}) {
		t.Errorf("asked for %v", asked)
	}
	if len(res.Files) != 2 || string(res.Files[sha256.Sum256([]byte("font of material"))]) != "font of material" {
		t.Errorf("files %v", res.Files)
	}
	keys := func(r *Result) map[string]string {
		out := map[string]string{}
		for _, b := range readAll(t, r)[1].Sections {
			if b.Kind != bundle.SectionAssetsIndex {
				continue
			}
			idx := fbs.GetRootAsAssetIndex(b.Data, 0)
			var a fbs.Asset
			for i := range idx.AssetsLength() {
				idx.Assets(&a, i)
				out[string(a.Key())] = string(a.MediaType())
			}
		}
		return out
	}
	got := keys(res)
	if got["@icons/material"] != "font/ttf" || got["@icons/cupertino"] != "font/ttf" {
		t.Errorf("the plugin's assets index lists %v", got)
	}
	if plain := keys(compileFS(m)); plain["@icons/material"] != "" {
		t.Error("an icon font without IconFont")
	}
	opts.IconFont = func(icons.Set, []string) ([]byte, error) { return nil, errors.New("broken") }
	wantDiag(t, Compile(m, opts), plxerr.InternalCompilerError, "", "")
}
