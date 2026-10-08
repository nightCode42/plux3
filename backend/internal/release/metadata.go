// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// MetadataOptions configures the update metadata (SEC-050, ADR-0054).
type MetadataOptions struct {
	// Expiry is how long each role is valid. A zero field takes the
	// specification's default, except Targets: zero keeps the manifest's
	// own lifetime, ManifestLifetime.
	Expiry updatemeta.Expiry
	// RootThreshold is the least root threshold a production
	// environment's root may ask for; zero is 2.
	RootThreshold int
	// SnapshotKeyPrefix and TimestampKeyPrefix name the online keys
	// "<prefix>-<environment ID>", as the targets key is named; empty is
	// "snapshot" and "timestamp".
	SnapshotKeyPrefix, TimestampKeyPrefix string
}

// withDefaults fills what an operator left out.
func (o MetadataOptions) withDefaults() MetadataOptions {
	d := updatemeta.DefaultExpiry()
	if o.Expiry.Timestamp <= 0 {
		o.Expiry.Timestamp = d.Timestamp
	}
	if o.Expiry.Snapshot <= 0 {
		o.Expiry.Snapshot = d.Snapshot
	}
	if o.Expiry.Root <= 0 {
		o.Expiry.Root = d.Root
	}
	if o.RootThreshold <= 0 {
		o.RootThreshold = 2
	}
	if o.SnapshotKeyPrefix == "" {
		o.SnapshotKeyPrefix = updatemeta.RoleSnapshot
	}
	if o.TimestampKeyPrefix == "" {
		o.TimestampKeyPrefix = updatemeta.RoleTimestamp
	}
	return o
}

// Fractions of a role's lifetime that may remain before the worker signs
// a fresh one: the timestamp at half, the snapshot at a quarter, so that
// an hourly sweep always signs well before expiry (SEC-050).
const (
	timestampRefreshDivisor = 2
	snapshotRefreshDivisor  = 4
)

// manifestRefresh is how long before its expiry a manifest is re-signed.
func (s *Service) manifestRefresh() time.Duration {
	if lifetime := s.o.Metadata.Expiry.Targets; lifetime > 0 {
		return lifetime / snapshotRefreshDivisor
	}
	return ManifestRefresh
}

// MetadataRef names the versions of the update metadata that are current
// for an environment; a role with no file is 0.
type MetadataRef struct {
	Root, Snapshot, Timestamp int64
}

// environmentType is the type a key of the environment belongs to
// (SEC-056).
func environmentType(env dbgen.Environment) string {
	if env.Production {
		return updatemeta.EnvProduction
	}
	return updatemeta.EnvDevelopment
}

// currentRoot returns the environment's newest root, when an operator has
// uploaded one. Without a root the environment is not enrolled in update
// metadata and signs manifests as before (BND-000).
func currentRoot(ctx context.Context, q *dbgen.Queries, env dbgen.Environment) (updatemeta.Root, bool, error) {
	row, err := q.LatestMetadata(ctx, dbgen.LatestMetadataParams{EnvironmentID: env.ID, Role: updatemeta.RoleRoot})
	if errors.Is(err, pgx.ErrNoRows) {
		return updatemeta.Root{}, false, nil
	}
	if err != nil {
		return updatemeta.Root{}, false, failure(err, "root metadata")
	}
	d, err := updatemeta.ParseDocument(row.Document)
	if err != nil {
		return updatemeta.Root{}, false, fmt.Errorf("release: the stored root: %w", err)
	}
	root, err := updatemeta.ParseRoot(d.Signed)
	if err != nil {
		return updatemeta.Root{}, false, fmt.Errorf("release: the stored root: %w", err)
	}
	return root, true, nil
}

// onlineRefs are the signing-backend references of an environment's
// online keys.
type onlineRefs struct{ targets, snapshot, timestamp string }

// roleRef pairs an online role with its key reference.
type roleRef struct{ role, ref string }

// list returns the references in a fixed order.
func (r onlineRefs) list() []roleRef {
	return []roleRef{{updatemeta.RoleTargets, r.targets}, {updatemeta.RoleSnapshot, r.snapshot}, {updatemeta.RoleTimestamp, r.timestamp}}
}

