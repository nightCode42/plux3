// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/benchproject"
	"github.com/nightCode42/plux3/backend/internal/netsim"
)

// syncBenchPlugins is how many plugins the synced app has: the fifty of
// NFR-008, of which a revision changes three (NFR-007).
const syncBenchPlugins = 50

// syncBaselinePath holds the committed byte counts the benchmark gates
// against (QA-007).
var syncBaselinePath = filepath.Join("..", "..", "..", "test", "bench", "runtime", "sync-baseline.json")

// syncBaseline is what the device's sync costs on the wire, in bytes.
type syncBaseline struct {
	// First is the first launch: registration and every bundle in full.
	First int64 `json:"first"`
	// Check is an up-to-date check at app start.
	Check int64 `json:"check"`
	// Update is the median sync of a revision that changes three plugins.
	Update int64 `json:"update"`
}

// syncBench is the Go half's state while the device syncs.
type syncBench struct {
	st      *stack
	proxy   *netsim.Proxy
	project string
	// released is the newest release publish made; only publish, which
	// the device calls one at a time, reads and writes it.
	released int64

	mu      sync.Mutex
	phase   string                       // guarded by mu
	phases  map[string][]netsim.Exchange // guarded by mu
	updates []float64                    // guarded by mu: milliseconds
}

