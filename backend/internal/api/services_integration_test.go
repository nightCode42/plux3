// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage/idempotency"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/telemetry"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type uuids struct{ g *uuid7.Generator }

// publishQueue records publish jobs for the test to run.
type publishQueue struct {
	mu   sync.Mutex
	jobs []release.Work
}

func (q *publishQueue) Enqueue(_ context.Context, _ pgx.Tx, j release.Work) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return nil
}

// assetQueue records asset jobs for the test to run.
type assetQueue struct {
	mu   sync.Mutex
	jobs []document.AssetJob
}

func (q *assetQueue) Enqueue(_ context.Context, _ pgx.Tx, j document.AssetJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return nil
}

// sharedCodecs are the image codecs the worker transcodes assets with,
// compiled once for the package's tests: compiling costs seconds.
var sharedCodecs = sync.OnceValues(func() (*media.Codecs, error) { return media.NewCodecs(context.Background()) })

func (u uuids) New() (string, error) {
	id, err := u.g.New()
	return id.String(), err
}

// world is the three services of this phase served over HTTP, the way
// the api role serves them.
type world struct {
	auth     *auth.Service
	releases *release.Service
	queue    *publishQueue
	assets   *assetQueue
	docs     *document.Service
	identity pluxv1connect.IdentityServiceClient
	org      pluxv1connect.OrgServiceClient
	app      pluxv1connect.AppServiceClient
	plugin   pluxv1connect.PluginServiceClient
	document pluxv1connect.DocumentServiceClient
	comp     pluxv1connect.ComponentServiceClient
	template pluxv1connect.TemplateServiceClient
	asset    pluxv1connect.AssetServiceClient
	publish  pluxv1connect.PublishServiceClient
	release  pluxv1connect.ReleaseServiceClient
	device   pluxv1connect.DeviceServiceClient
	token    pluxv1connect.TokenServiceClient
	manifest pluxv1connect.ManifestServiceClient
	control  pluxv1connect.ControlServiceClient
	events   pluxv1connect.TelemetryServiceClient
	devices  *device.Service
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := uuids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	shared := cache.NewMemory(nil)
	authService, err := auth.NewService(auth.Options{
		DB: db, Audit: log, Cache: shared, Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device",
	})
	if err != nil {
		t.Fatal(err)
	}
	tenancyService, err := tenancy.NewService(tenancy.Options{
		DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets",
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := objects.NewFilesystem(t.TempDir(), "https://cdn.example")
	if err != nil {
		t.Fatal(err)
	}
	codecs, err := sharedCodecs()
	if err != nil {
		t.Fatal(err)
	}
	assets := &assetQueue{}
	docs, err := document.NewService(document.Options{DB: db, Audit: log, Tenancy: tenancyService, IDs: gen, Objects: files, Jobs: assets, Codecs: codecs})
	if err != nil {
		t.Fatal(err)
	}
	for kind, k := range docs.TrashKinds() {
		tenancyService.RegisterTrashKind(kind, k)
	}
	queue := &publishQueue{}
	devices, err := device.NewService(device.Options{DB: db, IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	releases, err := release.NewService(release.Options{
		DB: db, Audit: log, Tenancy: tenancyService, Documents: docs, Objects: files, IDs: gen, Jobs: queue, Signer: backend,
		ProductionSigning: true, PublicBaseURL: "https://plux.example.com", Devices: devices,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := idempotency.NewStore(db, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := signing.InstallationKey(ctx, db, backend, "page-token")
	if err != nil {
		t.Fatal(err)
	}
	again, err := signing.InstallationKey(ctx, db, backend, "page-token")
	if err != nil || string(again) != string(key) {
		t.Fatalf("the installation key changed between reads: %v", err)
	}
	pages, err := api.NewPages(key, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	set := limits.Defaults()
	limiter := api.RateLimiter{Window: time.Minute, Count: shared.Increment}
	events, err := telemetry.NewService(telemetry.Options{DB: db, IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	h := &api.Handlers{
		Auth: authService, Tenancy: tenancyService, Documents: docs, Releases: releases, Devices: devices, Events: events,
		Idempotency: store, Pages: pages, Limiter: limiter, Limits: set,
	}
	people := api.Authentication(authService, api.IdentityPublic, nil, limiter, set.Get(limits.APIRequestsPerMinute))
	authn := api.DeviceAuthentication(devices, limiter, set.Get(limits.APIRequestsPerMinutePerDevice), people)
	opts := connect.WithInterceptors(api.Interceptors(api.Deps{Before: []api.Around{authn}})...)
	mux := http.NewServeMux()
	for _, r := range []func() (string, http.Handler){
		func() (string, http.Handler) { return pluxv1connect.NewIdentityServiceHandler(h.Identity(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewOrgServiceHandler(h.Org(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAppServiceHandler(h.App(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewPluginServiceHandler(h.Plugin(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewDocumentServiceHandler(h.Document(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewComponentServiceHandler(h.Component(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTemplateServiceHandler(h.Template(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewAssetServiceHandler(h.Asset(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewPublishServiceHandler(h.Publish(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewReleaseServiceHandler(h.Release(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewDeviceServiceHandler(h.Device(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTokenServiceHandler(h.Token(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewManifestServiceHandler(h.Manifest(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewControlServiceHandler(h.Control(), opts) },
		func() (string, http.Handler) { return pluxv1connect.NewTelemetryServiceHandler(h.Telemetry(), opts) },
	} {
		path, handler := r()
		mux.Handle(path, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &world{
		auth:     authService,
		assets:   assets,
		docs:     docs,
		identity: pluxv1connect.NewIdentityServiceClient(srv.Client(), srv.URL),
		org:      pluxv1connect.NewOrgServiceClient(srv.Client(), srv.URL),
		app:      pluxv1connect.NewAppServiceClient(srv.Client(), srv.URL),
		plugin:   pluxv1connect.NewPluginServiceClient(srv.Client(), srv.URL),
		document: pluxv1connect.NewDocumentServiceClient(srv.Client(), srv.URL),
		comp:     pluxv1connect.NewComponentServiceClient(srv.Client(), srv.URL),
		template: pluxv1connect.NewTemplateServiceClient(srv.Client(), srv.URL),
		asset:    pluxv1connect.NewAssetServiceClient(srv.Client(), srv.URL),
		publish:  pluxv1connect.NewPublishServiceClient(srv.Client(), srv.URL),
		release:  pluxv1connect.NewReleaseServiceClient(srv.Client(), srv.URL),
		device:   pluxv1connect.NewDeviceServiceClient(srv.Client(), srv.URL),
		token:    pluxv1connect.NewTokenServiceClient(srv.Client(), srv.URL),
		manifest: pluxv1connect.NewManifestServiceClient(srv.Client(), srv.URL),
		control:  pluxv1connect.NewControlServiceClient(srv.Client(), srv.URL),
		events:   pluxv1connect.NewTelemetryServiceClient(srv.Client(), srv.URL),
		devices:  devices,
		releases: releases,
		queue:    queue,
	}
}

// caller is how a request is authenticated.
type caller struct {
	cookie, csrf, bearer, org string
}

// req builds a request carrying the caller's credential.
func req[T any](c caller, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	if c.cookie != "" {
		r.Header().Set("Cookie", c.cookie)
		r.Header().Set(api.CSRFHeader, c.csrf)
	}
	if c.bearer != "" {
		r.Header().Set("Authorization", "Bearer "+c.bearer)
	}
	if c.org != "" {
		r.Header().Set(api.OrganizationHeader, c.org)
	}
	return r
}

// must returns a check that fails the test on an error and otherwise
// returns the message; it takes a call's two results directly.
func must[T any](res *connect.Response[T], err error) func(testing.TB) *T {
	return func(t testing.TB) *T {
		t.Helper()
		if err != nil {
			t.Fatalf("call failed: %v", err)
		}
		return res.Msg
	}
}

// codeOf returns a Connect error's code.
func codeOf(err error) connect.Code {
	var e *connect.Error
	if errors.As(err, &e) {
		return e.Code()
	}
	return 0
}

// signIn signs a person in through the API and returns the caller.
func (w *world) signIn(t *testing.T, email string) caller {
	const password = "correct horse battery"
	t.Helper()
	res, err := w.identity.StartPasswordLogin(context.Background(), connect.NewRequest(&pluxv1.StartPasswordLoginRequest{Email: email, Password: password}))
	if err != nil {
		t.Fatalf("StartPasswordLogin: %v", err)
	}
	return w.fromSession(t, res.Header(), res.Msg.GetSession())
}

// fromSession reads the session cookie a response set.
func (*world) fromSession(t *testing.T, h http.Header, s *pluxv1.Session) caller {
	t.Helper()
	set := h.Get("Set-Cookie")
	name, rest, _ := strings.Cut(set, "=")
	value, _, _ := strings.Cut(rest, ";")
	if name != api.SessionCookie || value == "" || s.GetCsrfToken() == "" {
		t.Fatalf("session cookie = %q, session = %+v", set, s)
	}
	return caller{cookie: name + "=" + value, csrf: s.GetCsrfToken()}
}

// Verifies: SEC-100, SEC-101, SRV-005, SRV-004, SRV-064, GOV-001, GOV-010, GOV-031, SEC-140.
// The whole identity and tenancy surface, driven over HTTP the way a
// client drives it.
func TestServicesEndToEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	const password = "correct horse battery"

	// Bootstrap, accept the invitation, sign in.
	_, invitation, err := w.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	accepted := must(w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{
		Invitation: invitation, DisplayName: "Admin", Password: password,
	})))(t)
	if accepted.GetUser().GetEmail() != "admin@example.com" {
		t.Errorf("AcceptInvitation = %+v", accepted)
	}
	if _, err := w.identity.StartPasswordLogin(ctx, connect.NewRequest(&pluxv1.StartPasswordLoginRequest{Email: "admin@example.com", Password: "wrong password!"})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a wrong password: %v", err)
	}
	admin := w.signIn(t, "admin@example.com")
	if _, err := w.identity.GetCurrentUser(ctx, connect.NewRequest(&pluxv1.GetCurrentUserRequest{})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous call: %v", err)
	}
	noCSRF := admin
	noCSRF.csrf = "plux_csrf_forged"
	if _, err := w.identity.GetCurrentUser(ctx, req(noCSRF, &pluxv1.GetCurrentUserRequest{})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a forged CSRF token: %v", err)
	}
	me := must(w.identity.GetCurrentUser(ctx, req(admin, &pluxv1.GetCurrentUserRequest{})))(t)
	if me.GetUser().GetMfaEnrolled() {
		t.Error("a new account reports a second factor")
	}

	// A second factor, then an organisation and an app.
	enrolled := must(w.identity.EnrollTotp(ctx, req(admin, &pluxv1.EnrollTotpRequest{Label: "phone"})))(t)
	code, _ := auth.TOTPCode(enrolled.GetSecret(), time.Now())
	must(w.identity.ConfirmFactor(ctx, req(admin, &pluxv1.ConfirmFactorRequest{FactorId: enrolled.GetFactor().GetId(), Code: code})))(t)
	factors := must(w.identity.ListFactors(ctx, req(admin, &pluxv1.ListFactorsRequest{})))(t)
	if len(factors.GetFactors()) != 1 || !factors.GetFactors()[0].GetConfirmed() {
		t.Errorf("ListFactors = %+v", factors)
	}
	org := must(w.org.CreateOrganization(ctx, req(admin, &pluxv1.CreateOrganizationRequest{Key: "acme", Name: "Acme"})))(t).GetOrganization()
	admin.org = org.GetId()
	me = must(w.identity.GetCurrentUser(ctx, req(admin, &pluxv1.GetCurrentUserRequest{})))(t)
	if len(me.GetMemberships()) != 1 || len(me.GetPermissions()) != len(auth.Permissions()) {
		t.Errorf("GetCurrentUser in the organisation = %+v", me)
	}

	// Idempotency: a repeat returns the first result without acting.
	create := req(admin, &pluxv1.CreateAppRequest{Key: "shop", Name: "Shop"})
	create.Header().Set(api.IdempotencyHeader, "create-shop-1")
	first, err := w.app.CreateApp(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	repeat := req(admin, &pluxv1.CreateAppRequest{Key: "shop", Name: "Shop"})
	repeat.Header().Set(api.IdempotencyHeader, "create-shop-1")
	second, err := w.app.CreateApp(ctx, repeat)
	if err != nil || second.Msg.GetApp().GetId() != first.Msg.GetApp().GetId() || second.Header().Get(api.Replayed) != "true" {
		t.Fatalf("the repeat = %+v, %v", second, err)
	}
	other := req(admin, &pluxv1.CreateAppRequest{Key: "blog", Name: "Blog"})
	other.Header().Set(api.IdempotencyHeader, "create-shop-1")
	if _, err := w.app.CreateApp(ctx, other); codeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a key reused for another request: %v", err)
	}
	if _, err := w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: "shop", Name: "Again"})); codeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a duplicate app key: %v", err)
	}
	for _, key := range []string{"blog", "docs", "news"} {
		must(w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: key, Name: key})))(t)
	}

	// Paging, filtering and masks.
	var keys []string
	page := &pluxv1.Page{PageSize: 3, ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"key"}}}
	for {
		res := must(w.app.ListApps(ctx, req(admin, &pluxv1.ListAppsRequest{Page: page})))(t)
		for _, a := range res.GetApps() {
			if a.GetId() != "" {
				t.Error("the read mask did not remove the ID")
			}
			keys = append(keys, a.GetKey())
		}
		if res.GetPage().GetNextPageToken() == "" {
			break
		}
		page = &pluxv1.Page{PageSize: 3, PageToken: res.GetPage().GetNextPageToken(), ReadMask: page.GetReadMask()}
	}
	if strings.Join(keys, ",") != "blog,docs,news,shop" {
		t.Errorf("paged keys = %v", keys)
	}
	filtered := must(w.app.ListApps(ctx, req(admin, &pluxv1.ListAppsRequest{Page: &pluxv1.Page{Filter: `key = "docs"`}})))(t)
	if len(filtered.GetApps()) != 1 {
		t.Errorf("filtered = %+v", filtered.GetApps())
	}
	if _, err := w.app.ListApps(ctx, req(admin, &pluxv1.ListAppsRequest{Page: &pluxv1.Page{PageToken: "forged"}})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a forged page token: %v", err)
	}

	// Environments, variables, secrets, channels.
	shop := first.Msg.GetApp().GetId()
	envs := must(w.app.ListEnvironments(ctx, req(admin, &pluxv1.ListEnvironmentsRequest{AppId: shop, Page: &pluxv1.Page{Filter: `production = "true"`}})))(t)
	if len(envs.GetEnvironments()) != 1 {
		t.Fatalf("production environments = %+v", envs.GetEnvironments())
	}
	prod := envs.GetEnvironments()[0].GetId()
	qa := must(w.app.CreateEnvironment(ctx, req(admin, &pluxv1.CreateEnvironmentRequest{AppId: shop, Key: "qa", Name: "QA"})))(t).GetEnvironment()
	must(w.app.UpdateEnvironment(ctx, req(admin, &pluxv1.UpdateEnvironmentRequest{Id: qa.GetId(), Name: "Quality"})))(t)
	must(w.app.SetVariable(ctx, req(admin, &pluxv1.SetVariableRequest{EnvironmentId: prod, Key: "API", Value: "https://api.example"})))(t)
	if vars := must(w.app.ListVariables(ctx, req(admin, &pluxv1.ListVariablesRequest{EnvironmentId: prod})))(t); len(vars.GetVariables()) != 1 {
		t.Errorf("ListVariables = %+v", vars)
	}
	secret := must(w.app.SetSecret(ctx, req(admin, &pluxv1.SetSecretRequest{EnvironmentId: prod, Key: "TOKEN", Value: "abcdefghijkl"})))(t)
	if secret.GetSecret().GetHint() != "…ijkl" {
		t.Errorf("SetSecret = %+v", secret)
	}
	if list := must(w.app.ListSecrets(ctx, req(admin, &pluxv1.ListSecretsRequest{EnvironmentId: prod})))(t); len(list.GetSecrets()) != 1 {
		t.Errorf("ListSecrets = %+v", list)
	}
	must(w.app.DeleteSecret(ctx, req(admin, &pluxv1.DeleteSecretRequest{EnvironmentId: prod, Key: "TOKEN"})))(t)
	beta := must(w.app.CreateChannel(ctx, req(admin, &pluxv1.CreateChannelRequest{EnvironmentId: prod, Key: "beta"})))(t).GetChannel()
	if chs := must(w.app.ListChannels(ctx, req(admin, &pluxv1.ListChannelsRequest{EnvironmentId: prod})))(t); len(chs.GetChannels()) != 2 {
		t.Errorf("ListChannels = %+v", chs)
	}
	must(w.app.DeleteChannel(ctx, req(admin, &pluxv1.DeleteChannelRequest{Id: beta.GetId()})))(t)
	must(w.app.DeleteEnvironment(ctx, req(admin, &pluxv1.DeleteEnvironmentRequest{Id: qa.GetId()})))(t)
	must(w.app.UpdateApp(ctx, req(admin, &pluxv1.UpdateAppRequest{Id: shop, Name: "Shop!"})))(t)
	if got := must(w.app.GetApp(ctx, req(admin, &pluxv1.GetAppRequest{Id: shop})))(t); got.GetApp().GetName() != "Shop!" {
		t.Errorf("GetApp = %+v", got)
	}

	// Members, teams, access.
	added := must(w.org.AddMember(ctx, req(admin, &pluxv1.AddMemberRequest{Email: "bob@example.com", Role: "developer"})))(t)
	if added.GetInvitation() == "" {
		t.Fatal("a new member got no invitation")
	}
	must(w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{
		Invitation: added.GetInvitation(), DisplayName: "Bob", Password: password,
	})))(t)
	team := must(w.org.CreateTeam(ctx, req(admin, &pluxv1.CreateTeamRequest{Key: "web", Name: "Web"})))(t).GetTeam()
	must(w.org.UpdateTeam(ctx, req(admin, &pluxv1.UpdateTeamRequest{Id: team.GetId(), Name: "Web team"})))(t)
	if teams := must(w.org.ListTeams(ctx, req(admin, &pluxv1.ListTeamsRequest{})))(t); len(teams.GetTeams()) != 1 {
		t.Errorf("ListTeams = %+v", teams)
	}
	members := must(w.org.ListMembers(ctx, req(admin, &pluxv1.ListMembersRequest{Page: &pluxv1.Page{Filter: `role = "developer"`}})))(t)
	if len(members.GetMembers()) != 1 || members.GetMembers()[0].GetEmail() != "bob@example.com" {
		t.Errorf("ListMembers = %+v", members)
	}
	grant := must(w.app.GrantAccess(ctx, req(admin, &pluxv1.GrantAccessRequest{AppId: shop, TeamId: team.GetId(), Role: "viewer"})))(t).GetGrant()
	if grants := must(w.app.ListAccess(ctx, req(admin, &pluxv1.ListAccessRequest{AppId: shop})))(t); len(grants.GetGrants()) != 1 {
		t.Errorf("ListAccess = %+v", grants)
	}
	must(w.app.RevokeAccess(ctx, req(admin, &pluxv1.RevokeAccessRequest{Id: grant.GetId()})))(t)
	must(w.org.DeleteTeam(ctx, req(admin, &pluxv1.DeleteTeamRequest{Id: team.GetId()})))(t)

	bob := w.signIn(t, "bob@example.com")
	bob.org = org.GetId()
	if _, err := w.org.AddMember(ctx, req(bob, &pluxv1.AddMemberRequest{Email: "eve@example.com", Role: "owner"})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a developer added a member: %v", err)
	}
	if _, err := w.app.SetSecret(ctx, req(bob, &pluxv1.SetSecretRequest{EnvironmentId: prod, Key: "K", Value: "v"})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a developer set a secret: %v", err)
	}
	stranger := bob
	stranger.org = "018f0000-0000-7000-8000-000000000000"
	if _, err := w.app.ListApps(ctx, req(stranger, &pluxv1.ListAppsRequest{})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("an organisation the caller is not in: %v", err)
	}
	if _, err := w.app.ListApps(ctx, req(bob, &pluxv1.ListAppsRequest{OrganizationId: stranger.org})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("header and request naming different organisations: %v", err)
	}
	must(w.org.RemoveMember(ctx, req(admin, &pluxv1.RemoveMemberRequest{UserId: added.GetMember().GetUserId()})))(t)

	// Tokens: a personal access token works as a bearer token, bound to
	// its organisation.
	minted := must(w.identity.CreateAccessToken(ctx, req(admin, &pluxv1.CreateAccessTokenRequest{
		Name: "laptop", Scopes: []string{"app.read"}, TtlSeconds: 3600,
	})))(t)
	robot := caller{bearer: minted.GetSecret()}
	if apps := must(w.app.ListApps(ctx, req(robot, &pluxv1.ListAppsRequest{})))(t); len(apps.GetApps()) != 4 {
		t.Errorf("the token listed %d apps", len(apps.GetApps()))
	}
	if _, err := w.app.CreateApp(ctx, req(robot, &pluxv1.CreateAppRequest{Key: "x", Name: "x"})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a read-only token created an app: %v", err)
	}
	tokens := must(w.identity.ListAccessTokens(ctx, req(admin, &pluxv1.ListAccessTokensRequest{Page: &pluxv1.Page{Filter: `source = "pat"`}})))(t)
	if len(tokens.GetTokens()) != 1 {
		t.Errorf("ListAccessTokens = %+v", tokens)
	}
	must(w.identity.RevokeAccessToken(ctx, req(admin, &pluxv1.RevokeAccessTokenRequest{Id: minted.GetToken().GetId()})))(t)
	if _, err := w.app.ListApps(ctx, req(robot, &pluxv1.ListAppsRequest{})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a revoked token: %v", err)
	}

	// `plux login` through the API.
	grantRes := must(w.identity.StartDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.StartDeviceAuthorizationRequest{Client: "plux-cli"})))(t)
	poll := must(w.identity.PollDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.PollDeviceAuthorizationRequest{DeviceCode: grantRes.GetDeviceCode()})))(t)
	if poll.GetStatus() != auth.PollPending {
		t.Errorf("poll = %+v", poll)
	}
	must(w.identity.ApproveDeviceAuthorization(ctx, req(admin, &pluxv1.ApproveDeviceAuthorizationRequest{UserCode: grantRes.GetUserCode()})))(t)
	poll = must(w.identity.PollDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.PollDeviceAuthorizationRequest{DeviceCode: grantRes.GetDeviceCode()})))(t)
	if poll.GetStatus() != auth.PollApproved || poll.GetSecret() == "" {
		t.Errorf("poll after approval = %+v", poll)
	}
	denied := must(w.identity.StartDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.StartDeviceAuthorizationRequest{Client: "plux-cli"})))(t)
	must(w.identity.DenyDeviceAuthorization(ctx, req(admin, &pluxv1.DenyDeviceAuthorizationRequest{UserCode: denied.GetUserCode()})))(t)

	// Workload identities: this installation trusts no issuer.
	if _, err := w.identity.CreateWorkloadIdentity(ctx, req(admin, &pluxv1.CreateWorkloadIdentityRequest{
		Issuer: "https://token.actions.githubusercontent.com", Audience: "x", SubjectPattern: "repo:a/b", Scopes: []string{"app.read"},
	})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an untrusted issuer: %v", err)
	}
	if ws := must(w.identity.ListWorkloadIdentities(ctx, req(admin, &pluxv1.ListWorkloadIdentitiesRequest{})))(t); len(ws.GetIdentities()) != 0 {
		t.Errorf("ListWorkloadIdentities = %+v", ws)
	}
	if _, err := w.identity.DeleteWorkloadIdentity(ctx, req(admin, &pluxv1.DeleteWorkloadIdentityRequest{Id: "018f0000-0000-7000-8000-000000000000"})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("deleting a missing workload identity: %v", err)
	}
	if _, err := w.identity.ExchangeWorkloadIdentity(ctx, connect.NewRequest(&pluxv1.ExchangeWorkloadIdentityRequest{IdentityToken: "a.b.c", OrganizationId: org.GetId()})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("exchanging a bad token: %v", err)
	}

	// Limits.
	limitsRes := must(w.org.ListOrganizationLimits(ctx, req(admin, &pluxv1.ListOrganizationLimitsRequest{})))(t)
	if len(limitsRes.GetLimits()) == 0 {
		t.Error("no organisation limits listed")
	}
	must(w.org.SetOrganizationLimit(ctx, req(admin, &pluxv1.SetOrganizationLimitRequest{Key: "app.plugins", Value: 10})))(t)
	must(w.app.SetAppLimit(ctx, req(admin, &pluxv1.SetAppLimitRequest{AppId: shop, Key: "app.plugins", Value: 5})))(t)
	if appLimits := must(w.app.ListAppLimits(ctx, req(admin, &pluxv1.ListAppLimitsRequest{AppId: shop})))(t); len(appLimits.GetLimits()) == 0 {
		t.Error("no app limits listed")
	}

	// Trash.
	trash := must(w.app.DeleteApp(ctx, req(admin, &pluxv1.DeleteAppRequest{Id: shop})))(t).GetTrash()
	items := must(w.app.ListTrash(ctx, req(admin, &pluxv1.ListTrashRequest{Page: &pluxv1.Page{Filter: `kind = "app"`}})))(t)
	if len(items.GetItems()) != 1 {
		t.Errorf("ListTrash = %+v", items)
	}
	must(w.app.RestoreFromTrash(ctx, req(admin, &pluxv1.RestoreFromTrashRequest{Id: trash.GetId()})))(t)
	trash = must(w.app.DeleteApp(ctx, req(admin, &pluxv1.DeleteAppRequest{Id: shop})))(t).GetTrash()
	must(w.app.PurgeFromTrash(ctx, req(admin, &pluxv1.PurgeFromTrashRequest{Id: trash.GetId()})))(t)

	// The organisation, then the audit log.
	must(w.org.UpdateOrganization(ctx, req(admin, &pluxv1.UpdateOrganizationRequest{Id: org.GetId(), Name: "Acme Ltd"})))(t)
	if got := must(w.org.GetOrganization(ctx, req(admin, &pluxv1.GetOrganizationRequest{Id: org.GetId()})))(t); got.GetOrganization().GetName() != "Acme Ltd" {
		t.Errorf("GetOrganization = %+v", got)
	}
	if orgs := must(w.org.ListOrganizations(ctx, req(admin, &pluxv1.ListOrganizationsRequest{})))(t); len(orgs.GetOrganizations()) != 1 {
		t.Errorf("ListOrganizations = %+v", orgs)
	}
	entries := must(w.identity.ListAuditEntries(ctx, req(admin, &pluxv1.ListAuditEntriesRequest{Page: &pluxv1.Page{Filter: `action = "app.created"`}})))(t)
	if len(entries.GetEntries()) != 4 {
		t.Errorf("app.created entries = %d; want 4", len(entries.GetEntries()))
	}
	installation := admin
	installation.org = ""
	if log := must(w.identity.ListAuditEntries(ctx, req(installation, &pluxv1.ListAuditEntriesRequest{})))(t); len(log.GetEntries()) == 0 {
		t.Error("the installation's audit log is empty")
	}

	// Second factor, password, sign-out.
	code2, _ := auth.TOTPCode(enrolled.GetSecret(), time.Now().Add(30*time.Second))
	must(w.identity.VerifySecondFactor(ctx, req(admin, &pluxv1.VerifySecondFactorRequest{Code: code2})))(t)
	must(w.identity.ChangePassword(ctx, req(admin, &pluxv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "another good passphrase"})))(t)
	must(w.identity.DeleteFactor(ctx, req(admin, &pluxv1.DeleteFactorRequest{FactorId: enrolled.GetFactor().GetId()})))(t)
	out, err := w.identity.Logout(ctx, req(admin, &pluxv1.LogoutRequest{}))
	if err != nil || !strings.Contains(out.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("Logout = %v; Set-Cookie %q", err, out.Header().Get("Set-Cookie"))
	}
	if _, err := w.identity.GetCurrentUser(ctx, req(admin, &pluxv1.GetCurrentUserRequest{})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a call after sign-out: %v", err)
	}
}

// Verifies: SEC-100.
// Signing in with a second factor goes through a challenge.
func TestSignInWithChallengeOverTheAPI(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	const password = "correct horse battery"
	_, invitation, _ := w.auth.Bootstrap(ctx, "admin@example.com")
	must(w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{Invitation: invitation, DisplayName: "A", Password: password})))(t)
	admin := w.signIn(t, "admin@example.com")
	enrolled := must(w.identity.EnrollTotp(ctx, req(admin, &pluxv1.EnrollTotpRequest{})))(t)
	c, _ := auth.TOTPCode(enrolled.GetSecret(), time.Now())
	must(w.identity.ConfirmFactor(ctx, req(admin, &pluxv1.ConfirmFactorRequest{FactorId: enrolled.GetFactor().GetId(), Code: c})))(t)
	started := must(w.identity.StartPasswordLogin(ctx, connect.NewRequest(&pluxv1.StartPasswordLoginRequest{Email: "admin@example.com", Password: password})))(t)
	if started.GetSession() != nil || started.GetChallenge().GetId() == "" {
		t.Fatalf("StartPasswordLogin = %+v", started)
	}
	next, _ := auth.TOTPCode(enrolled.GetSecret(), time.Now().Add(30*time.Second))
	done, err := w.identity.CompleteMfa(ctx, connect.NewRequest(&pluxv1.CompleteMfaRequest{ChallengeId: started.GetChallenge().GetId(), Code: next}))
	if err != nil {
		t.Fatalf("CompleteMfa: %v", err)
	}
	session := w.fromSession(t, done.Header(), done.Msg.GetSession())
	if !done.Msg.GetSession().GetSecondFactor() {
		t.Error("a session opened with a second factor does not say so")
	}
	must(w.identity.GetCurrentUser(ctx, req(session, &pluxv1.GetCurrentUserRequest{})))(t)
}
