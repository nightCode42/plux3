// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// LimitsSource is the registry file, relative to the repository root.
const LimitsSource = "schema/limits.json"

// Limit is one entry of the limits registry (LIM-001).
type Limit struct {
	Key          string   `json:"key"`
	Unit         string   `json:"unit"`
	Default      int64    `json:"default"`
	Warning      int64    `json:"warning,omitempty"`
	Max          int64    `json:"max"`
	Scopes       []string `json:"scopes"`
	EnforcedBy   []string `json:"enforcedBy"`
	Phase        string   `json:"phase"`
	Requirements []string `json:"requirements"`
	Description  string   `json:"description"`
}

// limitsFile is the shape of schema/limits.json.
type limitsFile struct {
	Schema  string  `json:"$schema"`
	Comment string  `json:"$comment"`
	Limits  []Limit `json:"limits"`
}

// Allowed values of the registry's enumerations, in canonical order.
var (
	limitUnits    = []string{"bytes", "count", "microseconds", "milliseconds", "operations", "codepoints"}
	limitScopes   = []string{"installation", "organization", "app", "plugin"}
	limitEnforcer = []string{"compiler", "server", "runtime", "studio"}
	limitKey      = regexp.MustCompile(`^[a-z][a-zA-Z]*\.[a-z][a-zA-Z]*$`)
	requirementID = regexp.MustCompile(`^[A-Z0-9]+-[0-9]{3}$`)
	phaseTag      = regexp.MustCompile(`^P([0-9]|1[0-5])$`)
)

// LoadLimits reads and validates the limits registry.
func LoadLimits(path string) ([]Limit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("codegen.LoadLimits: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f limitsFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("codegen.LoadLimits %s: %w", path, err)
	}
	if len(f.Limits) == 0 {
		return nil, fmt.Errorf("codegen.LoadLimits %s: no limits", path)
	}
	for i, l := range f.Limits {
		if i > 0 && f.Limits[i-1].Key >= l.Key {
			return nil, fmt.Errorf("codegen.LoadLimits: %s: keys must be unique and sorted", l.Key)
		}
		if err := l.validate(); err != nil {
			return nil, fmt.Errorf("codegen.LoadLimits: %s: %w", l.Key, err)
		}
	}
	return f.Limits, nil
}

// validate checks one entry.
func (l Limit) validate() error {
	switch {
	case !limitKey.MatchString(l.Key):
		return fmt.Errorf("key must be area.lowerCamelName")
	case !slices.Contains(limitUnits, l.Unit):
		return fmt.Errorf("unknown unit %q", l.Unit)
	case l.Default < 1 || l.Default > l.Max:
		return fmt.Errorf("default %d must be in [1, max %d]", l.Default, l.Max)
	case l.Warning < 0 || l.Warning >= l.Default && l.Warning != 0:
		return fmt.Errorf("warning %d must be below the default %d", l.Warning, l.Default)
	case !phaseTag.MatchString(l.Phase):
		return fmt.Errorf("invalid phase %q", l.Phase)
	case strings.TrimSpace(l.Description) == "" || !strings.HasSuffix(l.Description, "."):
		return fmt.Errorf("description must be a sentence")
	case len(l.Requirements) == 0:
		return fmt.Errorf("no requirement IDs")
	}
	if err := subsetInOrder("scope", l.Scopes, limitScopes); err != nil {
		return err
	}
	if err := subsetInOrder("enforcer", l.EnforcedBy, limitEnforcer); err != nil {
		return err
	}
	for _, id := range l.Requirements {
		if !requirementID.MatchString(id) {
			return fmt.Errorf("invalid requirement ID %q", id)
		}
	}
	return nil
}

// subsetInOrder checks that values is a non-empty, duplicate-free subset of
// allowed, listed in allowed's order, so the registry has one spelling.
func subsetInOrder(what string, values, allowed []string) error {
	if len(values) == 0 {
		return fmt.Errorf("no %ss", what)
	}
	last := -1
	for _, v := range values {
		i := slices.Index(allowed, v)
		if i < 0 {
			return fmt.Errorf("unknown %s %q", what, v)
		}
		if i <= last {
			return fmt.Errorf("%ss must be unique and in the order %v", what, allowed)
		}
		last = i
	}
	return nil
}

// LimitsFiles renders the registry for Go, Dart, TypeScript and the
// reference documentation.
func LimitsFiles(limits []Limit) ([]File, error) {
	goSrc, err := goFile("backend/internal/schema/limits/limits_gen.go", limitsGo(limits))
	if err != nil {
		return nil, err
	}
	return []File{
		goSrc,
		{Path: "packages/plux_flutter/lib/src/schema/limits.g.dart", Content: limitsDart(limits)},
		{Path: "studio/packages/schema/src/limits.gen.ts", Content: limitsTS(limits)},
		{Path: "docs/reference/limits.md", Content: limitsMarkdown(limits)},
	}, nil
}

