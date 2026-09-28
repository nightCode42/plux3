// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// Publish job states.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Stages a publish reports, in order, with the progress each starts at.
var stages = []struct {
	name    string
	percent int32
}{
	{"sources", 5}, {"compile", 20}, {"check", 60}, {"sign", 70}, {"store", 80}, {"record", 95},
}

// PublishJob is a publish and its progress (SRV-050).
type PublishJob struct {
	ID            string
	AppID         string
	PluginID      string
	EnvironmentID string
	Revision      int64
	SnapshotID    string
	State         string
	Stage         string
	Percent       int32
	Version       int64
	Diagnostics   plxerr.Diagnostics
	Actor         audit.Actor
	CreatedAt     time.Time
	FinishedAt    time.Time
}

// Done reports whether the job has finished, one way or another.
func (j PublishJob) Done() bool {
	return j.State == StateSucceeded || j.State == StateFailed || j.State == StateCancelled
}

// PublishRequest is what a publish is asked to do.
type PublishRequest struct {
	AppID, PluginID, EnvironmentID string
	// Revision freezes a draft revision; zero is the current one.
	Revision            int64
	Label, Notes        string
	AcknowledgeWarnings bool
}

// Publish freezes a draft and starts a durable publish job (SRV-050).
// An empty plugin ID publishes the app bundle. Idempotency of repeated
// calls is the API edge's (SRV-005).
func (s *Service) Publish(ctx context.Context, p auth.Principal, r PublishRequest) (PublishJob, error) {
	if err := authorize(p, auth.ReleasePublish, r.AppID); err != nil {
		return PublishJob{}, err
	}
	if s.o.Jobs == nil {
		return PublishJob{}, errors.New("release: no job queue to publish with")
	}
	if len(r.Label) > 64 || len(r.Notes) > 10000 {
		return PublishJob{}, plxerr.New(plxerr.OutOfRange, "a label is at most 64 characters and notes at most 10,000")
	}
	var out PublishJob
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, env, err := s.environment(ctx, q, r.AppID, r.EnvironmentID)
		if err != nil {
			return err
		}
		frozen, err := s.o.Documents.FreezeDraft(ctx, tx, r.AppID, r.PluginID, r.Revision)
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		var plugin pgtype.UUID
		if r.PluginID != "" {
			plugin = storage.MustUUID(r.PluginID)
		}
		row, err := q.CreatePublishJob(ctx, dbgen.CreatePublishJobParams{
			ID: storage.MustUUID(id), OrganizationID: storage.MustUUID(p.OrganizationID), AppID: app, PluginID: plugin,
			EnvironmentID: env.ID, Revision: frozen.Revision, SnapshotID: storage.MustUUID(frozen.SnapshotID),
			Label: r.Label, Notes: r.Notes, AcknowledgeWarnings: r.AcknowledgeWarnings,
			ActorKind: p.Kind, ActorID: p.ID, ActorDisplay: p.Display,
		})
		if err != nil {
			return failure(err, "publish job")
		}
		if err := s.o.Jobs.Enqueue(ctx, tx, Job{OrganizationID: p.OrganizationID, JobID: id}); err != nil {
			return fmt.Errorf("release: enqueue the publish: %w", err)
		}
		out = jobOf(row)
		return nil
	})
	return out, err
}

// environment resolves an app and one of its environments.
func (*Service) environment(ctx context.Context, q *dbgen.Queries, appID, envID string) (pgtype.UUID, dbgen.Environment, error) {
	app, err := parseID(appID, "app")
	if err != nil {
		return pgtype.UUID{}, dbgen.Environment{}, err
	}
	eid, err := parseID(envID, "environment")
	if err != nil {
		return pgtype.UUID{}, dbgen.Environment{}, err
	}
	env, err := q.GetEnvironment(ctx, eid)
	if err != nil {
		return pgtype.UUID{}, dbgen.Environment{}, failure(err, "environment")
	}
	if env.AppID != app {
		return pgtype.UUID{}, dbgen.Environment{}, plxerr.New(plxerr.ResourceNotFound, "no such environment in this app")
	}
	return app, env, nil
}

// GetPublishJob returns a publish job.
func (s *Service) GetPublishJob(ctx context.Context, p auth.Principal, jobID string) (PublishJob, error) {
	var out PublishJob
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.jobRow(ctx, dbgen.New(tx), p, jobID)
		out = jobOf(row)
		return err
	})
	return out, err
}

