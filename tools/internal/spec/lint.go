// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Problem is one consistency defect found by Lint.
type Problem struct {
	// Line is the 1-based line number the problem refers to.
	Line int
	// Message describes the defect.
	Message string
}

// String formats the problem as "line N: message".
func (p Problem) String() string {
	return fmt.Sprintf("line %d: %s", p.Line, p.Message)
}

// phasePattern accepts the phase tags P0 to P15 (spec §5).
var phasePattern = regexp.MustCompile(`^P(\d|1[0-5])$`)

// Lint checks the document's internal consistency and returns every problem
// found, ordered by line.
func (d *Document) Lint() []Problem {
	var problems []Problem
	problems = append(problems, d.lintRequirements()...)
	problems = append(problems, d.lintReferences()...)
	problems = append(problems, d.lintLinks()...)
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Line < problems[j].Line })
	return problems
}

// lintRequirements checks identifiers, areas, phases and withdrawn rows.
func (d *Document) lintRequirements() []Problem {
	var problems []Problem
	seen := map[string]int{}
	for _, r := range d.Requirements {
		if first, ok := seen[r.ID]; ok {
			problems = append(problems, Problem{r.Line, fmt.Sprintf("%s duplicates the identifier defined on line %d", r.ID, first)})
		} else {
			seen[r.ID] = r.Line
		}
		if !d.Areas[r.Area] {
			problems = append(problems, Problem{r.Line, fmt.Sprintf("%s uses area %q, which is not declared in the conventions table", r.ID, r.Area)})
		}
		if !phasePattern.MatchString(r.Phase) {
			problems = append(problems, Problem{r.Line, fmt.Sprintf("%s has phase %s outside P0–P15", r.ID, r.Phase)})
		}
		problems = append(problems, lintWithdrawal(r)...)
	}
	return problems
}

// lintWithdrawal checks that withdrawn requirements keep their row with a
// rationale and no priority, and that only withdrawn rows lack a priority
// (QA-073).
func lintWithdrawal(r Requirement) []Problem {
	withdrawn := r.Status == StatusWithdrawn
	switch {
	case withdrawn && r.Priority != "—":
		return []Problem{{r.Line, r.ID + " is WITHDRAWN but still has priority " + r.Priority}}
	case withdrawn && !strings.HasPrefix(r.Text, "Withdrawn:"):
		return []Problem{{r.Line, r.ID + ` is WITHDRAWN without a "Withdrawn: <rationale>" statement`}}
	case !withdrawn && r.Priority == "—":
		return []Problem{{r.Line, r.ID + " has no priority but is not WITHDRAWN"}}
	}
	return nil
}

// lintReferences checks that every cited identifier is defined.
func (d *Document) lintReferences() []Problem {
	defined := d.ByID()
	var problems []Problem
	for id, lines := range d.References {
		if _, ok := defined[id]; ok {
			continue
		}
		for _, line := range lines {
			problems = append(problems, Problem{line, fmt.Sprintf("reference to undefined requirement %s", id)})
		}
	}
	return problems
}

// lintLinks checks that every in-document link points at a heading.
func (d *Document) lintLinks() []Problem {
	var problems []Problem
	for anchor, lines := range d.Links {
		if d.Anchors[anchor] {
			continue
		}
		for _, line := range lines {
			problems = append(problems, Problem{line, fmt.Sprintf("link to missing heading #%s", anchor)})
		}
	}
	return problems
}
