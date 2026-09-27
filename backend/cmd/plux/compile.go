// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nightCode42/plux3/backend/internal/buildinfo"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
)

// report is the --json output of validate and build.
type report struct {
	OK          bool               `json:"ok"`
	Errors      int                `json:"errors"`
	Warnings    int                `json:"warnings"`
	Bundles     []bundleInfo       `json:"bundles,omitempty"`
	Diagnostics plxerr.Diagnostics `json:"diagnostics"`
}

// bundleInfo describes a written bundle.
type bundleInfo struct {
	Role        string   `json:"role"` // "app" or "plugin"
	Key         string   `json:"key"`
	ID          string   `json:"id"`
	Development bool     `json:"development"`
	File        string   `json:"file"`
	SourceMap   string   `json:"sourceMap,omitempty"`
	Size        int      `json:"size"`
	Hash        string   `json:"hash"`
	Features    []string `json:"features"`
}

// flags parses the flags of a command; it returns false and the exit code
// when the command must stop.
func flags(set *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	set.SetOutput(stderr)
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, false
		}
		return exitUsage, false
	}
	if set.NArg() != 1 {
		set.Usage()
		return exitUsage, false
	}
	return exitOK, true
}

// project checks that dir is a directory and returns it as a file system.
func project(cmd, dir string, stderr io.Writer) (fs.FS, bool) {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		_, _ = fmt.Fprintf(stderr, "%s %s: %s is not a directory\n", name, cmd, dir)
		return nil, false
	}
	return os.DirFS(dir), true
}

// options returns the compiler options of the CLI: the registry's
// default limits and this binary's version (CMP-005).
func options(dev bool) compiler.Options {
	opts := compiler.DefaultOptions()
	opts.Version = buildinfo.Get().Version
	if dev {
		opts.Mode = compiler.Development
	}
	return opts
}

// validate checks a project directory offline (CLI-005).
func validate(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := set.Bool("json", false, "print the result as JSON on stdout")
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), "Usage: plux validate [--json] <project-dir>\n\n"+
			"Validates a project in the Git layout without a server. Exit codes:\n"+
			"0 no errors, 1 the project has errors, 2 usage error.\n\nFlags:\n")
		set.PrintDefaults()
	}
	if code, ok := flags(set, args, stderr); !ok {
		return code
	}
	fsys, ok := project("validate", set.Arg(0), stderr)
	if !ok {
		return exitUsage
	}
	res := compiler.Compile(fsys, options(false))
	return finish(res.Diagnostics, nil, *jsonOut, stdout, stderr)
}

// build compiles a project directory into bundles offline (CLI-005).
func build(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("build", flag.ContinueOnError)
	jsonOut := set.Bool("json", false, "print the result as JSON on stdout")
	dev := set.Bool("dev", false, "build development bundles, which embed their source maps")
	out := set.String("o", "", "the output `directory` (required)")
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), "Usage: plux build [--dev] [--json] -o <out-dir> <project-dir>\n\n"+
			"Compiles a project in the Git layout into bundles without a server: the app bundle\n"+
			"in <out-dir>/app/<key>.pxb and one bundle per plugin in <out-dir>/plugins/<key>.pxb;\n"+
			"release builds write each source map beside its bundle as <key>.sourcemap.\n"+
			"Exit codes: 0 built, 1 the project has errors or the output cannot be written,\n"+
			"2 usage error.\n\nFlags:\n")
		set.PrintDefaults()
	}
	if code, ok := flags(set, args, stderr); !ok {
		return code
	}
	if *out == "" {
		set.Usage()
		return exitUsage
	}
	fsys, ok := project("build", set.Arg(0), stderr)
	if !ok {
		return exitUsage
	}
	res := compiler.Compile(fsys, options(*dev))
	if res.Diagnostics.HasErrors() {
		return finish(res.Diagnostics, nil, *jsonOut, stdout, stderr)
	}
	var infos []bundleInfo
	for i, b := range append([]*compiler.Bundle{res.App}, res.Plugins...) {
		role, dir := "plugin", "plugins"
		if i == 0 {
			role, dir = "app", "app"
		}
		info, err := write(*out, dir, role, b)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s build: %v\n", name, err)
			return exitFailed
		}
		infos = append(infos, info)
	}
	return finish(res.Diagnostics, infos, *jsonOut, stdout, stderr)
}

// write writes a bundle and its release source map.
func write(out, dir, role string, b *compiler.Bundle) (bundleInfo, error) {
	info := bundleInfo{
		Role: role, Key: b.Key, ID: uuid7.UUID(b.ID).String(), Development: b.SourceMap == nil,
		File: filepath.Join(out, dir, b.Key+".pxb"), Size: len(b.Data), Hash: hex.EncodeToString(b.Hash[:]), Features: b.Features,
	}
	if err := os.MkdirAll(filepath.Join(out, dir), 0o755); err != nil { //nolint:gosec // G301: build output is meant to be read by other tools.
		return info, fmt.Errorf("create the output directory: %w", err)
	}
	if err := writeFile(info.File, b.Data); err != nil {
		return info, err
	}
	if b.SourceMap != nil {
		info.SourceMap = filepath.Join(out, dir, b.Key+".sourcemap")
		if err := writeFile(info.SourceMap, b.SourceMap); err != nil {
			return info, err
		}
	}
	return info, nil
}

// writeFile writes data through a temporary file and a rename, so a
// reader never sees a partial bundle.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plux-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o644)); err != nil { //nolint:gosec // G302: build output is meant to be read by other tools.
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// finish prints the diagnostics and written bundles and returns the exit
// code: 1 when an error was reported.
func finish(diags plxerr.Diagnostics, bundles []bundleInfo, jsonOut bool, stdout, stderr io.Writer) int {
	rep := report{
		OK: !diags.HasErrors(), Errors: diags.Count(plxerr.SeverityError), Warnings: diags.Count(plxerr.SeverityWarning),
		Bundles: bundles, Diagnostics: diags,
	}
	if rep.Diagnostics == nil {
		rep.Diagnostics = plxerr.Diagnostics{}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return exitFailed
		}
	} else {
		for _, d := range diags {
			_, _ = fmt.Fprintln(stderr, d.String())
		}
		for _, b := range bundles {
			_, _ = fmt.Fprintf(stdout, "%s  %s (%d bytes)\n", b.Hash, b.File, b.Size)
		}
		_, _ = fmt.Fprintf(stderr, "%d errors, %d warnings\n", rep.Errors, rep.Warnings)
	}
	if !rep.OK {
		return exitFailed
	}
	return exitOK
}
