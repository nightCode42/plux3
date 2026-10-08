// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// MetadataRoute is where the update metadata of an environment is served,
// beside the objects devices download: "<environment>/<role>.json" for
// the newest file and "<environment>/<version>.<role>.json" for one
// version, for the roles root, snapshot and timestamp (SEC-050,
// SEC-051). The targets role is the manifest.
const MetadataRoute = "GET " + release.MetadataPath + "{environment}/{file}"

// metadataSource is what the handler reads files from.
type metadataSource interface {
	Metadata(ctx context.Context, environment, name string) (release.MetadataFile, error)
}

// metadataHandler serves the signed update metadata. The files are public
// and verified by the device against the root it embeds, so no credential
// is asked; the environment identifier names what is served. A versioned
// file never changes and is cached for ever, as an object is; the newest
// file is cached briefly, so that a timestamp reaches devices well inside
// its expiry.
func (*Server) metadataHandler(releases metadataSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := releases.Metadata(r.Context(), r.PathValue("environment"), r.PathValue("file"))
		var coded *plxerr.Error
		switch {
		case errors.As(err, &coded) && coded.Code == plxerr.ResourceNotFound:
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, "the metadata is unavailable", http.StatusServiceUnavailable)
			return
		}
		h := w.Header()
		if file.Versioned {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "public, max-age="+strconv.Itoa(release.MetadataMaxAge)+", must-revalidate")
		}
		h.Set("ETag", `"`+hex.EncodeToString(file.SHA256[:])+`"`)
		h.Set("Content-Type", "application/json")
		h.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(file.Document))
	})
}

// BuildReleases builds the release service for an administrative command
// that runs beside the serving roles, such as uploading a root. It holds
// the signing backend only to name the environment's online keys: the
// command signs nothing, and the offline root keys are never here
// (SEC-051).
func BuildReleases(ctx context.Context, cfg *config.Config, db *storage.DB) (*release.Service, error) {
	store, err := buildObjects(ctx, cfg)
	if err != nil {
		return nil, err
	}
	backend, err := BuildSigning(cfg)
	if err != nil {
		return nil, err
	}
	set, err := cfg.LimitSet()
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	services, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), set, backend, WorkDeps{
		Objects: store, Signer: backend, ProductionSigning: backend.AllowedInProduction(),
	})
	if err != nil {
		return nil, err
	}
	return services.Releases, nil
}