func (s *Service) onlineRefs(env dbgen.Environment) onlineRefs {
	id := storage.ID(env.ID)
	return onlineRefs{
		targets:   env.SigningKeyRef,
		snapshot:  s.o.Metadata.SnapshotKeyPrefix + "-" + id,
		timestamp: s.o.Metadata.TimestampKeyPrefix + "-" + id,
	}
}

// checkOnlineKey checks that the root names this environment's key for a
// role, and that one signature meets the role's threshold, since the
// server signs with one online key. It records the public key for
// GetRootKeys.
func (s *Service) checkOnlineKey(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, root updatemeta.Root, role, ref string) error {
	pub, id, err := s.o.Signer.PublicKey(ctx, ref)
	if err != nil {
		return fmt.Errorf("release: the %s key: %w", role, err)
	}
	keys, _ := root.Roles.Named(role)
	switch {
	case !slices.Contains(keys.KeyIDs, id):
		return plxerr.New(plxerr.PreconditionFailed, "root %d does not list this environment's %s key %s; upload a root that does (SEC-050)", root.Version, role, id)
	case keys.Threshold > 1:
		return plxerr.New(plxerr.PreconditionFailed, "root %d asks %d signatures of the %s role; the server signs it with one online key", root.Version, keys.Threshold, role)
	}
	if err := q.UpsertEnvironmentKey(ctx, dbgen.UpsertEnvironmentKeyParams{
		EnvironmentID: env.ID, OrganizationID: env.OrganizationID, KeyID: id, Algorithm: signing.Algorithm, PublicKey: pub,
		Role: role, EnvironmentType: environmentType(env),
	}); err != nil {
		return failure(err, "environment key")
	}
	return nil
}

// checkRoot refuses to sign under a root that has expired, or that does
// not list this environment's online keys, and refuses a backend that
// keeps keys on disk for a production environment (SEC-056).
func (s *Service) checkRoot(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, root updatemeta.Root, at time.Time) (onlineRefs, error) {
	if env.Production && !s.o.ProductionSigning {
		return onlineRefs{}, plxerr.New(plxerr.PermissionDenied, "the signing backend keeps keys on disk and cannot sign metadata for production environment %s (SEC-056)", env.Key)
	}
	if exp, err := time.Parse(time.RFC3339, root.Expires); err != nil || !at.Before(exp) {
		return onlineRefs{}, plxerr.New(plxerr.UpdateMetadataInvalid, "root %d expired at %s; upload a new root (SEC-050)", root.Version, root.Expires)
	}
	refs := s.onlineRefs(env)
	for _, k := range refs.list() {
		if err := s.checkOnlineKey(ctx, q, env, root, k.role, k.ref); err != nil {
			return onlineRefs{}, err
		}
	}
	return refs, nil
}

// publishMetadata writes the metadata of a release: it takes the next
// targets version, signs a manifest at that version for every channel of
// the environment, and signs the snapshot that pins it and the timestamp
// that names the snapshot. A manifest for each channel keeps every one
// equal to the version the snapshot pins, the exact match that defeats a
// mix-and-match attack (SEC-050). The caller holds channel's lock.
func (s *Service) publishMetadata(ctx context.Context, q *dbgen.Queries, channel dbgen.Channel, env dbgen.Environment, root updatemeta.Root) error {
	at := s.now()
	// The row lock orders this against every other signing of the
	// environment, so versions are taken and written in one order.
	env, err := q.LockEnvironment(ctx, env.ID)
	if err != nil {
		return failure(err, "environment")
	}
	refs, err := s.checkRoot(ctx, q, env, root, at)
	if err != nil {
		return err
	}
	targets, err := q.BumpTargetsVersion(ctx, env.ID)
	if err != nil {
		return failure(err, "environment")
	}
	channels, err := q.ListChannelsOfEnvironment(ctx, env.ID)
	if err != nil {
		return failure(err, "channel")
	}
	for _, c := range channels {
		if c.ID == channel.ID {
			c = channel
		}
		if c.ReleaseSequence == 0 {
			continue
		}
		if err := s.signAndStore(ctx, q, c, env, targets); err != nil {
			return err
		}
	}
	return s.signSnapshot(ctx, q, env, refs, targets, at)
}

