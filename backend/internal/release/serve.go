// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/delta"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// Precomputation defaults (REL-022): deltas from the newest RecentDeltas
// versions and from the PopularDeltas bundles most devices hold.
const (
	RecentDeltas  = 10
	PopularDeltas = 5
)

// DeltaMediaType is the media type of a stored delta.
const DeltaMediaType = "application/vnd.plux.delta"

// urlLifetime is how long a signed download URL stays valid.
const urlLifetime = 24 * time.Hour

// Sync actions (REL-032).
const (
	SyncKeep  = "keep"
	SyncDelta = "delta"
	SyncFull  = "full"
)

// DeltaJob asks the worker to precompute the deltas to a new bundle.
type DeltaJob struct {
	OrganizationID string `json:"organizationId"`
	AppID          string `json:"appId"`
	PluginKey      string `json:"pluginKey"`
	ToSHA256       string `json:"toSha256"`
}

// Kind names the job.
func (DeltaJob) Kind() string { return "delta.precompute" }

// ManifestRequest is what a device sends (REL-032). The organisation,
// app and environment come from its credential, not from the request.
type ManifestRequest struct {
	OrganizationID, AppID, EnvironmentID, Channel string
	InstalledSequence                             int64
	// Installed maps a plugin key, or "" for the app bundle, to the hash
	// of the bundle the device holds.
	Installed   map[string][]byte
	IfNoneMatch string
}

// SyncStep tells a device how to obtain one bundle.
type SyncStep struct {
	Action string
	// From is the installed bundle a delta applies to.
	From string
	URL  string
	Size int64
}

// ServedManifest is a manifest with the plan for one device.
type ServedManifest struct {
	NotModified bool
	ETag        string
	// Signed is the canonical JSON the signatures cover.
	Signed     []byte
	Document   SignedManifest
	Signatures []ManifestSignature
	// Plan holds a step per plugin key, and "" for the app bundle.
	Plan map[string]SyncStep
	// URLs holds the full bundle's location per key.
	URLs map[string]string
}

// GetManifest returns the newest signed manifest of a channel with the
// sync plan for the device's installed bundles (REL-030–REL-033). The
// same manifest and installed bundles always give the same answer; the
// ETag covers both, so an unchanged device costs one small response.
func (s *Service) GetManifest(ctx context.Context, r ManifestRequest) (ServedManifest, error) {
	var (
		row dbgen.Manifest
		out ServedManifest
	)
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: r.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		ch, err := q.GetChannel(ctx, dbgen.GetChannelParams{EnvironmentID: storage.MustUUID(r.EnvironmentID), Key: channelOrDefault(r.Channel)})
		if err != nil {
			return failure(err, "channel")
		}
		if row, err = q.LatestManifest(ctx, ch.ID); err != nil {
			return failure(err, "manifest")
		}
		return nil
	})
	if err != nil {
		return ServedManifest{}, fmt.Errorf("release: %w", err)
	}
	if err := json.Unmarshal(row.Signed, &out.Document); err != nil {
		return ServedManifest{}, fmt.Errorf("release: a stored manifest: %w", err)
	}
	if err := json.Unmarshal(row.Signatures, &out.Signatures); err != nil {
		return ServedManifest{}, fmt.Errorf("release: a stored manifest: %w", err)
	}
	out.Signed = row.Signed
	targets := map[string]SignedBundle{"": out.Document.AppBundle}
	for _, p := range out.Document.Plugins {
		targets[p.Key] = p.SignedBundle
	}
	out.ETag = etag(row.ID.Bytes[:], targets, r.Installed)
	if r.IfNoneMatch != "" && r.IfNoneMatch == out.ETag {
		return ServedManifest{NotModified: true, ETag: out.ETag}, nil
	}
	out.Plan = make(map[string]SyncStep, len(targets))
	out.URLs = make(map[string]string, len(targets))
	for _, key := range slices.Sorted(mapsKeys(targets)) {
		step, full, err := s.plan(ctx, r.OrganizationID, targets[key], r.Installed[key])
		if err != nil {
			return ServedManifest{}, err
		}
		out.Plan[key], out.URLs[key] = step, full
	}
	return out, nil
}

