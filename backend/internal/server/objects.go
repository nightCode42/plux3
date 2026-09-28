// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// servedKinds are the objects devices download directly, with the media
// type each is served as. Assets reach devices inside bundles; exports
// are fetched through the API.
var servedKinds = map[objects.Kind]string{objects.KindBundle: bundle.MediaType, objects.KindDelta: release.DeltaMediaType}

// objectHandler serves bundles and deltas by their content address when
// no CDN is configured (DEP-041). The address is the SHA-256 of the
// bytes, so a response never changes: it is cacheable for ever and
// supports range requests (REL-024). What a device downloads is verified
// against the signed manifest, never trusted.
func (s *Server) objectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, release.ObjectsPath)
		mediaType := served(key)
		if objects.ValidKey(key) != nil || mediaType == "" {
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
		h.Set("Content-Type", mediaType)
		h.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", info.ModTime, bytes.NewReader(data))
	})
}

// served returns the media type of a key devices may download, or "".
func served(key string) string {
	kind, _, _ := strings.Cut(key, "/")
	return servedKinds[objects.Kind(kind)]
}
