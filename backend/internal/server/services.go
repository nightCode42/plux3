// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"

	"connectrpc.com/connect"
	"github.com/riverqueue/river"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/buildinfo"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/httpx"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/idempotency"
	"github.com/nightCode42/plux3/backend/internal/telemetry"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Services are the domain services of this phase, built once per
// process and shared by the api role's handlers and the worker role's
// jobs. They hold envelope encryption only: signing is the worker's, and
// arrives with the publish pipeline.
type Services struct {
	Audit       *audit.Log
	Auth        *auth.Service
	Tenancy     *tenancy.Service
	Documents   *document.Service
	Releases    *release.Service
	Devices     *device.Service
	Events      *telemetry.Service
	Idempotency *idempotency.Store
	Pages       *api.Pages
	// KMS checks the key management service, when the signing backend
	// is one; nil for the development file backend (SRV-007).
	KMS func(context.Context) error
}

// WorkDeps are what the services need beyond the database: where files
// are stored, the job queue, and — in the worker role only — the codecs
// and the signer (SRV-052, ADR-0006). The zero value refuses uploads and
// leaves releases out.
type WorkDeps struct {
	Objects objects.Store
	Queue   *JobQueue
	Codecs  *media.Codecs
	Signer  signing.Signer
	// ProductionSigning reports whether the signer may sign for
	// production environments (SEC-056).
	ProductionSigning bool
}

// scanner returns the configured malware scanner, or nil (SRV-060).
func scanner(cfg *config.Config) document.Scanner {
	u, err := url.Parse(cfg.Assets.MalwareScanner)
	if cfg.Assets.MalwareScanner == "" || err != nil {
		return nil
	}
	if u.Scheme == "unix" {
		return document.Clamd{Network: "unix", Address: u.Path}
	}
	return document.Clamd{Network: "tcp", Address: u.Host}
}

// JobQueue enqueues the services' jobs on the job client, which is built
// after the services it runs jobs for; Build sets it before serving.
type JobQueue struct{ client *jobs.Client }

