// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
)

// testAsset is an asset of a hand-built index: its media type and file,
// and its variants as (media type, file) pairs.
type testAsset struct {
	mediaType, file string
	variants        [][2]string
}

// digest is the hex SHA-256 of a file's name, standing for its hash.
func digest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// assetBundle is an app bundle whose assets-index holds assets.
func assetBundle(t *testing.T, assets []testAsset) []byte {
	t.Helper()
	b := flatbuffers.NewBuilder(0)
	hash := func(name string) flatbuffers.UOffsetT {
		sum := sha256.Sum256([]byte(name))
		return b.CreateByteVector(sum[:])
	}
	offs := make([]flatbuffers.UOffsetT, len(assets))
	for i, a := range assets {
		vs := make([]flatbuffers.UOffsetT, len(a.variants))
		for j, v := range a.variants {
			mt, h := b.CreateString(v[0]), hash(v[1])
			fbs.AssetVariantStart(b)
			fbs.AssetVariantAddMediaType(b, mt)
			fbs.AssetVariantAddHash(b, h)
			vs[j] = fbs.AssetVariantEnd(b)
		}
		fbs.AssetStartVariantsVector(b, len(vs))
		for j := len(vs) - 1; j >= 0; j-- {
			b.PrependUOffsetT(vs[j])
		}
		variants := b.EndVector(len(vs))
		mt, h := b.CreateString(a.mediaType), hash(a.file)
		fbs.AssetStart(b)
		fbs.AssetAddMediaType(b, mt)
		fbs.AssetAddHash(b, h)
		fbs.AssetAddVariants(b, variants)
		offs[i] = fbs.AssetEnd(b)
	}
	fbs.AssetIndexStartAssetsVector(b, len(offs))
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	list := b.EndVector(len(offs))
	fbs.AssetIndexStart(b)
	fbs.AssetIndexAddAssets(b, list)
	bundle.Finish(b, fbs.AssetIndexEnd(b), bundle.SectionAssetsIndex)
	data, err := bundle.Encode(bundle.KindPlugin, []bundle.Section{{Kind: bundle.SectionAssetsIndex, Data: b.FinishedBytes()}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Verifies: AST-001, CMP-031.
// A baseline embeds a raster image's WebP variants at every density, an
// SVG's vector_graphics variant but never the SVG, and any other asset's
// own file — each file once across bundles.
func TestBaselineAssetFiles(t *testing.T) {
	t.Parallel()
	photo := testAsset{media.PNG, "photo.png", [][2]string{
		{media.AVIF, "photo@1.avif"}, {media.WebP, "photo@1.webp"}, {media.WebP, "photo@3.webp"},
	}}
	one := assetBundle(t, []testAsset{
		photo,
		{media.SVG, "icon.svg", [][2]string{{media.VectorGraphics, "icon.vec"}}},
		{media.SVG, "pending.svg", nil},
		{media.TTF, "font.ttf", nil},
		{media.GIF, "anim.gif", nil},
	})
	two := assetBundle(t, []testAsset{photo})
	got, err := baselineAssetFiles([][]byte{one, two})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{digest("photo@1.webp"), digest("photo@3.webp"), digest("icon.vec"), digest("font.ttf"), digest("anim.gif")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}
	if _, err := baselineAssetFiles([][]byte{[]byte("not a bundle")}); err == nil {
		t.Error("a malformed bundle must fail")
	}
}

// Verifies: AST-001.
// Asset files are downloaded by hash and checked, kept when already
// written, and files of earlier pulls the release does not use are
// removed; a file that does not match its hash fails the pull.
func TestWriteAssets(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{digest("a"): []byte("the bytes of a"), digest("b"): []byte("the bytes of b")}
	good := func(content []byte) string {
		sum := sha256.Sum256(content)
		return hex.EncodeToString(sum[:])
	}
	byHash := map[string][]byte{}
	for _, content := range files {
		byHash[good(content)] = content
	}
	byHash[digest("tampered")] = []byte("something else")
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		h := path.Base(r.URL.Path)
		data, ok := byHash[h]
		if !ok || r.URL.Path != "/v1/objects/assets/"+h[:2]+"/"+h {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	cl := &clients{http: srv.Client(), server: srv.URL}
	dir := t.TempDir()
	stale := filepath.Join(dir, "assets", digest("stale"))
	if err := os.MkdirAll(filepath.Dir(stale), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "assets", "README")
	if err := os.WriteFile(keep, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, content := range files {
		want = append(want, good(content))
	}
	slices.Sort(want)
	got, err := cl.writeAssets(context.Background(), dir, want)
	if err != nil || len(got) != 2 || got[0].File != "assets/"+want[0] {
		t.Fatalf("writeAssets: %+v %v", got, err)
	}
	for _, h := range want {
		if !fileHasHash(filepath.Join(dir, "assets", h), h) {
			t.Errorf("%s was not written", h)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a stale asset was kept")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("a file pull did not write was removed")
	}
	before := requests
	if _, err := cl.writeAssets(context.Background(), dir, want); err != nil || requests != before {
		t.Errorf("files already written were downloaded again: %v %d", err, requests-before)
	}
	for _, h := range []string{digest("tampered"), digest("missing")} {
		if _, err := cl.writeAssets(context.Background(), dir, []string{h}); err == nil {
			t.Errorf("%s: the pull must fail", h)
		}
	}
}
