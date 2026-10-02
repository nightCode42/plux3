// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/codegen"
	"github.com/nightCode42/plux3/backend/internal/generator"
)

// createFlags are plux create's flags beyond the server's.
type createFlags struct {
	env, channel, out, zip, remote, branch       *string
	name, applicationID, bundleID, version, icon *string
	splash, pluxPath                             *string
}

func registerCreate(set *flag.FlagSet) createFlags {
	return createFlags{
		env:           set.String("env", "production", "the environment `key` the app syncs from"),
		channel:       set.String("channel", "production", "the channel `key` whose release is embedded"),
		out:           set.String("out", "", "write the project into this new or empty `directory`"),
		zip:           set.String("zip", "", "write the project as this zip `file`"),
		remote:        set.String("git", "", "commit the project and push it to this Git `remote` with your own git"),
		branch:        set.String("branch", "main", "the `branch` --git pushes"),
		name:          set.String("name", "", "the app's display `name` (default the app's name)"),
		applicationID: set.String("application-id", "", "the Android application `ID` (default com.example.<package>)"),
		bundleID:      set.String("bundle-id", "", "the iOS bundle `ID` (default the application ID)"),
		version:       set.String("version", "1.0.0+1", "the app's `version`, major.minor.patch+build"),
		icon:          set.String("icon", "", "the icon, a square PNG `file` of 1024 to 4096 pixels (default a generated icon)"),
		splash:        set.String("splash", "#FFFFFF", "the launch screen's `colour`, #RRGGBB"),
		pluxPath:      set.String("plux-path", "", "depend on plux_flutter at this `directory` instead of its release (development, CI)"),
	}
}

// create generates the Flutter project of a no-code app (GEN-001,
// ADR-0024): the shell from the app's release and the flags, the release
// itself embedded as its baseline, written to a directory, a zip or a Git
// remote (GEN-003).
func (e env) create(args []string) int {
	var c common
	set := flag.NewFlagSet("create", flag.ContinueOnError)
	c.register(set, true)
	f := registerCreate(set)
	rest, code, ok := parse(set, args, e.stderr, "Usage: plux create <app> --out dir | --zip file | --git remote [--branch name] [flags]\n\n"+
		"Generates a ready-to-build Flutter project for a Plux app: its name, identifiers,\n"+
		"icons, splash, permissions from its plugins' device APIs, push and deep links, the\n"+
		"runtime wired in with the environment's root keys, the channel's release embedded,\n"+
		"build scripts and CI templates for signed releases. --git commits and pushes with\n"+
		"your own git and credentials; nothing is stored.", 1)
	if !ok {
		return code
	}
	outputs := 0
	for _, o := range []string{*f.out, *f.zip, *f.remote} {
		if o != "" {
			outputs++
		}
	}
	if outputs != 1 {
		return e.fail("create", usageError("give exactly one of --out, --zip and --git"))
	}
	c.app = rest[0]
	if err := c.resolve(); err != nil {
		return e.fail("create", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("create", err)
	}
	ctx := context.Background()
	app, err := findApp(ctx, cl, c.app)
	if err != nil {
		return e.fail("create", err)
	}
	c.app = app.GetId()
	pulled, err := os.MkdirTemp("", "plux-create-")
	if err != nil {
		return e.fail("create", err)
	}
	defer func() { _ = os.RemoveAll(pulled) }()
	base, err := pullBaseline(ctx, cl, c, *f.env, *f.channel, pulled)
	if err != nil {
		return e.fail("create", err)
	}
	in, err := createInput(pulled, base)
	if err != nil {
		return e.fail("create", err)
	}
	pkg := generator.PackageName(app.GetKey())
	if err := f.apply(&in, pkg, app.GetKey(), app.GetId(), c.server); err != nil {
		return e.fail("create", err)
	}
	files, err := generator.Generate(in)
	if err != nil {
		return e.fail("create", err)
	}
	where, err := deliver(ctx, files, pkg, *f.out, *f.zip, *f.remote, *f.branch)
	if err != nil {
		return e.fail("create", err)
	}
	return e.emit(c.json, map[string]any{"package": pkg, "files": len(files), "release": base.Sequence, "output": where},
		fmt.Sprintf("Generated %s (%d files) with release %d embedded: %s.\n", pkg, len(files), base.Sequence, where))
}

// apply sets what the flags give on the shell read from the release.
func (f createFlags) apply(in *generator.Input, pkg, key, appID, server string) error {
	in.Spec.Name = firstOf(*f.name, in.Spec.Name, key)
	in.Spec.Package = pkg
	in.Spec.ApplicationID = firstOf(*f.applicationID, "com.example."+pkg)
	in.Spec.BundleID = firstOf(*f.bundleID, in.Spec.ApplicationID)
	in.Spec.AppVersion = *f.version
	in.Spec.SplashColor = *f.splash
	in.Spec.Plux = generator.PluxSource{AppKey: key, AppID: appID, Endpoint: server, Environment: *f.env, Channel: *f.channel, EntryRoute: in.Spec.Plux.EntryRoute}
	in.PluxPath = *f.pluxPath
	if *f.icon != "" {
		icon, err := os.ReadFile(*f.icon) //nolint:gosec // the developer's own file
		if err != nil {
			return fmt.Errorf("read the icon: %w", err)
		}
		in.Icon = icon
	}
	return nil
}

// createInput reads the shell's settings from the pulled release — the
// app's name, entry route, push and deep links, and the device APIs its
// plugins request — and loads the release as the project's baseline.
func createInput(dir string, base baseline) (generator.Input, error) {
	var in generator.Input
	tree, err := readTree(dir)
	if err != nil {
		return in, err
	}
	in.Baseline = tree
	for _, b := range base.Bundles {
		meta, err := bundleMeta(tree[b.File])
		if err != nil {
			return in, fmt.Errorf("bundle %s: %w", b.File, err)
		}
		shellFrom(meta, &in.Spec)
	}
	if in.Spec.Plux.EntryRoute == "" {
		return in, errors.New("the app names no entry route, which the generated app opens")
	}
	for _, k := range base.Keys {
		key, err := hex.DecodeString(k.PublicKey)
		if err != nil {
			return in, fmt.Errorf("key %s: %w", k.KeyID, err)
		}
		in.Keys = append(in.Keys, codegen.RootKey{KeyID: k.KeyID, Algorithm: k.Algorithm, Role: k.Role, PublicKey: key})
	}
	return in, nil
}

// readTree reads every file under dir, by slash path.
func readTree(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		data, err := os.ReadFile(p) //nolint:gosec // the release just pulled
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the release: %w", err)
	}
	return out, nil
}

