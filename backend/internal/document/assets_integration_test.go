// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// jobs records the asset jobs a write enqueues.
type jobs struct {
	mu   sync.Mutex
	list []document.AssetJob
}

func (j *jobs) Enqueue(_ context.Context, _ pgx.Tx, job document.AssetJob) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.list = append(j.list, job)
	return nil
}

func (j *jobs) take() []document.AssetJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.list
	j.list = nil
	return out
}

// clamd is a ClamAV daemon that finds the EICAR string.
func clamd(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				r := bufio.NewReader(c)
				if cmd, err := r.ReadString(0); err != nil || cmd != "zINSTREAM\x00" {
					return
				}
				var body []byte
				for {
					var n [4]byte
					if _, err := io.ReadFull(r, n[:]); err != nil {
						return
					}
					size := binary.BigEndian.Uint32(n[:])
					if size == 0 {
						break
					}
					chunk := make([]byte, size)
					if _, err := io.ReadFull(r, chunk); err != nil {
						return
					}
					body = append(body, chunk...)
				}
				if bytes.Contains(body, []byte("EICAR")) {
					_, _ = c.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
					return
				}
				_, _ = c.Write([]byte("stream: OK\x00"))
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// fakeSVGCompiler stands in for plux-svgc: it rejects an SVG containing
// "broken" and otherwise writes "vec:" and the SVG.
func fakeSVGCompiler(t *testing.T) *media.SVGCompiler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plux-svgc")
	script := "#!/bin/sh\ninput=$(/bin/cat)\ncase \"$input\" in *broken*) echo 'plux-svgc: the SVG cannot be compiled: broken path' >&2; exit 1;; esac\nprintf 'vec:%s' \"$input\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // an executable of the test
		t.Fatal(err)
	}
	return &media.SVGCompiler{Path: path, PathOps: "/unused", Timeout: 10 * time.Second, MaxOutput: 1 << 20}
}