// nextVersion is the version after the newest file of a role.
func nextVersion(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, role string) (int64, error) {
	row, err := q.LatestMetadata(ctx, dbgen.LatestMetadataParams{EnvironmentID: env.ID, Role: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, failure(err, "metadata")
	}
	return row.Version + 1, nil
}

// storeMetadata records a signed file.
func storeMetadata(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, role string, version int64, doc []byte, at, expires time.Time) error {
	sum := sha256.Sum256(doc)
	err := q.InsertMetadata(ctx, dbgen.InsertMetadataParams{
		EnvironmentID: env.ID, OrganizationID: env.OrganizationID, Role: role, Version: version,
		Document: doc, Sha256: sum[:], IssuedAt: storage.Timestamp(at), ExpiresAt: storage.Timestamp(expires),
	})
	if err != nil {
		return failure(err, "metadata")
	}
	return nil
}

// signSnapshot signs a snapshot that pins a targets version, then the
// timestamp that names it.
func (s *Service) signSnapshot(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, refs onlineRefs, targets int64, at time.Time) error {
	version, err := nextVersion(ctx, q, env, updatemeta.RoleSnapshot)
	if err != nil {
		return err
	}
	expires := at.Add(s.o.Metadata.Expiry.Snapshot)
	doc, _, err := updatemeta.Sign(ctx, s.o.Signer, refs.snapshot, updatemeta.NewSnapshot(version, targets, expires))
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if err := storeMetadata(ctx, q, env, updatemeta.RoleSnapshot, version, doc, at, expires); err != nil {
		return err
	}
	return s.signTimestamp(ctx, q, env, refs, version, doc, at)
}

// signTimestamp signs a timestamp that names a snapshot file.
func (s *Service) signTimestamp(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, refs onlineRefs, snapshot int64, snapshotDoc []byte, at time.Time) error {
	version, err := nextVersion(ctx, q, env, updatemeta.RoleTimestamp)
	if err != nil {
		return err
	}
	expires := at.Add(s.o.Metadata.Expiry.Timestamp)
	doc, _, err := updatemeta.Sign(ctx, s.o.Signer, refs.timestamp, updatemeta.NewTimestamp(version, snapshot, snapshotDoc, expires))
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return storeMetadata(ctx, q, env, updatemeta.RoleTimestamp, version, doc, at, expires)
}

// RefreshMetadata keeps the short-lived roles of every enrolled
// environment of an organisation ahead of their expiry: it signs a new
// timestamp when half its lifetime has passed and a new snapshot when
// three quarters of its have (SEC-050). An environment whose root was
// uploaded after its first release gets its first metadata here. It
// returns how many files it signed.
func (s *Service) RefreshMetadata(ctx context.Context, org string) (int, error) {
	if s.o.Signer == nil {
		return 0, nil
	}
	var envs []dbgen.Environment
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		envs, err = dbgen.New(tx).ListEnvironmentsWithRoot(ctx)
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("release: %w", err)
	}
	var errs []error
	signed := 0
	for _, env := range envs {
		n, err := s.refreshEnvironment(ctx, org, env.ID)
		signed += n
		errs = append(errs, err)
	}
	if signed > 0 {
		s.manifests.clear()
		s.metadata.clear()
	}
	return signed, errors.Join(errs...)
}

// refreshEnvironment signs what one environment's metadata needs.
func (s *Service) refreshEnvironment(ctx context.Context, org string, id pgtype.UUID) (int, error) {
	signed := 0
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := q.LockEnvironment(ctx, id)
		if err != nil {
			return failure(err, "environment")
		}
		root, enrolled, err := currentRoot(ctx, q, env)
		if err != nil || !enrolled {
			return err
		}
		signed, err = s.refreshLocked(ctx, q, env, root)
		return err
	})
	if err != nil {
		return signed, fmt.Errorf("release: refresh the metadata of environment %s: %w", storage.ID(id), err)
	}
	return signed, nil
}

