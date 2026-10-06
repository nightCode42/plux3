// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// referenceApp names a reference host app, its fixture and where the
// stack of its test listens.
type referenceApp struct {
	fixture string
	dir     string
	addr    string
}

// Verifies: DX-004, QA-006.
// Plux Bank's exit flow (apps/plux_bank/integration_test/bank_flows.dart)
// against this server and the reference backend: login, accounts,
// a validated transfer, its result and the server-sent notification on the
// dashboard. See runReferenceApp.
func TestPluxBankAgainstTheServer(t *testing.T) {
	runReferenceApp(t, referenceApp{fixture: "plux_bank", dir: "plux_bank", addr: "127.0.0.1:18097"})
}

// Verifies: DX-004, QA-006.
// Plux Express's flows (apps/plux_express/integration_test/express_flows.dart)
// against this server and the reference backend: the paginated catalogue,
// the persisted cart, an order tracked over a WebSocket to its delivery,
// and a courier's confirmation queued offline and replayed once. See
// runReferenceApp.
func TestPluxExpressAgainstTheServer(t *testing.T) {
	runReferenceApp(t, referenceApp{fixture: "plux_express", dir: "plux_express", addr: "127.0.0.1:18098"})
}

// runReferenceApp builds and starts the reference backend (test/refapi),
// publishes the app's fixture with its environments pointed at that
// backend, and runs the app's flows against both. It runs only when
// PLUX_E2E_FLUTTER names the flutter executable (make e2e-starter);
// PLUX_E2E_DEVICE names an emulator or simulator, as for the starter.
//
// The fixtures' environments name refapi.invalid, which no device resolves:
// the copy of the project the test publishes names the backend's loopback
// address instead (the apps trust only its test CA, which the flows get as
// the PLUX_REFAPI_CA define). PLUX_E2E_REFAPI_ADDR fixes the address the
// backend listens on, for runs where a device reaches it through a port
// forward.
func runReferenceApp(t *testing.T, app referenceApp) {
	flutter := os.Getenv("PLUX_E2E_FLUTTER")
	if flutter == "" {
		t.Skip("set PLUX_E2E_FLUTTER to the flutter executable (make e2e-starter)")
	}
	dir := filepath.Join("..", "..", "..", "apps", app.dir)
	ref := startReferenceAPI(t)
	st := startStack(t, app.addr)
	project := st.project(t, app.fixture)
	pointAtReferenceAPI(t, project, ref.url)

	var pub struct {
		OK      bool
		Release int64
	}
	decode(t, st.run(t, 0, "publish", "-C", project, "--env", "staging", "--promote", "staging", "--json"), &pub)
	if !pub.OK || pub.Release != 1 {
		t.Fatalf("publish: %+v", pub)
	}
	var keys struct {
		Keys []struct{ KeyID, PublicKey string }
	}
	decode(t, st.run(t, 0, "keys", "-C", project, "--env", "staging", "--json"), &keys)
	var roots []string
	for _, k := range keys.Keys {
		roots = append(roots, k.KeyID+":"+k.PublicKey)
	}

	defines := []string{
		"--dart-define=PLUX_ENDPOINT=" + st.server,
		"--dart-define=PLUX_APP_ID=" + st.app.ID,
		"--dart-define=PLUX_ENVIRONMENT=staging",
		"--dart-define=PLUX_HOST_BUILD=e2e",
		"--dart-define=PLUX_ROOT_KEYS=" + strings.Join(roots, ","),
		"--dart-define=PLUX_REFAPI_CA=" + ref.ca,
		"--dart-define=PLUX_REFAPI_URL=" + ref.url,
	}
	// The expanded reporter everywhere: on GitHub Actions flutter test
	// picks another, whose summary line differs.
	steps := [][]string{append([]string{flutter, "test", "--reporter=expanded", "test/e2e_test.dart"}, defines...)}
	succeeded := "All tests passed!"
	ctx := st.ctx
	live := io.Discard
	if device := os.Getenv("PLUX_E2E_DEVICE"); device != "" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deviceTimeout(t))
		defer cancel()
		live = os.Stdout
		// One result bundle per app: xcodebuild refuses a path that exists.
		if bundle := os.Getenv("PLUX_E2E_XCRESULT"); bundle != "" {
			t.Setenv("PLUX_E2E_XCRESULT", strings.TrimSuffix(bundle, ".xcresult")+"-"+app.dir+".xcresult")
		}
		steps, succeeded = deviceSteps(flutter, device, defines)
	}
	var out bytes.Buffer
	var err error
	for _, step := range steps {
		cmd := exec.CommandContext(ctx, step[0], step[1:]...) //nolint:gosec // G204: the flutter, Xcode and device the developer named.
		cmd.Dir = dir
		cmd.Stdout = io.MultiWriter(&out, live)
		cmd.Stderr = cmd.Stdout
		if err = cmd.Run(); err != nil {
			break
		}
	}
	t.Logf("the flows (%s):\n%s", dir, out.String())
	if err != nil || !strings.Contains(out.String(), succeeded) {
		t.Logf("the reference backend said:\n%s", ref.log())
		t.Fatalf("%s's flows failed: %v", app.dir, err)
	}

	// The device synced from this server: it reported what the runtime
	// sends without consent.
	events, err := deviceEvents(st)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Name] = true
	}
	for _, want := range []string{"session_start", "sync_result"} {
		if !seen[want] {
			t.Errorf("no %s event reached the server: %v", want, seen)
		}
	}
}

