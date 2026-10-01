// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// servedKinds are the objects devices download directly, with the media
// type each is served as; "" serves an asset file with the media type it
// was stored with. Exports are fetched through the API.
var servedKinds = map[objects.Kind]string{objects.KindBundle: bundle.MediaType, objects.KindDelta: release.DeltaMediaType, objects.KindAsset: ""}

// objectHandler serves bundles, deltas and asset files by their content
// address (DEP-041, AST-001). The address is the SHA-256 of the bytes, so
// a response never changes: it is cacheable for ever and supports range
// requests (REL-024). What a device downloads is verified against the
// signed manifest, or for an asset against the hash its signed bundle
// lists, never trusted.
func (s *Server) objectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, release.ObjectsPath)
		mediaType, ok := served(key)
		if objects.ValidKey(key) != nil || !ok {
			http.NotFound(w, r)
			return
		}
		data, info, err := s.objects.Get(r.Context(), key)
		if errors.Is(err, objects.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "the object store is unavailable", http.StatusServiceUnavailable)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("ETag", `"`+key[strings.LastIndexByte(key, '/')+1:]+`"`)
		if mediaType == "" {
			mediaType = assetMediaType(info.MediaType, data)
		}
		h.Set("Content-Type", mediaType)
		h.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", info.ModTime, bytes.NewReader(data))
	})
}

// served returns the media type a key devices may download is served
// with ("" for the stored one) and whether they may download it.
func served(key string) (string, bool) {
	kind, _, _ := strings.Cut(key, "/")
	mediaType, ok := servedKinds[objects.Kind(kind)]
	return mediaType, ok
}

// assetMediaType is the media type an asset file is served with: the one
// it was stored with, or, from a store that keeps none (the filesystem
// backend), the one its bytes show, as at upload.
func assetMediaType(stored string, data []byte) string {
	if stored != "" {
		return stored
	}
	if t, err := media.Sniff(data); err == nil {
		return t
	}
	return "application/octet-stream"
}