// refreshLocked signs the files of an environment that are near expiry,
// holding its row lock.
func (s *Service) refreshLocked(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, root updatemeta.Root) (int, error) {
	at := s.now()
	if env.TargetsVersion == 0 {
		return s.firstMetadata(ctx, q, env, root)
	}
	refs, err := s.checkRoot(ctx, q, env, root, at)
	if err != nil {
		return 0, err
	}
	snap, err := q.LatestMetadata(ctx, dbgen.LatestMetadataParams{EnvironmentID: env.ID, Role: updatemeta.RoleSnapshot})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, s.signSnapshot(ctx, q, env, refs, env.TargetsVersion, at) // lost or purged: write it again
	}
	if err != nil {
		return 0, failure(err, "snapshot")
	}
	if snap.ExpiresAt.Time.Sub(at) <= s.o.Metadata.Expiry.Snapshot/snapshotRefreshDivisor {
		return 2, s.signSnapshot(ctx, q, env, refs, env.TargetsVersion, at)
	}
	ts, err := q.LatestMetadata(ctx, dbgen.LatestMetadataParams{EnvironmentID: env.ID, Role: updatemeta.RoleTimestamp})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, failure(err, "timestamp")
	}
	if err == nil && ts.ExpiresAt.Time.Sub(at) > s.o.Metadata.Expiry.Timestamp/timestampRefreshDivisor {
		return 0, nil
	}
	return 1, s.signTimestamp(ctx, q, env, refs, snap.Version, snap.Document, at)
}

// firstMetadata writes the first metadata of an environment whose root
// arrived after its releases: it signs the manifests of its channels
// under the first targets version, through the channel whose lock it
// takes. Nothing is written until some channel has a release.
func (s *Service) firstMetadata(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, root updatemeta.Root) (int, error) {
	channels, err := q.ListChannelsOfEnvironment(ctx, env.ID)
	if err != nil {
		return 0, failure(err, "channel")
	}
	for _, c := range channels {
		if c.ReleaseSequence == 0 {
			continue
		}
		locked, err := q.GetChannelByIDForUpdate(ctx, c.ID)
		if err != nil {
			return 0, failure(err, "channel")
		}
		return 3, s.publishMetadata(ctx, q, locked, env, root)
	}
	return 0, nil
}

// PurgeMetadata deletes expired snapshots and timestamps, keeping the
// newest of each and every root.
func (s *Service) PurgeMetadata(ctx context.Context, org string) (int64, error) {
	var n int64
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = dbgen.New(tx).PurgeMetadata(ctx, storage.Timestamp(s.now()))
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("release: %w", err)
	}
	return n, nil
}

// UploadRoot stores a root an operator signed offline. The first root of
// an environment must be version 1 and signed by its own threshold; a
// later one must be the next version and signed by the previous root's
// threshold and its own (SEC-051). A production environment's root must
// ask for at least the configured threshold, must not have expired, and
// must list the environment's online keys, or the worker could not sign
// under it. The server never holds a root key.
func (s *Service) UploadRoot(ctx context.Context, org, environment string, raw []byte) (updatemeta.Root, error) {
	envID, err := parseID(environment, "environment")
	if err != nil {
		return updatemeta.Root{}, err
	}
	var out updatemeta.Root
	err = s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		env, err := q.LockEnvironment(ctx, envID)
		if err != nil {
			return failure(err, "environment")
		}
		root, doc, err := s.acceptRoot(ctx, q, env, raw)
		if err != nil {
			return err
		}
		at := s.now()
		expires, _ := time.Parse(time.RFC3339, root.Expires)
		if err := storeMetadata(ctx, q, env, updatemeta.RoleRoot, root.Version, doc, at, expires); err != nil {
			return err
		}
		out = root
		return s.auditRoot(ctx, tx, env, root, doc)
	})
	if err != nil {
		return updatemeta.Root{}, fmt.Errorf("release: %w", err)
	}
	s.manifests.clear()
	s.metadata.clear()
	return out, nil
}

// OperatorActor is who an administrative command of plux-server acts as
// in the audit log: there is no signed-in person, and the operator's
// access to the host is the credential.
var OperatorActor = audit.Actor{Kind: "system", ID: "plux-server", Display: "plux-server operator command"}

