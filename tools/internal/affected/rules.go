// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// ruleFile is the JSON form of ci/affected.json.
type ruleFile struct {
	Comment    []string           `json:"$comment"`
	Always     []string           `json:"always"`
	Everything []string           `json:"everything"`
	NoJob      []string           `json:"noJob"`
	Jobs       map[string]jobRule `json:"jobs"`
}

// jobRule is one selectable job's entry.
type jobRule struct {
	Description string   `json:"description"`
	CI          string   `json:"ci"`
	Paths       []string `json:"paths"`
	Go          []string `json:"go"`
	GoTest      []string `json:"goTest"`
	Dart        []string `json:"dart"`
	Exclude     []string `json:"exclude"`
	SkipOnPush  bool     `json:"skipOnPush"`
	Make        []string `json:"make"`
}

// Rules are the compiled selection rules. They are safe for concurrent
// use: nothing changes after Load.
type Rules struct {
	always     []string
	everything PathSet
	noJob      PathSet
	jobs       []*Job
}

// Job is one job the rules select or skip.
type Job struct {
	// Name is the key of the job's selection in the output.
	Name string
	// CI is the id of the job's block in ci.yml.
	CI string
	// SkipOnPush leaves the job out of the runs of pushes and merge
	// queues, which otherwise run every job: it runs on pull requests
	// that change its roots, and on scheduled and manual runs.
	SkipOnPush bool
	// Make lists the targets `make check-changed` runs for the job; none
	// for a job that needs CI's runners.
	Make    []string
	sources []source
	exclude PathSet
}

// source is one root of a job: what a changed file must be to affect it.
type source interface {
	contains(p string) bool
	String() string
}

// fileSource is a list of exact paths.
type fileSource []string

func (s fileSource) contains(p string) bool { return slices.Contains(s, p) }

func (s fileSource) String() string { return "the file list " + strings.Join(s, ", ") }

// globSource is a job's own path globs.
type globSource struct{ set PathSet }

func (s globSource) contains(p string) bool { return s.set.Contains(p) }

func (globSource) String() string { return "its paths" }

// Load reads the rules file at rulesPath and the Go and Dart graphs of
// the repository at root.
func Load(root, rulesPath string) (*Rules, error) {
	data, err := os.ReadFile(rulesPath) //nolint:gosec // G304: the rules file the caller names.
	if err != nil {
		return nil, fmt.Errorf("affected: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file ruleFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("affected: %s: %w", rulesPath, err)
	}
	r := &Rules{always: slices.Sorted(slices.Values(file.Always))}
	if r.everything, err = ParsePathSet(file.Everything); err != nil {
		return nil, err
	}
	if r.noJob, err = ParsePathSet(file.NoJob); err != nil {
		return nil, err
	}
	graphs := lazyGraphs{root: root}
	for _, name := range slices.Sorted(maps.Keys(file.Jobs)) {
		job, err := compileJob(name, file.Jobs[name], &graphs)
		if err != nil {
			return nil, err
		}
		r.jobs = append(r.jobs, job)
	}
	return r, nil
}

// lazyGraphs reads each graph once, when a job first needs it.
type lazyGraphs struct {
	root string
	goG  *goGraph
	dart *dartGraph
}

func (l *lazyGraphs) golang() (*goGraph, error) {
	if l.goG == nil {
		g, err := loadGoGraph(l.root)
		if err != nil {
			return nil, err
		}
		l.goG = g
	}
	return l.goG, nil
}

func (l *lazyGraphs) dartGraph() (*dartGraph, error) {
	if l.dart == nil {
		g, err := loadDartGraph(l.root)
		if err != nil {
			return nil, err
		}
		l.dart = g
	}
	return l.dart, nil
}

func compileJob(name string, rule jobRule, graphs *lazyGraphs) (*Job, error) {
	job := &Job{Name: name, CI: rule.CI, SkipOnPush: rule.SkipOnPush, Make: rule.Make}
	if job.CI == "" {
		job.CI = name
	}
	exclude, err := ParsePathSet(rule.Exclude)
	if err != nil {
		return nil, fmt.Errorf("affected: job %s: %w", name, err)
	}
	job.exclude = exclude
	if len(rule.Paths) > 0 {
		set, err := ParsePathSet(rule.Paths)
		if err != nil {
			return nil, fmt.Errorf("affected: job %s: %w", name, err)
		}
		job.sources = append(job.sources, globSource{set})
	}
	for _, g := range []struct {
		patterns []string
		tests    bool
	}{{rule.Go, false}, {rule.GoTest, true}} {
		if len(g.patterns) == 0 {
			continue
		}
		graph, err := graphs.golang()
		if err != nil {
			return nil, err
		}
		srcs, err := graph.sources(g.patterns, g.tests)
		if err != nil {
			return nil, fmt.Errorf("affected: job %s: %w", name, err)
		}
		job.sources = append(job.sources, srcs...)
	}
	for _, dir := range rule.Dart {
		graph, err := graphs.dartGraph()
		if err != nil {
			return nil, err
		}
		srcs, err := graph.sources(dir)
		if err != nil {
			return nil, fmt.Errorf("affected: job %s: %w", name, err)
		}
		job.sources = append(job.sources, srcs...)
	}
	if len(job.sources) == 0 {
		return nil, fmt.Errorf("affected: job %s has no roots", name)
	}
	return job, nil
}

// Jobs returns the selectable jobs, sorted by name.
func (r *Rules) Jobs() []*Job { return slices.Clone(r.jobs) }

// Always returns the jobs that run on every change, sorted.
func (r *Rules) Always() []string { return slices.Clone(r.always) }

// why returns the first of the job's roots that contains p, or nil
// when none does or the job excludes p.
func (j *Job) why(p string) source {
	if j.exclude.Contains(p) {
		return nil
	}
	for _, s := range j.sources {
		if s.contains(p) {
			return s
		}
	}
	return nil
}
