// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	codecsOnce sync.Once
	shared     *Codecs
	sharedErr  error
)

func codecs(t testing.TB) *Codecs {
	t.Helper()
	codecsOnce.Do(func() { shared, sharedErr = NewCodecs(context.Background()) })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

// gradient is a test image with a transparent corner.
func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if x < w/4 && y < h/4 {
				a = 0
			}
			img.Set(x, y, color.NRGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: a}) //nolint:gosec // below 256
		}
	}
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Verifies: CMP-030.
// A raster image becomes WebP and AVIF at 1×, 2× and 3×, from pixels, so
// no metadata survives; the same input gives the same bytes.
func TestTranscode(t *testing.T) {
	t.Parallel()
	c := codecs(t)
	ctx := context.Background()
	src := encodePNG(t, gradient(90, 60))
	info, variants, err := c.Transcode(ctx, src, PNG, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 90 || info.Height != 60 || len(variants) != 6 {
		t.Fatalf("info %+v, %d variants", info, len(variants))
	}
	for _, v := range variants {
		wantW, wantH := 30*v.Density, 20*v.Density
		if v.Width != wantW || v.Height != wantH {
			t.Errorf("%s %d× is %d×%d, want %d×%d", v.MediaType, v.Density, v.Width, v.Height, wantW, wantH)
		}
		switch v.MediaType {
		case WebP:
			px, err := c.decodeWebP(ctx, v.Data)
			if err != nil || px.W != wantW || px.H != wantH {
				t.Errorf("the %d× WebP does not decode: %v", v.Density, err)
			}
			if px != nil && px.Pix[3] != 0 {
				t.Errorf("the transparent corner is opaque at %d×", v.Density)
			}
		case AVIF:
			if len(v.Data) < 12 || string(v.Data[4:12]) != "ftypavif" {
				t.Errorf("the %d× AVIF has no AVIF brand", v.Density)
			}
		}
	}
	_, again, err := c.Transcode(ctx, src, PNG, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for i := range variants {
		if !bytes.Equal(variants[i].Data, again[i].Data) {
			t.Errorf("variant %d differs between runs", i)
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, gradient(30, 30), nil); err != nil {
		t.Fatal(err)
	}
	if _, v, err := c.Transcode(ctx, jpg.Bytes(), JPEG, 1_000_000); err != nil || len(v) != 6 {
		t.Errorf("a JPEG: %d variants, %v", len(v), err)
	}
	webp := variants[0].Data
	if _, v, err := c.Transcode(ctx, webp, WebP, 1_000_000); err != nil || len(v) != 6 {
		t.Errorf("a WebP: %d variants, %v", len(v), err)
	}
	if _, _, err := c.Transcode(ctx, src, PNG, 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("above asset.imagePixels: %v", err)
	}
	if _, _, err := c.Transcode(ctx, []byte("not an image"), PNG, 100); err == nil {
		t.Error("garbage was transcoded")
	}
	if _, _, err := c.Transcode(ctx, src, SVG, 100); !errors.Is(err, ErrUnsupported) {
		t.Errorf("an SVG: %v", err)
	}
	// An animated GIF is described and kept.
	pal := color.Palette{color.Black, color.White}
	anim := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 4, 4), pal), image.NewPaletted(image.Rect(0, 0, 4, 4), pal)}, Delay: []int{10, 10}}
	var g bytes.Buffer
	if err := gif.EncodeAll(&g, anim); err != nil {
		t.Fatal(err)
	}
	info, v, err := c.Transcode(ctx, g.Bytes(), GIF, 1_000_000)
	if err != nil || !info.Animated || v != nil {
		t.Errorf("an animated GIF: %+v %d %v", info, len(v), err)
	}
	if !Raster(GIF) || Raster(SVG) {
		t.Error("Raster")
	}
}

// Verifies: CMP-030.
// Resizing averages areas with integer arithmetic and never lends colour
// from transparent pixels.
func TestResize(t *testing.T) {
	t.Parallel()
	src := &RGBA{W: 2, H: 1, Pix: []byte{255, 0, 0, 255, 0, 255, 0, 0}}
	got := resize(src, 1, 1)
	if !bytes.Equal(got.Pix, []byte{255, 0, 0, 128}) {
		t.Errorf("resize = %v", got.Pix)
	}
	three := &RGBA{W: 3, H: 1, Pix: []byte{0, 0, 0, 255, 90, 90, 90, 255, 180, 180, 180, 255}}
	if got := resize(three, 2, 1); !bytes.Equal(got.Pix, []byte{30, 30, 30, 255, 150, 150, 150, 255}) {
		t.Errorf("3 → 2 = %v", got.Pix)
	}
	if got := resize(three, 3, 1); !bytes.Equal(got.Pix, three.Pix) {
		t.Error("the same size changed the pixels")
	}
}

