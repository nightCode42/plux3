// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"fmt"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Migration upgrades a parsed document from one released schema version to
// the next (SCH-000). It must be deterministic and must accept every valid
// document of From (SCH-043).
type Migration struct {
	// From and To are consecutive released versions.
	From, To string
	// Apply rewrites the document in place; it never changes schemaVersion,
	// which the Migrator sets.
	Apply func(doc map[string]any) error
}

// Migrator applies migrations, forward only, up to the current version.
type Migrator struct {
	current string
	steps   []Migration
}

// NewMigrator returns a migrator for a chain of consecutive steps ending at
// current. It rejects chains with gaps or cycles.
func NewMigrator(current string, steps []Migration) (*Migrator, error) {
	seen := map[string]bool{current: true}
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		want := current
		if i < len(steps)-1 {
			want = steps[i+1].From
		}
		if s.To != want || seen[s.From] || s.Apply == nil {
			return nil, fmt.Errorf("schema.NewMigrator: step %s → %s does not continue the chain to %s", s.From, s.To, current)
		}
		seen[s.From] = true
	}
	return &Migrator{current: current, steps: append([]Migration(nil), steps...)}, nil
}

// DefaultMigrator returns the migrations of released schema versions.
// Version 1.0.0 is the first released version, so the chain is empty.
func DefaultMigrator() *Migrator {
	return &Migrator{current: CurrentVersion}
}

// Released returns every schema version the migrator accepts, oldest first.
func (m *Migrator) Released() []string {
	out := make([]string, 0, len(m.steps)+1)
	for _, s := range m.steps {
		out = append(out, s.From)
	}
	return append(out, m.current)
}

// Migrate upgrades doc to the current version and returns the diagnostic of
// a failure: UNSUPPORTED_SCHEMA_VERSION for a version outside the chain,
// MIGRATION_FAILED when a step fails. Documents are never reinterpreted
// silently: an unknown version is an error, not a guess.
func (m *Migrator) Migrate(doc map[string]any, file string) *plxerr.Diagnostic {
	loc := plxerr.Location{File: file, Path: "/schemaVersion"}
	version, ok := doc["schemaVersion"].(string)
	if !ok {
		d := plxerr.NewDiagnostic(plxerr.MissingProperty, plxerr.Location{File: file}, "missing required property %q", "schemaVersion")
		return &d
	}
	start := len(m.steps)
	for i, s := range m.steps {
		if s.From == version {
			start = i
			break
		}
	}
	if start == len(m.steps) && version != m.current {
		d := plxerr.NewDiagnostic(plxerr.UnsupportedSchemaVersion, loc, "schema version %q is not one of the released versions %v", version, m.Released())
		return &d
	}
	for _, s := range m.steps[start:] {
		if err := s.Apply(doc); err != nil {
			d := plxerr.NewDiagnostic(plxerr.MigrationFailed, loc, "migration %s → %s: %v", s.From, s.To, err)
			return &d
		}
		doc["schemaVersion"] = s.To
	}
	return nil
}