// referenceAPI is a running reference backend.
type referenceAPI struct {
	// url is its base URL, https://127.0.0.1:PORT.
	url string
	// ca is the PEM of the certificate authority it generated, in base64.
	ca     string
	stderr *bytes.Buffer
}

// log is what the backend wrote to its standard error.
func (r *referenceAPI) log() string { return r.stderr.String() }

// startReferenceAPI builds test/refapi, starts it as the e2e flows do
// (-ca-out and a short -track-step) and waits for the line that announces
// where it listens. It stops with the test.
func startReferenceAPI(t *testing.T) *referenceAPI {
	t.Helper()
	module := filepath.Join("..", "..", "..", "test", "refapi")
	dir := t.TempDir()
	bin := filepath.Join(dir, "refapi")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".") //nolint:gosec // G204: a path the test chose.
	build.Dir = module
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build test/refapi: %v\n%s", err, out)
	}

	caFile := filepath.Join(dir, "ca.pem")
	addr := os.Getenv("PLUX_E2E_REFAPI_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-addr", addr, "-ca-out", caFile, "-track-step", "200ms") //nolint:gosec // G204: the binary the test built.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(30 * time.Second):
		t.Fatal("the reference backend did not announce where it listens")
	}
	const prefix = "refapi listening on "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("the reference backend said %q, want %q…\n%s", line, prefix, stderr)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	return &referenceAPI{
		url:    strings.TrimSpace(strings.TrimPrefix(line, prefix)),
		ca:     base64.StdEncoding.EncodeToString(pem),
		stderr: stderr,
	}
}

// pointAtReferenceAPI rewrites the project's documents so that its
// environments and its declared network domain name the reference backend
// at url rather than refapi.invalid: the URL's origin in every value, the
// host in every domain list. It fails if the project names refapi.invalid
// nowhere.
func pointAtReferenceAPI(t *testing.T, project, url string) {
	t.Helper()
	const placeholder = "refapi.invalid"
	host := strings.TrimPrefix(url, "https://")
	host = host[:strings.LastIndex(host, ":")]
	rewrote := 0
	err := filepath.WalkDir(project, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: inside the project the test copied.
		if err != nil {
			return err
		}
		out := strings.ReplaceAll(string(data), "https://"+placeholder, url)
		out = strings.ReplaceAll(out, `"`+placeholder+`"`, `"`+host+`"`)
		if out == string(data) {
			return nil
		}
		rewrote++
		return os.WriteFile(path, []byte(out), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rewrote == 0 {
		t.Fatalf("%s names %s nowhere", project, placeholder)
	}
}