// Verifies: QA-007, NFR-006, NFR-007.
// The sync benchmark: the real runtime (test/bench/runtime/test/sync_test.dart)
// syncs the fifty-plugin benchmark app through the spec's slow network
// (§30.1, backend/internal/netsim) while this test publishes revisions
// that change three plugins. It checks that an update syncs within 3 s at
// the 95th percentile, and that no phase costs more than 10% over the
// committed bytes; the report lists every request. It runs only when
// PLUX_E2E_FLUTTER names the flutter executable (make bench-sync);
// PLUX_BENCH_SYNC_UPDATE=1 rewrites the committed bytes instead of
// checking them, and PLUX_BENCH_SYNC_OUT names a file for the report.
func TestSyncOnSlowNetwork(t *testing.T) {
	flutter := os.Getenv("PLUX_E2E_FLUTTER")
	if flutter == "" {
		t.Skip("set PLUX_E2E_FLUTTER to the flutter executable (make bench-sync)")
	}
	// Devices reach the server only through the simulated network: the
	// proxy is its public address, so the manifest's download URLs point
	// through it too. The server trusts the proxy's X-Forwarded-For, so
	// the device's requests have a rate budget of their own and are never
	// refused for the CLI's publishes.
	const addr, public = "127.0.0.1:18095", "127.0.0.1:18096"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxy, err := netsim.New(netsim.Slow3G()).Proxy(ctx, public, "http://"+addr, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	st := startStackWith(t, addr, stackOptions{publicBaseURL: proxy.URL(), serverYAML: "  trustedProxies: [127.0.0.1/32]\n"})
	b := &syncBench{st: st, proxy: proxy, phases: map[string][]netsim.Exchange{}}
	b.project = filepath.Join(st.dir, "project-bench")
	if err := b.write(1); err != nil {
		t.Fatal(err)
	}
	st.run(t, 0, "init", "--server", st.server, "--org", st.org, "--app", "demo", "-C", b.project, "--json")
	if _, err := b.publish(); err != nil {
		t.Fatal(err)
	}
	var keys struct {
		Keys []struct{ KeyID, PublicKey string }
	}
	decode(t, st.run(t, 0, "keys", "-C", b.project, "--env", "staging", "--json"), &keys)
	var roots []string
	for _, k := range keys.Keys {
		roots = append(roots, k.KeyID+":"+k.PublicKey)
	}
	control := b.serve(t)

	cmd := exec.CommandContext(st.ctx, flutter, "test", "--reporter=expanded", "test/sync_test.dart", //nolint:gosec // G204: the flutter the developer named.
		"--dart-define=PLUX_BENCH_SYNC_CONTROL="+control,
		"--dart-define=PLUX_ENDPOINT="+proxy.URL(),
		"--dart-define=PLUX_APP_ID="+st.app.ID,
		"--dart-define=PLUX_ROOT_KEYS="+strings.Join(roots, ","))
	cmd.Dir = filepath.Join("..", "..", "..", "test", "bench", "runtime")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil || !strings.Contains(out.String(), "All tests passed!") {
		t.Fatalf("the device's sync failed: %v\n%s", err, out.String())
	}
	b.check(t)
}

// write writes the benchmark project at revision into the project
// directory, keeping the CLI's settings.
func (b *syncBench) write(revision int) error {
	for name, f := range benchproject.Project(syncBenchPlugins, revision) {
		path := filepath.Join(b.project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path, f.Data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// publish publishes the project and promotes it to staging; the CLI
// returns once the release's manifest is signed. A publish of all fifty
// plugins costs about 110 API calls, so publishing a revision every few
// seconds reaches the per-address limit (api.requestsPerMinutePerAddress,
// which an installation may lower, never raise); a refused call waits and
// runs again. A publish refused after it created the release — while
// promoting it or waiting for its manifest — is not run again, which would
// create a second release: the release it made is promoted instead.
func (b *syncBench) publish() (int64, error) {
	made := false
	rel, err := b.retry(func() (int64, error) {
		if newest, err := b.newestRelease(); err != nil || newest > b.released {
			made = err == nil
			return newest, err
		}
		var pub struct {
			OK      bool
			Release int64
		}
		out, err := b.cli("publish", "-C", b.project, "--env", "staging", "--promote", "staging", "--json")
		if err == nil {
			if err = json.Unmarshal(out, &pub); err == nil && !pub.OK {
				err = errors.New("not published")
			}
		}
		if err != nil {
			return 0, fmt.Errorf("plux publish: %w\n%s", err, out)
		}
		return pub.Release, nil
	})
	if err != nil {
		return 0, err
	}
	if made {
		if _, err := b.retry(func() (int64, error) {
			out, err := b.cli("release", "promote", "-C", b.project, "--env", "staging", "--json", strconv.FormatInt(rel, 10))
			if err != nil {
				return 0, fmt.Errorf("plux release promote: %w\n%s", err, out)
			}
			return rel, nil
		}); err != nil {
			return 0, err
		}
	}
	b.released = rel
	return rel, nil
}

// retry runs f again while the server refuses it with PLX-8040.
func (*syncBench) retry(f func() (int64, error)) (int64, error) {
	for attempt := 1; ; attempt++ {
		rel, err := f()
		if err == nil || attempt == 12 || !strings.Contains(err.Error(), "PLX-8040") {
			return rel, err
		}
		time.Sleep(10 * time.Second)
	}
}

// newestRelease is the newest release of staging, 0 when there is none.
func (b *syncBench) newestRelease() (int64, error) {
	out, err := b.cli("release", "list", "-C", b.project, "--env", "staging", "--json")
	if err != nil {
		return 0, fmt.Errorf("plux release list: %w\n%s", err, out)
	}
	var list struct{ Releases []struct{ Sequence int64 } }
	if err := json.Unmarshal(out, &list); err != nil {
		return 0, fmt.Errorf("plux release list: %w", err)
	}
	var newest int64
	for _, r := range list.Releases {
		newest = max(newest, r.Sequence)
	}
	return newest, nil
}

// cli runs the CLI against the stack and returns its standard output,
// with its standard error appended when it fails.
func (b *syncBench) cli(args ...string) ([]byte, error) {
	cmd := exec.CommandContext(b.st.ctx, b.st.cli, args...) //nolint:gosec // G204: the binary the test built.
	cmd.Env = append(os.Environ(), "PLUX_TOKEN="+b.st.token, "PLUX_SERVER=", "PLUX_ORGANIZATION=", "HOME="+b.st.dir, "XDG_CONFIG_HOME="+filepath.Join(b.st.dir, "config"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return append(stdout.Bytes(), stderr.Bytes()...), err
	}
	return stdout.Bytes(), nil
}

// serve starts the control endpoint the device asks: phase (the
// exchanges from now on belong to the named phase), publish (the next
// revision) and result (a measured sync time).
func (b *syncBench) serve(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /phase", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.phases[b.phase] = append(b.phases[b.phase], b.proxy.Exchanges()...)
		b.proxy.Reset()
		b.phase = r.URL.Query().Get("name")
	})
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		rev, err := strconv.Atoi(r.URL.Query().Get("revision"))
		if err == nil {
			err = b.write(rev)
		}
		var release int64
		if err == nil {
			release, err = b.publish()
		}
		if err == nil && release != int64(rev) {
			err = fmt.Errorf("revision %d published as release %d", rev, release)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("POST /result", func(w http.ResponseWriter, r *http.Request) {
		ms, err := strconv.ParseFloat(r.URL.Query().Get("ms"), 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.updates = append(b.updates, ms)
	})
	var lc net.ListenConfig
	ln, err := lc.Listen(b.st.ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Minute}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// syncOnly leaves out telemetry, which the runtime sends after a sync
// with the token the sync obtained: it is not part of the sync.
func syncOnly(ex []netsim.Exchange) []netsim.Exchange {
	var out []netsim.Exchange
	for _, e := range ex {
		if !strings.HasPrefix(e.Path, "/plux.v1.TelemetryService/") {
			out = append(out, e)
		}
	}
	return out
}

// total is the bytes the exchanges cost, both ways.
func total(ex []netsim.Exchange) int64 {
	var n int64
	for _, e := range ex {
		n += e.Up + e.Down
	}
	return n
}

// median is the median of xs, which is not empty.
func median(xs []int64) int64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

// check writes the report and applies the gates.
func (b *syncBench) check(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var report strings.Builder
	fmt.Fprintf(&report, "Sync of the %d-plugin benchmark app on the slow network (%s): %d up-to-date checks and %d updates of three plugins.\n\n",
		syncBenchPlugins, "750/250 kbit/s, 300 ms RTT, 1% loss", len(phaseNames(b.phases, "check-")), len(b.updates))
	report.WriteString("| Phase | Request | Status | Up (bytes) | Down (bytes) | Server time (ms) |\n|---|---|---:|---:|---:|---:|\n")
	var checks, updates []int64
	var checkRequests []int
	for _, name := range append(append([]string{"first"}, phaseNames(b.phases, "check-")...), phaseNames(b.phases, "update-")...) {
		all := b.phases[name]
		for _, e := range all {
			fmt.Fprintf(&report, "| %s | `%s %s` | %d | %d | %d | %d |\n", name, e.Method, e.Path, e.Status, e.Up, e.Down, e.End.Sub(e.Start).Milliseconds())
		}
		ex := syncOnly(all)
		switch {
		case strings.HasPrefix(name, "check-"):
			checks = append(checks, total(ex))
			checkRequests = append(checkRequests, len(ex))
		case strings.HasPrefix(name, "update-"):
			updates = append(updates, total(ex))
		}
	}
	if len(checks) == 0 || len(updates) == 0 || len(b.updates) != len(updates) {
		t.Fatalf("phases missing: %d checks, %d updates, %d times", len(checks), len(updates), len(b.updates))
	}
	got := syncBaseline{First: total(syncOnly(b.phases["first"])), Check: median(checks), Update: median(updates)}
	times := slices.Clone(b.updates)
	slices.Sort(times)
	p95 := times[(len(times)*95+99)/100-1]
	fmt.Fprintf(&report, "\nTelemetry (`IngestEvents`) is listed but not counted: it rides on the sync's token after the sync.\n")
	fmt.Fprintf(&report, "\n| Measure | Value |\n|---|---:|\n")
	fmt.Fprintf(&report, "| First launch, bytes | %d |\n| Up-to-date check, requests | %d |\n| Up-to-date check, bytes (median) | %d |\n", got.First, median(intsToInt64(checkRequests)), got.Check)
	fmt.Fprintf(&report, "| Update of three plugins, bytes (median) | %d |\n| Update of three plugins, p50 (ms) | %.0f |\n| Update of three plugins, p95 (ms) | %.0f |\n",
		got.Update, times[len(times)/2], p95)
	t.Log("\n" + report.String())
	if path := os.Getenv("PLUX_BENCH_SYNC_OUT"); path != "" {
		if err := os.WriteFile(path, []byte(report.String()), 0o600); err != nil {
			t.Error(err)
		}
	}

	// NFR-007: an update of three plugins within 3 s at the 95th
	// percentile on the slow network.
	if p95 > 3000 {
		t.Errorf("NFR-007: an update of three plugins takes %.0f ms at p95, over 3,000 ms", p95)
	}
	if os.Getenv("PLUX_BENCH_SYNC_UPDATE") == "1" {
		data, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(syncBaselinePath, append(data, '\n'), 0o644); err != nil { //nolint:gosec // A committed file, readable by all.
			t.Fatal(err)
		}
		return
	}
	var want syncBaseline
	data, err := os.ReadFile(syncBaselinePath)
	if err == nil {
		err = json.Unmarshal(data, &want)
	}
	if err != nil {
		t.Fatalf("the committed bytes: %v (PLUX_BENCH_SYNC_UPDATE=1 writes them)", err)
	}
	// QA-007: no phase costs more than 10% over the committed bytes.
	for _, c := range []struct {
		name      string
		got, want int64
	}{{"first launch", got.First, want.First}, {"up-to-date check", got.Check, want.Check}, {"update", got.Update, want.Update}} {
		if c.want <= 0 || float64(c.got) > float64(c.want)*1.10 {
			t.Errorf("QA-007: the %s costs %d bytes, more than 10%% over the committed %d", c.name, c.got, c.want)
		}
	}
}

// phaseNames lists the phases with prefix in their order: prefix-1,
// prefix-2 and so on.
func phaseNames(phases map[string][]netsim.Exchange, prefix string) []string {
	var out []string
	for i := 1; ; i++ {
		name := prefix + strconv.Itoa(i)
		if _, ok := phases[name]; !ok {
			return out
		}
		out = append(out, name)
	}
}

func intsToInt64(xs []int) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = int64(x)
	}
	return out
}
