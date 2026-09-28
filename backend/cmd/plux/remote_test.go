// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/zalando/go-keyring"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// fake is a Plux Server with just enough behaviour for the CLI.
type fake struct {
	pluxv1connect.UnimplementedIdentityServiceHandler
	pluxv1connect.UnimplementedAppServiceHandler
	pluxv1connect.UnimplementedPluginServiceHandler
	pluxv1connect.UnimplementedDocumentServiceHandler
	pluxv1connect.UnimplementedPublishServiceHandler
	pluxv1connect.UnimplementedReleaseServiceHandler
	pluxv1connect.UnimplementedManifestServiceHandler

	mu      sync.Mutex
	token   string
	polls   int
	drafts  map[string][]byte
	bundle  []byte
	hash    string
	fail    bool
	promote []string
}

func (f *fake) authorised(h http.Header) error {
	if h.Get("Authorization") != "Bearer "+f.token {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return nil
}

func (*fake) StartDeviceAuthorization(context.Context, *connect.Request[pluxv1.StartDeviceAuthorizationRequest]) (*connect.Response[pluxv1.StartDeviceAuthorizationResponse], error) {
	return connect.NewResponse(&pluxv1.StartDeviceAuthorizationResponse{DeviceCode: "dc", UserCode: "ABCD-EFGH", VerificationUri: "https://studio/device", IntervalSeconds: 0}), nil
}

func (f *fake) PollDeviceAuthorization(_ context.Context, r *connect.Request[pluxv1.PollDeviceAuthorizationRequest]) (*connect.Response[pluxv1.PollDeviceAuthorizationResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.polls < 2 {
		return connect.NewResponse(&pluxv1.PollDeviceAuthorizationResponse{Status: "pending"}), nil
	}
	return connect.NewResponse(&pluxv1.PollDeviceAuthorizationResponse{Status: "approved", Secret: f.token}), nil
}

func (f *fake) GetCurrentUser(_ context.Context, r *connect.Request[pluxv1.GetCurrentUserRequest]) (*connect.Response[pluxv1.GetCurrentUserResponse], error) {
	if err := f.authorised(r.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluxv1.GetCurrentUserResponse{
		User:        &pluxv1.User{Id: "u1", DisplayName: "Ada", Email: "ada@example.com"},
		Memberships: []*pluxv1.Member{{OrganizationId: "o1", Role: "owner"}},
	}), nil
}

func (f *fake) ListApps(_ context.Context, r *connect.Request[pluxv1.ListAppsRequest]) (*connect.Response[pluxv1.ListAppsResponse], error) {
	if err := f.authorised(r.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluxv1.ListAppsResponse{Apps: []*pluxv1.App{{Id: "a1", Key: "demo", OrganizationId: "o1"}}}), nil
}

func (*fake) GetApp(_ context.Context, r *connect.Request[pluxv1.GetAppRequest]) (*connect.Response[pluxv1.GetAppResponse], error) {
	return connect.NewResponse(&pluxv1.GetAppResponse{App: &pluxv1.App{Id: r.Msg.GetId()}}), nil
}

func (*fake) ListEnvironments(context.Context, *connect.Request[pluxv1.ListEnvironmentsRequest]) (*connect.Response[pluxv1.ListEnvironmentsResponse], error) {
	return connect.NewResponse(&pluxv1.ListEnvironmentsResponse{Environments: []*pluxv1.Environment{{Id: "e-dev", Key: "development"}, {Id: "e-prod", Key: "production"}}}), nil
}

func (*fake) ListChannels(_ context.Context, _ *connect.Request[pluxv1.ListChannelsRequest]) (*connect.Response[pluxv1.ListChannelsResponse], error) {
	return connect.NewResponse(&pluxv1.ListChannelsResponse{Channels: []*pluxv1.Channel{{Key: "production", ReleaseSequence: 3}}}), nil
}

func (*fake) ListPlugins(context.Context, *connect.Request[pluxv1.ListPluginsRequest]) (*connect.Response[pluxv1.ListPluginsResponse], error) {
	return connect.NewResponse(&pluxv1.ListPluginsResponse{Plugins: []*pluxv1.Plugin{{Id: "p1", Key: "loans"}}}), nil
}

func (f *fake) ExportDraft(_ context.Context, _ *connect.Request[pluxv1.ExportDraftRequest], s *connect.ServerStream[pluxv1.ExportDraftResponse]) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, c := range f.drafts {
		if err := s.Send(&pluxv1.ExportDraftResponse{Path: p, Content: c}); err != nil {
			return err
		}
	}
	return nil
}

func (f *fake) ImportDraft(_ context.Context, s *connect.ClientStream[pluxv1.ImportDraftRequest]) (*connect.Response[pluxv1.ImportDraftResponse], error) {
	files := map[string][]byte{}
	for s.Receive() {
		files[s.Msg().GetPath()] = s.Msg().GetContent()
	}
	f.mu.Lock()
	f.drafts = files
	f.mu.Unlock()
	return connect.NewResponse(&pluxv1.ImportDraftResponse{Revision: 7}), s.Err()
}

func (*fake) Publish(_ context.Context, r *connect.Request[pluxv1.PublishRequest]) (*connect.Response[pluxv1.PublishResponse], error) {
	return connect.NewResponse(&pluxv1.PublishResponse{Job: &pluxv1.PublishJob{Id: "j-" + r.Msg.GetPluginId(), State: "queued"}}), nil
}

func (f *fake) WatchPublish(_ context.Context, r *connect.Request[pluxv1.WatchPublishRequest], s *connect.ServerStream[pluxv1.WatchPublishResponse]) error {
	state := "succeeded"
	if f.fail {
		state = "failed"
	}
	_ = s.Send(&pluxv1.WatchPublishResponse{Job: &pluxv1.PublishJob{Id: r.Msg.GetJobId(), State: "running"}})
	return s.Send(&pluxv1.WatchPublishResponse{Job: &pluxv1.PublishJob{
		Id: r.Msg.GetJobId(), State: state, Version: 2,
		Diagnostics: []*pluxv1.Diagnostic{{Code: "PLX-1401", Severity: pluxv1.Severity_SEVERITY_WARNING, Message: "no name"}},
	}})
}

func (*fake) CreateRelease(_ context.Context, r *connect.Request[pluxv1.CreateReleaseRequest]) (*connect.Response[pluxv1.CreateReleaseResponse], error) {
	if r.Msg.GetPluginVersions()["loans"] != 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(&pluxv1.CreateReleaseResponse{Release: &pluxv1.Release{Sequence: 4}}), nil
}

func (f *fake) PromoteRelease(_ context.Context, r *connect.Request[pluxv1.PromoteReleaseRequest]) (*connect.Response[pluxv1.PromoteReleaseResponse], error) {
	f.mu.Lock()
	f.promote = append(f.promote, r.Msg.GetEnvironmentId()+"/"+r.Msg.GetChannelKey())
	f.mu.Unlock()
	return connect.NewResponse(&pluxv1.PromoteReleaseResponse{}), nil
}

func (*fake) RollbackRelease(context.Context, *connect.Request[pluxv1.RollbackReleaseRequest]) (*connect.Response[pluxv1.RollbackReleaseResponse], error) {
	return connect.NewResponse(&pluxv1.RollbackReleaseResponse{Release: &pluxv1.Release{Sequence: 5}}), nil
}

func (*fake) ListReleases(context.Context, *connect.Request[pluxv1.ListReleasesRequest]) (*connect.Response[pluxv1.ListReleasesResponse], error) {
	return connect.NewResponse(&pluxv1.ListReleasesResponse{Releases: []*pluxv1.Release{{Sequence: 3, Notes: "n", CreatedBy: &pluxv1.Actor{Display: "Ada"}}}}), nil
}

func (f *fake) GetRelease(context.Context, *connect.Request[pluxv1.GetReleaseRequest]) (*connect.Response[pluxv1.GetReleaseResponse], error) {
	return connect.NewResponse(&pluxv1.GetReleaseResponse{Release: &pluxv1.Release{Sequence: 3}, Versions: []*pluxv1.PluginVersion{
		{PluginKey: "", Version: 1, BundleSha256: f.hash, KeyId: "k1", Algorithm: "ed25519", Signature: []byte{9, 9}},
		{PluginKey: "loans", Version: 2, BundleSha256: f.hash, KeyId: "k1", Algorithm: "ed25519", Signature: []byte{8, 8}},
	}}), nil
}

func (*fake) GetRootKeys(context.Context, *connect.Request[pluxv1.GetRootKeysRequest]) (*connect.Response[pluxv1.GetRootKeysResponse], error) {
	return connect.NewResponse(&pluxv1.GetRootKeysResponse{Keys: []*pluxv1.PublicKey{{KeyId: "k1", Algorithm: "ed25519", Role: "targets", PublicKey: []byte{1, 2}}}}), nil
}

// newFake starts the fake server and returns it with its URL.
func newFake(t *testing.T) (*fake, string) {
	t.Helper()
	b, err := bundle.Encode(bundle.KindPlugin, []bundle.Section{{Kind: bundle.SectionStrings, Data: []byte("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	read, _ := bundle.ReadStructure(b)
	f := &fake{token: "plux_pat_test", bundle: b, hash: hex.EncodeToString(read.Hash[:]), drafts: map[string][]byte{}} //nolint:gosec // G101: a test token.
	mux := http.NewServeMux()
	for _, r := range []func() (string, http.Handler){
		func() (string, http.Handler) { return pluxv1connect.NewIdentityServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewAppServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewPluginServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewDocumentServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewPublishServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewReleaseServiceHandler(f) },
		func() (string, http.Handler) { return pluxv1connect.NewManifestServiceHandler(f) },
	} {
		p, h := r()
		mux.Handle(p, h)
	}
	mux.HandleFunc("GET /v1/objects/bundles/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(f.bundle) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// cli runs the CLI with a mocked keychain and a private config dir.
func cli(t *testing.T, config string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := newEnv(&out, &errOut)
	e.creds = credentials{dir: config}
	cmd, ok := e.commands()[args[0]]
	if !ok {
		t.Fatalf("no command %s", args[0])
	}
	return cmd(args[1:]), out.String(), errOut.String()
}

// Verifies: CLI-002, CLI-003, CLI-004, CLI-007, CLI-008.
// Every server command, against a fake server: sign-in through the
// device grant into the keychain, the project commands with --json, and
// the documented exit codes.
func TestServerCommands(t *testing.T) { //nolint:paralleltest // the keychain mock and environment are process-wide
	keyring.MockInit()
	t.Setenv(tokenEnv, "")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	f, url := newFake(t)
	config := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "app.json"), []byte(`{"b": 1, "a": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := cli(t, config, "whoami", "--server", url); code != exitAuth {
		t.Errorf("whoami before login: %d", code)
	}
	if code, _, stderr := cli(t, config, "whoami"); code != exitUsage || !strings.Contains(stderr, "no server") {
		t.Errorf("whoami without a server: %d %s", code, stderr)
	}
	if code, _, _ := cli(t, config, "whoami", "--server", "ftp://x"); code != exitUsage {
		t.Errorf("a non-HTTP server: %d", code)
	}
	code, out, stderr := cli(t, config, "login", "--server", url, "--json")
	if code != exitOK || !strings.Contains(out, `"stored": "keychain"`) || !strings.Contains(stderr, "ABCD-EFGH") {
		t.Fatalf("login: %d %s %s", code, out, stderr)
	}
	if tok, _ := keyring.Get(keyringService, url); tok != f.token {
		t.Errorf("the keychain holds %q", tok)
	}
	if code, out, _ := cli(t, config, "whoami", "--server", url, "--json"); code != exitOK || !strings.Contains(out, "ada@example.com") {
		t.Errorf("whoami: %d %s", code, out)
	}
	if code, out, _ := cli(t, config, "init", "--server", url, "--app", "demo", "-C", project, "--json"); code != exitOK || !strings.Contains(out, `"app": "a1"`) {
		t.Fatalf("init: %d %s", code, out)
	}
	if code, _, _ := cli(t, config, "init", "--server", url, "--app", "nope", "-C", project); code != exitFailed {
		t.Errorf("init with an unknown app: %d", code)
	}
	if code, out, _ := cli(t, config, "diff", "-C", project, "--json"); code != exitFailed || !strings.Contains(out, `"added"`) {
		t.Errorf("diff before import: %d %s", code, out)
	}
	if code, _, _ := cli(t, config, "import", "-C", project); code != exitOK {
		t.Errorf("import: %d", code)
	}
	f.drafts["app.json"] = []byte(`{"a":2,"b":1}`) // canonical on the server
	if code, out, _ := cli(t, config, "diff", "-C", project); code != exitOK || out != "" {
		t.Errorf("diff after import: %d %q", code, out)
	}
	exported := t.TempDir()
	if code, _, _ := cli(t, config, "export", "-C", project, "-o", exported); code != exitOK {
		t.Errorf("export: %d", code)
	}
	if _, err := os.Stat(filepath.Join(exported, "app.json")); err != nil {
		t.Errorf("export wrote nothing: %v", err)
	}
	code, out, _ = cli(t, config, "publish", "-C", project, "--release", "--promote", "production/beta", "--json")
	var pub struct {
		OK        bool
		Release   int64
		Publishes []published
	}
	if code != exitOK || json.Unmarshal([]byte(out), &pub) != nil || !pub.OK || pub.Release != 4 || len(pub.Publishes) != 2 || f.promote[0] != "e-prod/beta" {
		t.Errorf("publish: %d %s %v", code, out, f.promote)
	}
	f.fail = true
	if code, out, _ := cli(t, config, "publish", "-C", project, "--no-import"); code != exitFailed || !strings.Contains(out, "PLX-1401") {
		t.Errorf("a failed publish: %d %s", code, out)
	}
	f.fail = false
	host := t.TempDir()
	code, out, _ = cli(t, config, "pull", "-C", project, "-o", host, "--json")
	if code != exitOK || !strings.Contains(out, `"releaseSequence": 3`) {
		t.Errorf("pull: %d %s", code, out)
	}
	for _, p := range []string{"bundles/_app.pxb", "bundles/loans.pxb", "keys.json", "baseline.json"} {
		if _, err := os.Stat(filepath.Join(host, p)); err != nil {
			t.Errorf("pull did not write %s", p)
		}
	}
	// Each bundle carries its publish signature, so the runtime can verify
	// the baseline under the embedded keys (SEC-052, ADR-0029).
	if data, err := os.ReadFile(filepath.Join(host, "baseline.json")); err != nil ||
		!strings.Contains(string(data), `"signature": "CQk="`) || !strings.Contains(string(data), `"keyId": "k1"`) {
		t.Errorf("baseline.json lacks the bundle signatures: %v %s", err, data)
	}
	f.bundle = []byte("tampered")
	if code, _, stderr := cli(t, config, "pull", "-C", project, "-o", host); code != exitFailed || !strings.Contains(stderr, "does not match") {
		t.Errorf("a tampered bundle: %d %s", code, stderr)
	}
	if code, out, _ := cli(t, config, "keys", "-C", project); code != exitOK || !strings.Contains(out, "k1  ed25519  targets  0102") {
		t.Errorf("keys: %d %s", code, out)
	}
	if code, out, _ := cli(t, config, "release", "list", "-C", project); code != exitOK || !strings.Contains(out, "Ada") {
		t.Errorf("release list: %d %s", code, out)
	}
	if code, _, _ := cli(t, config, "release", "promote", "-C", project, "--env", "production", "3"); code != exitOK {
		t.Errorf("release promote: %d", code)
	}
	if code, out, _ := cli(t, config, "release", "rollback", "-C", project, "--env", "production", "--json", "2"); code != exitOK || !strings.Contains(out, `"sequence": 5`) {
		t.Errorf("release rollback: %d %s", code, out)
	}
	for _, args := range [][]string{
		{"release"},
		{"release", "nope"},
		{"release", "promote", "-C", project, "3"},
		{"release", "promote", "-C", project, "--env", "production", "x"},
		{"release", "promote", "-C", project, "--env", "moon", "3"},
		{"completion"},
		{"completion", "tcsh"},
		{"keys", "extra"},
	} {
		if code, _, _ := cli(t, config, args...); code != exitUsage {
			t.Errorf("%v: %d, want usage", args, code)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if code, out, _ := cli(t, config, "completion", shell); code != exitOK || !strings.Contains(out, "publish") {
			t.Errorf("completion %s: %d", shell, code)
		}
	}
	if code, out, _ := cli(t, config, "doctor", "-C", project, "--json"); code != exitFailed || !strings.Contains(out, `"sign-in"`) {
		t.Errorf("doctor (the project does not compile): %d %s", code, out)
	}
	if code, _, _ := cli(t, config, "logout", "--server", url); code != exitOK {
		t.Errorf("logout: %d", code)
	}
	if code, _, _ := cli(t, config, "whoami", "--server", url); code != exitAuth {
		t.Errorf("whoami after logout: %d", code)
	}
	// PLUX_TOKEN needs no stored credential.
	t.Setenv(tokenEnv, f.token)
	if code, _, _ := cli(t, config, "whoami", "--server", url); code != exitOK {
		t.Errorf("whoami with PLUX_TOKEN: %d", code)
	}
	t.Setenv(tokenEnv, "wrong")
	if code, _, _ := cli(t, config, "whoami", "--server", url); code != exitAuth {
		t.Errorf("a refused token: %d", code)
	}
	if code, _, _ := cli(t, config, "whoami", "--server", "http://127.0.0.1:1"); code != exitUnavailable {
		t.Errorf("an unreachable server: %d", code)
	}
}

// Verifies: CLI-002.
// Without a reachable credential store, the token goes to a file only
// its owner can read, and logout removes it.
func TestCredentialFileFallback(t *testing.T) {
	t.Parallel()
	c := credentials{dir: filepath.Join(t.TempDir(), "plux")}
	if err := c.writeFile(map[string]string{"https://a": "t1"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(c.file())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
	all, err := c.readFile()
	if err != nil || all["https://a"] != "t1" {
		t.Errorf("readFile: %v %v", all, err)
	}
	if err := c.forgetFile("https://a"); err != nil {
		t.Fatal(err)
	}
	if all, _ := c.readFile(); len(all) != 0 {
		t.Errorf("after forget: %v", all)
	}
	if err := os.WriteFile(c.file(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.readFile(); err == nil {
		t.Error("a corrupt file was read")
	}
}
