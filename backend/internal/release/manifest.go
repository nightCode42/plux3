// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// ManifestLifetime is how long a signed manifest is valid; the worker
// signs a fresh one when fewer than ManifestRefresh remain (REL-031).
const (
	ManifestLifetime = 7 * 24 * time.Hour
	ManifestRefresh  = 2 * 24 * time.Hour
)

// manifestDepth bounds the manifest's JSON nesting.
const manifestDepth = 8

// ManifestJob asks the worker to sign a channel's manifest.
type ManifestJob struct {
	OrganizationID string `json:"organizationId"`
	ChannelID      string `json:"channelId"`
}

// Kind names the job.
func (ManifestJob) Kind() string { return "manifest.sign" }

// SignedManifest is the signed part of a manifest (REL-030, Appendix
// B.3). Its RFC 8785 canonical JSON is what the signatures cover. It
// carries the metadata fields of ADR-0004 so that the P6 roles can be
// added without changing it.
type SignedManifest struct {
	Type            string          `json:"type"`
	SpecVersion     int             `json:"specVersion"`
	Role            string          `json:"role"`
	App             string          `json:"app"`
	Environment     string          `json:"environment"`
	Channel         string          `json:"channel"`
	ReleaseSequence int64           `json:"releaseSequence"`
	IssuedAt        string          `json:"issuedAt"`
	Expires         string          `json:"expires"`
	AppBundle       SignedBundle    `json:"appBundle"`
	Plugins         []SignedPlugin  `json:"plugins"`
	Control         SignedControl   `json:"control"`
	Experiments     []SignedVariant `json:"experiments"`
}

// SignedBundle describes one bundle a device must end up with.
type SignedBundle struct {
	Hash             string   `json:"hash"`
	Size             int64    `json:"size"`
	RequiredFeatures []string `json:"requiredFeatures"`
	MinRuntime       string   `json:"minRuntime"`
}

// SignedPlugin is one plugin's version and bundle.
type SignedPlugin struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
	SignedBundle
}

// SignedControl holds the switches a device obeys.
type SignedControl struct {
	KillSwitches  []string `json:"killSwitches"`
	AppKillSwitch bool     `json:"appKillSwitch"`
	Mandatory     bool     `json:"mandatory"`
	Message       string   `json:"message"`
}

// SignedVariant is one experiment assignment; none exist until P9.
type SignedVariant struct {
	Key     string `json:"key"`
	Layer   string `json:"layer"`
	Variant string `json:"variant"`
}

// ManifestSignature is one signature over the signed part.
type ManifestSignature struct {
	KeyID     string `json:"keyid"`
	Algorithm string `json:"alg"`
	Signature string `json:"sig"`
}

// hashRef renders a bundle hash as the manifest names it.
func hashRef(sum []byte) string { return "sha256:" + hex.EncodeToString(sum) }

// parseHashRef reads a "sha256:<hex>" reference.
func parseHashRef(s string) ([]byte, bool) {
	h, ok := strings.CutPrefix(s, "sha256:")
	if !ok {
		return nil, false
	}
	b, err := hex.DecodeString(h)
	return b, err == nil && len(b) == sha256.Size
}

// SignManifest signs the manifest of a channel's current release and its
// controls, and stores it for the api role to serve (SRV-052, REL-031).
// A production environment refuses a backend that keeps keys on disk
// (SEC-056).
func (s *Service) SignManifest(ctx context.Context, job ManifestJob) error {
	if s.o.Signer == nil {
		return errors.New("release: this role cannot sign")
	}
	ch, err := storage.UUID(job.ChannelID)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: job.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck // InTx wraps its own failures
		q := dbgen.New(tx)
		channel, err := q.GetChannelByID(ctx, ch)
		if err != nil {
			return failure(err, "channel")
		}
		if channel.ReleaseSequence == 0 {
			return nil // nothing to sign until a release is promoted
		}
		env, err := q.GetEnvironment(ctx, channel.EnvironmentID)
		if err != nil {
			return failure(err, "environment")
		}
		if env.Production && !s.o.ProductionSigning {
			return plxerr.New(plxerr.PermissionDenied, "the signing backend keeps keys on disk and cannot sign for production environment %s (SEC-056)", env.Key)
		}
		return s.signAndStore(ctx, q, channel, env)
	})
}