// jobRow reads a job the principal may see.
func (*Service) jobRow(ctx context.Context, q *dbgen.Queries, p auth.Principal, jobID string) (dbgen.PublishJob, error) {
	id, err := parseID(jobID, "publish job")
	if err != nil {
		return dbgen.PublishJob{}, err
	}
	row, err := q.GetPublishJob(ctx, id)
	if err != nil {
		return dbgen.PublishJob{}, failure(err, "publish job")
	}
	if err := authorize(p, auth.PluginRead, storage.ID(row.AppID)); err != nil {
		return dbgen.PublishJob{}, plxerr.New(plxerr.ResourceNotFound, "no such publish job")
	}
	return row, nil
}

// ListPublishJobs lists an app's publish jobs, or one plugin's, newest
// first.
func (s *Service) ListPublishJobs(ctx context.Context, p auth.Principal, appID, pluginID string, before storage.Cursor, size int32) ([]PublishJob, error) {
	if err := authorize(p, auth.PluginRead, appID); err != nil {
		return nil, err
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	var plugin pgtype.UUID
	if pluginID != "" {
		if plugin, err = parseID(pluginID, "plugin"); err != nil {
			return nil, err
		}
	}
	var out []PublishJob
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		t := before.Time
		if t.IsZero() {
			t = s.now().Add(time.Hour)
		}
		rows, err := dbgen.New(tx).ListPublishJobs(ctx, dbgen.ListPublishJobsParams{
			AppID: app, PluginID: plugin, BeforeTime: storage.Timestamp(t), BeforeID: maxUUID(before), PageSize: size,
		})
		if err != nil {
			return failure(err, "publish job")
		}
		for _, r := range rows {
			out = append(out, jobOf(r))
		}
		return nil
	})
	return out, err
}

// maxUUID is the cursor's identifier, or the largest one for a first
// page ordered descending.
func maxUUID(c storage.Cursor) pgtype.UUID {
	if c.ID == "" {
		var u pgtype.UUID
		for i := range u.Bytes {
			u.Bytes[i] = 0xff
		}
		u.Valid = true
		return u
	}
	return storage.MustUUID(c.ID)
}

// CancelPublishJob cancels a job that has not recorded its version.
func (s *Service) CancelPublishJob(ctx context.Context, p auth.Principal, jobID string) (PublishJob, error) {
	var out PublishJob
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := s.jobRow(ctx, q, p, jobID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.ReleasePublish, storage.ID(row.AppID)); err != nil {
			return err
		}
		cancelled, err := q.CancelPublishJob(ctx, row.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return plxerr.New(plxerr.PreconditionFailed, "the publish has already finished")
		}
		if err != nil {
			return failure(err, "publish job")
		}
		out = jobOf(cancelled)
		return s.record(ctx, tx, p, audit.Entry{Action: audit.PublishCancelled, TargetKind: "publish", TargetID: jobID})
	})
	return out, err
}

// compiled is the outcome of compiling a publish's sources.
type compiled struct {
	result  *compiler.Result
	sources []string
	fsys    fstest.MapFS
	// key is the published plugin's key; "" for the app bundle.
	key string
}

