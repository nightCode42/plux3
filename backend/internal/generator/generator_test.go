// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/codegen"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func sample() Input {
	return Input{
		Spec: ShellSpec{
			Name: "Loans & More", Package: "loans", ApplicationID: "com.example.loans", BundleID: "com.example.loans",
			AppVersion: "1.2.0+7", SplashColor: "#0B6E4F", DeviceAPIs: []string{"location", "camera", "photos", "camera"},
			Push: true, DeepLinkHosts: []string{"loans.example.com"}, DeepLinkSchemes: []string{"loans"},
			Plux: PluxSource{AppKey: "loans", AppID: "01e0c450-6c00-7000-8000-000000000001", Endpoint: "https://plux.example.com", Environment: "production", Channel: "beta", EntryRoute: "welcome"},
		},
		Keys:     []codegen.RootKey{{KeyID: "k1", Algorithm: "ed25519", Role: "targets", PublicKey: []byte{1, 2, 3}}},
		Baseline: map[string][]byte{"baseline.json": []byte("{}\n"), "keys.json": []byte("[]\n"), "bundles/_app.pxb": {0x50, 0x58}, "assets/ab12": {1}},
	}
}

func byPath(files []File) map[string]File {
	out := map[string]File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

// TestGenerateIsDeterministic generates the sample project twice: the
// same files and the same zip bytes, every template rendered, and a
// golden list of the files' hashes.
// Verifies: GEN-001, GEN-003.
func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()
	first, err := Generate(sample())
	if err != nil {
		t.Fatal(err)
	}
	again, err := Generate(sample())
	if err != nil {
		t.Fatal(err)
	}
	z1, err := Zip("loans", first)
	if err != nil {
		t.Fatal(err)
	}
	z2, _ := Zip("loans", again)
	if !bytes.Equal(z1, z2) {
		t.Error("two zips of the same project differ")
	}
	var list strings.Builder
	for i, f := range first {
		if i > 0 && first[i-1].Path >= f.Path {
			t.Errorf("files are not sorted: %s before %s", first[i-1].Path, f.Path)
		}
		text := !strings.HasSuffix(f.Path, ".png") && !strings.HasPrefix(f.Path, "assets/plux/")
		if text && (bytes.Contains(f.Data, []byte("{%")) || bytes.Contains(f.Data, []byte("%}"))) {
			t.Errorf("%s holds an unrendered action", f.Path)
		}
		if text && bytes.Contains(f.Data, []byte("\r\n")) {
			t.Errorf("%s has CRLF line endings", f.Path)
		}
		sum := sha256.Sum256(f.Data)
		fmt.Fprintf(&list, "%s %o %s\n", hex.EncodeToString(sum[:]), f.Mode, f.Path)
	}
	path := filepath.Join("testdata", "sample.sha256")
	if *update {
		if err := os.WriteFile(path, []byte(list.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(path); err != nil || string(want) != list.String() { //nolint:gosec // the test's own golden
		t.Errorf("the sample project differs from %s (%v); review, then run go test ./internal/generator -update", path, err)
	}

	r, err := zip.NewReader(bytes.NewReader(z1), int64(len(z1)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "loans/") || strings.Contains(f.Name, "..") || len(f.Extra) != 0 || f.ModifiedDate != 33 || f.ModifiedTime != 0 { //nolint:staticcheck // the DOS fields are what the archive holds
			t.Errorf("zip entry %s: extra %d, date %d %d", f.Name, len(f.Extra), f.ModifiedDate, f.ModifiedTime) //nolint:staticcheck // as above
		}
	}
	if m := byPath(first)["scripts/build_android.sh"].Mode; m != 0o755 {
		t.Errorf("a script's mode is %o", m)
	}
}

// TestShellAndPlatformSettings checks what the shell spec turns into: the
// identifiers, permissions and usage descriptions, push and deep links,
// the configuration with its keys, and the embedded release.
// Verifies: GEN-001, SEC-080.
func TestShellAndPlatformSettings(t *testing.T) {
	t.Parallel()
	files, err := Generate(sample())
	if err != nil {
		t.Fatal(err)
	}
	f := byPath(files)
	contains := func(path string, wants ...string) {
		t.Helper()
		got, ok := f[path]
		if !ok {
			t.Errorf("no %s", path)
			return
		}
		for _, w := range wants {
			if !bytes.Contains(got.Data, []byte(w)) {
				t.Errorf("%s lacks %q", path, w)
			}
		}
	}
	contains("android/app/src/main/AndroidManifest.xml",
		`android:label="Loans &amp; More"`, `android.permission.CAMERA`, `android.permission.READ_EXTERNAL_STORAGE" android:maxSdkVersion="32"`,
		`android.permission.POST_NOTIFICATIONS`, `android:host="loans.example.com"`, `android:scheme="loans"`, `android.permission.INTERNET`)
	contains("android/app/build.gradle.kts", `applicationId = "com.example.loans"`, `rootProject.file("key.properties")`)
	contains("android/app/src/main/kotlin/com/example/loans/MainActivity.kt", "package com.example.loans")
	contains("android/app/src/main/res/values/colors.xml", "#0B6E4F")
	contains("ios/Runner/Info.plist", "<string>Loans &amp; More</string>", "NSCameraUsageDescription", "NSLocationWhenInUseUsageDescription",
		"Loans &amp; More uses the camera", "<string>loans</string>", "remote-notification")
	contains("ios/Runner/Runner.entitlements", "aps-environment", "applinks:loans.example.com")
	contains("ios/Runner.xcodeproj/project.pbxproj", "CODE_SIGN_ENTITLEMENTS = Runner/Runner.entitlements;", "PRODUCT_BUNDLE_IDENTIFIER = com.example.loans;")
	contains("ios/Runner/Base.lproj/LaunchScreen.storyboard", `red="0.043" green="0.431" blue="0.310"`)
	contains("pubspec.yaml", "name: loans\n", "version: 1.2.0+7", "plux_flutter: "+RuntimeConstraint, "- assets/plux/assets/")
	contains("lib/main.dart", "title: 'Loans & More'", "PluxView(\n          'welcome'", "PluxOptions.config(navigatorKey: navigatorKey)")
	contains("lib/plux/plux_options.g.dart", "static const appId = '01e0c450-6c00-7000-8000-000000000001';", "static const channel = 'beta';", "Uint8List.fromList([0x01, 0x02, 0x03])")
	contains("integration_test/app_test.dart", "import 'package:loans/main.dart' as app;")
	contains("assets/plux/bundles/_app.pxb", "PX")
	contains(".github/workflows/release.yml", "FLUTTER_VERSION: "+FlutterVersion, "secrets.ANDROID_KEYSTORE_BASE64", "<key>com.example.loans</key>")
	contains(".gitlab-ci.yml", "ANDROID_KEYSTORE_BASE64")
	contains("README.md", "## Push notifications")

	var spec ShellSpec
	if err := json.Unmarshal(f["plux.shell.json"].Data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Version != ShellVersion || strings.Join(spec.DeviceAPIs, ",") != "camera,location,photos" || spec.Packages == nil ||
		!strings.Contains(strings.Join(spec.AndroidPermissions, ","), "READ_EXTERNAL_STORAGE;maxSdkVersion=32") {
		t.Errorf("plux.shell.json: %+v", spec)
	}
	for path, data := range f {
		if strings.HasPrefix(path, ".github/") || path == ".gitlab-ci.yml" {
			if regexp.MustCompile(`(?i)(password|token|secret)\s*[:=]\s*['"]?[A-Za-z0-9+/]{12,}`).Match(data.Data) {
				t.Errorf("%s may hold a secret", path)
			}
		}
	}

	plain := sample()
	plain.Spec.Push, plain.Spec.DeepLinkHosts, plain.Spec.DeepLinkSchemes, plain.Spec.DeviceAPIs = false, nil, nil, nil
	plain.Baseline = map[string][]byte{"baseline.json": []byte("{}")}
	files, err = Generate(plain)
	if err != nil {
		t.Fatal(err)
	}
	f = byPath(files)
	if _, ok := f["ios/Runner/Runner.entitlements"]; ok {
		t.Error("entitlements without push or links")
	}
	for path, unwanted := range map[string]string{
		"ios/Runner.xcodeproj/project.pbxproj": "CODE_SIGN_ENTITLEMENTS", "ios/Runner/Info.plist": "UIBackgroundModes",
		"android/app/src/main/AndroidManifest.xml": "android.intent.action.VIEW", "pubspec.yaml": "assets/plux/assets/", "README.md": "Push notifications",
	} {
		if bytes.Contains(f[path].Data, []byte(unwanted)) {
			t.Errorf("%s holds %q", path, unwanted)
		}
	}
	plain.PluxPath = "/repo/packages/plux_flutter"
	files, _ = Generate(plain)
	if !bytes.Contains(byPath(files)["pubspec.yaml"].Data, []byte("plux_flutter:\n    path: /repo/packages/plux_flutter")) {
		t.Error("a path dependency on the runtime")
	}
}

// TestEveryDeviceAPIIsMapped checks the permissions table against the
// document schema's deviceApi enum.
// Verifies: GEN-001, SEC-080.
func TestEveryDeviceAPIIsMapped(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "json", "common.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	enum := schema.Defs["deviceApi"].Enum
	if len(enum) == 0 {
		t.Fatal("no deviceApi enum")
	}
	for _, api := range enum {
		if _, ok := deviceAPIs[api]; !ok {
			t.Errorf("device API %s is not mapped", api)
		}
	}
	if len(deviceAPIs) != len(enum) {
		t.Errorf("the table maps %d APIs, the schema has %d", len(deviceAPIs), len(enum))
	}
}

// TestIcons resizes the icon for every slot: the right sizes, opaque iOS
// icons; a source that is not a large square PNG is refused.
// Verifies: GEN-001.
func TestIcons(t *testing.T) {
	t.Parallel()
	src := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for y := range 1024 {
		for x := range 1024 {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x / 4), G: uint8(y / 4), B: 0x80, A: uint8((x + y) / 8)}) //nolint:gosec // within 8 bits
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	in := sample()
	in.Icon = buf.Bytes()
	files, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png":                 192,
		"android/app/src/main/res/mipmap-mdpi/ic_launcher.png":                    48,
		"ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-83.5x83.5@2x.png": 167,
		"ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-1024x1024@1x.png": 1024,
		"ios/Runner/Assets.xcassets/LaunchImage.imageset/LaunchImage@3x.png":      384,
	} {
		img, err := png.Decode(bytes.NewReader(byPath(files)[path].Data))
		if err != nil || img.Bounds().Dx() != want || img.Bounds().Dy() != want {
			t.Errorf("%s: %v %v", path, err, img.Bounds())
			continue
		}
		if strings.Contains(path, "AppIcon") {
			for _, p := range []image.Point{{0, 0}, {want / 2, want / 3}, {want - 1, want - 1}} {
				if _, _, _, a := img.At(p.X, p.Y).RGBA(); a != 0xffff {
					t.Errorf("%s is not opaque at %v", path, p)
				}
			}
		}
	}
	var spec ShellSpec
	_ = json.Unmarshal(byPath(files)["plux.shell.json"].Data, &spec)
	if sum := sha256.Sum256(in.Icon); spec.IconSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("the icon's hash: %s", spec.IconSHA256)
	}
	small := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	buf.Reset()
	_ = png.Encode(&buf, small)
	for _, bad := range [][]byte{buf.Bytes(), []byte("not a png")} {
		in.Icon = bad
		if _, err := Generate(in); err == nil {
			t.Error("a bad icon was accepted")
		}
	}
}

// TestDeriveRefusesBadSettings checks the spec's fields.
// Verifies: GEN-001.
func TestDeriveRefusesBadSettings(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(*ShellSpec){
		"name":        func(s *ShellSpec) { s.Name = " " },
		"package":     func(s *ShellSpec) { s.Package = "Loans-App" },
		"reserved":    func(s *ShellSpec) { s.Package = "flutter" },
		"application": func(s *ShellSpec) { s.ApplicationID = "loans" },
		"bundle":      func(s *ShellSpec) { s.BundleID = "com example" },
		"version":     func(s *ShellSpec) { s.AppVersion = "1.0" },
		"splash":      func(s *ShellSpec) { s.SplashColor = "green" },
		"route":       func(s *ShellSpec) { s.Plux.EntryRoute = "Home" },
		"plux":        func(s *ShellSpec) { s.Plux.Endpoint = "" },
		"host":        func(s *ShellSpec) { s.DeepLinkHosts = []string{"https://x.com"} },
		"scheme":      func(s *ShellSpec) { s.DeepLinkSchemes = []string{"1x"} },
		"api":         func(s *ShellSpec) { s.DeviceAPIs = []string{"teleport"} },
	} {
		in := sample()
		edit(&in.Spec)
		if _, err := Derive(in.Spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	in := sample()
	in.Baseline = map[string][]byte{"../escape": {1}}
	if _, err := Generate(in); err == nil {
		t.Error("a baseline path outside assets/plux was accepted")
	}
	if got := PackageName("My-App"); got != "my_app" {
		t.Errorf("PackageName = %q", got)
	}
}

// TestWriteDir writes into a new directory and refuses a non-empty one.
// Verifies: GEN-003.
func TestWriteDir(t *testing.T) {
	t.Parallel()
	files, err := Generate(sample())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "app")
	if err := WriteDir(dir, files); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "scripts", "build_ios.sh")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("a script: %v", err)
	}
	if err := WriteDir(dir, files); err == nil {
		t.Error("a non-empty directory was accepted")
	}
}

// TestPinsFollowTheRepository keeps the templates' Flutter version and
// action pins in step with the repository's CI.
// Verifies: GEN-002, GEN-003.
func TestPinsFollowTheRepository(t *testing.T) {
	t.Parallel()
	makefile, err := os.ReadFile(filepath.Join("..", "..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^FLUTTER_VERSION\s*:=\s*` + regexp.QuoteMeta(FlutterVersion) + `$`).Match(makefile) {
		t.Errorf("the Makefile pins another Flutter than %s", FlutterVersion)
	}
	ci, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := templates.ReadFile("templates/.github/workflows/release.yml.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, uses := range regexp.MustCompile(`uses: (\S+@[0-9a-f]{40})`).FindAllSubmatch(release, -1) {
		if !bytes.Contains(ci, uses[1]) {
			t.Errorf("the release template uses %s, which CI does not", uses[1])
		}
	}
}
