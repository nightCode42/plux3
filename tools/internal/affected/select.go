// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Change is what the selection looks at.
type Change struct {
	// Event is the GitHub event name: pull_request selects by the
	// change; push and merge_group run every job but those marked
	// skipOnPush; any other event (schedule, workflow_dispatch) runs
	// every job.
	Event string
	// Files are the changed paths, slash-separated and relative to the
	// repository root.
	Files []string
	// BaseWorkflow and HeadWorkflow are ci.yml at the base and the head;
	// empty where the file does not exist.
	BaseWorkflow, HeadWorkflow string
}

// Result is the selection.
type Result struct {
	// Everything says why every job runs; empty when jobs were selected
	// one by one.
	Everything string
	// Jobs holds every job of the rules and whether it runs.
	Jobs map[string]bool
	// Reasons lists, for each job that runs, why: the files that
	// selected it and the root each one is in.
	Reasons map[string][]string
	// Skipped lists the changed files no job needs (noJob).
	Skipped []string
}

// Selected returns the names of the jobs that run, sorted.
func (res Result) Selected() []string {
	var names []string
	for name, on := range res.Jobs {
		if on {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// Select decides which jobs the change runs.
func (r *Rules) Select(c Change) Result {
	res := Result{Jobs: map[string]bool{}, Reasons: map[string][]string{}}
	switch c.Event {
	case "pull_request":
	case "push", "merge_group":
		return r.all(res, "a "+strings.ReplaceAll(c.Event, "_", " ")+" runs every job", true)
	default:
		return r.all(res, "a "+strings.ReplaceAll(c.Event, "_", " ")+" run runs every job", false)
	}
	files := slices.Sorted(slices.Values(c.Files))
	for _, f := range files {
		if r.everything.Contains(f) {
			return r.all(res, f+" changed, and it decides what every job does", false)
		}
	}
	if slices.Contains(files, WorkflowPath) {
		if why := r.selectWorkflow(res, c.BaseWorkflow, c.HeadWorkflow); why != "" {
			return r.all(res, why, false)
		}
	}
	skipped, unknown := r.selectFiles(res, files)
	if unknown != "" {
		return r.all(Result{Jobs: map[string]bool{}, Reasons: map[string][]string{}},
			"no rule in ci/affected.json names "+unknown, false)
	}
	res.Skipped = skipped
	for _, job := range r.jobs {
		res.Jobs[job.Name] = len(res.Reasons[job.Name]) > 0
	}
	return res
}

// selectWorkflow selects the jobs whose block of ci.yml changed, or says
// why every job must run.
func (r *Rules) selectWorkflow(res Result, base, head string) (everything string) {
	if base == "" || head == "" {
		return WorkflowPath + " was added or removed"
	}
	global, blocks := changedBlocks(base, head)
	if global {
		return WorkflowPath + " changed outside the job blocks (triggers, env, defaults)"
	}
	for _, job := range r.jobs {
		if slices.Contains(blocks, job.CI) {
			res.add(job.Name, "its block of "+WorkflowPath+" changed")
		}
	}
	return ""
}

// selectFiles selects the jobs whose roots contain a changed file. It
// returns the files no job needs, and the first file that is in no root
// and not in noJob.
func (r *Rules) selectFiles(res Result, files []string) (skipped []string, unknown string) {
	for _, f := range files {
		matched := f == WorkflowPath
		for _, job := range r.jobs {
			if s := job.why(f); s != nil {
				res.add(job.Name, f+" ("+s.String()+")")
				matched = true
			}
		}
		if matched {
			continue
		}
		if !r.noJob.Contains(f) {
			return nil, f
		}
		skipped = append(skipped, f)
	}
	return skipped, ""
}

// all selects every job, except on pushes those marked skipOnPush.
func (r *Rules) all(res Result, why string, push bool) Result {
	res.Everything = why
	for _, job := range r.jobs {
		res.Jobs[job.Name] = !push || !job.SkipOnPush
	}
	return res
}

func (res Result) add(job, why string) {
	if !slices.Contains(res.Reasons[job], why) {
		res.Reasons[job] = append(res.Reasons[job], why)
	}
}

// Summary writes the selection as Markdown for a job summary or a
// terminal: every job that runs with why, then the jobs that do not.
func (res Result) Summary(always []string) string {
	var b strings.Builder
	b.WriteString("### Affected jobs\n\n")
	if res.Everything != "" {
		fmt.Fprintf(&b, "Every job runs: %s.\n\n", res.Everything)
	}
	fmt.Fprintf(&b, "Always: %s.\n\n", strings.Join(always, ", "))
	const shown = 5
	var skipped []string
	for _, name := range slices.Sorted(maps.Keys(res.Jobs)) {
		if !res.Jobs[name] {
			skipped = append(skipped, name)
			continue
		}
		reasons := res.Reasons[name]
		if len(reasons) == 0 {
			fmt.Fprintf(&b, "- **%s**\n", name)
			continue
		}
		more := ""
		if len(reasons) > shown {
			more = fmt.Sprintf("; and %d more", len(reasons)-shown)
			reasons = reasons[:shown]
		}
		fmt.Fprintf(&b, "- **%s**: %s%s\n", name, strings.Join(reasons, "; "), more)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\nNot affected: %s.\n", strings.Join(skipped, ", "))
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(&b, "\nChanged files no job needs: %s.\n", strings.Join(res.Skipped, ", "))
	}
	return b.String()
}
