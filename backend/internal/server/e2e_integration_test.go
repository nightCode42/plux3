// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/delta"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: CLI-002, CLI-003, CLI-004, CLI-007, REL-020, REL-030, REL-031, REL-032.
// The exit criterion of P2: a plugin authored as JSON is published with
// the CLI against a running server with both roles, and a test client
// fetches the manifest and a delta, verifies the signature, rebuilds the
// new bundle and checks it against the signed hash.
func TestPublishWithTheCLIAndSyncADevice(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	const addr = "127.0.0.1:18093"
	server := "http://" + addr
	cfg := testConfig(t, "server:\n  roles: [api, worker]\n  listen: \""+addr+"\"\n  publicBaseURL: \""+server+"\"\n"+
		"database:\n  url: \""+url+"\"\n"+
		"objectStorage:\n  directory: \""+filepath.Join(dir, "objects")+"\"\n"+
		"signing:\n  directory: \""+filepath.Join(dir, "keys")+"\"\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	built, err := Build(ctx, cfg, discard(), "test")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer built.Close()
	done := make(chan error, 1)
	go func() { done <- built.Server.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitReady(t, server)

	// An administrator with a token, an organisation and an app, made
	// with the services directly.
	db, err := storage.Open(ctx, storage.Options{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backend, err := BuildSigning(cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{})
	if err != nil {
		t.Fatal(err)
	}
	user, invitation, err := svc.Auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Auth.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	id := auth.Identity{Kind: auth.KindUser, ID: user.ID, UserID: user.ID, Display: "Admin", SecondFactor: true, InstallationAdmin: true}
	org, err := svc.Tenancy.CreateOrganization(ctx, id, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.Auth.Resolve(ctx, id, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := svc.Tenancy.CreateApp(ctx, owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	minted, err := svc.Auth.CreateAccessToken(ctx, owner, "e2e", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// The CLI, built from source, run as a user would with PLUX_TOKEN.
	plux := filepath.Join(dir, "plux")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", plux, "../../cmd/plux").CombinedOutput(); err != nil { //nolint:gosec // G204: a path the test chose.
		t.Fatalf("go build: %v\n%s", err, out)
	}
	project := filepath.Join(dir, "project")
	copyTree(t, filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator"), project)
	run := func(want int, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, plux, args...) //nolint:gosec // G204: the binary the test built.
		cmd.Env = append(os.Environ(), "PLUX_TOKEN="+minted.Secret, "PLUX_SERVER=", "PLUX_ORGANIZATION=", "HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != want {
			t.Fatalf("plux %s: exit %d, want %d\n%s%s", strings.Join(args, " "), code, want, stdout.String(), stderr.String())
		}
		return stdout.Bytes()
	}
	run(0, "init", "--server", server, "--org", org.ID, "--app", "demo", "-C", project, "--json")
	var who struct{ Email string }
	decode(t, run(0, "whoami", "--server", server, "--json"), &who)
	if who.Email != "admin@example.com" {
		t.Errorf("whoami: %+v", who)
	}
	run(1, "diff", "-C", project) // nothing uploaded yet
	var pub struct {
		OK      bool
		Release int64
	}
	decode(t, run(0, "publish", "-C", project, "--promote", "staging", "--json"), &pub)
	if !pub.OK || pub.Release != 1 {
		t.Fatalf("publish: %+v", pub)
	}
	run(0, "diff", "-C", project)
	run(0, "doctor", "-C", project)
	var keys struct{ Keys []struct{ PublicKey string } }
	decode(t, run(0, "keys", "-C", project, "--env", "staging", "--json"), &keys)
	var base struct {
		ReleaseSequence int64
		Bundles         []struct{ Plugin, SHA256, File string }
	}
	decode(t, run(0, "pull", "-C", project, "--env", "staging", "-o", filepath.Join(dir, "host", "assets", "plux"), "--json"), &base)
	if base.ReleaseSequence != 1 || len(base.Bundles) != 2 || len(keys.Keys) != 1 {
		t.Fatalf("pull: %+v keys %+v", base, keys)
	}
	var appBundle []byte
	for _, b := range base.Bundles {
		data, err := os.ReadFile(filepath.Join(dir, "host", "assets", "plux", b.File))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := bundle.ReadStructure(data); err != nil || hex.EncodeToString(got.Hash[:]) != b.SHA256 {
			t.Errorf("the baseline bundle %s does not match its hash", b.File)
		}
		if b.Plugin == "" {
			appBundle = data
		}
	}
	var list struct{ Releases []struct{ Sequence int64 } }
	decode(t, run(0, "release", "list", "-C", project, "--json"), &list)
	if len(list.Releases) != 1 {
		t.Errorf("release list: %+v", list)
	}
	run(2, "release", "promote", "-C", project, "1") // --env missing

	// A device registers and syncs from nothing.
	hc := &http.Client{Timeout: 30 * time.Second}
	devices := pluxv1connect.NewDeviceServiceClient(hc, server)
	reg, err := devices.RegisterDevice(ctx, connect.NewRequest(&pluxv1.RegisterDeviceRequest{AppId: app.ID, Environment: "staging", Platform: "android", RuntimeVersion: "1.0.0"}))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := pluxv1connect.NewTokenServiceClient(hc, server).IssueDeviceToken(ctx, connect.NewRequest(&pluxv1.IssueDeviceTokenRequest{DeviceId: reg.Msg.GetDevice().GetId(), DeviceSecret: reg.Msg.GetDeviceSecret()}))
	if err != nil {
		t.Fatal(err)
	}
	manifests := pluxv1connect.NewManifestServiceClient(hc, server, connect.WithInterceptors(bearer(tok.Msg.GetAccessToken())))
	m1 := waitManifest(t, manifests, 1, nil)
	pub0, _ := hex.DecodeString(keys.Keys[0].PublicKey)
	if !ed25519.Verify(pub0, m1.GetSigned(), m1.GetSignatures()[0].GetSignature()) {
		t.Fatal("the manifest does not verify with the pulled key")
	}

	// Change one translated message, publish and promote release 2.
	en := filepath.Join(project, "translations", "en.json")
	data, err := os.ReadFile(en)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(en, bytes.Replace(data, []byte("Your schedule"), []byte("Your repayment plan"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	decode(t, run(0, "publish", "-C", project, "--promote", "staging", "--json"), &pub)
	if !pub.OK || pub.Release != 2 {
		t.Fatalf("the second publish: %+v", pub)
	}
	oldHash := strings.TrimPrefix(m1.GetAppBundle().GetSha256(), "sha256:")
	installed := []*pluxv1.InstalledBundle{{Key: "", Sha256: oldHash}}
	for _, p := range m1.GetPlugins() {
		installed = append(installed, &pluxv1.InstalledBundle{Key: p.GetKey(), Sha256: p.GetBundle().GetSha256()})
	}
	m2 := waitManifest(t, manifests, 2, installed)
	step := m2.GetAppBundle().GetSync()
	if step.GetAction() != "delta" || step.GetSize() > 2048 {
		t.Fatalf("the app bundle's sync step: %+v", step)
	}
	d := fetch(t, hc, step.GetUrl())
	rebuilt, err := delta.Apply(appBundle, d, 1<<24)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, err := bundle.ReadStructure(rebuilt); err != nil || "sha256:"+hex.EncodeToString(got.Hash[:]) != m2.GetAppBundle().GetSha256() {
		t.Fatal("the rebuilt bundle does not match the signed manifest")
	}
	if !ed25519.Verify(pub0, m2.GetSigned(), m2.GetSignatures()[0].GetSignature()) {
		t.Fatal("the second manifest does not verify")
	}
}

func waitReady(t *testing.T, server string) {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if res, err := get(t, c, server+"/readyz"); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
	}
	t.Fatal("the server did not become ready")
}

// waitManifest polls until the worker has signed the manifest of a
// release.
func waitManifest(t *testing.T, c pluxv1connect.ManifestServiceClient, seq int64, installed []*pluxv1.InstalledBundle) *pluxv1.Manifest {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		res, err := c.GetManifest(context.Background(), connect.NewRequest(&pluxv1.GetManifestRequest{Installed: installed}))
		if err == nil && res.Msg.GetManifest().GetReleaseSequence() == seq {
			return res.Msg.GetManifest()
		}
	}
	t.Fatalf("no manifest for release %d", seq)
	return nil
}

func fetch(t *testing.T, c *http.Client, url string) []byte {
	t.Helper()
	res, err := get(t, c, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("GET %s: %d %v", url, res.StatusCode, err)
	}
	return data
}

func decode(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%v in %s", err, data)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := fs.WalkDir(os.DirFS(from), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(from, path))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to, path)), 0o750); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, path), data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// bearer adds a device token to every call.
type bearer string

func (b bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
		r.Header().Set("Authorization", "Bearer "+string(b))
		return next(ctx, r)
	}
}

func (bearer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (bearer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
