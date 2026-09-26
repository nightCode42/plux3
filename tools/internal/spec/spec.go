// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Status values allowed in the Status column (spec §4.3).
const (
	StatusSpec      = "SPEC"
	StatusWIP       = "WIP"
	StatusDone      = "DONE"
	StatusWithdrawn = "WITHDRAWN"
)

// Requirement is one normative requirement of the specification.
type Requirement struct {
	// ID is the stable identifier, e.g. "SYN-005".
	ID string
	// Area is the identifier prefix, e.g. "SYN".
	Area string
	// Phase is the phase tag, e.g. "P3".
	Phase string
	// Priority is MUST, SHOULD, MAY, or "—" for withdrawn requirements.
	Priority string
	// Text is the requirement statement (for table rows, every cell between
	// the priority and the status).
	Text string
	// Status is SPEC, WIP, DONE or WITHDRAWN. Forward-compatibility rules
	// written as block quotes carry no Status column and are reported as SPEC.
	Status string
	// Line is the 1-based line number in the document.
	Line int
}

// Document is a parsed specification.
type Document struct {
	// Requirements in document order, including duplicates, so that Lint can
	// report them.
	Requirements []Requirement
	// Areas are the identifier prefixes declared in the conventions table.
	Areas map[string]bool
	// References maps each back-quoted identifier to the lines citing it.
	References map[string][]int
	// Anchors are the heading slugs available as link targets.
	Anchors map[string]bool
	// Links maps each in-document link target to the lines using it.
	Links map[string][]int
}

var (
	idPattern    = `[A-Z][A-Z0-9]*-\d{3}`
	rowPattern   = regexp.MustCompile("^\\| `(" + idPattern + ")` \\| (P\\d+) \\| (MUST|SHOULD|MAY|—) \\| (.*) \\| (SPEC|WIP|DONE|WITHDRAWN) \\|$")
	quotePattern = regexp.MustCompile("^> \\*\\*(" + idPattern + ")\\*\\* `(P\\d+)` \\*\\*(MUST|SHOULD|MAY)\\*\\* — (.*)$")
	areaPattern  = regexp.MustCompile("^\\| `([A-Z][A-Z0-9]*)` \\| [^|]+ \\| §\\d+(, §\\d+)* \\|$")
	refPattern   = regexp.MustCompile("`(" + idPattern + ")`")
	linkPattern  = regexp.MustCompile(`\]\(#([^)]+)\)`)
	headPattern  = regexp.MustCompile(`^(#{1,6}) (.+)$`)
	fencePattern = regexp.MustCompile("^```")
)

// Parse reads a specification document.
func Parse(r io.Reader) (*Document, error) {
	doc := &Document{
		Areas:      map[string]bool{},
		References: map[string][]int{},
		Anchors:    map[string]bool{},
		Links:      map[string][]int{},
	}
	slugCounts := map[string]int{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inFence := false
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if fencePattern.MatchString(text) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		doc.parseLine(text, line, slugCounts)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("spec.Parse: %w", err)
	}
	return doc, nil
}

// parseLine extracts every kind of information one line can carry.
func (d *Document) parseLine(text string, line int, slugCounts map[string]int) {
	if m := rowPattern.FindStringSubmatch(text); m != nil {
		d.Requirements = append(d.Requirements, newRequirement(m[1], m[2], m[3], m[4], m[5], line))
	} else if m := quotePattern.FindStringSubmatch(text); m != nil {
		d.Requirements = append(d.Requirements, newRequirement(m[1], m[2], m[3], m[4], StatusSpec, line))
	}
	if m := areaPattern.FindStringSubmatch(text); m != nil {
		d.Areas[m[1]] = true
	}
	if m := headPattern.FindStringSubmatch(text); m != nil {
		slug := Slug(m[2])
		if n := slugCounts[slug]; n > 0 {
			d.Anchors[fmt.Sprintf("%s-%d", slug, n)] = true
		} else {
			d.Anchors[slug] = true
		}
		slugCounts[slug]++
	}
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		d.References[m[1]] = append(d.References[m[1]], line)
	}
	for _, m := range linkPattern.FindAllStringSubmatch(text, -1) {
		d.Links[m[1]] = append(d.Links[m[1]], line)
	}
}

// newRequirement builds a Requirement from parsed fields.
func newRequirement(id, phase, priority, text, status string, line int) Requirement {
	return Requirement{
		ID:       id,
		Area:     id[:strings.LastIndex(id, "-")],
		Phase:    phase,
		Priority: priority,
		Text:     strings.TrimSpace(text),
		Status:   status,
		Line:     line,
	}
}

// ByID returns the requirements indexed by identifier. When an identifier is
// duplicated, the first occurrence wins; Lint reports the duplicate.
func (d *Document) ByID() map[string]Requirement {
	out := make(map[string]Requirement, len(d.Requirements))
	for _, r := range d.Requirements {
		if _, ok := out[r.ID]; !ok {
			out[r.ID] = r
		}
	}
	return out
}