// limitsGo renders the Go registry table.
func limitsGo(limits []Limit) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangGo, LimitsSource))
	b.WriteString("package limits\n\n// Keys of the limits registry.\nconst (\n")
	for _, l := range limits {
		b.WriteString(wrapComment("\t// ", fmt.Sprintf("%s: %s (%s)", GoName(l.Key), l.Description, strings.Join(l.Requirements, ", ")), 78))
		fmt.Fprintf(&b, "\t%s Key = %q\n", GoName(l.Key), l.Key)
	}
	b.WriteString(")\n\n// registry holds every definition in key order. It is read-only.\nvar registry = [...]Definition{\n")
	for _, l := range limits {
		scopes := make([]string, len(l.Scopes))
		for i, s := range l.Scopes {
			scopes[i] = "Scope" + GoName(s)
		}
		enforcers := make([]string, len(l.EnforcedBy))
		for i, e := range l.EnforcedBy {
			enforcers[i] = "Enforcer" + GoName(e)
		}
		fmt.Fprintf(&b, "\t{Key: %s, Unit: Unit%s, Default: %d, Warning: %d, Max: %d, Scopes: %s, EnforcedBy: %s, Phase: %q, Description: %q},\n",
			GoName(l.Key), GoName(l.Unit), l.Default, l.Warning, l.Max, strings.Join(scopes, "|"), strings.Join(enforcers, "|"), l.Phase, l.Description)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// limitsDart renders the Dart registry as an enhanced enum.
func limitsDart(limits []Limit) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangDart, LimitsSource))
	b.WriteString("/// The limits registry (LIM-001, spec §30.4).\nlibrary;\n\n")
	b.WriteString("/// Units in which limits are expressed.\nenum PluxLimitUnit {\n")
	for _, u := range limitUnits {
		fmt.Fprintf(&b, "  /// Measured in %s.\n  %s,\n", u, LowerCamel(u))
	}
	b.WriteString("}\n\n/// Every limit, with its installation default and hard maximum.\nenum PluxLimit {\n")
	for i, l := range limits {
		b.WriteString(wrapComment("  /// ", l.Description, 80))
		sep := ","
		if i == len(limits)-1 {
			sep = ";"
		}
		fmt.Fprintf(&b, "  %s(%s, PluxLimitUnit.%s, %d, %d, %d)%s\n", LowerCamel(l.Key), quoteDart(l.Key), LowerCamel(l.Unit), l.Default, l.Warning, l.Max, sep)
	}
	b.WriteString(`
  const PluxLimit(this.key, this.unit, this.defaultValue, this.warning, this.max);

  /// The stable registry key.
  final String key;

  /// The unit of [defaultValue], [warning] and [max].
  final PluxLimitUnit unit;

  /// The installation default.
  final int defaultValue;

  /// The explicit warning threshold, or zero for 80% of the limit (LIM-003).
  final int warning;

  /// The hard maximum that no scope can exceed.
  final int max;
}
`)
	return b.Bytes()
}

// limitsTS renders the TypeScript registry.
func limitsTS(limits []Limit) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangTS, LimitsSource))
	b.WriteString("/** Units in which limits are expressed. */\nexport type LimitUnit = " + tsUnion(limitUnits) + ";\n\n")
	b.WriteString("/** Levels at which a limit can be set; lower levels only tighten (LIM-002). */\nexport type LimitScope = " + tsUnion(limitScopes) + ";\n\n")
	b.WriteString("/** One entry of the limits registry (LIM-001). */\nexport interface LimitDefinition {\n  readonly key: string;\n  readonly unit: LimitUnit;\n  readonly default: number;\n  readonly warning: number;\n  readonly max: number;\n  readonly scopes: readonly LimitScope[];\n  readonly phase: string;\n  readonly description: string;\n}\n\n")
	b.WriteString("/** The limits registry, in key order. */\nexport const limits = [\n")
	for _, l := range limits {
		scopes := make([]string, len(l.Scopes))
		for i, s := range l.Scopes {
			scopes[i] = fmt.Sprintf("%q", s)
		}
		fmt.Fprintf(&b, "  {\n    key: %q,\n    unit: %q,\n    default: %d,\n    warning: %d,\n    max: %d,\n    scopes: [%s],\n    phase: %q,\n    description: %q,\n  },\n",
			l.Key, l.Unit, l.Default, l.Warning, l.Max, strings.Join(scopes, ", "), l.Phase, l.Description)
	}
	b.WriteString("] as const satisfies readonly LimitDefinition[];\n\n/** A key of the limits registry. */\nexport type LimitKey = (typeof limits)[number][\"key\"];\n")
	return b.Bytes()
}

// tsUnion renders a union of string literal types.
func tsUnion(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, " | ")
}

// limitsMarkdown renders docs/reference/limits.md.
func limitsMarkdown(limits []Limit) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangMarkdown, LimitsSource))
	b.WriteString("# Limits Reference\n\nEvery size and count in Plux is governed by one registry, `schema/limits.json` (`LIM-001`, spec §30.4). ")
	b.WriteString("Limits are set per installation, organisation, app and plugin, and a lower level can only tighten a limit set above it (`LIM-002`). ")
	b.WriteString("Publication fails when a limit is exceeded and warns at the warning threshold — by default 80% of the limit (`LIM-003`).\n\n")
	b.WriteString("| Key | Unit | Default | Warning | Maximum | Scopes | Phase | Requirements | Description |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, l := range limits {
		warning := "80%"
		if l.Warning > 0 {
			warning = fmt.Sprint(l.Warning)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d | %s | %d | %s | %s | %s | %s |\n",
			l.Key, l.Unit, l.Default, warning, l.Max, strings.Join(l.Scopes, ", "), l.Phase, strings.Join(l.Requirements, ", "), l.Description)
	}
	return b.Bytes()
}
