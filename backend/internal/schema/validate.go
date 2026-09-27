// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Validator checks documents against the embedded JSON Schemas. It is
// immutable after NewValidator and safe for concurrent use.
type Validator struct {
	schemas map[DocumentKind]*jsonschema.Schema
}

// NewValidator compiles the embedded schemas.
func NewValidator() (*Validator, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	entries, err := fs.ReadDir(schemaFiles, "schemas")
	if err != nil {
		return nil, fmt.Errorf("schema.NewValidator: %w", err)
	}
	for _, e := range entries {
		data, err := schemaFiles.ReadFile("schemas/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("schema.NewValidator: %w", err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("schema.NewValidator: %s: %w", e.Name(), err)
		}
		if err := c.AddResource(schemaBase+e.Name(), doc); err != nil {
			return nil, fmt.Errorf("schema.NewValidator: %s: %w", e.Name(), err)
		}
	}
	v := &Validator{schemas: map[DocumentKind]*jsonschema.Schema{}}
	for _, d := range documentSchemas {
		sch, err := c.Compile(schemaBase + d.File)
		if err != nil {
			return nil, fmt.Errorf("schema.NewValidator: %s: %w", d.File, err)
		}
		v.schemas[d.Kind] = sch
	}
	return v, nil
}

// Kinds reports whether k is a known document kind.
func (v *Validator) Kinds(k DocumentKind) bool {
	_, ok := v.schemas[k]
	return ok
}

// Validate checks a parsed document tree (as returned by jcs.Parse) against
// the schema of its kind and returns the diagnostics, located in file.
func (v *Validator) Validate(k DocumentKind, tree any, file string) plxerr.Diagnostics {
	sch, ok := v.schemas[k]
	if !ok {
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InvalidEnumValue, plxerr.Location{File: file, Path: "/kind"}, "unknown document kind %q", k)}
	}
	err := sch.Validate(tree)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{File: file}, "validation failed: %v", err)}
	}
	c := collector{file: file}
	c.walk(verr, nil)
	c.out.Sort()
	return c.out
}

// printer formats library messages in English.
var printer = message.NewPrinter(language.English)

// collector turns a validation error tree into diagnostics.
type collector struct {
	file string
	out  plxerr.Diagnostics
}

// add appends a diagnostic at an instance location.
func (c *collector) add(code plxerr.Code, loc []string, format string, args ...any) {
	c.out = append(c.out, plxerr.NewDiagnostic(code, plxerr.Location{File: c.file, Path: plxerr.Pointer(loc...)}, format, args...))
}

// walk reports the leaves of the error tree, choosing the most specific
// branch of oneOf and anyOf failures so authors see the real problem.
// parent is the instance location of the enclosing error.
func (c *collector) walk(e *jsonschema.ValidationError, parent []string) {
	switch k := e.ErrorKind.(type) {
	case *kind.OneOf:
		if len(k.Subschemas) > 0 {
			c.add(plxerr.InvalidStructure, e.InstanceLocation, "value matches more than one of the allowed forms (%s)", keywordLocation(e))
			return
		}
		c.walkBranches(e)
	case *kind.AnyOf:
		c.walkBranches(e)
	case *kind.Group, *kind.Reference, *kind.AllOf, *kind.Schema:
		for _, cause := range e.Causes {
			c.walk(cause, e.InstanceLocation)
		}
	case *kind.PropertyNames:
		// jsonschema v6.0.3 stores an aliased, later overwritten location for
		// propertyNames errors; the enclosing object's location is reliable.
		c.add(plxerr.InvalidFormat, append(parent[:len(parent):len(parent)], k.Property), "property name %q is not allowed here", k.Property)
	default:
		c.leaf(e)
	}
}

// walkBranches descends into the best branch of a failed union, or reports
// the union itself when no branch stands out.
func (c *collector) walkBranches(e *jsonschema.ValidationError) {
	if best := bestBranch(e); best != nil {
		c.walk(best, e.InstanceLocation)
		return
	}
	c.add(plxerr.InvalidStructure, e.InstanceLocation, "value matches none of the allowed forms (%s)", keywordLocation(e))
}

// bestBranch returns the branch whose failure is most specific: branches
// that fail on the JSON type of the value itself are discarded, and of the
// rest the one whose errors lie deepest wins. Ties return nil.
func bestBranch(e *jsonschema.ValidationError) *jsonschema.ValidationError {
	var best *jsonschema.ValidationError
	bestDepth, tie := -1, false
	for _, branch := range e.Causes {
		if wrongTypeAt(branch, len(e.InstanceLocation)) {
			continue
		}
		d := maxDepth(branch)
		switch {
		case d > bestDepth:
			best, bestDepth, tie = branch, d, false
		case d == bestDepth:
			tie = true
		}
	}
	if tie {
		return nil
	}
	return best
}

