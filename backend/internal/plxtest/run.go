// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/signing/runkey"
)

// Options are what a run of the scenarios is prepared from.
type Options struct {
	// Project is the project's root.
	Project fs.FS
	// Compiler are the options the project is compiled with.
	Compiler compiler.Options
	// Tests is the glob that finds the scenario files; DefaultTests when
	// empty.
	Tests string
	// Entropy makes the key of the run; crypto/rand in the CLI, a fixed
	// reader in tests that compare generated files.
	Entropy io.Reader
	// Runtime says where the generated project gets plux_flutter from.
	Runtime Dependency
}

// Plan is a prepared run: the Flutter project to write and the scenarios
// it holds, or the reasons there is nothing to run.
type Plan struct {
	// Compile are the compiler's diagnostics; any error stops the run, as
	// a project that does not compile has nothing to test.
	Compile plxerr.Diagnostics
	// Findings are the problems of the scenario files.
	Findings []Finding
	// Files are the files of the Flutter project by relative path.
	Files map[string][]byte
	// Cases are the scenarios in the order they run.
	Cases []Case
}

// Runnable reports whether the plan has scenarios to run.
func (p *Plan) Runnable() bool {
	return !p.Compile.HasErrors() && !HasErrors(p.Findings) && len(p.Cases) > 0
}

// Prepare reads the scenarios, compiles the project and generates the
// Flutter project. It starts nothing and writes nothing.
func Prepare(o Options) (*Plan, error) {
	pattern := o.Tests
	if pattern == "" {
		pattern = DefaultTests
	}
	paths, err := Find(o.Project, pattern)
	if err != nil {
		return nil, err
	}
	plan := &Plan{}
	if len(paths) == 0 {
		fd := Finding{Diagnostic: plxerr.NewDiagnostic(plxerr.ScenarioFilesNone, plxerr.Location{}, "no file matches %s", pattern)}
		plan.Findings = append(plan.Findings, fd)
		return plan, nil
	}
	res := compiler.Compile(o.Project, o.Compiler)
	plan.Compile = res.Diagnostics
	if res.Diagnostics.HasErrors() {
		return plan, nil
	}
	validator, err := schema.NewValidator()
	if err != nil {
		return nil, fmt.Errorf("plxtest: %w", err)
	}
	lim := limits.Defaults()
	var files []*File
	for _, p := range paths {
		data, err := fs.ReadFile(o.Project, p)
		if err != nil {
			return nil, fmt.Errorf("plxtest: read %s: %w", p, err)
		}
		f, findings := ParseFile(validator, lim, p, data)
		plan.Findings = append(plan.Findings, findings...)
		if f == nil {
			continue
		}
		findings = Check(res.Project, f)
		plan.Findings = append(plan.Findings, findings...)
		if !HasErrors(findings) {
			files = append(files, f)
		}
	}
	if HasErrors(plan.Findings) {
		return plan, nil
	}
	key, err := runkey.New(o.Entropy)
	if err != nil {
		return nil, fmt.Errorf("plxtest: %w", err)
	}
	rel, err := BuildRelease(res, key)
	if err != nil {
		return nil, err
	}
	in := Input{Release: rel, Files: files, Runtime: o.Runtime}
	if res.Natives != nil {
		in.Natives = res.Natives
	}
	plan.Files, plan.Cases, err = Generate(in)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// Write writes the files of a plan below dir.
func (p *Plan) Write(dir string) error {
	for _, name := range slices.Sorted(maps.Keys(p.Files)) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("plxtest: %w", err)
		}
		if err := os.WriteFile(path, p.Files[name], 0o600); err != nil {
			return fmt.Errorf("plxtest: %w", err)
		}
	}
	return nil
}

// Flutter runs `flutter test --reporter json` in dir with the Flutter
// executable at path, returns the reporter's output and copies what Flutter
// prints to its error stream to log. The Flutter SDK is the developer's own;
// nothing here installs it.
func Flutter(ctx context.Context, path, dir string, log io.Writer) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "test", "--reporter", "json")
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		// A failing scenario makes flutter exit non-zero with a complete
		// report; only a run that reported nothing is a harness failure.
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(out.String(), `"type":"done"`) {
			return out.Bytes(), nil
		}
		return out.Bytes(), fmt.Errorf("run %s test: %w", path, err)
	}
	return out.Bytes(), nil
}