func pngWithText(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Verifies: SRV-060, CMP-030, CMP-033.
// An upload is typed from its bytes, stripped, scanned, stored once per
// content and listed in the asset index; a raster image is transcoded
// by a job into WebP and AVIF at three densities, and the same content
// uploaded again reuses them; Lottie is stored as dotLottie; the file
// and the index travel with an export and an import.
func TestAssets(t *testing.T) {
	t.Parallel()
	store, err := objects.NewFilesystem(t.TempDir(), "https://cdn.example")
	if err != nil {
		t.Fatal(err)
	}
	codecs, err := media.NewCodecs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = codecs.Close(context.Background()) })
	queue := &jobs{}
	addr := clamd(t)
	svgc := fakeSVGCompiler(t)
	f := newFixture(t, func(o *document.Options) {
		o.Objects, o.Jobs, o.Codecs, o.SVG = store, queue, codecs, svgc
		o.Scanner = document.Clamd{Network: "tcp", Address: addr}
	})
	ctx := context.Background()
	const s = "tab-1"
	logo := pngWithText(t, 30, 30)
	if _, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "images/logo.png", logo); code(err) != plxerr.EditingLockHeld {
		t.Errorf("an upload without the app's lock: %v", err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, "", s, false); err != nil {
		t.Fatal(err)
	}
	a, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "images/logo.png", logo)
	if err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	if a.MediaType != media.PNG || a.Processing != "pending" || a.Width != 30 {
		t.Errorf("uploaded %+v", a)
	}
	index, err := f.docs.GetDocument(ctx, f.viewer, f.app, "", "assets/index.json")
	if err != nil || !strings.Contains(string(index.Content), `"key":"logo"`) || !strings.Contains(string(index.Content), a.ID) {
		t.Fatalf("the index: %s %v", index.Content, err)
	}
	pending := queue.take()
	if len(pending) != 1 {
		t.Fatalf("%d jobs", len(pending))
	}
	if err := f.docs.ProcessAsset(ctx, pending[0]); err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	a, err = f.docs.GetAsset(ctx, f.viewer, a.ID)
	if err != nil || a.Processing != "ready" || len(a.Variants) != 6 {
		t.Fatalf("processed %+v %v", a, err)
	}
	for _, v := range a.Variants {
		if _, err := store.Stat(ctx, objectKey(t, v.SHA256)); err != nil {
			t.Errorf("variant %s %d× is not stored: %v", v.MediaType, v.Density, err)
		}
	}
	if err := f.docs.ProcessAsset(ctx, pending[0]); err != nil {
		t.Errorf("a repeated job: %v", err)
	}
	// The same content under another name reuses the variants.
	copyOf, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "images/logo-copy.png", logo)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range queue.take() {
		if err := f.docs.ProcessAsset(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	copyOf, _ = f.docs.GetAsset(ctx, f.viewer, copyOf.ID)
	if copyOf.SHA256 != a.SHA256 || len(copyOf.Variants) != 6 || copyOf.Variants[0].SHA256 != a.Variants[0].SHA256 {
		t.Errorf("the copy: %+v", copyOf)
	}
	// Uploading again under the same name keeps the asset's identifier.
	bigger := pngWithText(t, 60, 60)
	again, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "images/logo.png", bigger)
	if err != nil || again.ID != a.ID || again.SHA256 == a.SHA256 {
		t.Errorf("a re-upload: %+v %v", again, err)
	}
	// The re-upload waits for its job; a publish counts it as pending.
	pendingOf := func(app string) (int64, error) {
		var n int64
		err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			n, err = f.docs.PendingAssets(ctx, tx, app)
			return err
		})
		return n, err
	}
	if n, err := pendingOf(f.app); err != nil || n != 1 {
		t.Errorf("pending before the job: %d %v", n, err)
	}
	for _, j := range queue.take() {
		if err := f.docs.ProcessAsset(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := pendingOf(f.app); err != nil || n != 0 {
		t.Errorf("pending after the job: %d %v", n, err)
	}
	if _, err := pendingOf("not-an-id"); code(err) != plxerr.InvalidFormat {
		t.Errorf("a bad app: %v", err)
	}
	// Lottie becomes dotLottie; an SVG keeps its drawing but not its
	// metadata.
	anim, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "anim/spinner.lottie", []byte(`{"v":"5.7.0","fr":30,"ip":0,"op":10,"w":10,"h":10,"layers":[]}`))
	if err != nil || anim.MediaType != media.DotLottie || anim.Processing != "ready" {
		t.Errorf("a Lottie upload: %+v %v", anim, err)
	}
	svg, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "icons/star.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><metadata>by Ada</metadata><path d="M0 0"/></svg>`))
	if err != nil || svg.MediaType != media.SVG {
		t.Fatalf("an SVG upload: %+v %v", svg, err)
	}
	stored, _, err := store.Get(ctx, objectKey(t, svg.SHA256))
	if err != nil || bytes.Contains(stored, []byte("Ada")) {
		t.Errorf("the SVG's metadata was kept: %s %v", stored, err)
	}
	// An SVG is compiled to vector_graphics by the worker (CMP-031); one
	// the compiler rejects fails with a diagnostic.
	broken, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "icons/broken.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="broken"/></svg>`))
	if err != nil || svg.Processing != "pending" {
		t.Fatalf("SVG uploads: %+v %+v %v", svg, broken, err)
	}
	for _, j := range queue.take() {
		if err := f.docs.ProcessAsset(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	svg, _ = f.docs.GetAsset(ctx, f.viewer, svg.ID)
	if svg.Processing != "ready" || len(svg.Variants) != 1 || svg.Variants[0].MediaType != media.VectorGraphics {
		t.Fatalf("the compiled SVG: %+v", svg)
	}
	if vec, _, err := store.Get(ctx, objectKey(t, svg.Variants[0].SHA256)); err != nil || !bytes.Equal(vec, append([]byte("vec:"), stored...)) {
		t.Errorf("the vector_graphics variant: %q %v", vec, err)
	}
	broken, _ = f.docs.GetAsset(ctx, f.viewer, broken.ID)
	if broken.Processing != "failed" || len(broken.Diagnostics) != 1 || !strings.Contains(broken.Diagnostics[0].Message, "broken") {
		t.Errorf("a rejected SVG: %+v", broken)
	}
	if err := f.docs.DeleteAsset(ctx, f.owner, broken.ID, s); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		file string
		data []byte
		want plxerr.Code
	}{
		"a disguised file":  {"images/evil.png", []byte("MZ\x90\x00 an executable"), plxerr.InvalidFormat},
		"malware":           {"images/virus.svg", []byte(`<svg><!-- EICAR --></svg>`), plxerr.AssetRejected},
		"a bad name":        {"../escape.png", logo, plxerr.InvalidFormat},
		"the index's name":  {"index.json", logo, plxerr.InvalidFormat},
		"a truncated image": {"images/broken.png", logo[:40], plxerr.InvalidFormat},
	} {
		if _, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, c.file, c.data); code(err) != c.want {
			t.Errorf("%s: %v, want PLX-%d", name, err, c.want)
		}
	}
	plugin := f.plugin(t)
	if _, err := f.docs.UploadAsset(ctx, f.owner, f.app, plugin, s, "images/x.png", logo); code(err) != plxerr.InvalidProjectLayout {
		t.Errorf("a plugin asset: %v", err)
	}
	if _, err := f.docs.UploadAsset(ctx, f.developer, f.app, "", "dev", "images/x.png", logo); code(err) != plxerr.PermissionDenied {
		t.Errorf("a developer uploaded to the app's draft: %v", err)
	}
	if _, err := f.tenancy.SetAppLimit(ctx, f.owner, f.app, "asset.fileSize", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.UploadAsset(ctx, f.owner, f.app, "", s, "images/big.png", logo); code(err) != plxerr.LimitExceeded {
		t.Errorf("above asset.fileSize: %v", err)
	}
	list, err := f.docs.ListAssets(ctx, f.viewer, f.app, "", 10)
	if err != nil || len(list) != 4 || list[0].File != "anim/spinner.lottie" {
		t.Errorf("ListAssets: %+v %v", list, err)
	}
	url, err := f.docs.AssetURL(ctx, a.SHA256, 0)
	if err != nil || !strings.HasPrefix(url, "https://cdn.example/") {
		t.Errorf("AssetURL: %q %v", url, err)
	}
	// Validation sees the files; export carries them raw.
	if _, err := f.docs.ValidateDraft(ctx, f.viewer, f.app, ""); err != nil {
		t.Fatal(err)
	}
	files, err := f.docs.Export(ctx, f.viewer, f.app, "")
	if err != nil {
		t.Fatal(err)
	}
	var exportedLogo []byte
	for _, file := range files {
		if file.Path == "assets/images/logo.png" {
			exportedLogo = file.Content
		}
	}
	if exportedLogo == nil {
		t.Fatal("the export has no asset files")
	}
	if err := f.docs.DeleteAsset(ctx, f.owner, copyOf.ID, s); err != nil {
		t.Fatalf("DeleteAsset: %v", err)
	}
	if _, err := f.docs.GetAsset(ctx, f.viewer, copyOf.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a deleted asset: %v", err)
	}
	// The export imports into another app, assets included.
	b, err := f.tenancy.CreateApp(ctx, f.owner, "copy", "Copy")
	if err != nil {
		t.Fatal(err)
	}
	var clean []document.File
	for _, file := range files {
		if file.Path == "assets/index.json" || !strings.HasPrefix(file.Path, "assets/images/logo-copy") {
			clean = append(clean, file)
		}
	}
	for i, file := range clean {
		if file.Path == "assets/index.json" {
			clean[i].Content = dropEntry(t, file.Content, "images/logo-copy.png")
		}
	}
	if _, err := f.docs.Import(ctx, f.owner, b.ID, "", "imp", clean); err != nil {
		t.Fatalf("Import with assets: %v", err)
	}
	moved, err := f.docs.ListAssets(ctx, f.viewer, b.ID, "", 10)
	if err != nil || len(moved) != 3 {
		t.Errorf("imported assets: %+v %v", moved, err)
	}
}

func objectKey(t *testing.T, sum string) string {
	t.Helper()
	k, err := objects.Key(objects.KindAsset, sum)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// dropEntry removes an asset from an exported index.
func dropEntry(t *testing.T, index []byte, file string) []byte {
	t.Helper()
	s := string(index)
	i := strings.Index(s, `"file": "`+file+`"`)
	if i < 0 {
		return index
	}
	start := strings.LastIndex(s[:i], "{")
	end := i + strings.Index(s[i:], "}") + 1
	if rest := strings.TrimLeft(s[end:], " \n"); strings.HasPrefix(rest, ",") {
		end += strings.Index(s[end:], ",") + 1
	} else if j := strings.LastIndex(s[:start], ","); j >= 0 {
		start = j
	}
	return []byte(s[:start] + s[end:])
}
