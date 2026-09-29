// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// This file keeps an app's asset files (SRV-060). An upload is checked
// against asset.fileSize, typed from its bytes, packaged (Lottie becomes
// dotLottie, CMP-033), stripped of metadata, offered to the malware
// scanner, and stored once per content; the app-level draft's
// assets/index.json then lists it, so an upload is a draft write like any
// other, under the app's lock. Raster images are transcoded to their
// variants by a job afterwards (CMP-030).

// indexPath is the asset index in the Git layout.
const indexPath = "assets/index.json"

// Processing states of an asset.
const (
	processingPending = "pending"
	processingReady   = "ready"
	processingFailed  = "failed"
)

// assetFile is the form of a path under assets/ (the document model's).
var assetFile = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

// Asset is an uploaded asset file.
type Asset struct {
	ID          string
	AppID       string
	File        string
	MediaType   string
	SHA256      string
	Size        int64
	Width       int
	Height      int
	Processing  string
	Variants    []Variant
	Diagnostics plxerr.Diagnostics
	UploadedBy  audit.Actor
	CreatedAt   time.Time
}

// Variant is a transcoded form of an asset, stored as its own object.
type Variant struct {
	MediaType string `json:"mediaType"`
	Density   int    `json:"density"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

// AssetJob asks the worker to transcode an asset (CMP-030).
type AssetJob struct {
	OrganizationID string `json:"organizationId"`
	// RowID is the asset row, which a later upload of the same file
	// replaces rather than changes.
	RowID string `json:"rowId"`
}

// Kind names the job.
func (AssetJob) Kind() string { return "asset.process" }

// Enqueuer adds a job in the caller's transaction, so it exists exactly
// when the write that asked for it does (SRV-024).
type Enqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, job AssetJob) error
}

// objectKey is the storage key of an asset object.
func objectKey(sum []byte) string {
	key, _ := objects.Key(objects.KindAsset, hex.EncodeToString(sum)) //nolint:errcheck // a SHA-256 is always a valid digest
	return key
}

// assetsRequired refuses when no object store is configured.
func (s *Service) assetsRequired() error {
	if s.o.Objects == nil {
		return errors.New("document: asset storage is not configured")
	}
	return nil
}

// UploadAsset stores a file under assets/ and lists it in the asset
// index, replacing a file uploaded under the same name. Assets belong to
// the app, so pluginID must be empty (SCH-006).
func (s *Service) UploadAsset(ctx context.Context, p auth.Principal, appID, pluginID, session, file string, data []byte) (Asset, error) {
	if err := s.assetsRequired(); err != nil {
		return Asset{}, err
	}
	if pluginID != "" {
		return Asset{}, plxerr.New(plxerr.InvalidProjectLayout, "assets belong to the app: they are listed in assets/index.json, not in a plugin")
	}
	if len(file) > 255 || !assetFile.MatchString(file) || file == "index.json" || strings.Contains(file, "..") {
		return Asset{}, plxerr.New(plxerr.InvalidFormat, "an asset's name is a lower-case path under assets/, such as images/logo.png")
	}
	lim, err := s.appLimits(ctx, p, appID)
	if err != nil {
		return Asset{}, err
	}
	content, mediaType, info, err := s.prepareAsset(ctx, lim, data)
	if err != nil {
		return Asset{}, err
	}
	sum := sha256.Sum256(content)
	if _, err := s.o.Objects.Put(ctx, objectKey(sum[:]), content, mediaType); err != nil {
		return Asset{}, fmt.Errorf("document: store the asset: %w", err)
	}
	var out Asset
	_, err = s.writeThen(ctx, p, appID, "", session, reasonWrite,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			c, id, err := s.indexWith(ctx, q, d, lim, func(idx *assetIndex) (string, error) {
				return idx.put(file, mediaType, s.newID)
			})
			out.ID = id
			return []change{c}, nil, err
		},
		func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, d draft) error {
			row, err := s.insertAsset(ctx, tx, q, d, p, out.ID, file, mediaType, sum[:], int64(len(content)), info)
			if err != nil {
				return err
			}
			out = assetOf(row)
			return nil
		})
	return out, err
}

// appLimits reads the limits in force for an app.
func (s *Service) appLimits(ctx context.Context, p auth.Principal, appID string) (limits.Set, error) {
	var lim limits.Set
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.resolve(ctx, dbgen.New(tx), p, appID, "")
		if err != nil {
			return err
		}
		lim, err = s.limitsFor(ctx, tx, d)
		return err
	})
	return lim, err
}

// prepareAsset checks and cleans an upload: its size, its type from its
// bytes, packaging, metadata, an image's pixel count, and the scanner.
func (s *Service) prepareAsset(ctx context.Context, lim limits.Set, data []byte) ([]byte, string, media.Info, error) {
	max := lim.Get(limits.AssetFileSize)
	if int64(len(data)) > max {
		return nil, "", media.Info{}, plxerr.New(plxerr.LimitExceeded, "the file has %d bytes, above asset.fileSize = %d", len(data), max)
	}
	sniffed, err := media.Sniff(data)
	if err != nil {
		return nil, "", media.Info{}, plxerr.Wrap(plxerr.InvalidFormat, err, "the file is not an accepted asset type: PNG, JPEG, WebP, GIF, SVG, TrueType, OpenType, Lottie, dotLottie or Rive")
	}
	content, mediaType, err := media.Package(data, sniffed, max)
	if err != nil {
		return nil, "", media.Info{}, plxerr.Wrap(plxerr.InvalidFormat, err, "the file is malformed")
	}
	if content, err = media.Strip(content, mediaType); err != nil {
		return nil, "", media.Info{}, plxerr.Wrap(plxerr.InvalidFormat, err, "the file is malformed")
	}
	var info media.Info
	if media.Raster(mediaType) {
		if info, err = media.Describe(content, mediaType); err != nil {
			return nil, "", media.Info{}, plxerr.Wrap(plxerr.InvalidFormat, err, "the image is malformed")
		}
		if px := int64(info.Width) * int64(info.Height); px > s.o.Limits.Get(limits.AssetImagePixels) {
			return nil, "", media.Info{}, plxerr.New(plxerr.LimitExceeded, "the image has %d pixels, above asset.imagePixels = %d", px, s.o.Limits.Get(limits.AssetImagePixels))
		}
	}
	if s.o.Scanner != nil {
		if err := s.o.Scanner.Scan(ctx, content); err != nil {
			if errors.Is(err, ErrInfected) {
				return nil, "", media.Info{}, plxerr.Wrap(plxerr.AssetRejected, err, "the malware scanner rejected the file")
			}
			return nil, "", media.Info{}, plxerr.Wrap(plxerr.UpstreamUnavailable, err, "the malware scanner could not check the file")
		}
	}
	return content, mediaType, info, nil
}

// insertAsset records an uploaded file, replacing the one before it
// under the same name, and asks for the variants of a raster image or an
// SVG.
func (s *Service) insertAsset(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, d draft, p auth.Principal,
	assetID, file, mediaType string, sum []byte, size int64, info media.Info,
) (dbgen.Asset, error) {
	if _, err := q.RetireAssetFile(ctx, dbgen.RetireAssetFileParams{AppID: d.row.AppID, File: file}); err != nil {
		return dbgen.Asset{}, fmt.Errorf("document: replace the asset: %w", err)
	}
	rowID, err := s.newID()
	if err != nil {
		return dbgen.Asset{}, err
	}
	processing := processingReady
	if media.Raster(mediaType) && !info.Animated || mediaType == media.SVG {
		processing = processingPending
	}
	row, err := q.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: storage.MustUUID(rowID), AssetID: storage.MustUUID(assetID), OrganizationID: d.row.OrganizationID,
		AppID: d.row.AppID, File: file, MediaType: mediaType, Sha256: sum, Size: size,
		Width: int32(info.Width), Height: int32(info.Height), Processing: processing, //nolint:gosec // bounded by asset.imagePixels
		UploadedByKind: p.Kind, UploadedByID: p.ID, UploadedBy: p.Display,
	})
	if err != nil {
		return dbgen.Asset{}, failure(err, "asset")
	}
	if processing == processingPending {
		if s.o.Jobs == nil {
			return dbgen.Asset{}, errors.New("document: no job queue to transcode the asset")
		}
		if err := s.o.Jobs.Enqueue(ctx, tx, AssetJob{OrganizationID: storage.ID(row.OrganizationID), RowID: rowID}); err != nil {
			return dbgen.Asset{}, fmt.Errorf("document: enqueue the asset's transcoding: %w", err)
		}
	}
	return row, s.recordEntry(ctx, tx, p, audit.Entry{
		Action: audit.AssetUploaded, TargetKind: "asset", TargetID: assetID, AfterHash: hexHash(sum), Detail: file,
	})
}

// DeleteAsset removes an asset from the index. Its file stays in object
// storage while any snapshot's index may still name it.
func (s *Service) DeleteAsset(ctx context.Context, p auth.Principal, assetID, session string) error {
	if err := s.assetsRequired(); err != nil {
		return err
	}
	var appID, file string
	if err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.assetRow(ctx, dbgen.New(tx), p, assetID)
		appID, file = storage.ID(row.AppID), row.File
		return err
	}); err != nil {
		return err
	}
	_, err := s.writeThen(ctx, p, appID, "", session, reasonDelete,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			c, _, err := s.indexWith(ctx, q, d, lim, func(idx *assetIndex) (string, error) {
				return "", idx.remove(assetID)
			})
			return []change{c}, nil, err
		},
		func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, d draft) error {
			if _, err := q.RetireAssetFile(ctx, dbgen.RetireAssetFileParams{AppID: d.row.AppID, File: file}); err != nil {
				return fmt.Errorf("document: delete the asset: %w", err)
			}
			return s.recordEntry(ctx, tx, p, audit.Entry{Action: audit.AssetDeleted, TargetKind: "asset", TargetID: assetID, Detail: file})
		})
	return err
}

// assetRow reads an asset the principal may read.
func (s *Service) assetRow(ctx context.Context, q *dbgen.Queries, p auth.Principal, assetID string) (dbgen.Asset, error) {
	id, err := parseID(assetID, "asset")
	if err != nil {
		return dbgen.Asset{}, err
	}
	row, err := q.GetAsset(ctx, id)
	if err != nil {
		return dbgen.Asset{}, failure(err, "asset")
	}
	if _, err := s.resolve(ctx, q, p, storage.ID(row.AppID), ""); err != nil {
		return dbgen.Asset{}, err
	}
	return row, nil
}

// GetAsset returns an asset.
func (s *Service) GetAsset(ctx context.Context, p auth.Principal, assetID string) (Asset, error) {
	var out Asset
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.assetRow(ctx, dbgen.New(tx), p, assetID)
		out = assetOf(row)
		return err
	})
	return out, err
}

// ListAssets lists an app's assets in file order.
func (s *Service) ListAssets(ctx context.Context, p auth.Principal, appID, afterFile string, size int32) ([]Asset, error) {
	var out []Asset
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, "")
		if err != nil {
			return err
		}
		rows, err := q.ListAssets(ctx, dbgen.ListAssetsParams{AppID: d.row.AppID, AfterFile: afterFile, PageSize: size})
		if err != nil {
			return failure(err, "asset")
		}
		for _, r := range rows {
			out = append(out, assetOf(r))
		}
		return nil
	})
	return out, err
}

// AssetURL returns where an asset object may be fetched from.
func (s *Service) AssetURL(ctx context.Context, sum string, ttl time.Duration) (string, error) {
	key, err := objects.Key(objects.KindAsset, sum)
	if err != nil {
		return "", fmt.Errorf("document: %w", err)
	}
	url, err := s.o.Objects.URL(ctx, key, ttl)
	if err != nil {
		return "", fmt.Errorf("document: %w", err)
	}
	return url, nil
}

// ProcessAsset transcodes a raster image into its variants (CMP-030):
// the worker's asset job. The same content transcoded before is reused;
// an image the codecs cannot handle is marked failed with a diagnostic.
// Transcoding runs outside any transaction, so a slow image holds no
// lock.
func (s *Service) ProcessAsset(ctx context.Context, job AssetJob) error {
	if s.o.Codecs == nil {
		return errors.New("document: no codecs to transcode assets")
	}
	system := auth.System(job.OrganizationID)
	id, err := parseID(job.RowID, "asset")
	if err != nil {
		return err
	}
	var row dbgen.Asset
	var reused *dbgen.Asset
	if err := s.inOrg(ctx, system, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		var err error
		if row, err = q.GetAssetIncludingDeleted(ctx, id); err != nil {
			return failure(err, "asset")
		}
		prev, err := q.FindProcessedAsset(ctx, dbgen.FindProcessedAssetParams{OrganizationID: row.OrganizationID, Sha256: row.Sha256, ID: row.ID})
		if err == nil {
			reused = &prev
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return failure(err, "asset")
		}
		return nil
	}); err != nil {
		return err
	}
	if row.Processing != processingPending {
		return nil
	}
	params := dbgen.CompleteAssetParams{ID: row.ID, Processing: processingReady, Width: row.Width, Height: row.Height, Variants: []byte("[]"), Diagnostics: []byte("[]")}
	if reused != nil {
		params.Variants, params.Width, params.Height = reused.Variants, reused.Width, reused.Height
	} else if variants, diags, err := s.transcode(ctx, row); err != nil {
		return err
	} else if diags != nil {
		params.Processing, params.Diagnostics = processingFailed, diags
	} else {
		params.Variants = variants
	}
	return s.inOrg(ctx, system, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := dbgen.New(tx).CompleteAsset(ctx, params); err != nil {
			return failure(err, "asset")
		}
		return nil
	})
}

// PendingAssets counts the app's assets whose variants the asset job
// has not made yet. A publish waits until there are none, so that the
// variants its bundle lists are the ones a release's recompilation finds
// (CMP-030, REL-003).
func (*Service) PendingAssets(ctx context.Context, tx pgx.Tx, appID string) (int64, error) {
	app, err := parseID(appID, "app")
	if err != nil {
		return 0, err
	}
	n, err := dbgen.New(tx).CountPendingAssets(ctx, app)
	if err != nil {
		return 0, failure(err, "asset")
	}
	return n, nil
}

// transcode makes and stores an asset's variants: WebP and AVIF of a
// raster image (CMP-030), vector_graphics of an SVG (CMP-031). It returns
// the variants as stored, or diagnostics when the file cannot be
// transcoded; an error only for a failure worth retrying.
func (s *Service) transcode(ctx context.Context, row dbgen.Asset) ([]byte, []byte, error) {
	original, _, err := s.o.Objects.Get(ctx, objectKey(row.Sha256))
	if err != nil {
		return nil, nil, fmt.Errorf("document: read the asset: %w", err)
	}
	variants, err := s.variants(ctx, original, row.MediaType)
	if err != nil {
		if ctx.Err() != nil || !errors.Is(err, errUntranscodable) {
			return nil, nil, fmt.Errorf("document: transcode: %w", err)
		}
		d := plxerr.NewDiagnostic(plxerr.InvalidFormat, plxerr.Location{File: "assets/" + row.File}, "the file could not be transcoded: %v", err)
		diags, _ := json.Marshal(plxerr.Diagnostics{d}) //nolint:errcheck // a diagnostic always encodes
		return nil, diags, nil
	}
	out := make([]Variant, 0, len(variants))
	for _, v := range variants {
		sum := sha256.Sum256(v.Data)
		if _, err := s.o.Objects.Put(ctx, objectKey(sum[:]), v.Data, v.MediaType); err != nil {
			return nil, nil, fmt.Errorf("document: store a variant: %w", err)
		}
		out = append(out, Variant{MediaType: v.MediaType, Density: v.Density, Width: v.Width, Height: v.Height, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(v.Data))})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("document: %w", err)
	}
	return b, nil, nil
}

// errUntranscodable marks a file no retry can transcode.
var errUntranscodable = errors.New("document: the file cannot be transcoded")

// variants makes the variants of a file. An error wrapping
// errUntranscodable is the file's fault, or a server with no SVG compiler.
func (s *Service) variants(ctx context.Context, data []byte, mediaType string) ([]media.Variant, error) {
	if mediaType != media.SVG {
		_, variants, err := s.o.Codecs.Transcode(ctx, data, mediaType, s.o.Limits.Get(limits.AssetImagePixels))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errUntranscodable, err)
		}
		return variants, nil
	}
	if s.o.SVG == nil {
		return nil, fmt.Errorf("%w: this server has no SVG compiler (assets.svgCompiler)", errUntranscodable)
	}
	vec, err := s.o.SVG.Compile(ctx, data)
	switch {
	case errors.Is(err, media.ErrSVGRejected):
		return nil, fmt.Errorf("%w: %w", errUntranscodable, err)
	case err != nil:
		return nil, err //nolint:wrapcheck // transcode wraps it
	}
	return []media.Variant{{MediaType: media.VectorGraphics, Data: vec}}, nil
}

// AssetVariants returns, for an app, the variants of each asset file by
// its SHA-256, in the form the compiler takes (CMP-030).
func (*Service) AssetVariants(ctx context.Context, tx pgx.Tx, appID string) (func([sha256.Size]byte) []compiler.AssetVariant, error) {
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	rows, err := dbgen.New(tx).ListAllAssets(ctx, app)
	if err != nil {
		return nil, failure(err, "asset")
	}
	byHash := map[[sha256.Size]byte][]compiler.AssetVariant{}
	for _, r := range rows {
		var sum [sha256.Size]byte
		copy(sum[:], r.Sha256)
		var vs []Variant
		if err := json.Unmarshal(r.Variants, &vs); err != nil {
			return nil, fmt.Errorf("document: read the variants of %s: %w", r.File, err)
		}
		for _, v := range vs {
			var h [sha256.Size]byte
			if b, err := hex.DecodeString(v.SHA256); err == nil && len(b) == sha256.Size {
				copy(h[:], b)
			}
			byHash[sum] = append(byHash[sum], compiler.AssetVariant{
				MediaType: v.MediaType, Density: v.Density, Width: v.Width, Height: v.Height, Hash: h, Size: v.Size,
			})
		}
	}
	return func(sum [sha256.Size]byte) []compiler.AssetVariant { return byHash[sum] }, nil
}

// assetOf converts a stored asset.
func assetOf(r dbgen.Asset) Asset {
	a := Asset{
		ID: storage.ID(r.AssetID), AppID: storage.ID(r.AppID), File: r.File, MediaType: r.MediaType,
		SHA256: hexHash(r.Sha256), Size: r.Size, Width: int(r.Width), Height: int(r.Height), Processing: r.Processing,
		UploadedBy: audit.Actor{Kind: r.UploadedByKind, ID: r.UploadedByID, Display: r.UploadedBy},
		CreatedAt:  storage.Time(r.CreatedAt),
	}
	_ = json.Unmarshal(r.Variants, &a.Variants)       //nolint:errcheck // written by this package
	_ = json.Unmarshal(r.Diagnostics, &a.Diagnostics) //nolint:errcheck // written by this package
	return a
}

// assetIndex is assets/index.json being edited.
type assetIndex struct {
	tree map[string]any
}

// entries returns the index's asset list.
func (x *assetIndex) entries() []any {
	list, _ := x.tree["assets"].([]any)
	return list
}

// put lists a file, keeping the identifier of an entry for the same
// file, and returns the entry's identifier.
func (x *assetIndex) put(file, mediaType string, newID func() (string, error)) (string, error) {
	list := x.entries()
	keys := map[string]bool{}
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m["file"] == file {
			m["mediaType"] = mediaType
			id, _ := m["id"].(string)
			return id, nil
		}
		if k, ok := m["key"].(string); ok {
			keys[k] = true
		}
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	key := keyFor(file)
	for i := 2; keys[key]; i++ {
		key = fmt.Sprintf("%s-%d", keyFor(file), i)
	}
	x.tree["assets"] = append(list, map[string]any{"id": id, "key": key, "file": file, "mediaType": mediaType})
	return id, nil
}

// remove drops an entry by identifier.
func (x *assetIndex) remove(id string) error {
	list := x.entries()
	for i, e := range list {
		if m, _ := e.(map[string]any); m["id"] == id {
			x.tree["assets"] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return plxerr.New(plxerr.ResourceNotFound, "the asset index does not list asset %s", id)
}

// keyFor derives an asset key from its file name: "images/Logo_2.png"
// becomes "logo-2".
func keyFor(file string) string {
	base := strings.TrimSuffix(path.Base(file), path.Ext(file))
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9' && b.Len() > 0:
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	key := strings.TrimSuffix(b.String(), "-")
	if len(key) > 50 {
		key = strings.TrimSuffix(key[:50], "-")
	}
	if !schema.ValidKey(key) {
		return "asset"
	}
	return key
}

// indexWith reads the draft's asset index, or starts one, lets f edit it,
// and returns the change that writes it back with the id f returned.
func (s *Service) indexWith(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set, f func(*assetIndex) (string, error)) (change, string, error) {
	idx := &assetIndex{}
	revision := int64(0)
	row, err := q.GetDocument(ctx, dbgen.GetDocumentParams{DraftID: d.row.ID, Path: indexPath})
	switch {
	case err == nil:
		content, err := s.blob(ctx, q, d.row.OrganizationID, row.Sha256)
		if err != nil {
			return change{}, "", err
		}
		tree, err := jcs.Parse(content, int(lim.Get(limits.DocumentJSONDepth)))
		if err != nil {
			return change{}, "", fmt.Errorf("document: read %s: %w", indexPath, err)
		}
		idx.tree, _ = tree.(map[string]any)
		revision = row.Revision
	case errors.Is(err, pgx.ErrNoRows):
		id, err := s.newID()
		if err != nil {
			return change{}, "", err
		}
		idx.tree = map[string]any{"schemaVersion": schema.CurrentVersion, "kind": string(schema.KindAssetIndex), "id": id, "assets": []any{}}
	default:
		return change{}, "", failure(err, "document")
	}
	id, err := f(idx)
	if err != nil {
		return change{}, "", err
	}
	data, err := jcs.Marshal(idx.tree)
	if err != nil {
		return change{}, "", fmt.Errorf("document: %w", err)
	}
	c, diags, err := s.prepare(d, lim, indexPath, data, revision)
	if err != nil {
		return change{}, "", fmt.Errorf("document: the asset index would be invalid (%s): %w", strconv.Itoa(len(diags)), err)
	}
	return c, id, nil
}