// plan chooses how a device gets from what it holds to the target
// bundle: keep it, apply a delta, or download the bundle (REL-023).
func (s *Service) plan(ctx context.Context, org string, target SignedBundle, installed []byte) (SyncStep, string, error) {
	to, ok := parseHashRef(target.Hash)
	if !ok {
		return SyncStep{}, "", fmt.Errorf("release: a stored manifest names %q", target.Hash)
	}
	full, err := s.objectURL(ctx, objects.KindBundle, to)
	if err != nil {
		return SyncStep{}, "", err
	}
	if bytes.Equal(installed, to) {
		return SyncStep{Action: SyncKeep}, full, nil
	}
	fullStep := SyncStep{Action: SyncFull, URL: full, Size: target.Size}
	if len(installed) != sha256.Size {
		return fullStep, full, nil
	}
	d, found, err := s.Delta(ctx, org, installed, to)
	if err != nil || !found || !delta.Worthwhile(d.Size, d.FullSize) {
		return fullStep, full, err
	}
	u, err := s.objectURL(ctx, objects.KindDelta, d.DeltaSha256)
	if err != nil {
		return SyncStep{}, "", err
	}
	return SyncStep{Action: SyncDelta, From: hashRef(installed), URL: u, Size: d.Size}, full, nil
}

// etag identifies a manifest together with the plan it implies.
func etag(manifestID []byte, targets map[string]SignedBundle, installed map[string][]byte) string {
	h := sha256.New()
	h.Write(manifestID)
	for _, k := range slices.Sorted(mapsKeys(targets)) {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(installed[k])
		h.Write([]byte{0})
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

func mapsKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// objectURL is where a device downloads an object: the store's own
// location when it has one (a CDN or a signed URL), else this server's
// object endpoint (DEP-041).
func (s *Service) objectURL(ctx context.Context, kind objects.Kind, sum []byte) (string, error) {
	key, err := objects.Key(kind, hex.EncodeToString(sum))
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	u, err := s.o.Objects.URL(ctx, key, urlLifetime)
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	if u == "" {
		u = strings.TrimSuffix(s.o.PublicBaseURL, "/") + ObjectsPath + key
	}
	return u, nil
}

// ObjectsPath is where the server serves content-addressed objects
// itself when no CDN is configured.
const ObjectsPath = "/v1/objects/"

// Delta returns the delta between two bundles of an organisation,
// computing and storing it on first request. Concurrent requests for
// the same pair compute it once (REL-022); found is false when the old
// bundle is not one this organisation published.
func (s *Service) Delta(ctx context.Context, org string, from, to []byte) (dbgen.Delta, bool, error) {
	var row dbgen.Delta
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = dbgen.New(tx).GetDelta(ctx, dbgen.GetDeltaParams{FromSha256: from, ToSha256: to})
		return err //nolint:wrapcheck // translated below
	})
	if err == nil {
		return row, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Delta{}, false, fmt.Errorf("release: %w", err)
	}
	key := org + "/" + hex.EncodeToString(from) + "/" + hex.EncodeToString(to)
	return s.flights.do(key, func() (dbgen.Delta, bool, error) { return s.computeDelta(ctx, org, from, to) })
}

// computeDelta builds, stores and records one delta.
func (s *Service) computeDelta(ctx context.Context, org string, from, to []byte) (dbgen.Delta, bool, error) {
	var owned bool
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := dbgen.New(tx).CountVersionsWithBundle(ctx, from)
		owned = n > 0
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return dbgen.Delta{}, false, fmt.Errorf("release: %w", err)
	}
	if !owned {
		return dbgen.Delta{}, false, nil
	}
	oldBytes, err := s.load(ctx, from)
	if err != nil {
		return dbgen.Delta{}, false, err
	}
	newBytes, err := s.load(ctx, to)
	if err != nil {
		return dbgen.Delta{}, false, err
	}
	d, err := delta.Diff(oldBytes, newBytes)
	if err != nil {
		return dbgen.Delta{}, false, fmt.Errorf("release: %w", err)
	}
	compressed, err := bundle.Compress(newBytes)
	if err != nil {
		return dbgen.Delta{}, false, fmt.Errorf("release: %w", err)
	}
	sum := sha256.Sum256(d)
	if err := s.store(ctx, objects.KindDelta, sum[:], d, DeltaMediaType); err != nil {
		return dbgen.Delta{}, false, err
	}
	row := dbgen.Delta{
		OrganizationID: storage.MustUUID(org), FromSha256: from, ToSha256: to, DeltaSha256: sum[:],
		Size: int64(len(d)), FullSize: int64(len(compressed)),
	}
	err = s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		return dbgen.New(tx).InsertDelta(ctx, dbgen.InsertDeltaParams{ //nolint:wrapcheck // one statement
			OrganizationID: row.OrganizationID, FromSha256: from, ToSha256: to, DeltaSha256: row.DeltaSha256,
			Size: row.Size, FullSize: row.FullSize,
		})
	})
	if err != nil {
		return dbgen.Delta{}, false, fmt.Errorf("release: %w", err)
	}
	return row, true, nil
}