// wrongTypeAt reports whether a branch fails because the value at depth has
// the wrong JSON type or lacks the shape that selects the branch: a type,
// constant, required-property or property-name failure on the value itself.
// Nested unions are not searched; they are judged when walked.
func wrongTypeAt(e *jsonschema.ValidationError, depth int) bool {
	switch e.ErrorKind.(type) {
	case *kind.OneOf, *kind.AnyOf:
		return false
	case *kind.Type, *kind.Const, *kind.Required, *kind.PropertyNames:
		if len(e.InstanceLocation) == depth {
			return true
		}
	}
	for _, cause := range e.Causes {
		if wrongTypeAt(cause, depth) {
			return true
		}
	}
	return false
}

// maxDepth returns the deepest instance location in an error tree.
func maxDepth(e *jsonschema.ValidationError) int {
	d := len(e.InstanceLocation)
	for _, cause := range e.Causes {
		d = max(d, maxDepth(cause))
	}
	return d
}

// leaf maps one elementary error to a diagnostic.
func (c *collector) leaf(e *jsonschema.ValidationError) {
	loc := e.InstanceLocation
	switch k := e.ErrorKind.(type) {
	case *kind.FalseSchema:
		c.add(plxerr.UnknownProperty, loc, "unknown property %q", last(loc))
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			c.add(plxerr.UnknownProperty, append(loc[:len(loc):len(loc)], p), "unknown property %q", p)
		}
	case *kind.Required:
		for _, p := range k.Missing {
			c.add(plxerr.MissingProperty, loc, "missing required property %q", p)
		}
	case *kind.Type:
		c.add(plxerr.WrongJSONType, loc, "expected %s, got %s", strings.Join(k.Want, " or "), k.Got)
	case *kind.Enum, *kind.Const:
		c.add(plxerr.InvalidEnumValue, loc, "%s", e.ErrorKind.LocalizedString(printer))
	case *kind.Pattern:
		c.add(plxerr.InvalidFormat, loc, "%q is not a valid %s", k.Got, formatName(e.SchemaURL))
	case *kind.MinLength, *kind.MaxLength, *kind.Minimum, *kind.Maximum, *kind.MinItems, *kind.MaxItems,
		*kind.UniqueItems, *kind.MinProperties, *kind.MaxProperties, *kind.ExclusiveMinimum, *kind.ExclusiveMaximum:
		c.add(plxerr.OutOfRange, loc, "%s", e.ErrorKind.LocalizedString(printer))
	default:
		c.add(plxerr.InvalidStructure, loc, "%s (%s)", e.ErrorKind.LocalizedString(printer), keywordLocation(e))
	}
}

// formatNames describes the string formats of common.schema.json.
var formatNames = map[string]string{
	"id":          "identifier (lower-case UUIDv7)",
	"key":         "key (lower-kebab slug)",
	"name":        "name (lowerCamelCase identifier)",
	"typeName":    "type name (UpperCamelCase)",
	"routeName":   "route name (lower-kebab slug)",
	"locale":      "locale (BCP 47 tag such as de-DE)",
	"semver":      "semantic version (major.minor.patch)",
	"typeExpr":    "type expression",
	"tokenPath":   "design token path (e.g. color.primary)",
	"concurrency": "concurrency policy (parallel, drop, restart, queue, debounce:<ms> or throttle:<ms>)",
}

// formatName names the format a pattern belongs to, from the location of
// the failing keyword.
func formatName(schemaURL string) string {
	_, frag, _ := strings.Cut(schemaURL, "#")
	parts := strings.Split(strings.TrimPrefix(frag, "/"), "/")
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i-1] == "$defs" {
			if n, ok := formatNames[parts[i]]; ok {
				return n
			}
			return "value for " + parts[i]
		}
	}
	return "value"
}

// keywordLocation returns the schema location of an error without the base URI.
func keywordLocation(e *jsonschema.ValidationError) string {
	return path.Base(strings.TrimPrefix(e.SchemaURL, schemaBase))
}

// last returns the final token of a location, or "" for the root.
func last(loc []string) string {
	if len(loc) == 0 {
		return ""
	}
	return loc[len(loc)-1]
}
