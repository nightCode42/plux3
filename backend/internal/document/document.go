// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package document keeps the drafts of an app: one per plugin and one for
// the app-level documents (ADR-0015). A write carries the revision it
// read and is refused when the document has moved on (SRV-030); every
// accepted write appends a snapshot of what it changed (SRV-031); and
// nothing is written without the draft's exclusive editing lock
// (SRV-040–SRV-042).
//
// Content is canonical JSON (SCH-003), structurally valid before it is
// stored, compressed and addressed by its SHA-256 so that history costs
// little. The compiler reads a draft as the Git layout (SCH-006), which is
// also what Export writes and Import reads.
package document

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/klauspost/compress/zstd"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Lock timing is fixed by SRV-040: a heartbeat every 30 seconds, expiry
// two minutes after the last one.
const (
	LockHeartbeat = 30 * time.Second
	LockTTL       = 2 * time.Minute
)

// IDs generates identifiers for the rows this package writes.
type IDs interface {
	New() (string, error)
}

// Options configures a Service.
type Options struct {
	DB      *storage.DB
	Audit   *audit.Log
	Tenancy *tenancy.Service
	IDs     IDs
	// Limits are the installation's limits; organisations, apps and
	// plugins tighten them (LIM-002).
	Limits limits.Set
	// SnapshotDays is how long history is kept, at least 90 (SRV-031).
	SnapshotDays int
	// CompilerVersion is recorded in the bundles validation compiles.
	CompilerVersion string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Objects stores asset files (SRV-060); nil refuses uploads.
	Objects objects.Store
	// Jobs enqueues the transcoding of uploaded images (CMP-030).
	Jobs Enqueuer
	// Scanner checks uploads for malware; nil scans nothing.
	Scanner Scanner
	// Codecs transcode images in the worker; nil in the api role.
	Codecs *media.Codecs
}

// Service is the domain logic of drafts.
type Service struct {
	o               Options
	now             func() time.Time
	schemaValidator *schema.Validator
	encoder         *zstd.Encoder
	decoder         *zstd.Decoder
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.DB == nil:
		return nil, errors.New("document: a database is required")
	case o.Audit == nil:
		return nil, errors.New("document: an audit log is required")
	case o.Tenancy == nil:
		return nil, errors.New("document: the tenancy service is required")
	case o.IDs == nil:
		return nil, errors.New("document: an identifier generator is required")
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	if o.SnapshotDays < 90 {
		o.SnapshotDays = 90
	}
	if o.CompilerVersion == "" {
		o.CompilerVersion = "dev"
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	validator, err := schema.NewValidator()
	if err != nil {
		return nil, fmt.Errorf("document: %w", err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("document: %w", err)
	}
	// Decompression is bounded by the largest document the registry's
	// hard maximum allows, so a damaged blob cannot exhaust memory.
	def, _ := limits.Lookup(limits.DocumentFileSize)
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(uint64(def.Max))) //nolint:gosec // a positive registry value
	if err != nil {
		return nil, fmt.Errorf("document: %w", err)
	}
	return &Service{o: o, now: now, schemaValidator: validator, encoder: encoder, decoder: decoder}, nil
}

// Document is one file of a draft.
type Document struct {
	ID        string
	AppID     string
	PluginID  string
	Path      string
	Kind      string
	Content   []byte
	SHA256    string
	Revision  int64
	UpdatedBy audit.Actor
	UpdatedAt time.Time
}

// Snapshot is one entry of a draft's history (SRV-031).
type Snapshot struct {
	ID        string
	AppID     string
	PluginID  string
	Sequence  int64
	Revision  int64
	Reason    string
	Paths     []string
	Actor     audit.Actor
	Kept      bool
	CreatedAt time.Time
}

// Written is the outcome of an accepted write.
type Written struct {
	Documents   []Document
	SnapshotID  string
	Revision    int64
	Diagnostics plxerr.Diagnostics
}

// draft is a resolved draft: its row and, for a plugin draft, the plugin.
type draft struct {
	row    dbgen.Draft
	app    string
	plugin *dbgen.Plugin
}

// pluginID is the draft's plugin, or "".
func (d draft) pluginID() string {
	if d.plugin == nil {
		return ""
	}
	return storage.ID(d.plugin.ID)
}

// prefix is the path prefix every document of the draft has: "" for the
// app-level draft, "plugins/<key>/" for a plugin's.
func (d draft) prefix() string {
	if d.plugin == nil {
		return ""
	}
	return "plugins/" + d.plugin.Key + "/"
}

// editPermission is what writing to the draft needs: plugin.edit for a
// plugin's documents, app.manage for the app-level ones.
func (d draft) editPermission() auth.Permission {
	if d.plugin == nil {
		return auth.AppManage
	}
	return auth.PluginEdit
}

// inOrg runs f in a transaction bound to the principal's organisation.
func (s *Service) inOrg(ctx context.Context, p auth.Principal, f func(context.Context, pgx.Tx) error) error {
	return s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, f) //nolint:wrapcheck // InTx wraps its own failures
}