// RunPublish runs a publish job in the worker (SRV-050): it gathers the
// sources, compiles, checks the diagnostics, signs and stores the
// bundle, and records the version. A diagnostic of severity error, or an
// unacknowledged warning, fails the job with nothing recorded (SRV-051).
// An error returned is one worth retrying; the job stays running.
func (s *Service) RunPublish(ctx context.Context, job Job) error {
	if s.o.Signer == nil {
		return errors.New("release: publishing needs the worker's signer")
	}
	system := auth.System(job.OrganizationID)
	row, env, err := s.startJob(ctx, system, job.JobID)
	if err != nil || !row.ID.Valid {
		return err
	}
	c, err := s.compileJob(ctx, system, row)
	if err != nil {
		return err
	}
	if err := s.progress(ctx, system, row, "check"); err != nil {
		return err
	}
	diags := c.result.Diagnostics
	if diags.HasErrors() {
		return s.finish(ctx, system, row, StateFailed, diags, 0)
	}
	if !row.AcknowledgeWarnings && slices.ContainsFunc(diags, func(d plxerr.Diagnostic) bool { return d.Severity == plxerr.SeverityWarning }) {
		d := plxerr.NewDiagnostic(plxerr.WarningsNotAcknowledged, plxerr.Location{}, "the publish found warnings; acknowledge them to publish")
		return s.finish(ctx, system, row, StateFailed, append(diags, d), 0)
	}
	b := pickBundle(c.result, c.key)
	if b == nil {
		return s.finish(ctx, system, row, StateFailed, append(diags, plxerr.NewDiagnostic(plxerr.PluginNotPublished, plxerr.Location{}, "the compilation produced no bundle for this draft")), 0)
	}
	if err := s.progress(ctx, system, row, "sign"); err != nil {
		return err
	}
	sig, keyID, err := s.o.Signer.Sign(ctx, env.SigningKeyRef, b.Hash[:])
	if err != nil {
		return fmt.Errorf("release: sign: %w", err)
	}
	if err := s.progress(ctx, system, row, "store"); err != nil {
		return err
	}
	var mapSum []byte
	if err := s.store(ctx, objects.KindBundle, b.Hash[:], b.Data, bundle.MediaType); err != nil {
		return err
	}
	if b.SourceMap != nil {
		sum := sha256.Sum256(b.SourceMap)
		mapSum = sum[:]
		if err := s.store(ctx, objects.KindBundle, sum[:], b.SourceMap, "application/octet-stream"); err != nil {
			return err
		}
	}
	if err := s.progress(ctx, system, row, "record"); err != nil {
		return err
	}
	return s.recordVersion(ctx, system, row, b, c, versionSignature{sig: sig, keyID: keyID, mapSum: mapSum}, diags)
}

// versionSignature is how a version was signed.
type versionSignature struct {
	sig    []byte
	keyID  string
	mapSum []byte
}

// startJob marks a job running and returns it with its environment; a
// job that has finished, or was cancelled, returns an invalid row.
func (s *Service) startJob(ctx context.Context, p auth.Principal, jobID string) (dbgen.PublishJob, dbgen.Environment, error) {
	var (
		row dbgen.PublishJob
		env dbgen.Environment
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		id, err := parseID(jobID, "publish job")
		if err != nil {
			return err
		}
		r, err := q.ProgressPublishJob(ctx, dbgen.ProgressPublishJobParams{ID: id, Stage: stages[0].name, Percent: stages[0].percent})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // finished or cancelled: nothing to do
		}
		if err != nil {
			return failure(err, "publish job")
		}
		if env, err = q.GetEnvironment(ctx, r.EnvironmentID); err != nil {
			return failure(err, "environment")
		}
		row = r
		return nil
	})
	return row, env, err
}

// progress moves a job to a stage.
func (s *Service) progress(ctx context.Context, p auth.Principal, row dbgen.PublishJob, stage string) error {
	var percent int32
	for _, st := range stages {
		if st.name == stage {
			percent = st.percent
		}
	}
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		_, err := dbgen.New(tx).ProgressPublishJob(ctx, dbgen.ProgressPublishJobParams{ID: row.ID, Stage: stage, Percent: percent})
		if errors.Is(err, pgx.ErrNoRows) {
			return errCancelled
		}
		return err //nolint:wrapcheck // examined by the caller
	})
}

// errCancelled stops a job that was cancelled while it ran.
var errCancelled = errors.New("release: the publish was cancelled")

// compileJob gathers a publish's sources and compiles them. The draft
// being published is at its frozen snapshot; the app-level documents and
// every other plugin are at their newest published versions, or their
// current drafts if they have none, so the version is compiled against
// what a release would hold (REL-003).
func (s *Service) compileJob(ctx context.Context, p auth.Principal, row dbgen.PublishJob) (compiled, error) {
	var out compiled
	appID := storage.ID(row.AppID)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		sources, err := s.sourcesFor(ctx, tx, row)
		if err != nil {
			return err
		}
		fsys, err := s.o.Documents.SourceFiles(ctx, tx, appID, sources)
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		opts, err := s.compileOptions(ctx, tx, p.OrganizationID, appID, storage.ID(row.PluginID))
		if err != nil {
			return err
		}
		out = compiled{sources: sources, fsys: fsys}
		if row.PluginID.Valid {
			pl, err := dbgen.New(tx).GetPlugin(ctx, row.PluginID)
			if err != nil {
				return failure(err, "plugin")
			}
			out.key = pl.Key
		}
		out.result = compiler.Compile(fsys, opts)
		return nil
	})
	if err != nil {
		return compiled{}, err
	}
	return out, s.progress(ctx, p, row, "compile")
}

