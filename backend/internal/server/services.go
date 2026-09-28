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
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/httpx"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/idempotency"
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
	Idempotency *idempotency.Store
	Pages       *api.Pages
}

// AssetDeps are what asset handling needs beyond the database: where
// files are stored, the job queue that transcodes them, and — in the
// worker role — the codecs. The zero value refuses uploads.
type AssetDeps struct {
	Objects objects.Store
	Jobs    document.Enqueuer
	Codecs  *media.Codecs
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

// enqueuer adds asset jobs to the job client, which is built after the
// services it runs jobs for; Build sets the client before serving.
type enqueuer struct{ client *jobs.Client }

// Enqueue inserts a transcoding job in the caller's transaction.
func (e *enqueuer) Enqueue(ctx context.Context, tx pgx.Tx, job document.AssetJob) error {
	if e.client == nil {
		return errors.New("server: the job client is not ready")
	}
	if _, err := e.client.InsertTx(ctx, tx, job, &river.InsertOpts{Queue: jobs.QueueAsset, MaxAttempts: 5}); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
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
func BuildServices(ctx context.Context, cfg *config.Config, db *storage.DB, shared cache.Cache, set limits.Set, backend signing.Crypter, assets AssetDeps) (*Services, error) {
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
		Objects: assets.Objects, Jobs: assets.Jobs, Scanner: scanner(cfg), Codecs: assets.Codecs,
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
	return &Services{
		Audit: log, Auth: authService, Tenancy: tenancyService, Documents: docs,
		Idempotency: store, Pages: pages,
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
		Auth: svc.Auth, Tenancy: svc.Tenancy, Documents: svc.Documents, Idempotency: svc.Idempotency, Pages: svc.Pages,
		Limiter: limiter, Limits: s.limits,
	}
	authn := api.Authentication(svc.Auth, api.IdentityPublic, s.trusted, limiter, s.limits.Get(limits.APIRequestsPerMinute))
	opts := connect.WithInterceptors(s.Interceptors(authn)...)
	for _, register := range []func() (string, http.Handler){
		func() (string, http.Handler) { return pluxv1connect.NewIdentityServiceHandler(h.Identity(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewOrgServiceHandler(h.Org(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAppServiceHandler(h.App(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewPluginServiceHandler(h.Plugin(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewDocumentServiceHandler(h.Document(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewComponentServiceHandler(h.Component(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTemplateServiceHandler(h.Template(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAssetServiceHandler(h.Asset(), opts) },
	} {
		path, handler := register()
		s.Register(path, handler)
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
	err = w.svc.Tenancy.ForEachOrganization(ctx, func(org string) error {
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
			slog.Int("trashPurged", purged), slog.Int("snapshotsPurged", snapshots), slog.Int64("idempotencyKeysExpired", keys),
			slog.Int64("credentialsExpired", creds))
	}
	return errors.Join(errs...)
}

// MaintenanceJobs registers the sweep's worker and returns its schedule.
func MaintenanceJobs(workers *jobs.Workers, svc *Services, log *slog.Logger) []*river.PeriodicJob {
	jobs.AddWorker(workers, &maintenanceWorker{svc: svc, log: log})
	return []*river.PeriodicJob{jobs.Every(time.Hour, Maintenance{})}
}
