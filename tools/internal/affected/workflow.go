// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package affected

import (
	"regexp"
	"strings"
)

// WorkflowPath is the workflow whose job blocks the selection compares.
const WorkflowPath = ".github/workflows/ci.yml"

// jobHeader is a job's first line: its id, indented by two spaces under
// the top-level jobs key.
var jobHeader = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*(#.*)?$`)

// workflowBlocks splits a workflow into the text outside its jobs (the
// triggers, env, defaults, and the jobs key itself) and the text of each
// job. Blank lines and comments in the jobs section belong to the job
// that follows them, so the comment above a job is part of its block.
// The file is read by indentation, as the repository writes it; no YAML
// library is needed.
func workflowBlocks(text string) (global string, jobs map[string]string) {
	jobs = map[string]string{}
	var outside, pending strings.Builder
	current := ""
	inJobs := false
	for line := range strings.Lines(text) {
		bare := strings.TrimRight(line, "\r\n")
		switch {
		case !inJobs:
			outside.WriteString(line)
			inJobs = strings.HasPrefix(bare, "jobs:") && strings.TrimSpace(strings.SplitN(bare, "#", 2)[0]) == "jobs:"
		case strings.TrimSpace(bare) == "" || strings.HasPrefix(strings.TrimSpace(bare), "#") && indent(bare) <= 2:
			pending.WriteString(line)
		case jobHeader.MatchString(bare):
			current = jobHeader.FindStringSubmatch(bare)[1]
			jobs[current] += pending.String() + line
			pending.Reset()
		case indent(bare) == 0:
			// A top-level key after the jobs: outside again.
			outside.WriteString(pending.String() + line)
			pending.Reset()
			current, inJobs = "", false
		default:
			if current == "" {
				outside.WriteString(pending.String() + line)
			} else {
				jobs[current] += pending.String() + line
			}
			pending.Reset()
		}
	}
	if current != "" {
		jobs[current] += pending.String()
	} else {
		outside.WriteString(pending.String())
	}
	return outside.String(), jobs
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// changedBlocks compares a workflow at the base and at the head: whether
// anything outside the job blocks changed, and which jobs were added or
// changed.
func changedBlocks(base, head string) (globalChanged bool, jobs []string) {
	baseGlobal, baseJobs := workflowBlocks(base)
	headGlobal, headJobs := workflowBlocks(head)
	for id, text := range headJobs {
		if baseJobs[id] != text {
			jobs = append(jobs, id)
		}
	}
	return baseGlobal != headGlobal, jobs
}
