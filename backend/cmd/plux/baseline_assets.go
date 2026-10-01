// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
)

// baselineAsset is one asset file of a baseline, named by its SHA-256.
type baselineAsset struct {
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

// maxAssetFile bounds one downloaded asset file: the hard maximum of
// asset.fileSize.
const maxAssetFile = 100 << 20

// assetFileName matches the files pull writes under assets/.
var assetFileName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// baselineAssetFiles lists, sorted and once each, the asset files a
// baseline embeds for the assets its bundles index (AST-001): the WebP
// variants of a raster image at every density, so that the device picks
// its density at run time and every platform decodes it; the
// vector_graphics variant of an SVG, never the SVG itself (CMP-031); the
// file itself for anything else.
func baselineAssetFiles(bundles [][]byte) ([]string, error) {
	seen := map[string]bool{}
	for _, data := range bundles {
		b, err := bundle.ReadStructure(data)
		if err != nil {
			return nil, fmt.Errorf("read a bundle: %w", err)
		}
		for _, s := range b.Sections {
			if s.Kind == bundle.SectionAssetsIndex {
				indexFiles(fbs.GetRootAsAssetIndex(s.Data, 0), seen)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	slices.Sort(out)
	return out, nil
}

// indexFiles adds the files of every asset of an index to seen.
func indexFiles(idx *fbs.AssetIndex, seen map[string]bool) {
	var a fbs.Asset
	for i := range idx.AssetsLength() {
		if idx.Assets(&a, i) {
			for _, h := range assetFiles(&a) {
				seen[h] = true
			}
		}
	}
}

// assetFiles names the files of one asset a baseline embeds.
func assetFiles(a *fbs.Asset) []string {
	var webp, vec []string
	var v fbs.AssetVariant
	for j := range a.VariantsLength() {
		if !a.Variants(&v, j) {
			continue
		}
		switch string(v.MediaType()) {
		case media.WebP:
			webp = append(webp, hex.EncodeToString(v.HashBytes()))
		case media.VectorGraphics:
			vec = append(vec, hex.EncodeToString(v.HashBytes()))
		}
	}
	switch {
	case string(a.MediaType()) == media.SVG:
		return vec
	case len(webp) > 0:
		return webp
	}
	return []string{hex.EncodeToString(a.HashBytes())}
}

// writeAssets downloads the asset files into dir/assets, checking each
// against its SHA-256, and removes the files of earlier pulls the
// release no longer uses.
func (cl *clients) writeAssets(ctx context.Context, dir string, files []string) ([]baselineAsset, error) {
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil { //nolint:gosec // G301: project files.
		return nil, fmt.Errorf("create %s: %w", assets, err)
	}
	out := make([]baselineAsset, 0, len(files))
	for _, h := range files {
		path := filepath.Join(assets, h)
		if !fileHasHash(path, h) {
			data, err := cl.downloadAsset(ctx, h)
			if err != nil {
				return nil, err
			}
			if err := writeFile(path, data); err != nil {
				return nil, err
			}
		}
		out = append(out, baselineAsset{SHA256: h, File: "assets/" + h})
	}
	entries, err := os.ReadDir(assets)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", assets, err)
	}
	for _, e := range entries {
		if assetFileName.MatchString(e.Name()) && !slices.Contains(files, e.Name()) {
			if err := os.Remove(filepath.Join(assets, e.Name())); err != nil {
				return nil, fmt.Errorf("remove a stale asset: %w", err)
			}
		}
	}
	return out, nil
}

// fileHasHash reports whether path holds bytes with SHA-256 h.
func fileHasHash(path, h string) bool {
	data, err := os.ReadFile(path) //nolint:gosec // G304: a file pull itself writes.
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == h
}

// downloadAsset fetches an asset file by its SHA-256 and checks it.
func (cl *clients) downloadAsset(ctx context.Context, h string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.server+"/v1/objects/assets/"+h[:2]+"/"+h, nil)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	res, err := cl.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download asset %s: %w", h, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset %s: status %d", h, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxAssetFile+1))
	if err != nil {
		return nil, fmt.Errorf("download asset %s: %w", h, err)
	}
	sum := sha256.Sum256(data)
	if len(data) > maxAssetFile || hex.EncodeToString(sum[:]) != h {
		return nil, fmt.Errorf("asset %s does not match its hash", h)
	}
	return data, nil
}
