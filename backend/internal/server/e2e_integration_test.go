// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/delta"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
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
	st := startStack(t, "127.0.0.1:18093")
	ctx, server, dir, app := st.ctx, st.server, st.dir, st.app
	run := func(want int, args ...string) []byte {
		t.Helper()
		return st.run(t, want, args...)
	}
	project := st.project(t, "loan-calculator")
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
	// A promotion returns once its manifest is signed, which also records
	// the environment's key: keys and pull work at once.
	var keys struct{ Keys []struct{ PublicKey string } }
	decode(t, run(0, "keys", "-C", project, "--env", "staging", "--json"), &keys)
	if len(keys.Keys) != 1 {
		t.Fatalf("keys %+v", keys)
	}
	run(0, "diff", "-C", project)
	run(0, "doctor", "-C", project)
	var base struct {
		ReleaseSequence int64
		Bundles         []struct{ Plugin, SHA256, File string }
		Assets          []struct{ SHA256, File string }
	}
	decode(t, run(0, "pull", "-C", project, "--env", "staging", "-o", filepath.Join(dir, "host", "assets", "plux"), "--json"), &base)
	if base.ReleaseSequence != 1 || len(base.Bundles) != 2 {
		t.Fatalf("pull: %+v", base)
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
	// The logo's WebP variants — a 1×1 image has only its 3× one — each
	// its own file named by its hash (AST-001).
	if len(base.Assets) == 0 {
		t.Errorf("baseline assets: %+v", base.Assets)
	}
	for _, a := range base.Assets {
		data, err := os.ReadFile(filepath.Join(dir, "host", "assets", "plux", a.File))
		sum := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(sum[:]) != a.SHA256 || !bytes.HasPrefix(data, []byte("RIFF")) {
			t.Errorf("the baseline asset %s: %v", a.File, err)
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
	m1 := manifest(t, manifests, 1, nil)
	ingestGzip(t, hc, server, tok.Msg.GetAccessToken())
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
	m2 := manifest(t, manifests, 2, installed)
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

// Verifies: ANL-002.
// A device sends its events as a gzip-compressed Connect request (ADR-0034);
// the decompressed message is bounded by api.requestSize, so a small body
// that expands without bound is refused.
func ingestGzip(t *testing.T, hc *http.Client, server, token string) {
	t.Helper()
	post := func(body []byte) (int, string) {
		t.Helper()
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		_, _ = w.Write(body)
		_ = w.Close()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server+pluxv1connect.TelemetryServiceIngestEventsProcedure, &gz)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	fields := base64.StdEncoding.EncodeToString([]byte(`{"runtime_version":"1.0.0","host_build":"7","os_version":"18.1"}`))
	batch := `{"events":[{"name":"session_start","time":"` + time.Now().UTC().Format(time.RFC3339) + `","fields":"` + fields + `"}]}`
	if status, body := post([]byte(batch)); status != http.StatusOK || !strings.Contains(body, `"accepted":1`) {
		t.Fatalf("a gzip batch: %d %s", status, body)
	}
	bomb := append(append([]byte(`{"events":[{"name":"custom","route":"`), bytes.Repeat([]byte("0"), 20<<20)...), []byte(`"}]}`)...)
	if status, body := post(bomb); status == http.StatusOK || !strings.Contains(body, "resource_exhausted") {
		t.Fatalf("a gzip bomb: %d %.200s", status, body)
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

// manifest fetches the device's manifest, which must already name
// release seq: the promotion that made it returned only once it was
// signed.
func manifest(t *testing.T, c pluxv1connect.ManifestServiceClient, seq int64, installed []*pluxv1.InstalledBundle) *pluxv1.Manifest {
	t.Helper()
	res, err := c.GetManifest(context.Background(), connect.NewRequest(&pluxv1.GetManifestRequest{Installed: installed}))
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if got := res.Msg.GetManifest().GetReleaseSequence(); got != seq {
		t.Fatalf("the manifest names release %d, want %d", got, seq)
	}
	return res.Msg.GetManifest()
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