// insert adds a job on a queue in the caller's transaction.
func (q *JobQueue) insert(ctx context.Context, tx pgx.Tx, args river.JobArgs, queue string) error {
	if q == nil || q.client == nil {
		return errors.New("server: the job client is not ready")
	}
	if _, err := q.client.InsertTx(ctx, tx, args, &river.InsertOpts{Queue: queue, MaxAttempts: 5}); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// assetQueue enqueues transcoding jobs (CMP-030).
type assetQueue struct{ q *JobQueue }

func (a assetQueue) Enqueue(ctx context.Context, tx pgx.Tx, job document.AssetJob) error {
	return a.q.insert(ctx, tx, job, jobs.QueueAsset)
}

// publishQueue enqueues publish jobs (SRV-050).
type publishQueue struct{ q *JobQueue }

func (p publishQueue) Enqueue(ctx context.Context, tx pgx.Tx, job release.Work) error {
	return p.q.insert(ctx, tx, job, jobs.QueuePublish)
}

// manifestWorker signs manifests (REL-031).
type manifestWorker struct {
	river.WorkerDefaults[release.ManifestJob]
	svc *Services
}

// Work signs one channel's manifest.
func (w *manifestWorker) Work(ctx context.Context, job *river.Job[release.ManifestJob]) error {
	return w.svc.Releases.SignManifest(ctx, job.Args) //nolint:wrapcheck // a domain error
}

// deltaWorker precomputes deltas after a publish (REL-022).
type deltaWorker struct {
	river.WorkerDefaults[release.DeltaJob]
	svc *Services
}

// Work computes one version's deltas.
func (w *deltaWorker) Work(ctx context.Context, job *river.Job[release.DeltaJob]) error {
	return w.svc.Releases.PrecomputeDeltas(ctx, job.Args) //nolint:wrapcheck // a domain error
}

// publishWorker runs publishes (SRV-050).
type publishWorker struct {
	river.WorkerDefaults[release.Job]
	svc *Services
}

// Work runs one publish. A publish waiting for assets is snoozed, which
// River does not count as an attempt; RunPublish bounds the wait itself.
func (w *publishWorker) Work(ctx context.Context, job *river.Job[release.Job]) error {
	return snoozePending(w.svc.Releases.RunPublish(ctx, job.Args))
}

// snoozePending turns a publish's wait for assets into a snooze.
func snoozePending(err error) error {
	if pending, ok := errors.AsType[*release.AssetsPendingError](err); ok {
		return river.JobSnooze(pending.RetryAfter) //nolint:wrapcheck // River recognises a snooze by its type
	}
	return err //nolint:wrapcheck // a domain error
}

// assetWorker transcodes uploaded images (CMP-030).
type assetWorker struct {
	river.WorkerDefaults[document.AssetJob]
	svc *Services
}

// Work runs one asset job.
func (w *assetWorker) Work(ctx context.Context, job *river.Job[document.AssetJob]) error {
	return w.svc.Documents.ProcessAsset(ctx, job.Args) //nolint:wrapcheck // a domain error
}

// ids adapts the UUIDv7 generator to the string identifiers the domain
// packages use (SCH-002).
type ids struct{ g *uuid7.Generator }

// New returns a fresh identifier.
func (i ids) New() (string, error) {
	id, err := i.g.New()
	if err != nil {
		return "", fmt.Errorf("server: %w", err)
	}
	return id.String(), nil
}

// NewIDs returns the identifier generator every service shares.
func NewIDs() interface{ New() (string, error) } {
	return ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
}

// BuildSigning opens the configured signing backend (SEC-120). Only this
// function and the signing package know which one it is.
func BuildSigning(cfg *config.Config) (signing.Backend, error) {
	switch cfg.Signing.Backend {
	case "file":
		b, err := signing.NewFile(cfg.Signing.Directory)
		if err != nil {
			return nil, fmt.Errorf("server: signing: %w", err)
		}
		return b, nil
	case "vault":
		v := cfg.Signing.Vault
		b, err := signing.NewVault(signing.VaultOptions{
			Address: v.Address, Token: v.Token.Value(), Mount: v.Mount, Namespace: v.Namespace, WrapKey: v.WrapKey,
		})
		if err != nil {
			return nil, fmt.Errorf("server: signing: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("server: signing backend %q is not available in this phase", cfg.Signing.Backend)
	}
}

// BuildServices assembles the domain services. They are given only the
// envelope-encryption half of the signing backend (L-3, ADR-0006).
func BuildServices(ctx context.Context, cfg *config.Config, db *storage.DB, shared cache.Cache, set limits.Set, backend signing.Crypter, work WorkDeps) (*Services, error) {
	if db == nil || shared == nil {
		return nil, errors.New("server: the services need a database and a cache")
	}
	backend = signing.CrypterOnly(backend)
	gen := NewIDs()
	log := audit.NewLog(gen, nil)
	issuers, err := trustedIssuers(cfg)
	if err != nil {
		return nil, err
	}
	oidc, err := oidcProvider(cfg)
	if err != nil {
		return nil, err
	}
	authService, err := auth.NewService(auth.Options{
		DB: db, Audit: log, Cache: shared, Limits: set, Crypter: backend, IDs: gen,
		SessionTTL:      cfg.Auth.Studio.SessionTTL.Duration(),
		CITokenTTL:      cfg.Auth.CI.TokenTTL.Duration(),
		Issuer:          "Plux",
		VerificationURI: verificationURI(cfg),
		Issuers:         issuers,
		OIDC:            oidc,
		WebAuthn:        webAuthn(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	tenancyService, err := tenancy.NewService(tenancy.Options{
		DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, Limits: set,
		TrashDays: cfg.Retention.TrashDays, SigningKeyPrefix: cfg.Signing.Keys.Targets,
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	docs, err := document.NewService(document.Options{
		DB: db, Audit: log, Tenancy: tenancyService, IDs: gen, Limits: set,
		SnapshotDays: cfg.Retention.SnapshotDays, CompilerVersion: buildinfo.Get().Version,
		Objects: work.Objects, Jobs: assetQueue{work.Queue}, Scanner: scanner(cfg), Codecs: work.Codecs,
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	for kind, k := range docs.TrashKinds() {
		tenancyService.RegisterTrashKind(kind, k)
	}
	store, err := idempotency.NewStore(db, backend, nil)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	key, err := signing.InstallationKey(ctx, db, backend, "page-token")
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	pages, err := api.NewPages(key, int32(min(set.Get(limits.APIPageSize), 1<<30)), nil) //nolint:gosec // bounded by min
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	devices, err := device.NewService(device.Options{DB: db, IDs: gen})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	events, err := telemetry.NewService(telemetry.Options{DB: db, IDs: gen, Limits: set})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	var releases *release.Service
	if work.Objects != nil {
		if releases, err = release.NewService(release.Options{
			DB: db, Audit: log, Tenancy: tenancyService, Documents: docs, Objects: work.Objects, IDs: gen,
			Jobs: publishQueue{work.Queue}, Signer: work.Signer, ProductionSigning: work.ProductionSigning, Limits: set,
			Devices: devices, PublicBaseURL: cfg.Server.PublicBaseURL,
			CompilerVersion: buildinfo.Get().Version, DevelopmentDays: cfg.Retention.DevelopmentReleaseDays,
		}); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
		tenancyService.RegisterUsage(releases.Usage)
	}
	return &Services{
		Audit: log, Auth: authService, Tenancy: tenancyService, Documents: docs, Releases: releases,
		Devices: devices, Events: events, Idempotency: store, Pages: pages,
	}, nil
}

// verificationURI is where a person approves `plux login`: Studio's
// device page, under the installation's public URL (CLI-002).
func verificationURI(cfg *config.Config) string {
	if cfg.Server.PublicBaseURL == "" {
		return ""
	}
	return strings.TrimSuffix(cfg.Server.PublicBaseURL, "/") + "/device"
}

// oidcProvider builds the OpenID Connect provider people sign in with,
// when one is configured (SEC-100).
func oidcProvider(cfg *config.Config) (*auth.OIDCProvider, error) {
	o := cfg.Auth.Studio.OIDC
	if o.Issuer == "" {
		return nil, nil //nolint:nilnil // no provider is configured
	}
	p, err := auth.NewOIDCProvider(auth.OIDCConfig{
		Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: string(o.ClientSecret), RedirectURL: o.RedirectURL,
		Client: httpx.NewClient(httpx.Options{Timeout: 15 * time.Second}),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return p, nil
}

// webAuthn names the relying party security keys are registered for:
// the host of the public base URL, which serves Studio and its
// backend-for-frontend. Without a public base URL, security keys are
// off.
func webAuthn(cfg *config.Config) auth.WebAuthnConfig {
	u, err := url.Parse(cfg.Server.PublicBaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return auth.WebAuthnConfig{}
	}
	return auth.WebAuthnConfig{RPID: u.Hostname(), RPName: "Plux", Origins: []string{u.Scheme + "://" + u.Host}}
}

// trustedIssuers builds a verifier for each configured CI provider
// (SRV-064). Their discovery documents and keys are fetched through the
// SSRF-safe client (SEC-105).
func trustedIssuers(cfg *config.Config) (map[string]auth.TrustedIssuer, error) {
	client := httpx.NewClient(httpx.Options{Timeout: 15 * time.Second})
	out := map[string]auth.TrustedIssuer{}
	for _, is := range cfg.Auth.CI.Issuers {
		v, err := auth.NewVerifier(is.Issuer, client, nil)
		if err != nil {
			return nil, fmt.Errorf("server: CI issuer %s: %w", is.Issuer, err)
		}
		out[strings.TrimSuffix(is.Issuer, "/")] = auth.TrustedIssuer{
			Verifier: v, Audience: is.Audience, SubjectPattern: is.SubjectPattern,
		}
	}
	return out, nil
}

// RegisterAPI mounts the services of this phase on the api role's mux,
// each behind the standard interceptor chain with authentication.
func (s *Server) RegisterAPI(svc *Services) {
	limiter := api.RateLimiter{Window: time.Minute}
	if s.cache != nil {
		limiter.Count = s.cache.Increment
	}
	h := &api.Handlers{
		Auth: svc.Auth, Tenancy: svc.Tenancy, Documents: svc.Documents, Releases: svc.Releases, Devices: svc.Devices, Events: svc.Events,
		Idempotency: svc.Idempotency, Pages: svc.Pages,
		Limiter: limiter, Limits: s.limits,
	}
	people := api.Authentication(svc.Auth, api.IdentityPublic, s.trusted, limiter, s.limits.Get(limits.APIRequestsPerMinute))
	authn := api.DeviceAuthentication(svc.Devices, limiter, s.limits.Get(limits.APIRequestsPerMinutePerDevice), people)
	opts := connect.WithInterceptors(s.Interceptors(authn)...)
	registrations := []func() (string, http.Handler){
		func() (string, http.Handler) { return pluxv1connect.NewIdentityServiceHandler(h.Identity(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewOrgServiceHandler(h.Org(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAppServiceHandler(h.App(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewPluginServiceHandler(h.Plugin(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewDocumentServiceHandler(h.Document(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewComponentServiceHandler(h.Component(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTemplateServiceHandler(h.Template(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAssetServiceHandler(h.Asset(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewDeviceServiceHandler(h.Device(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTokenServiceHandler(h.Token(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTelemetryServiceHandler(h.Telemetry(), opts) },
	}
	if svc.Releases != nil {
		registrations = append(registrations,
			func() (string, http.Handler) { return pluxv1connect.NewPublishServiceHandler(h.Publish(), opts) },
			func() (string, http.Handler) { return pluxv1connect.NewReleaseServiceHandler(h.Release(), opts) },
			func() (string, http.Handler) { return pluxv1connect.NewManifestServiceHandler(h.Manifest(), opts) },
			func() (string, http.Handler) { return pluxv1connect.NewControlServiceHandler(h.Control(), opts) })
	}
	for _, register := range registrations {
		path, handler := register()
		s.Register(path, handler)
	}
	if s.objects != nil {
		s.Register("GET "+release.ObjectsPath, s.objectHandler())
	}
}

// Maintenance is the periodic sweep of the worker role: it purges the
// trash past its retention (GOV-031) and draft history past its own
// (SRV-031), forgets idempotency keys older
// than a day (SRV-005) and deletes expired credentials.
type Maintenance struct{}

// Kind names the job.
func (Maintenance) Kind() string { return "maintenance.sweep" }

// InsertOpts puts the sweep on the maintenance queue, at most one
// waiting at a time.
func (Maintenance) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, UniqueOpts: river.UniqueOpts{ByPeriod: 10 * time.Minute}}
}

// maintenanceWorker runs the sweep.
type maintenanceWorker struct {
	river.WorkerDefaults[Maintenance]
	svc *Services
	log *slog.Logger
}

// Work runs one sweep. Each part runs even if another fails, and the
// job fails if any did, so River retries it.
func (w *maintenanceWorker) Work(ctx context.Context, _ *river.Job[Maintenance]) error {
	var errs []error
	purged, err := w.svc.Tenancy.PurgeExpired(ctx, 100)
	errs = append(errs, err)
	snapshots := 0
	released := 0
	signed := 0
	err = w.svc.Tenancy.ForEachOrganization(ctx, func(org string) error {
		if w.svc.Releases != nil {
			n, err := w.svc.Releases.PurgeReleases(ctx, org)
			released += n
			if err != nil {
				return err //nolint:wrapcheck // a domain error
			}
			m, err := w.svc.Releases.RefreshManifests(ctx, org)
			signed += m
			if err != nil {
				return err //nolint:wrapcheck // a domain error
			}
			if _, err := w.svc.Releases.PurgeManifests(ctx, org); err != nil {
				return err //nolint:wrapcheck // a domain error
			}
		}
		if _, err := w.svc.Devices.ExpireTokens(ctx, org); err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		if _, err := w.svc.Events.Purge(ctx, org); err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		n, err := w.svc.Documents.PurgeSnapshots(ctx, org)
		snapshots += n
		return err //nolint:wrapcheck // a domain error
	})
	errs = append(errs, err)
	keys, err := w.svc.Idempotency.Expire(ctx)
	errs = append(errs, err)
	creds, err := w.svc.Auth.Expire(ctx)
	errs = append(errs, err)
	if w.log != nil {
		w.log.InfoContext(ctx, "maintenance sweep",
			slog.Int("trashPurged", purged), slog.Int("snapshotsPurged", snapshots), slog.Int("releasesPurged", released), slog.Int("manifestsSigned", signed), slog.Int64("idempotencyKeysExpired", keys),
			slog.Int64("credentialsExpired", creds))
	}
	return errors.Join(errs...)
}

// MaintenanceJobs registers the sweep's worker and returns its schedule.
func MaintenanceJobs(workers *jobs.Workers, svc *Services, log *slog.Logger) []*river.PeriodicJob {
	jobs.AddWorker(workers, &maintenanceWorker{svc: svc, log: log})
	return []*river.PeriodicJob{jobs.Every(time.Hour, Maintenance{})}
}
