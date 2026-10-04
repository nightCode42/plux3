// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nightCode42/plux3/backend/internal/codegen"
	"github.com/nightCode42/plux3/backend/internal/compiler"
)

// generatedFile is where plux codegen writes, relative to the host app.
const generatedFile = "lib/plux/plux.g.dart"

// codegenCmd writes the typed Dart API of a project into a host app
// (HST-030): it compiles the project offline and writes the library only
// when its content changed.
func codegenCmd(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("codegen", flag.ContinueOnError)
	host := set.String("host", ".", "the host app's project `directory`")
	out := set.String("o", generatedFile, "the generated `file`, relative to the host app")
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), "Usage: plux codegen [--host dir] [-o file] <project-dir>\n\n"+
			"Writes the typed Dart API of a project into a host app, by default as\n"+
			generatedFile+": route builders with typed parameters and results, exported\n"+
			"components, host event classes, exposed state and feature flags. Run it again\n"+
			"after the app document changes; an unchanged API leaves the file untouched.\n"+
			"Exit codes: 0 written or unchanged, 1 the project has errors or the file cannot\n"+
			"be written, 2 usage error.\n\nFlags:\n")
		set.PrintDefaults()
	}
	if code, ok := flags(set, args, stderr); !ok {
		return code
	}
	fsys, ok := project("codegen", set.Arg(0), stderr)
	if !ok {
		return exitUsage
	}
	res := compiler.Compile(fsys, options(false))
	if res.Diagnostics.HasErrors() {
		return finish(res.Diagnostics, nil, false, stdout, stderr)
	}
	lib, err := codegen.Dart(res.Project)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s codegen: %v\n", name, err)
		return exitFailed
	}
	path := filepath.Join(*host, filepath.FromSlash(*out))
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, lib) { //nolint:gosec // the developer's own file
		_, _ = fmt.Fprintf(stdout, "%s: unchanged\n", *out)
		return exitOK
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s codegen: %v\n", name, err)
		return exitFailed
	}
	if err := writeFile(path, lib); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s codegen: %v\n", name, err)
		return exitFailed
	}
	_, _ = fmt.Fprintf(stdout, "%s: written\n", *out)
	return exitOK
}