// shellFrom adds what a bundle's meta says about the shell: an app
// bundle's name, entry route, push and deep links, a plugin bundle's
// device APIs.
func shellFrom(meta *fbs.Meta, s *generator.ShellSpec) {
	if meta.Kind() != fbs.BundleKindApp {
		if caps := meta.Capabilities(nil); caps != nil {
			for i := range caps.DeviceApisLength() {
				s.DeviceAPIs = append(s.DeviceAPIs, string(caps.DeviceApis(i)))
			}
		}
		return
	}
	s.Name = string(meta.Name())
	s.Plux.EntryRoute = string(meta.EntryRoute())
	if p := meta.Push(nil); p != nil {
		s.Push = p.Enabled()
	}
	if links := meta.DeepLinks(nil); links != nil {
		for i := range links.HostsLength() {
			s.DeepLinkHosts = append(s.DeepLinkHosts, string(links.Hosts(i)))
		}
		for i := range links.SchemesLength() {
			s.DeepLinkSchemes = append(s.DeepLinkSchemes, string(links.Schemes(i)))
		}
	}
}

// bundleMeta is a bundle's meta section.
func bundleMeta(data []byte) (*fbs.Meta, error) {
	b, err := bundle.ReadStructure(data)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	for _, s := range b.Sections {
		if s.Kind == bundle.SectionMeta {
			return fbs.GetRootAsMeta(s.Data, 0), nil
		}
	}
	return nil, errors.New("no meta section")
}

// deliver writes the project to the one output given (GEN-003).
func deliver(ctx context.Context, files []generator.File, pkg, out, zipFile, remote, branch string) (string, error) {
	switch {
	case out != "":
		return out, generator.WriteDir(out, files) //nolint:wrapcheck // its errors name the path
	case zipFile != "":
		data, err := generator.Zip(pkg, files)
		if err != nil {
			return "", err //nolint:wrapcheck // its errors say what failed
		}
		return zipFile, writeFile(zipFile, data)
	}
	dir, err := os.MkdirTemp("", "plux-create-git-")
	if err != nil {
		return "", fmt.Errorf("create a work tree: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := generator.WriteDir(dir, files); err != nil {
		return "", err //nolint:wrapcheck // its errors name the path
	}
	// The developer's own git, as a subprocess with arguments and never a
	// shell; it uses their identity and credential helpers.
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch", branch},
		{"add", "--all"},
		{"commit", "--quiet", "--message", "Generate the Flutter project with plux create"},
		{"push", "--quiet", remote, "HEAD:refs/heads/" + branch},
	} {
		cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: git with fixed subcommands and the developer's remote and branch
		cmd.Dir = dir
		if msg, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w\n%s", args[0], err, msg)
		}
	}
	return remote + " " + branch, nil
}
