// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package generator writes the Flutter project of a no-code Plux app
// (GEN-001, ADR-0024): a ready-to-build embedded-mode app whose shell — its
// name, identifiers, icons, splash, permissions, push and deep links — is
// described by a versioned ShellSpec, and whose content ships as Plux
// releases (GEN-005). The CLI's `plux create` uses it, and P11's server
// endpoint will.
//
// The output is deterministic: the same input gives the same files and
// the same zip bytes. Nothing reads the clock, the environment or the
// machine, and icons are resized with integer arithmetic.
package generator

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/nightCode42/plux3/backend/internal/codegen"
)

// ShellVersion is the version of ShellSpec this generator writes; it
// changes only additively.
const ShellVersion = 1

// ShellSpec is everything about an app that needs a new store build
// (GEN-004 compares two of them in P11). The project records it as
// plux.shell.json.
type ShellSpec struct {
	Version int `json:"version"`
	// Name is the app's display name.
	Name string `json:"name"`
	// Package is the Dart package name, lower_snake_case.
	Package string `json:"package"`
	// ApplicationID is the Android application ID; BundleID the iOS one.
	ApplicationID string `json:"applicationId"`
	BundleID      string `json:"bundleId"`
	// AppVersion is pubspec.yaml's version, such as 1.0.0+1.
	AppVersion string `json:"appVersion"`
	// IconSHA256 identifies the icon's source PNG; empty for the default.
	IconSHA256 string `json:"iconSha256"`
	// SplashColor is the launch screen's background, #RRGGBB.
	SplashColor string `json:"splashColor"`
	// DeviceAPIs are the device APIs the app's plugins request, sorted.
	DeviceAPIs []string `json:"deviceApis"`
	// AndroidPermissions and UsageDescriptions are derived from
	// DeviceAPIs and Push.
	AndroidPermissions []string           `json:"androidPermissions"`
	UsageDescriptions  []UsageDescription `json:"iosUsageDescriptions"`
	// Push is whether the app declares push notifications.
	Push bool `json:"push"`
	// DeepLinkHosts and DeepLinkSchemes are the links the app answers.
	DeepLinkHosts   []string `json:"deepLinkHosts"`
	DeepLinkSchemes []string `json:"deepLinkSchemes"`
	// Packages are optional packages the shell adds; none in P4.
	Packages []string `json:"packages"`
	// Plux is where the app's content comes from.
	Plux PluxSource `json:"plux"`
}

// PluxSource is the Plux app, server, environment and entry route.
type PluxSource struct {
	AppKey      string `json:"appKey"`
	AppID       string `json:"appId"`
	Endpoint    string `json:"endpoint"`
	Environment string `json:"environment"`
	Channel     string `json:"channel"`
	EntryRoute  string `json:"entryRoute"`
}

// Input is what Generate needs besides the ShellSpec.
type Input struct {
	Spec ShellSpec
	// Icon is the source PNG, square, 1024 to 4096 pixels a side; nil for
	// the default icon.
	Icon []byte
	// Keys are the environment's root public keys (SEC-051).
	Keys []codegen.RootKey
	// Baseline holds the release `plux pull` writes, by path under
	// assets/plux/ (SYN-007).
	Baseline map[string][]byte
	// PluxPath, when set, makes the project depend on plux_flutter at
	// this path instead of its published version (development and CI).
	PluxPath string
}

// File is one file of the generated project.
type File struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

// RuntimeConstraint is the plux_flutter version generated projects use.
const RuntimeConstraint = "^0.4.0"

// FlutterVersion is the Flutter the templates are written against: the
// repository's pin (a test keeps them in step).
const FlutterVersion = "3.47.5"

//go:embed all:templates
var templates embed.FS

