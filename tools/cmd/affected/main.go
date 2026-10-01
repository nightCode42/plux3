// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command affected decides which CI jobs a change runs (CI-002,
// ADR-0043), from the rules in ci/affected.json.
//
//	affected -event pull_request -base <rev> -head <rev> [-github-output file] [-summary file]
//	affected -event push
//	affected -base origin/main -make
//
// With -head, the change is base..head; without it, the change is from
// base to the working tree, untracked files included, which is what
// `make check-changed` asks about. -github-output appends `jobs=` with a
// JSON object of every selectable job and whether it runs; -summary
// appends the reasons as Markdown, which is also printed. -make prints
// the make targets of the selected jobs that run locally, one line, and
// lists on stderr the selected jobs that only CI's runners run.
//
// Exit codes: 0 the selection was made, 2 a usage, git or rules error.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/affected"
)

const (
	exitOK    = 0
	exitError = 2
)

// main delegates to run so that the command logic is testable.
func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	root, rules, event, base, head string
	githubOutput, summary          string
	makeTargets                    bool
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("affected", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.root, "root", ".", "repository root")
	fs.StringVar(&o.rules, "rules", "", "rules file (default <root>/ci/affected.json)")
	fs.StringVar(&o.event, "event", "pull_request", "GitHub event name")
	fs.StringVar(&o.base, "base", "", "the base revision of a pull request's change")
	fs.StringVar(&o.head, "head", "", "the head revision (default: the working tree)")
	fs.StringVar(&o.githubOutput, "github-output", "", "append the selection to this GitHub Actions output file")
	fs.StringVar(&o.summary, "summary", "", "append the reasons to this Markdown file")
	fs.BoolVar(&o.makeTargets, "make", false, "print the selected jobs' local make targets")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || (o.event == "pull_request" && o.base == "") {
		_, _ = fmt.Fprintln(stderr, "usage: affected [-event pull_request] -base <rev> [-head <rev>] [-github-output file] [-summary file] [-make]")
		return exitError
	}
	if o.rules == "" {
		o.rules = filepath.Join(o.root, "ci", "affected.json")
	}
	if err := selectJobs(ctx, o, stdout, stderr); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	return exitOK
}

func selectJobs(ctx context.Context, o options, stdout, stderr io.Writer) error {
	rules, err := affected.Load(o.root, o.rules)
	if err != nil {
		return fmt.Errorf("rules: %w", err)
	}
	change := affected.Change{Event: o.event}
	if o.event == "pull_request" {
		if change, err = readChange(ctx, o); err != nil {
			return err
		}
	}
	res := rules.Select(change)
	summary := res.Summary(rules.Always())
	if o.makeTargets {
		targets, ciOnly := makeTargets(rules, res)
		_, _ = fmt.Fprint(stderr, summary)
		if len(ciOnly) > 0 {
			_, _ = fmt.Fprintf(stderr, "\nOnly CI's runners run: %s.\n", strings.Join(ciOnly, ", "))
		}
		_, _ = fmt.Fprintln(stdout, strings.Join(targets, " "))
	} else {
		_, _ = fmt.Fprint(stdout, summary)
	}
	if o.githubOutput != "" {
		jobs, err := json.Marshal(res.Jobs)
		if err != nil {
			return fmt.Errorf("affected: %w", err)
		}
		if err := appendFile(o.githubOutput, "jobs="+string(jobs)+"\n"); err != nil {
			return err
		}
	}
	if o.summary != "" {
		return appendFile(o.summary, summary)
	}
	return nil
}

// makeTargets returns the make targets of the selected jobs, after the
// repository checks every change runs, and the selected jobs that have
// none.
func makeTargets(rules *affected.Rules, res affected.Result) (targets, ciOnly []string) {
	targets = []string{"repo-check"}
	for _, job := range rules.Jobs() {
		if !res.Jobs[job.Name] {
			continue
		}
		if len(job.Make) == 0 {
			ciOnly = append(ciOnly, job.Name)
		}
		for _, t := range job.Make {
			if !slices.Contains(targets, t) {
				targets = append(targets, t)
			}
		}
	}
	return targets, ciOnly
}

// readChange lists the changed files and reads ci.yml at both ends.
func readChange(ctx context.Context, o options) (affected.Change, error) {
	c := affected.Change{Event: o.event}
	diff := []string{"diff", "--name-only", "--no-renames", "-z", o.base}
	if o.head != "" {
		diff = append(diff, o.head)
	}
	out, err := git(ctx, o.root, diff...)
	if err != nil {
		return c, err
	}
	c.Files = splitZ(out)
	if o.head == "" {
		untracked, err := git(ctx, o.root, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return c, err
		}
		c.Files = append(c.Files, splitZ(untracked)...)
	}
	if c.BaseWorkflow, err = show(ctx, o.root, o.base); err != nil {
		return c, err
	}
	if o.head != "" {
		c.HeadWorkflow, err = show(ctx, o.root, o.head)
		return c, err
	}
	data, err := os.ReadFile(filepath.Join(o.root, filepath.FromSlash(affected.WorkflowPath)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("affected: %w", err)
	}
	c.HeadWorkflow = string(data)
	return c, nil
}

// show returns ci.yml at rev, or "" when it does not exist there.
func show(ctx context.Context, root, rev string) (string, error) {
	if _, err := git(ctx, root, "cat-file", "-e", rev+":"+affected.WorkflowPath); err != nil {
		return "", nil //nolint:nilerr // The file does not exist at rev.
	}
	out, err := git(ctx, root, "show", rev+":"+affected.WorkflowPath)
	return string(out), err
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...) //nolint:gosec // G204: git with arguments this command builds.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("affected: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func splitZ(out []byte) []string {
	var files []string
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: a file the caller names.
	if err != nil {
		return fmt.Errorf("affected: %w", err)
	}
	_, werr := f.WriteString(text)
	return errors.Join(werr, f.Close())
}
