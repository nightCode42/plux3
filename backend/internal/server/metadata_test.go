// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
)

type fakeMetadata struct {
	files map[string]release.MetadataFile
}

func (f fakeMetadata) Metadata(_ context.Context, env, name string) (release.MetadataFile, error) {
	if env == "down" {
		return release.MetadataFile{}, errors.New("database unavailable")
	}
	if file, ok := f.files[env+"/"+name]; ok {
		return file, nil
	}
	return release.MetadataFile{}, plxerr.New(plxerr.ResourceNotFound, "no such metadata file")
}

// Verifies: SEC-050, SEC-051.
// Metadata is served as JSON by environment and name: a versioned file is
// cacheable for ever, the newest file briefly; a missing file is 404 and
// a failing store 503.
func TestMetadataHandler_SEC_050(t *testing.T) {
	t.Parallel()
	doc := []byte(`{"signatures":[],"signed":{}}`)
	sum := sha256.Sum256(doc)
	src := fakeMetadata{files: map[string]release.MetadataFile{
		"env/timestamp.json": {Document: doc, SHA256: sum, Version: 4},
		"env/3.root.json":    {Document: doc, SHA256: sum, Version: 3, Versioned: true},
	}}
	s := &Server{}
	mux := http.NewServeMux()
	mux.Handle(MetadataRoute, s.metadataHandler(src))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	type reply struct {
		status int
		header http.Header
		body   string
	}
	get := func(path string) reply {
		t.Helper()
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		return reply{res.StatusCode, res.Header, string(body)}
	}
	res := get("/v1/metadata/env/timestamp.json")
	if res.status != 200 || res.body != string(doc) || res.header.Get("Content-Type") != "application/json" ||
		res.header.Get("Cache-Control") != "public, max-age=30, must-revalidate" ||
		res.header.Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` {
		t.Errorf("the newest file: %d %v %q", res.status, res.header, res.body)
	}
	res = get("/v1/metadata/env/3.root.json")
	if res.status != 200 || res.header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("a versioned file: %d %v", res.status, res.header)
	}
	if res = get("/v1/metadata/env/9.root.json"); res.status != http.StatusNotFound {
		t.Errorf("a missing file: %d", res.status)
	}
	if res = get("/v1/metadata/down/root.json"); res.status != http.StatusServiceUnavailable {
		t.Errorf("a failing store: %d", res.status)
	}
}
