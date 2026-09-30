// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Verifies: QA-006, QA-010.
// The starter host app's end-to-end flows (apps/starter/integration_test)
// against this server: the starter fixture is published and promoted with
// the CLI, and `flutter test` runs the real runtime, which syncs,
// verifies and renders it; the server then holds the device's telemetry.
// It runs only when PLUX_E2E_FLUTTER names the flutter executable
// (make e2e-starter). PLUX_E2E_STARTER_DIR selects the app, so the
// compatibility harness can run an older runtime's app against today's
// server and compiler (make compat). PLUX_E2E_DEVICE names an emulator or
// simulator (flutter devices): the flows then run there, in the app built
// for it, with the platform's own key store and HTTP client
// (test/e2e/android.sh, test/e2e/ios.sh); the device reaches the server on
// its loopback address (adb reverse on Android).
func TestStarterAppAgainstTheServer(t *testing.T) {
	flutter := os.Getenv("PLUX_E2E_FLUTTER")
	if flutter == "" {
		t.Skip("set PLUX_E2E_FLUTTER to the flutter executable (make e2e-starter)")
	}
	starter := os.Getenv("PLUX_E2E_STARTER_DIR")
	if starter == "" {
		starter = filepath.Join("..", "..", "..", "apps", "starter")
	}
	st := startStack(t, "127.0.0.1:18094")
	project := st.project(t, "starter")
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

	// The expanded reporter everywhere: on GitHub Actions flutter test
	// picks another, whose summary line differs.
	args := []string{"test", "--reporter=expanded", "test/e2e_test.dart"}
	ctx := st.ctx
	live := io.Discard
	if device := os.Getenv("PLUX_E2E_DEVICE"); device != "" {
		args = []string{"test", "--reporter=expanded", "integration_test/app_test.dart", "-d", device}
		// A device build and run can hang in the platform's tools: bound
		// it, and show its output as it comes so a hang can be diagnosed.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		live = os.Stdout
		if os.Getenv("PLUX_E2E_VERBOSE") != "" {
			args = append(args, "--verbose")
		}
	}
	cmd := exec.CommandContext(ctx, flutter, append(args, //nolint:gosec // G204: the flutter and device the developer named.
		"--dart-define=PLUX_ENDPOINT="+st.server,
		"--dart-define=PLUX_APP_ID="+st.app.ID,
		"--dart-define=PLUX_ENVIRONMENT=staging",
		"--dart-define=PLUX_HOST_BUILD=e2e",
		"--dart-define=PLUX_ROOT_KEYS="+strings.Join(roots, ","))...)
	cmd.Dir = starter
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(&out, live)
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	t.Logf("flutter test (%s):\n%s", starter, out.String())
	if err != nil || !strings.Contains(out.String(), "All tests passed!") {
		t.Fatalf("the starter's flows failed: %v", err)
	}

	// The device reported what the runtime sends without consent, and
	// what the flows consented to afterwards.
	envs, err := st.svc.Tenancy.ListEnvironments(st.ctx, st.owner, st.app.ID, tenancy.Page{Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	var staging string
	for _, e := range envs {
		if e.Key == "staging" {
			staging = e.ID
		}
	}
	events, err := st.svc.Events.List(st.ctx, st.owner, st.app.ID, staging, "", time.Time{}, storage.Cursor{}, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Name] = true
		if e.Name == "session_start" {
			var f struct {
				HostBuild string `json:"host_build"`
			}
			if json.Unmarshal(e.Fields, &f) != nil || f.HostBuild != "e2e" {
				t.Errorf("session_start fields: %s", e.Fields)
			}
		}
	}
	for _, want := range []string{"session_start", "sync_result"} {
		if !seen[want] {
			t.Errorf("no %s event reached the server: %v", want, seen)
		}
	}
}
