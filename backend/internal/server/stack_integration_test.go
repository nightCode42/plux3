// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// stack is a running server with both roles, an administrator's access
// token, an organisation and an app, and the CLI built from source: what
// the end-to-end tests drive.
type stack struct {
	ctx    context.Context
	server string
	dir    string
	svc    *Services
	owner  auth.Principal
	org    string
	app    tenancy.App
	token  string
	cli    string
}

// stackOptions change how startStackWith configures the server.
type stackOptions struct {
	// publicBaseURL is where devices reach the server, when not at addr.
	publicBaseURL string
	// serverYAML is added to the configuration's server section, as
	// indented lines.
	serverYAML string
}

// freeAddr returns a loopback address nothing listens on, which the caller
// is about to bind. A fixed port would let a second process (another test
// run, another checkout) answer the readiness probe of a server that never
// got the port, and every call would then reach the wrong installation.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// startStack starts the stack listening on addr; "127.0.0.1:0" picks a
// free port, which st.server then names. With
// PLUX_TEST_S3_ENDPOINT and PLUX_TEST_VALKEY_URL set it runs as the
// Compose stack does: objects in S3 served through the server's own
// route, the cache in Valkey (QA-005, DEP-041).
func startStack(t *testing.T, addr string) *stack {
	t.Helper()
	return startStackWith(t, addr, stackOptions{})
}

// startStackWith is startStack with options.
func startStackWith(t *testing.T, addr string, o stackOptions) *stack {
	t.Helper()
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	if strings.HasSuffix(addr, ":0") {
		addr = freeAddr(t)
	}
	server := "http://" + addr
	public := server
	if o.publicBaseURL != "" {
		public = o.publicBaseURL
	}
	stores := "objectStorage:\n  directory: \"" + filepath.Join(dir, "objects") + "\"\n"
	if endpoint := os.Getenv("PLUX_TEST_S3_ENDPOINT"); endpoint != "" {
		stores = "objectStorage:\n  backend: s3\n  endpoint: \"" + endpoint + "\"\n  bucket: \"" + os.Getenv("PLUX_TEST_S3_BUCKET") + "\"\n" +
			"  pathStyle: true\n  accessKeyID: \"" + os.Getenv("PLUX_TEST_S3_ACCESS_KEY_ID") + "\"\n" +
			"  secretAccessKey: \"" + os.Getenv("PLUX_TEST_S3_SECRET_ACCESS_KEY") + "\"\n  cdnBaseURL: \"" + server + "/v1/objects\"\n"
	}
	if valkey := os.Getenv("PLUX_TEST_VALKEY_URL"); valkey != "" {
		stores += "cache:\n  backend: valkey\n  valkeyURL: \"" + valkey + "\"\n"
	}
	cfg := testConfig(t, "server:\n  roles: [api, worker]\n  listen: \""+addr+"\"\n  publicBaseURL: \""+public+"\"\n"+o.serverYAML+
		"database:\n  url: \""+url+"\"\n"+stores+
		"signing:\n  directory: \""+filepath.Join(dir, "keys")+"\"\n"+
		"attestation:\n  developmentProvider: true\n")
	ctx, cancel := context.WithCancel(context.Background())
	built, err := Build(ctx, cfg, discard(), "test")
	if err != nil {
		cancel()
		t.Fatalf("Build: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- built.Server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; built.Close() })
	waitReady(t, server)
	select {
	case err := <-done:
		// The readiness probe was answered by some other server.
		cancel()
		t.Fatalf("the server stopped while starting: %v", err)
	default:
	}

	// An administrator with a token, an organisation and an app, made
	// with the services directly.
	db, err := storage.Open(ctx, storage.Options{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
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
	cli := filepath.Join(dir, "plux")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", cli, "../../cmd/plux").CombinedOutput(); err != nil { //nolint:gosec // G204: a path the test chose.
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return &stack{ctx: ctx, server: server, dir: dir, svc: svc, owner: owner, org: org.ID, app: app, token: minted.Secret, cli: cli}
}

// run runs the CLI with args and fails the test unless it exits with want;
// it returns what the CLI printed on standard output.
func (s *stack) run(t *testing.T, want int, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(s.ctx, s.cli, args...) //nolint:gosec // G204: the binary the test built.
	cmd.Env = append(os.Environ(), "PLUX_TOKEN="+s.token, "PLUX_SERVER=", "PLUX_ORGANIZATION=", "HOME="+s.dir, "XDG_CONFIG_HOME="+filepath.Join(s.dir, "config"))
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

// project copies a fixture project of schema/testdata/documents and
// records the server, organisation and app in it with `plux init`.
func (s *stack) project(t *testing.T, fixture string) string {
	t.Helper()
	project := filepath.Join(s.dir, "project-"+fixture)
	copyTree(t, filepath.Join("..", "..", "..", "schema", "testdata", "documents", fixture), project)
	s.run(t, 0, "init", "--server", s.server, "--org", s.org, "--app", "demo", "-C", project, "--json")
	return project
}