// resolve reads the draft of an app, or of one of its plugins, after
// checking that the principal may read the app. The app-level draft is
// created on first use.
func (s *Service) resolve(ctx context.Context, q *dbgen.Queries, p auth.Principal, appID, pluginID string) (draft, error) {
	app, err := parseID(appID, "app")
	if err != nil {
		return draft{}, err
	}
	if !p.HoldsOnApp(auth.PluginRead, appID) && !p.HoldsOnApp(auth.AppRead, appID) {
		return draft{}, plxerr.New(plxerr.ResourceNotFound, "no such app")
	}
	appRow, err := q.GetApp(ctx, app)
	if err != nil {
		return draft{}, failure(err, "app")
	}
	d := draft{app: appID}
	var plugin pgtype.UUID
	if pluginID != "" {
		if plugin, err = parseID(pluginID, "plugin"); err != nil {
			return draft{}, err
		}
		row, err := q.GetPlugin(ctx, plugin)
		if err != nil {
			return draft{}, failure(err, "plugin")
		}
		if row.AppID != appRow.ID {
			return draft{}, plxerr.New(plxerr.ResourceNotFound, "no such plugin in this app")
		}
		d.plugin = &row
	}
	row, err := q.GetDraft(ctx, dbgen.GetDraftParams{AppID: app, PluginID: plugin})
	if errors.Is(err, pgx.ErrNoRows) {
		id, idErr := s.newID()
		if idErr != nil {
			return draft{}, idErr
		}
		row, err = q.CreateDraft(ctx, dbgen.CreateDraftParams{
			ID: storage.MustUUID(id), OrganizationID: appRow.OrganizationID, AppID: app, PluginID: plugin,
		})
	}
	if err != nil {
		return draft{}, failure(err, "draft")
	}
	d.row = row
	return d, nil
}

// newID generates an identifier.
func (s *Service) newID() (string, error) {
	id, err := s.o.IDs.New()
	if err != nil {
		return "", fmt.Errorf("document: %w", err)
	}
	return id, nil
}

// record appends an audit entry with the hashes of the content before and
// after, as SEC-140 requires.
func (s *Service) record(ctx context.Context, tx pgx.Tx, p auth.Principal, action audit.Action, kind, id, before, after string) error {
	return s.recordEntry(ctx, tx, p, audit.Entry{
		Action: action, TargetKind: kind, TargetID: id, BeforeHash: before, AfterHash: after,
	})
}

// recordEntry appends an audit entry for the principal.
func (s *Service) recordEntry(ctx context.Context, tx pgx.Tx, p auth.Principal, e audit.Entry) error {
	e.OrganizationID, e.Actor = p.OrganizationID, p.Actor()
	if _, err := s.o.Audit.Append(ctx, tx, e); err != nil {
		return fmt.Errorf("document: %w", err)
	}
	return nil
}

// putBlob stores content under its hash, compressed.
func (s *Service) putBlob(ctx context.Context, q *dbgen.Queries, org pgtype.UUID, content []byte) ([]byte, error) {
	sum := sha256.Sum256(content)
	if err := q.PutBlob(ctx, dbgen.PutBlobParams{
		OrganizationID: org, Sha256: sum[:], Size: int64(len(content)),
		Content: s.encoder.EncodeAll(content, nil),
	}); err != nil {
		return nil, fmt.Errorf("document: store content: %w", err)
	}
	return sum[:], nil
}

// blob reads content by its hash and checks it against the hash.
func (s *Service) blob(ctx context.Context, q *dbgen.Queries, org pgtype.UUID, sum []byte) ([]byte, error) {
	row, err := q.GetBlob(ctx, dbgen.GetBlobParams{OrganizationID: org, Sha256: sum})
	if err != nil {
		return nil, fmt.Errorf("document: read content %x: %w", sum, err)
	}
	content, err := s.decoder.DecodeAll(row.Content, nil)
	if err != nil {
		return nil, fmt.Errorf("document: decompress content %x: %w", sum, err)
	}
	if got := sha256.Sum256(content); !equalBytes(got[:], sum) {
		return nil, fmt.Errorf("document: content %x does not match its hash", sum)
	}
	return content, nil
}

// withDetail adds a detail to a Plux error.
func withDetail(err error, key, value string) error {
	var pe *plxerr.Error
	if errors.As(err, &pe) {
		return pe.WithDetail(key, value)
	}
	return err
}

// equalBytes compares two byte slices.
func equalBytes(a, b []byte) bool { return string(a) == string(b) }

// hexHash renders a hash for responses and the audit log.
func hexHash(sum []byte) string {
	if len(sum) == 0 {
		return ""
	}
	return hex.EncodeToString(sum)
}

// failure turns a database error into the refusal a caller understands.
func failure(err error, what string) error {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return plxerr.New(plxerr.ResourceNotFound, "no such %s", what)
	case errors.As(err, &pg) && pg.Code == "23505":
		return plxerr.New(plxerr.ResourceExists, "a %s with this key already exists", what)
	default:
		return fmt.Errorf("document: %s: %w", what, err)
	}
}

// parseID validates an identifier from a caller.
func parseID(s, what string) (pgtype.UUID, error) {
	id, err := storage.UUID(s)
	if err != nil {
		return pgtype.UUID{}, plxerr.New(plxerr.InvalidFormat, "the %s identifier is not valid", what)
	}
	return id, nil
}

// authorize is Principal.AuthorizeApp with the refusal marked as this
// package's.
func authorize(p auth.Principal, want auth.Permission, appID string) error {
	if err := p.AuthorizeApp(want, appID); err != nil {
		return fmt.Errorf("document: %w", err)
	}
	return nil
}

// documentOf converts a stored document without its content.
func documentOf(row dbgen.Document, d draft) Document {
	return Document{
		ID: storage.ID(row.ID), AppID: d.app, PluginID: d.pluginID(), Path: row.Path, Kind: row.Kind,
		SHA256: hexHash(row.Sha256), Revision: row.Revision,
		UpdatedBy: audit.Actor{Kind: row.UpdatedByKind, ID: row.UpdatedByID, Display: row.UpdatedBy},
		UpdatedAt: storage.Time(row.UpdatedAt),
	}
}