// Verifies: SRV-060.
// A file's type comes from its bytes, never its name.
func TestSniff(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"\x89PNG\r\n\x1a\nrest":        PNG,
		"\xff\xd8\xff\xe0":             JPEG,
		"GIF89a....":                   GIF,
		"RIFF\x10\x00\x00\x00WEBPVP8 ": WebP,
		"\x00\x01\x00\x00font":         TTF,
		"OTTOfont":                     OTF,
		"RIVE\x07":                     Rive,
		"PK\x03\x04zip":                DotLottie,
		`{"v":"5.7.0","fr":30,"w":10,"h":10,"layers":[]}`:                                                   lottieJSON,
		"<?xml version=\"1.0\"?>\n<!-- c --><!DOCTYPE svg><svg xmlns=\"http://www.w3.org/2000/svg\"></svg>": SVG,
	}
	for in, want := range cases {
		if got, err := Sniff([]byte(in)); err != nil || got != want {
			t.Errorf("Sniff(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "hello", "<html></html>", "\x00\x00\x00\x1cftypavif", `{"a":1}`, "<svgx/>", "\xff\xfe<svg>"} {
		if _, err := Sniff([]byte(in)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Sniff(%q) = %v", in, err)
		}
	}
}

// pngChunk builds one PNG chunk.
func pngChunk(kind string, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body))) //nolint:gosec // small
	out = append(out, kind...)
	out = append(out, body...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), body...)))
}

