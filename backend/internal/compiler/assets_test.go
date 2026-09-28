// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Verifies: CMP-030.
// The asset index lists each image's transcoded variants, sorted, from
// what the asset pipeline supplies; the bundle reads back.
func TestAssetVariantsAreIndexed(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	asked := 0
	opts.AssetVariants = func(sum [sha256.Size]byte) []AssetVariant {
		asked++
		return []AssetVariant{
			{MediaType: "image/webp", Density: 3, Width: 90, Height: 60, Hash: sha256.Sum256([]byte("w3")), Size: 30},
			{MediaType: "image/avif", Density: 1, Width: 30, Height: 20, Hash: sha256.Sum256([]byte("a1")), Size: 10},
			{MediaType: "image/webp", Density: 1, Width: 30, Height: 20, Hash: sha256.Sum256([]byte("w1")), Size: 11},
		}
	}
	res := Compile(fixture(t), opts)
	if res.Diagnostics.HasErrors() {
		t.Fatal(list(res.Diagnostics))
	}
	if asked == 0 {
		t.Fatal("the variants were never asked for")
	}
	read := readAll(t, res)
	var found bool
	for _, b := range read {
		for _, s := range b.Sections {
			if s.Kind != bundle.SectionAssetsIndex {
				continue
			}
			idx := fbs.GetRootAsAssetIndex(s.Data, 0)
			var a fbs.Asset
			if !idx.Assets(&a, 0) {
				t.Fatal("an empty asset index")
			}
			if a.VariantsLength() != 3 {
				t.Fatalf("%d variants", a.VariantsLength())
			}
			var v fbs.AssetVariant
			want := []struct {
				media   string
				density uint32
			}{{"image/avif", 1}, {"image/webp", 1}, {"image/webp", 3}}
			for i, w := range want {
				a.Variants(&v, i)
				if string(v.MediaType()) != w.media || v.Density() != w.density {
					t.Errorf("variant %d = %s %d×", i, v.MediaType(), v.Density())
				}
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no asset index")
	}
}

// Verifies: AST-003.
// An asset file above asset.fileSize, and a plugin whose assets exceed
// plugin.assetBytes, fail the compilation with a clear diagnostic.
func TestAssetSizeLimits(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key  limits.Key
		file string
	}{
		{limits.AssetFileSize, "assets/index.json"},
		{limits.PluginAssetBytes, "plugins/loans/plugin.json"},
	} {
		opts := DefaultOptions()
		set, err := opts.Limits.Tighten(c.key, limits.ScopeInstallation, 10)
		if err != nil {
			t.Fatal(err)
		}
		opts.Limits = set
		res := Compile(fixture(t), opts)
		found := false
		for _, d := range res.Diagnostics {
			if d.Code == plxerr.LimitExceeded && d.File == c.file && d.Severity == plxerr.SeverityError {
				found = true
			}
		}
		if !found || res.App != nil {
			t.Errorf("%s: %s", c.key, list(res.Diagnostics))
		}
	}
}
