// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// hostConfigFile is the host project's Plux configuration (ADR-0041).
const hostConfigFile = "plux.yaml"

// catalogueFile is what plux native scan writes and sync uploads.
const catalogueFile = "plux.catalogue.json"

// hostConfig is what plux.yaml says: the slot widget classes, and
// optionally the host build, the app's identifier and the catalogue's.
type hostConfig struct {
	Slots     []string
	HostBuild string
	AppID     string
	ID        string
}

// native runs plux native scan|sync (CLI-006).
func (e env) native(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "scan":
			return e.nativeScan(args[1:])
		case "sync":
			return e.nativeSync(args[1:])
		}
	}
	_, _ = fmt.Fprint(e.stderr, "Usage: plux native scan|sync [flags]\n")
	return exitUsage
}

// nativeScan builds the host's native catalogue by static analysis: it
// runs `dart run plux_native_scan` in the host project with the slots
// plux.yaml lists and the build pubspec.yaml names.
func (e env) nativeScan(args []string) int {
	set := flag.NewFlagSet("native scan", flag.ContinueOnError)
	host := set.String("host", ".", "the host app's project `directory`")
	output := set.String("o", catalogueFile, "the catalogue `file`, relative to the host project")
	build := set.String("build", "", "the host `build` (default plux.yaml's hostBuild, then pubspec.yaml's version)")
	dart := set.String("dart", "dart", "the Dart `command`")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux native scan [--host dir] [-o file] [--build id]\n\nWrites the host app's native catalogue (native routes, slots and custom actions) by static analysis, without changing its code.", 0); !ok {
		return code
	}
	cfg, err := readHostConfig(*host)
	if err != nil {
		return e.fail("native scan", err)
	}
	b, err := hostBuild(*host, *build, cfg)
	if err != nil {
		return e.fail("native scan", err)
	}
	run := []string{"run", "plux_native_scan", "--root", ".", "--host", b, "--output", *output}
	for _, s := range cfg.Slots {
		run = append(run, "--slot", s)
	}
	if cfg.AppID != "" {
		run = append(run, "--app-id", cfg.AppID)
	}
	if cfg.ID != "" {
		run = append(run, "--id", cfg.ID)
	}
	cmd := exec.CommandContext(context.Background(), *dart, run...) //nolint:gosec // the developer's own Dart command
	cmd.Dir, cmd.Stdout, cmd.Stderr = *host, e.stdout, e.stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return e.fail("native scan", fmt.Errorf("run %s: %w (is the Dart SDK installed, and plux_native_scan a dev dependency of the host?)", *dart, err))
	}
	return exitOK
}

// nativeSync uploads the catalogue for one host build.
func (e env) nativeSync(args []string) int {
	var c common
	set := flag.NewFlagSet("native sync", flag.ContinueOnError)
	c.register(set, true)
	host := set.String("host", ".", "the host app's project `directory`")
	file := set.String("catalogue", "", "the catalogue `file` (default plux.catalogue.json in the host project)")
	build := set.String("build", "", "the host `build` devices report (default plux.yaml's hostBuild, then pubspec.yaml's version)")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux native sync [--build id] [--host dir] [--catalogue file] [-C dir] [--app id] [--json]\n\nUploads the native catalogue of one host build. A build's catalogue never changes once uploaded.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("native sync", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("native sync", err)
	}
	cfg, err := readHostConfig(*host)
	if err != nil {
		return e.fail("native sync", err)
	}
	b, err := hostBuild(*host, *build, cfg)
	if err != nil {
		return e.fail("native sync", err)
	}
	path := *file
	if path == "" {
		path = filepath.Join(*host, catalogueFile)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the developer's own file
	if err != nil {
		return e.fail("native sync", fmt.Errorf("read the catalogue: %w (run plux native scan first)", err))
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("native sync", err)
	}
	native := pluxv1connect.NewNativeCatalogueServiceClient(e.httpClient, c.server, connect.WithInterceptors(headers{token: cl.token, org: c.org}))
	res, err := native.UploadNativeCatalogue(context.Background(), connect.NewRequest(&pluxv1.UploadNativeCatalogueRequest{
		AppId: c.app, HostBuild: b, Catalogue: data,
	}))
	if err != nil {
		return e.fail("native sync", err)
	}
	hb := res.Msg.GetHostBuild()
	state := "uploaded"
	if !res.Msg.GetCreated() {
		state = "already uploaded, unchanged"
	}
	return e.emit(c.json, map[string]any{"hostBuild": hb.GetHostBuild(), "sha256": hb.GetSha256(), "created": res.Msg.GetCreated()},
		fmt.Sprintf("host build %s: %s (sha256 %s)\n", hb.GetHostBuild(), state, hb.GetSha256()))
}

// hostBuild is the build to scan or upload: the flag, else plux.yaml's
// hostBuild, else the version pubspec.yaml names, as `1.4.0+52`.
func hostBuild(dir, flagValue string, cfg hostConfig) (string, error) {
	if b := firstOf(flagValue, cfg.HostBuild); b != "" {
		return b, nil
	}
	f, err := os.Open(filepath.Join(dir, "pubspec.yaml")) //nolint:gosec // the developer's own project
	if err != nil {
		return "", usageError("no --build given, and no pubspec.yaml to read the version from: " + err.Error())
	}
	defer func() { _ = f.Close() }()
	top, _, err := readYAMLSubset(f)
	if err != nil {
		return "", fmt.Errorf("read pubspec.yaml: %w", err)
	}
	if top["version"] == "" {
		return "", usageError("pubspec.yaml names no version: give --build")
	}
	return top["version"], nil
}

// readHostConfig reads plux.yaml; a host without one has no slots.
func readHostConfig(dir string) (hostConfig, error) {
	f, err := os.Open(filepath.Join(dir, hostConfigFile)) //nolint:gosec // the developer's own project
	if errors.Is(err, os.ErrNotExist) {
		return hostConfig{}, nil
	}
	if err != nil {
		return hostConfig{}, fmt.Errorf("read %s: %w", hostConfigFile, err)
	}
	defer func() { _ = f.Close() }()
	top, lists, err := readYAMLSubset(f)
	if err != nil {
		return hostConfig{}, fmt.Errorf("read %s: %w", hostConfigFile, err)
	}
	return hostConfig{Slots: lists["slots"], HostBuild: top["hostBuild"], AppID: top["appId"], ID: top["catalogueId"]}, nil
}

// readYAMLSubset reads the YAML plux.yaml is written in, and the top of a
// pubspec.yaml: top-level `key: value` scalars, optionally quoted, and
// top-level lists of scalars written as `key:` followed by `  - item`
// lines; comments and anything indented under another key are skipped.
// It needs no YAML library.
func readYAMLSubset(r io.Reader) (map[string]string, map[string][]string, error) {
	top, lists := map[string]string{}, map[string][]string{}
	list := ""
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if item, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && list != "" {
				lists[list] = append(lists[list], unquote(strings.TrimSpace(item)))
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, nil, fmt.Errorf("line %d: expected key: value", n)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		list = ""
		if value == "" {
			list = key
			continue
		}
		top[key] = unquote(value)
	}
	if err := s.Err(); err != nil {
		return nil, nil, fmt.Errorf("read: %w", err)
	}
	return top, lists, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}