var (
	dartPackage = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	javaID      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$`)
	appleID     = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
	appVersion  = regexp.MustCompile(`^\d+\.\d+\.\d+\+\d+$`)
	hexColour   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	hostName    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	scheme      = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	routeName   = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

// PackageName is a Dart package name for an app key: lower-kebab becomes
// lower_snake.
func PackageName(key string) string {
	return strings.ReplaceAll(strings.ToLower(key), "-", "_")
}

// Derive fills the spec's derived fields — sorted device APIs, Android
// permissions and iOS usage descriptions — and checks every field.
func Derive(s ShellSpec) (ShellSpec, error) {
	s.Version = ShellVersion
	s.DeviceAPIs = sortedUnique(s.DeviceAPIs)
	s.DeepLinkHosts = sortedUnique(s.DeepLinkHosts)
	s.DeepLinkSchemes = sortedUnique(s.DeepLinkSchemes)
	if s.Packages == nil {
		s.Packages = []string{}
	}
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(strings.TrimSpace(s.Name) != "" && len(s.Name) <= 64, "the name must have 1 to 64 characters")
	check(dartPackage.MatchString(s.Package) && !dartReserved[s.Package], "package %q is not a Dart package name", s.Package)
	check(javaID.MatchString(s.ApplicationID), "application ID %q is not a Java package name such as com.example.app", s.ApplicationID)
	check(appleID.MatchString(s.BundleID), "bundle ID %q is not a reverse-DNS identifier such as com.example.app", s.BundleID)
	check(appVersion.MatchString(s.AppVersion), "version %q is not major.minor.patch+build", s.AppVersion)
	check(hexColour.MatchString(s.SplashColor), "splash colour %q is not #RRGGBB", s.SplashColor)
	check(routeName.MatchString(s.Plux.EntryRoute), "the entry route %q is not a route name", s.Plux.EntryRoute)
	check(s.Plux.AppID != "" && s.Plux.Endpoint != "" && s.Plux.Environment != "", "the Plux app, server and environment are required")
	for _, h := range s.DeepLinkHosts {
		check(hostName.MatchString(h), "deep-link host %q is not a host name", h)
	}
	for _, sc := range s.DeepLinkSchemes {
		check(scheme.MatchString(sc), "deep-link scheme %q is not a URL scheme", sc)
	}
	perms := map[string]int{}
	var usage []UsageDescription
	for _, api := range s.DeviceAPIs {
		g, ok := deviceAPIs[api]
		check(ok, "device API %q is unknown", api)
		for _, p := range g.android {
			perms[p.Name] = p.MaxSDK
		}
		for _, u := range g.ios {
			usage = append(usage, UsageDescription{Key: u.Key, Text: fmt.Sprintf(u.Text, s.Name)})
		}
	}
	if s.Push {
		perms["android.permission.POST_NOTIFICATIONS"] = 0
	}
	s.AndroidPermissions = []string{}
	for name, maxSDK := range perms {
		if maxSDK > 0 {
			name += ";maxSdkVersion=" + strconv.Itoa(maxSDK)
		}
		s.AndroidPermissions = append(s.AndroidPermissions, name)
	}
	slices.Sort(s.AndroidPermissions)
	slices.SortFunc(usage, func(a, b UsageDescription) int { return strings.Compare(a.Key, b.Key) })
	s.UsageDescriptions = append([]UsageDescription{}, usage...)
	return s, errors.Join(errs...)
}

// dartReserved are names a Dart package may not take.
var dartReserved = map[string]bool{"flutter": true, "plux_flutter": true, "test": true, "integration_test": true}

func sortedUnique(in []string) []string {
	out := append([]string{}, in...)
	slices.Sort(out)
	return slices.Compact(out)
}

// view is what the templates read.
type view struct {
	ShellSpec
	Permissions                        []AndroidPermission
	Entitlements                       bool
	SplashHex                          string
	SplashRed, SplashGreen, SplashBlue string
	RuntimeConstraint                  string
	FlutterVersion                     string
	PluxPath                           string
	BaselineAssets                     bool
}

// Generate renders the project of in, sorted by path, after Derive has
// checked its spec.
func Generate(in Input) ([]File, error) {
	in.Spec.IconSHA256 = ""
	if in.Icon != nil {
		sum := sha256.Sum256(in.Icon)
		in.Spec.IconSHA256 = hex.EncodeToString(sum[:])
	}
	spec, err := Derive(in.Spec)
	if err != nil {
		return nil, err
	}
	v := view{
		ShellSpec: spec, Entitlements: spec.Push || len(spec.DeepLinkHosts) > 0,
		SplashHex: strings.ToUpper(spec.SplashColor), RuntimeConstraint: RuntimeConstraint, FlutterVersion: FlutterVersion, PluxPath: in.PluxPath,
	}
	for _, p := range spec.AndroidPermissions {
		name, maxSDK, _ := strings.Cut(p, ";maxSdkVersion=")
		n, _ := strconv.Atoi(maxSDK)
		v.Permissions = append(v.Permissions, AndroidPermission{Name: name, MaxSDK: n})
	}
	bg := splash(spec.SplashColor)
	v.SplashRed, v.SplashGreen, v.SplashBlue = unit(bg.R), unit(bg.G), unit(bg.B)
	for p := range in.Baseline {
		if strings.HasPrefix(p, "assets/") {
			v.BaselineAssets = true
		}
	}
	files := map[string]File{}
	if err := renderTemplates(v, files); err != nil {
		return nil, err
	}
	shell, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode the shell spec: %w", err)
	}
	files["plux.shell.json"] = File{Path: "plux.shell.json", Data: append(shell, '\n'), Mode: 0o644}
	files["lib/plux/plux_options.g.dart"] = File{Path: "lib/plux/plux_options.g.dart", Mode: 0o644, Data: codegen.Options(codegen.OptionsSpec{
		AppKey: spec.Plux.AppKey, AppID: spec.Plux.AppID, Endpoint: spec.Plux.Endpoint, Environment: spec.Plux.Environment,
		Channel: spec.Plux.Channel, Keys: in.Keys,
	})}
	if err := icons(in.Icon, bg, files); err != nil {
		return nil, err
	}
	for p, data := range in.Baseline {
		clean := path.Clean(p)
		if path.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return nil, fmt.Errorf("baseline path %q leaves assets/plux", p)
		}
		files["assets/plux/"+clean] = File{Path: "assets/plux/" + clean, Data: data, Mode: 0o644}
	}
	out := make([]File, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// renderTemplates renders every embedded template into files.
func renderTemplates(v view, files map[string]File) error {
	funcs := template.FuncMap{
		"xml":  xmlEscape,
		"dart": func(s string) string { return strings.Trim(codegen.DartString(s), "'") },
		"json": func(s string) (string, error) { b, err := json.Marshal(s); return string(b), err },
	}
	err := fs.WalkDir(templates, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err //nolint:wrapcheck // an embedded file system does not fail
		}
		out := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".tmpl")
		switch {
		case out == "ios/Runner/Runner.entitlements" && !v.Entitlements:
			return nil
		case out == "android/app/src/main/kotlin/MainActivity.kt":
			out = "android/app/src/main/kotlin/" + strings.ReplaceAll(v.ApplicationID, ".", "/") + "/MainActivity.kt"
		}
		src, err := templates.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read template %s: %w", p, err)
		}
		t, err := template.New(p).Delims("{%", "%}").Funcs(funcs).Option("missingkey=error").Parse(string(src))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", p, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, v); err != nil {
			return fmt.Errorf("render %s: %w", out, err)
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(out, ".sh") {
			mode = 0o755
		}
		files[out] = File{Path: out, Data: buf.Bytes(), Mode: mode}
		return nil
	})
	if err != nil {
		return fmt.Errorf("render the templates: %w", err)
	}
	return nil
}

// icons writes the Android launcher icons, the iOS app icon set and the
// iOS launch image from the source PNG, or from the default icon.
func icons(source []byte, bg color.NRGBA, files map[string]File) error {
	img := defaultIcon(bg)
	if source != nil {
		var err error
		if img, err = decodeIcon(source); err != nil {
			return err
		}
	}
	add := func(p string, side int, opaque bool) error {
		data, err := resize(img, side, opaque, bg)
		if err != nil {
			return err
		}
		files[p] = File{Path: p, Data: data, Mode: 0o644}
		return nil
	}
	for density, side := range map[string]int{"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192} {
		if err := add("android/app/src/main/res/mipmap-"+density+"/ic_launcher.png", side, false); err != nil {
			return err
		}
	}
	for name, side := range map[string]int{
		"20x20@1x": 20, "20x20@2x": 40, "20x20@3x": 60, "29x29@1x": 29, "29x29@2x": 58, "29x29@3x": 87,
		"40x40@1x": 40, "40x40@2x": 80, "40x40@3x": 120, "60x60@2x": 120, "60x60@3x": 180, "76x76@1x": 76,
		"76x76@2x": 152, "83.5x83.5@2x": 167, "1024x1024@1x": 1024,
	} {
		if err := add("ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-"+name+".png", side, true); err != nil {
			return err
		}
	}
	for name, side := range map[string]int{"LaunchImage.png": 128, "LaunchImage@2x.png": 256, "LaunchImage@3x.png": 384} {
		if err := add("ios/Runner/Assets.xcassets/LaunchImage.imageset/"+name, side, false); err != nil {
			return err
		}
	}
	return nil
}

// splash parses #RRGGBB, which the spec's validation has checked. Each
// channel is parsed as 8 bits, so no conversion narrows a wider value.
func splash(hex string) color.NRGBA {
	channel := func(i int) uint8 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		return uint8(v)
	}
	return color.NRGBA{R: channel(1), G: channel(3), B: channel(5), A: 0xff}
}

// unit is a channel as Interface Builder writes it, from 0 to 1.
func unit(c uint8) string {
	return strconv.FormatFloat(float64(c)/255, 'f', 3, 64)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