// load reads a stored bundle.
func (s *Service) load(ctx context.Context, sum []byte) ([]byte, error) {
	key, err := objects.Key(objects.KindBundle, hex.EncodeToString(sum))
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	data, _, err := s.o.Objects.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("release: read bundle: %w", err)
	}
	return data, nil
}

// PrecomputeDeltas computes the deltas to a newly published bundle from
// the plugin's newest versions and from the bundles most devices hold
// (REL-022). The worker runs it after a publish.
func (s *Service) PrecomputeDeltas(ctx context.Context, job DeltaJob) error {
	to, err := hex.DecodeString(job.ToSHA256)
	if err != nil || len(to) != sha256.Size {
		return fmt.Errorf("release: a delta job names %q", job.ToSHA256)
	}
	var froms [][]byte
	err = s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: job.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app := storage.MustUUID(job.AppID)
		recent, err := q.RecentVersionBundles(ctx, dbgen.RecentVersionBundlesParams{AppID: app, PluginKey: job.PluginKey, N: RecentDeltas + 1})
		if err != nil {
			return err //nolint:wrapcheck // translated below
		}
		popular, err := q.PopularBundles(ctx, dbgen.PopularBundlesParams{AppID: app, PluginKey: job.PluginKey, N: PopularDeltas})
		if err != nil {
			return err //nolint:wrapcheck // translated below
		}
		froms = recent
		for _, p := range popular {
			froms = append(froms, p.BundleSha256)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	seen := map[string]bool{string(to): true}
	var errs []error
	for _, from := range froms {
		if seen[string(from)] {
			continue
		}
		seen[string(from)] = true
		_, _, err := s.Delta(ctx, job.OrganizationID, from, to)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RootKey is a public key devices verify manifests and bundles with.
type RootKey struct {
	KeyID, Algorithm, Role string
	PublicKey              []byte
}

// RootKeys returns the keys an environment has signed with (SEC-051).
// They are public; a host project embeds them (CLI-004).
func (s *Service) RootKeys(ctx context.Context, org, envID string) ([]RootKey, error) {
	var out []RootKey
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListEnvironmentKeys(ctx, storage.MustUUID(envID))
		if err != nil {
			return fmt.Errorf("release: %w", err)
		}
		for _, r := range rows {
			out = append(out, RootKey{KeyID: r.KeyID, Algorithm: r.Algorithm, Role: "targets", PublicKey: r.PublicKey})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	return out, nil
}

// enqueueDeltas asks the worker to precompute the deltas to a version.
func (s *Service) enqueueDeltas(ctx context.Context, tx pgx.Tx, org, app, pluginKey string, to []byte) error {
	if s.o.Jobs == nil {
		return nil
	}
	if err := s.o.Jobs.Enqueue(ctx, tx, DeltaJob{OrganizationID: org, AppID: app, PluginKey: pluginKey, ToSHA256: hex.EncodeToString(to)}); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// flightGroup runs one computation per key at a time; later callers for
// the same key wait for the first one's result.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flight
}

type flight struct {
	done  chan struct{}
	row   dbgen.Delta
	found bool
	err   error
}

func (g *flightGroup) do(key string, f func() (dbgen.Delta, bool, error)) (dbgen.Delta, bool, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.row, c.found, c.err
	}
	c := &flight{done: make(chan struct{})}
	if g.calls == nil {
		g.calls = map[string]*flight{}
	}
	g.calls[key] = c
	g.mu.Unlock()
	c.row, c.found, c.err = f()
	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	close(c.done)
	return c.row, c.found, c.err
}