// auditRoot records an uploaded root: its version, threshold and key
// identifiers, never key material (SEC-140).
func (s *Service) auditRoot(ctx context.Context, tx pgx.Tx, env dbgen.Environment, root updatemeta.Root, doc []byte) error {
	sum := sha256.Sum256(doc)
	_, err := s.o.Audit.Append(ctx, tx, audit.Entry{
		OrganizationID: storage.ID(env.OrganizationID), Actor: OperatorActor, Action: audit.UpdateMetadataRootUploaded,
		TargetKind: "environment", TargetID: storage.ID(env.ID), AfterHash: hex.EncodeToString(sum[:]),
		Detail: fmt.Sprintf("version=%d threshold=%d keys=%s", root.Version, root.Roles.Root.Threshold, strings.Join(root.Roles.Root.KeyIDs, ",")),
	})
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// acceptRoot verifies an uploaded root against the environment's current
// one and the server's policy, and returns it with the file to store.
func (s *Service) acceptRoot(ctx context.Context, q *dbgen.Queries, env dbgen.Environment, raw []byte) (updatemeta.Root, []byte, error) {
	cur, enrolled, err := currentRoot(ctx, q, env)
	if err != nil {
		return updatemeta.Root{}, nil, err
	}
	var root updatemeta.Root
	var doc updatemeta.Document
	if enrolled {
		root, doc, err = updatemeta.NextRoot(cur, raw)
	} else {
		root, doc, err = updatemeta.FirstRoot(raw)
	}
	if err != nil {
		return updatemeta.Root{}, nil, plxerr.New(plxerr.UpdateMetadataInvalid, "%v", err)
	}
	if exp, perr := time.Parse(time.RFC3339, root.Expires); perr != nil || !s.now().Before(exp) {
		return updatemeta.Root{}, nil, plxerr.New(plxerr.UpdateMetadataInvalid, "the root has already expired (%s)", root.Expires)
	}
	if env.Production && root.Roles.Root.Threshold < s.o.Metadata.RootThreshold {
		return updatemeta.Root{}, nil, plxerr.New(plxerr.UpdateMetadataInvalid,
			"a production root asks %d signatures; the server requires at least %d (updateMetadata.rootThreshold)", root.Roles.Root.Threshold, s.o.Metadata.RootThreshold)
	}
	if s.o.Signer != nil {
		for _, k := range s.onlineRefs(env).list() {
			if err := s.checkOnlineKey(ctx, q, env, root, k.role, k.ref); err != nil {
				return updatemeta.Root{}, nil, err
			}
		}
	}
	canonical, err := doc.Bytes()
	if err != nil {
		return updatemeta.Root{}, nil, fmt.Errorf("release: %w", err)
	}
	return root, canonical, nil
}

// OnlineKey is the public half of one of an environment's online keys.
type OnlineKey struct {
	Role, KeyID, Algorithm string
	PublicKey              []byte
}

// OnlineKeys returns the keys the worker signs this environment's
// targets, snapshot and timestamp with, for the offline ceremony that
// lists them in a root. It needs a signer, so only a process that may
// sign can answer.
func (s *Service) OnlineKeys(ctx context.Context, org, environment string) ([]OnlineKey, error) {
	if s.o.Signer == nil {
		return nil, errors.New("release: this role cannot sign")
	}
	envID, err := parseID(environment, "environment")
	if err != nil {
		return nil, err
	}
	var env dbgen.Environment
	err = s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		env, err = dbgen.New(tx).GetEnvironment(ctx, envID)
		return err //nolint:wrapcheck // translated below
	})
	if err != nil {
		return nil, failure(err, "environment")
	}
	var out []OnlineKey
	for _, k := range s.onlineRefs(env).list() {
		pub, id, err := s.o.Signer.PublicKey(ctx, k.ref)
		if err != nil {
			return nil, fmt.Errorf("release: the %s key: %w", k.role, err)
		}
		out = append(out, OnlineKey{Role: k.role, KeyID: id, Algorithm: updatemeta.AlgEd25519, PublicKey: pub})
	}
	return out, nil
}