// sourcesFor lists the snapshots a publish compiles.
func (s *Service) sourcesFor(ctx context.Context, tx pgx.Tx, row dbgen.PublishJob) ([]string, error) {
	q := dbgen.New(tx)
	appID := storage.ID(row.AppID)
	sources := []string{}
	pick := func(pluginID string, plugin pgtype.UUID) error {
		if plugin == row.PluginID {
			sources = append(sources, storage.ID(row.SnapshotID))
			return nil
		}
		v, err := q.LatestVersion(ctx, dbgen.LatestVersionParams{AppID: row.AppID, PluginID: plugin})
		if err == nil {
			sources = append(sources, storage.ID(v.SourceSnapshotID))
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return failure(err, "version")
		}
		frozen, err := s.o.Documents.FreezeDraft(ctx, tx, appID, pluginID, 0)
		if code, ok := plxerr.CodeOf(err); ok && code == plxerr.PreconditionFailed {
			return nil // an empty draft contributes nothing
		}
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		sources = append(sources, frozen.SnapshotID)
		return nil
	}
	if err := pick("", pgtype.UUID{}); err != nil {
		return nil, err
	}
	plugins, err := q.ListAllPlugins(ctx, row.AppID)
	if err != nil {
		return nil, failure(err, "plugin")
	}
	for _, pl := range plugins {
		if err := pick(storage.ID(pl.ID), pl.ID); err != nil {
			return nil, err
		}
	}
	return sources, nil
}

// compileOptions are the options of a publish or release: release mode,
// the app's (or plugin's) limits, and the asset pipeline's variants.
func (s *Service) compileOptions(ctx context.Context, tx pgx.Tx, orgID, appID, pluginID string) (compiler.Options, error) {
	lim, err := s.o.Tenancy.Effective(ctx, tx, orgID, appID, pluginID)
	if err != nil {
		return compiler.Options{}, fmt.Errorf("release: %w", err)
	}
	variants, err := s.o.Documents.AssetVariants(ctx, tx, appID)
	if err != nil {
		return compiler.Options{}, err //nolint:wrapcheck // a domain error
	}
	opts := compiler.DefaultOptions()
	opts.Limits, opts.Version, opts.AssetVariants = lim, s.o.CompilerVersion, variants
	return opts, nil
}

// pickBundle returns the bundle of a plugin key, or the app bundle.
func pickBundle(res *compiler.Result, key string) *compiler.Bundle {
	if key == "" {
		return res.App
	}
	for _, b := range res.Plugins {
		if b.Key == key {
			return b
		}
	}
	return nil
}

// store puts an object under a hash: a bundle's is its bundle hash
// (BND-005), which versions and manifests name it by.
func (s *Service) store(ctx context.Context, kind objects.Kind, sum, data []byte, mediaType string) error {
	key, err := objects.Key(kind, hex.EncodeToString(sum))
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if _, err := s.o.Objects.Put(ctx, key, data, mediaType); err != nil {
		return fmt.Errorf("release: store: %w", err)
	}
	return nil
}

// recordVersion records a published version and finishes the job, in
// one transaction: until it commits, nothing of the publish is visible.
func (s *Service) recordVersion(ctx context.Context, p auth.Principal, row dbgen.PublishJob, b *compiler.Bundle, c compiled, sig versionSignature, diags plxerr.Diagnostics) error {
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		if _, err := q.LockApp(ctx, row.AppID); err != nil {
			return failure(err, "app")
		}
		current, err := q.GetPublishJobForUpdate(ctx, row.ID)
		if err != nil {
			return failure(err, "publish job")
		}
		if current.State != StateRunning {
			return nil // cancelled while it ran
		}
		id, next, err := s.insertVersion(ctx, q, row, b, c, sig)
		if err != nil {
			return err
		}
		if err := s.finishTx(ctx, q, row, StateSucceeded, diags, next); err != nil {
			return err
		}
		actor := auth.Principal{Identity: auth.Identity{Kind: row.ActorKind, ID: row.ActorID, Display: row.ActorDisplay}, OrganizationID: p.OrganizationID}
		return s.record(ctx, tx, actor, audit.Entry{
			Action: audit.VersionPublished, TargetKind: "version", TargetID: id,
			AfterHash: hex.EncodeToString(b.Hash[:]), Detail: fmt.Sprintf("%s version %d", keyOrApp(b.Key, row), next),
		})
	})
}