// Verifies: SRV-060.
// Metadata goes: PNG text and profile chunks, JPEG EXIF and comments
// (with the orientation applied first), GIF comments, WebP EXIF, XMP and
// profiles, SVG metadata elements.
func TestStrip(t *testing.T) {
	t.Parallel()
	clean := encodePNG(t, gradient(4, 4))
	iend := bytes.LastIndex(clean, []byte("IEND")) - 4
	dirty := append(append(append([]byte{}, clean[:iend]...), pngChunk("tEXt", []byte("Author\x00Ada"))...), clean[iend:]...)
	got, err := Strip(dirty, PNG)
	if err != nil || !bytes.Equal(got, clean) {
		t.Errorf("PNG: %v", err)
	}
	broken := append([]byte{}, dirty...)
	broken[len(broken)-20] ^= 1
	if _, err := Strip(broken, PNG); err == nil {
		t.Error("a PNG with a bad checksum was accepted")
	}

	// A JPEG with a comment and an EXIF orientation of 6 (turned 90°).
	var j bytes.Buffer
	if err := jpeg.Encode(&j, gradient(8, 4), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	app1 := append([]byte{0xff, 0xe1}, binary.BigEndian.AppendUint16(nil, uint16(len(exif)+2))...) //nolint:gosec // small
	app1 = append(app1, exif...)
	com := []byte{0xff, 0xfe, 0x00, 0x05, 'h', 'i', '!'}
	rotated := append(append(append([]byte{0xff, 0xd8}, app1...), com...), j.Bytes()[2:]...)
	got, err = Strip(rotated, JPEG)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 4 || cfg.Height != 8 || bytes.Contains(got, []byte("Exif")) {
		t.Errorf("the orientation was not applied: %+v %v", cfg, err)
	}
	upright := append(append([]byte{0xff, 0xd8}, com...), j.Bytes()[2:]...)
	got, err = Strip(upright, JPEG)
	if err != nil || bytes.Contains(got, []byte("hi!")) {
		t.Errorf("JPEG comment: %v", err)
	}
	for o := 2; o <= 8; o++ {
		px := orient(&RGBA{W: 2, H: 1, Pix: []byte{1, 1, 1, 1, 2, 2, 2, 2}}, o)
		if px.W*px.H != 2 {
			t.Errorf("orientation %d lost pixels", o)
		}
	}

	// A GIF with a comment extension.
	var g bytes.Buffer
	if err := gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	head := 13 + 3*2
	withComment := append(append(append([]byte{}, g.Bytes()[:head]...), 0x21, 0xfe, 0x03, 'a', 'b', 'c', 0x00), g.Bytes()[head:]...)
	got, err = Strip(withComment, GIF)
	if err != nil || !bytes.Equal(got, g.Bytes()) {
		t.Errorf("GIF comment: %v", err)
	}

	// A WebP with an EXIF chunk.
	c := codecs(t)
	webp, err := c.encodeWebP(context.Background(), fromImage(gradient(4, 4)), -1)
	if err != nil {
		t.Fatal(err)
	}
	exifChunk := append([]byte("EXIF\x03\x00\x00\x00abc"), 0)
	withExif := append(append([]byte{}, webp...), exifChunk...)
	binary.LittleEndian.PutUint32(withExif[4:], uint32(len(withExif)-8)) //nolint:gosec // small
	got, err = Strip(withExif, WebP)
	if err != nil || !bytes.Equal(got, webp) {
		t.Errorf("WebP EXIF: %v", err)
	}

	svg := []byte(`<svg><metadata><rdf>author</rdf></metadata><rect/></svg>`)
	if got, _ := Strip(svg, SVG); string(got) != "<svg><rect/></svg>" {
		t.Errorf("SVG = %s", got)
	}
	if got, _ := Strip([]byte("RIVE"), Rive); string(got) != "RIVE" {
		t.Error("a Rive file changed")
	}
	for _, mt := range []string{PNG, JPEG, GIF, WebP} {
		if _, err := Strip([]byte("xx"), mt); err == nil {
			t.Errorf("a malformed %s was accepted", mt)
		}
	}
}

const lottie = `{"v":"5.7.0","fr":30,"ip":0,"op":60,"w":100,"h":100,"layers":[]}`

// Verifies: CMP-033.
// A Lottie animation is stored as a dotLottie archive, written the same
// way every time; an uploaded dotLottie is checked and rewritten; one
// with unsafe paths or no animation is refused.
func TestDotLottie(t *testing.T) {
	t.Parallel()
	out, mt, err := Package([]byte(lottie), lottieJSON, 1<<20)
	if err != nil || mt != DotLottie {
		t.Fatalf("Package = %s, %v", mt, err)
	}
	again, _, _ := Package([]byte(lottie), lottieJSON, 1<<20)
	if !bytes.Equal(out, again) {
		t.Error("packaging is not deterministic")
	}
	r, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil || len(r.File) != 2 || r.File[0].Name != "manifest.json" || r.File[1].Name != "animations/animation.json" {
		t.Fatalf("archive: %v", err)
	}
	if kind, _ := Sniff(out); kind != DotLottie {
		t.Errorf("sniffed %s", kind)
	}
	repacked, _, err := Package(out, DotLottie, 1<<20)
	if err != nil || !bytes.Equal(repacked, out) {
		t.Errorf("a canonical archive changed on repacking: %v", err)
	}
	if got, mt, _ := Package([]byte("RIVE"), Rive, 10); string(got) != "RIVE" || mt != Rive {
		t.Error("a Rive file was not kept as is")
	}
	zipOf := func(files map[string]string) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			f, _ := w.Create(n)
			_, _ = f.Write([]byte(files[n]))
		}
		_ = w.Close()
		return b.Bytes()
	}
	for name, archive := range map[string][]byte{
		"not a zip":         []byte("PK\x03\x04garbage"),
		"no manifest":       zipOf(map[string]string{"animations/a.json": lottie}),
		"missing animation": zipOf(map[string]string{"manifest.json": `{"animations":[{"id":"a"}]}`}),
		"zip slip":          zipOf(map[string]string{"manifest.json": `{"animations":[{"id":"a"}]}`, "animations/a.json": lottie, "../evil": "x"}),
		"too large":         zipOf(map[string]string{"manifest.json": `{"animations":[{"id":"a"}]}`, "animations/a.json": lottie + strings.Repeat(" ", 2000)}),
	} {
		if _, _, err := Package(archive, DotLottie, 1000); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// FuzzMedia checks that no file makes sniffing, stripping or packaging
// panic.
func FuzzMedia(f *testing.F) {
	f.Add([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x00IEND\xae\x42\x60\x82"))
	f.Add([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00"))
	f.Add([]byte("RIFF\x00\x00\x00\x00WEBPVP8X"))
	f.Add([]byte("\xff\xd8\xff\xe1\x00\x10Exif\x00\x00II*\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		kind, err := Sniff(data)
		if err != nil {
			return
		}
		_, _ = Strip(data, kind)
		_, _, _ = Package(data, kind, 1<<16)
		if Raster(kind) {
			_, _, _ = describe(data, kind)
		}
	})
}

// Verifies: CMP-030, CI-006.
// The embedded codec modules are the ones codecs.lock records, which
// `make wasm-codecs-check` rebuilds reproducibly from pinned sources.
func TestCodecsMatchTheLock(t *testing.T) {
	t.Parallel()
	lock, err := os.ReadFile(filepath.Join("codecs", "codecs.lock"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(lock)), "\n") {
		sum, name, _ := strings.Cut(line, "  ")
		want[name] = sum
	}
	for name, data := range map[string][]byte{"webp.wasm": webpModule, "avif.wasm": avifModule} {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want[name] {
			t.Errorf("%s does not match codecs.lock; rebuild with make wasm-codecs", name)
		}
	}
}

// Verifies: CMP-030.
// Codecs started in the background compile while the caller goes on: a
// wait that gives up reports its context's error, and a transcoding
// waits for the compilation instead.
func TestStartCodecs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := StartCodecs(ctx)
	t.Cleanup(func() { _ = c.Close(ctx) })
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Ready(gone); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled wait: %v", err)
	}
	if _, variants, err := c.Transcode(ctx, encodePNG(t, gradient(12, 12)), PNG, 1<<20); err != nil || len(variants) != 6 {
		t.Errorf("Transcode before the codecs were ready: %d %v", len(variants), err)
	}
	if err := c.Ready(ctx); err != nil {
		t.Errorf("Ready: %v", err)
	}
}