// RootChain returns the root files after a version, oldest first, for a
// caller that trusts the root at that version (SEC-051).
func (s *Service) RootChain(ctx context.Context, org, environment string, since int64) ([][]byte, error) {
	var out [][]byte
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListRootsSince(ctx, dbgen.ListRootsSinceParams{EnvironmentID: storage.MustUUID(environment), Version: since})
		if err != nil {
			return err //nolint:wrapcheck // translated below
		}
		for _, r := range rows {
			out = append(out, r.Document)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	return out, nil
}

// rootKeys lists the keys of an environment's current root, with the
// role each holds, for GetRootKeys.
func rootKeys(ctx context.Context, q *dbgen.Queries, env dbgen.Environment) ([]RootKey, error) {
	root, enrolled, err := currentRoot(ctx, q, env)
	if err != nil || !enrolled {
		return nil, err
	}
	var out []RootKey
	for _, role := range []string{updatemeta.RoleRoot, updatemeta.RoleTargets, updatemeta.RoleSnapshot, updatemeta.RoleTimestamp} {
		keys, _ := root.Roles.Named(role)
		for _, id := range keys.KeyIDs {
			pub, err := base64.StdEncoding.DecodeString(root.Keys[id].Public)
			if err != nil {
				return nil, fmt.Errorf("release: the stored root: %w", err)
			}
			out = append(out, RootKey{KeyID: id, Algorithm: root.Keys[id].Alg, Role: role, PublicKey: pub, EnvironmentType: environmentType(env)})
		}
	}
	return out, nil
}

// MetadataFile is one served metadata file.
type MetadataFile struct {
	Document []byte
	SHA256   [32]byte
	Version  int64
	// Versioned is true when the name carried a version: such a file
	// never changes.
	Versioned bool
}

// MetadataPath is where the server serves update metadata (SEC-050).
const MetadataPath = "/v1/metadata/"

// MetadataMaxAge is how many seconds a client may reuse the newest file
// of a role: well inside the shortest expiry the settings allow.
const MetadataMaxAge = 30

// metadataTTL is how long a replica reuses an unversioned file.
const metadataTTL = time.Second

// metadataCache holds the newest file of each role per environment
// briefly, so that a poll by many devices is one read a second.
type metadataCache struct {
	mu      sync.Mutex
	entries map[string]cachedMetadata
}

// clear forgets every cached file.
func (c *metadataCache) clear() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

type cachedMetadata struct {
	file  MetadataFile
	until time.Time
}

// ParseMetadataName reads a served name: "<role>.json" or
// "<version>.<role>.json", for the roles whose files are stored.
func ParseMetadataName(name string) (role string, version int64, err error) {
	base, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return "", 0, errors.New("not a metadata file")
	}
	if v, r, found := strings.Cut(base, "."); found {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 || strconv.FormatInt(n, 10) != v {
			return "", 0, errors.New("not a metadata version")
		}
		version, base = n, r
	}
	switch base {
	case updatemeta.RoleRoot, updatemeta.RoleSnapshot, updatemeta.RoleTimestamp:
		return base, version, nil
	}
	return "", 0, errors.New("not a metadata role")
}

// Metadata returns a metadata file of an environment by its served name.
// The files are signed and public, so the environment identifier is the
// only credential (SEC-050).
func (s *Service) Metadata(ctx context.Context, environment, name string) (MetadataFile, error) {
	role, version, err := ParseMetadataName(name)
	if err != nil {
		return MetadataFile{}, plxerr.New(plxerr.ResourceNotFound, "no such metadata file")
	}
	envID, err := parseID(environment, "environment")
	if err != nil {
		return MetadataFile{}, plxerr.New(plxerr.ResourceNotFound, "no such metadata file")
	}
	key := environment + "/" + name
	now := s.now()
	if version == 0 {
		s.metadata.mu.Lock()
		c, ok := s.metadata.entries[key]
		s.metadata.mu.Unlock()
		if ok && now.Before(c.until) {
			return c.file, nil
		}
	}
	var row dbgen.UpdateMetadatum
	err = s.o.DB.InTx(ctx, storage.Tenant{Scope: storage.ScopeMetadata}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		var err error
		if version == 0 {
			row, err = q.LatestMetadata(ctx, dbgen.LatestMetadataParams{EnvironmentID: envID, Role: role})
		} else {
			row, err = q.GetMetadata(ctx, dbgen.GetMetadataParams{EnvironmentID: envID, Role: role, Version: version})
		}
		return err //nolint:wrapcheck // translated below
	})
	if err != nil {
		return MetadataFile{}, failure(err, "metadata file")
	}
	file := MetadataFile{Document: row.Document, Version: row.Version, Versioned: version != 0}
	copy(file.SHA256[:], row.Sha256)
	if version == 0 {
		s.metadata.mu.Lock()
		if s.metadata.entries == nil || len(s.metadata.entries) >= maxCachedManifests {
			s.metadata.entries = map[string]cachedMetadata{}
		}
		s.metadata.entries[key] = cachedMetadata{file: file, until: now.Add(metadataTTL)}
		s.metadata.mu.Unlock()
	}
	return file, nil
}