// insertVersion records the next version of a job's draft.
func (s *Service) insertVersion(ctx context.Context, q *dbgen.Queries, row dbgen.PublishJob, b *compiler.Bundle, c compiled, sig versionSignature) (string, int64, error) {
	next, err := q.NextVersion(ctx, dbgen.NextVersionParams{AppID: row.AppID, PluginID: row.PluginID})
	if err != nil {
		return "", 0, failure(err, "version")
	}
	id, err := s.newID()
	if err != nil {
		return "", 0, err
	}
	sources := make([]pgtype.UUID, len(c.sources))
	for i, src := range c.sources {
		sources[i] = storage.MustUUID(src)
	}
	if _, err := q.InsertPluginVersion(ctx, dbgen.InsertPluginVersionParams{
		ID: storage.MustUUID(id), OrganizationID: row.OrganizationID, AppID: row.AppID, PluginID: row.PluginID,
		PluginKey: c.key, Version: next, Label: row.Label, Notes: row.Notes,
		BundleSha256: b.Hash[:], BundleSize: int64(len(b.Data)), SourceMapSha256: sig.mapSum,
		RequiredFeatures: nonNilStrings(b.Features), MinRuntime: minRuntime(c.fsys),
		Signature: sig.sig, KeyID: sig.keyID, Algorithm: signing.Algorithm, EnvironmentID: row.EnvironmentID,
		SourceSnapshotID: row.SnapshotID, Sources: sources,
		PublishedByKind: row.ActorKind, PublishedByID: row.ActorID, PublishedBy: row.ActorDisplay,
	}); err != nil {
		return "", 0, failure(err, "version")
	}
	if row.PluginID.Valid {
		if err := q.SetPluginVersion(ctx, dbgen.SetPluginVersionParams{ID: row.PluginID, LatestVersion: next}); err != nil {
			return "", 0, failure(err, "plugin")
		}
	}
	return id, next, nil
}

// keyOrApp names what was published.
func keyOrApp(key string, row dbgen.PublishJob) string {
	if row.PluginID.Valid {
		return "plugin " + key
	}
	return "app bundle"
}

// finish ends a job without a version.
func (s *Service) finish(ctx context.Context, p auth.Principal, row dbgen.PublishJob, state string, diags plxerr.Diagnostics, version int64) error {
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.finishTx(ctx, dbgen.New(tx), row, state, diags, version)
	})
}

// finishTx ends a job in the caller's transaction.
func (*Service) finishTx(ctx context.Context, q *dbgen.Queries, row dbgen.PublishJob, state string, diags plxerr.Diagnostics, version int64) error {
	encoded, err := json.Marshal(nonNilDiags(diags))
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	stage, percent := "done", int32(100)
	if state != StateSucceeded {
		stage, percent = "check", 60
	}
	if _, err := q.FinishPublishJob(ctx, dbgen.FinishPublishJobParams{
		ID: row.ID, State: state, Stage: stage, Percent: percent, Diagnostics: encoded, Version: version,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return failure(err, "publish job")
	}
	return nil
}

// minRuntime is the app's minimum runtime version from its sources.
func minRuntime(fsys fstest.MapFS) string {
	f, ok := fsys["app.json"]
	if !ok {
		return ""
	}
	var app struct {
		MinRuntimeVersion string `json:"minRuntimeVersion"`
	}
	_ = json.Unmarshal(f.Data, &app) //nolint:errcheck // validated by the compiler
	return app.MinRuntimeVersion
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilDiags(d plxerr.Diagnostics) plxerr.Diagnostics {
	if d == nil {
		return plxerr.Diagnostics{}
	}
	return d
}

// jobOf converts a stored job.
func jobOf(r dbgen.PublishJob) PublishJob {
	j := PublishJob{
		ID: storage.ID(r.ID), AppID: storage.ID(r.AppID), PluginID: storage.ID(r.PluginID),
		EnvironmentID: storage.ID(r.EnvironmentID), Revision: r.Revision, SnapshotID: storage.ID(r.SnapshotID),
		State: r.State, Stage: r.Stage, Percent: r.Percent, Version: r.Version,
		Actor:     audit.Actor{Kind: r.ActorKind, ID: r.ActorID, Display: r.ActorDisplay},
		CreatedAt: storage.Time(r.CreatedAt), FinishedAt: storage.Time(r.FinishedAt),
	}
	_ = json.Unmarshal(r.Diagnostics, &j.Diagnostics) //nolint:errcheck // written by this package
	return j
}
