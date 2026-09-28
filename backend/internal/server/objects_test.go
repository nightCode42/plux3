// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// Verifies: REL-024.
// Bundles and deltas are served by content address, cacheable for ever,
// with range requests; other kinds and malformed keys are not served.
func TestObjectHandler(t *testing.T) {
	t.Parallel()
	store, err := objects.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789abcdef")
	sum := sha256.Sum256(data)
	key, err := objects.Key(objects.KindDelta, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), key, data, "application/vnd.plux.delta"); err != nil {
		t.Fatal(err)
	}
	asset, _ := objects.Key(objects.KindAsset, hex.EncodeToString(sum[:]))
	if _, err := store.Put(context.Background(), asset, data, "image/png"); err != nil {
		t.Fatal(err)
	}
	s := &Server{objects: store}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/objects/", s.objectHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/objects/"+key, nil)
	r.Header.Set("Range", "bytes=4-7")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(body) != "4567" ||
		res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		res.Header.Get("Content-Type") != "application/vnd.plux.delta" || res.Header.Get("ETag") == "" {
		t.Errorf("a range request: %d %q %v", res.StatusCode, body, res.Header)
	}
	for _, path := range []string{asset, "deltas/00/" + hex.EncodeToString(make([]byte, 32)), "deltas/../x", "nope"} {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/objects/"+path, nil)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
}