// signAndStore builds, signs and stores a channel's manifest, and
// records the public key it was signed with.
func (s *Service) signAndStore(ctx context.Context, q *dbgen.Queries, channel dbgen.Channel, env dbgen.Environment) error {
	doc, err := s.manifestDocument(ctx, q, channel, env)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	signed, err := jcs.Canonicalize(plain, manifestDepth)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	sig, keyID, err := s.o.Signer.Sign(ctx, env.SigningKeyRef, signed)
	if err != nil {
		return fmt.Errorf("release: sign the manifest: %w", err)
	}
	pub, _, err := s.o.Signer.PublicKey(ctx, env.SigningKeyRef)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if err := q.UpsertEnvironmentKey(ctx, dbgen.UpsertEnvironmentKeyParams{
		EnvironmentID: env.ID, OrganizationID: env.OrganizationID, KeyID: keyID, Algorithm: signing.Algorithm, PublicKey: pub,
	}); err != nil {
		return failure(err, "environment key")
	}
	sigs, err := json.Marshal([]ManifestSignature{{KeyID: keyID, Algorithm: signing.Algorithm, Signature: base64.StdEncoding.EncodeToString(sig)}})
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return err
	}
	issued, _ := time.Parse(time.RFC3339, doc.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, doc.Expires)
	if _, err := q.InsertManifest(ctx, dbgen.InsertManifestParams{
		ID: storage.MustUUID(id), OrganizationID: channel.OrganizationID, ChannelID: channel.ID,
		ReleaseSequence: channel.ReleaseSequence, Signed: signed, Signatures: sigs,
		IssuedAt: storage.Timestamp(issued), ExpiresAt: storage.Timestamp(expires),
	}); err != nil {
		return failure(err, "manifest")
	}
	return nil
}

// manifestDocument builds the signed part for a channel.
func (s *Service) manifestDocument(ctx context.Context, q *dbgen.Queries, ch dbgen.Channel, env dbgen.Environment) (SignedManifest, error) {
	rel, err := q.GetRelease(ctx, dbgen.GetReleaseParams{AppID: env.AppID, Sequence: ch.ReleaseSequence})
	if err != nil {
		return SignedManifest{}, failure(err, "release")
	}
	appVersion, err := q.GetVersionByID(ctx, rel.AppVersionID)
	if err != nil {
		return SignedManifest{}, failure(err, "version")
	}
	versions, err := q.ListReleaseVersions(ctx, rel.ID)
	if err != nil {
		return SignedManifest{}, failure(err, "version")
	}
	control, err := s.controlRow(ctx, q, ch)
	if err != nil {
		return SignedManifest{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	doc := SignedManifest{
		Type: "manifest", SpecVersion: 1, Role: "targets",
		App: storage.ID(env.AppID), Environment: env.Key, Channel: ch.Key, ReleaseSequence: rel.Sequence,
		IssuedAt: now.Format(time.RFC3339), Expires: now.Add(ManifestLifetime).Format(time.RFC3339),
		AppBundle: signedBundle(appVersion),
		Plugins:   []SignedPlugin{},
		Control: SignedControl{
			KillSwitches: slices.Sorted(slices.Values(control.KillSwitchPlugins)), AppKillSwitch: control.AppKillSwitch,
			Mandatory: control.MandatoryUpdate, Message: control.Message,
		},
		Experiments: []SignedVariant{},
	}
	for _, v := range versions {
		if v.PluginKey == "" {
			continue
		}
		doc.Plugins = append(doc.Plugins, SignedPlugin{Key: v.PluginKey, Version: v.Version, SignedBundle: signedBundle(v)})
	}
	slices.SortFunc(doc.Plugins, func(a, b SignedPlugin) int { return strings.Compare(a.Key, b.Key) })
	return doc, nil
}

func signedBundle(v dbgen.PluginVersion) SignedBundle {
	features := slices.Clone(v.RequiredFeatures)
	if features == nil {
		features = []string{}
	}
	slices.Sort(features)
	return SignedBundle{Hash: hashRef(v.BundleSha256), Size: v.BundleSize, RequiredFeatures: features, MinRuntime: v.MinRuntime}
}

// RefreshManifests signs a fresh manifest for every channel of an
// organisation whose newest one expires within ManifestRefresh, or that
// has none. The worker's maintenance sweep runs it.
func (s *Service) RefreshManifests(ctx context.Context, org string) (int, error) {
	if s.o.Signer == nil {
		return 0, nil
	}
	var channels []dbgen.Channel
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		channels, err = dbgen.New(tx).ExpiringChannels(ctx, storage.Timestamp(s.now().Add(ManifestRefresh)))
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("release: %w", err)
	}
	var errs []error
	for _, c := range channels {
		errs = append(errs, s.SignManifest(ctx, ManifestJob{OrganizationID: org, ChannelID: storage.ID(c.ID)}))
	}
	return len(channels), errors.Join(errs...)
}

// PurgeManifests deletes expired manifests, keeping each channel's
// newest.
func (s *Service) PurgeManifests(ctx context.Context, org string) (int64, error) {
	var n int64
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = dbgen.New(tx).PurgeManifests(ctx, storage.Timestamp(s.now()))
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("release: %w", err)
	}
	return n, nil
}

// enqueueManifest asks the worker to sign a channel's manifest, in the
// transaction that changed what it says.
func (s *Service) enqueueManifest(ctx context.Context, tx pgx.Tx, ch dbgen.Channel) error {
	if s.o.Jobs == nil {
		return nil
	}
	if err := s.o.Jobs.Enqueue(ctx, tx, ManifestJob{OrganizationID: storage.ID(ch.OrganizationID), ChannelID: storage.ID(ch.ID)}); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}
