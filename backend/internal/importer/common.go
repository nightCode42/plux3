// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package importer turns OpenAPI documents and GraphQL schemas with their
// operations into Plux data-source documents (DAT-002) and serves an OpenAPI
// document as a mock API (TST-004).
//
// The package is a pure library: it reads bytes and returns values, never
// touching the network, the file system, the clock or a random source, so the
// same input always yields the same bytes. It is used by the CLI only.
package importer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
)

// Result is what an import produced: a document fragment and the
// diagnostics found while reading the source.
type Result struct {
	// Fragment holds the keys `dataSources`, `types` and `variables` of an
	// app or plugin document, plus `baseUrls`, the base URL each variable
	// should take in an environment, when the source names one.
	Fragment map[string]any
	// Diagnostics lists constructs that were left out and problems found.
	Diagnostics plxerr.Diagnostics
}

// JSON returns the fragment as canonical JSON with two-space indentation and
// a final newline, so importing an unchanged source changes no byte.
func (r Result) JSON() ([]byte, error) {
	raw, err := jcs.Marshal(r.Fragment)
	if err != nil {
		return nil, fmt.Errorf("importer: %w", err)
	}
	return jcs.Format(raw, int(limits.Defaults().Get(limits.DocumentJSONDepth)))
}

const maxNameLength = 64

// words splits s at every character that is not an ASCII letter or digit.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)))
	})
}

// upperCamel joins the words of s, each starting with a capital letter.
func upperCamel(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(w[1:])
	}
	out := b.String()
	if out == "" {
		return "X"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "X" + out
	}
	if len(out) > maxNameLength {
		out = out[:maxNameLength]
	}
	return out
}

// lowerCamel is upperCamel with a lower-case first letter.
func lowerCamel(s string) string {
	u := upperCamel(s)
	return strings.ToLower(u[:1]) + u[1:]
}

// identifier derives a deterministic UUIDv7-shaped identifier from a
// namespace and a name. Identifiers must be stable across imports, so they
// come from a hash instead of a clock and random bytes; the version and
// variant bits are set so that they satisfy the identifier pattern (SCH-002).
func identifier(namespace, name string) string {
	sum := sha256.Sum256([]byte("plux-import\x00" + namespace + "\x00" + name))
	var u uuid7.UUID
	copy(u[:], sum[:16])
	u[6] = 0x70 | u[6]&0x0f
	u[8] = 0x80 | u[8]&0x3f
	return u.String()
}

// unsupported is a construct without a Plux type.
type unsupported struct {
	ptr string
	msg string
}

func (e *unsupported) Error() string { return e.msg }

func unsupportedAt(ptr, format string, args ...any) *unsupported {
	return &unsupported{ptr: ptr, msg: fmt.Sprintf(format, args...)}
}

// typeSet collects the named types of an import.
type typeSet struct {
	decls map[string]map[string]any
}

func newTypeSet() *typeSet { return &typeSet{decls: map[string]map[string]any{}} }

// merge adds decls, failing when a name is already taken by a different
// declaration.
func (t *typeSet) merge(decls map[string]map[string]any) error {
	for _, name := range sortedKeys(decls) {
		if old, ok := t.decls[name]; ok && !reflect.DeepEqual(old, decls[name]) {
			return fmt.Errorf("type name %s is already used by a different type", name)
		}
	}
	for name, d := range decls {
		t.decls[name] = d
	}
	return nil
}

// list returns the declarations ordered by name.
func (t *typeSet) list() []any {
	out := make([]any, 0, len(t.decls))
	for _, name := range sortedKeys(t.decls) {
		out = append(out, t.decls[name])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tree converts any JSON-marshalable value to the tree jcs understands.
func tree(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jcs.Parse(raw, int(limits.Defaults().Get(limits.DocumentJSONDepth)))
}

// skeleton is the smallest app document that the schema accepts; the
// fragment is validated inside it.
func skeleton() map[string]any {
	return map[string]any{
		"schemaVersion":     "1.0.0",
		"kind":              "app",
		"id":                identifier("skeleton", "app"),
		"key":               "import-check",
		"name":              "Import check",
		"icon":              map[string]any{"monogram": map[string]any{"background": "#0B6E4F", "text": "IC"}},
		"defaultLocale":     "en",
		"supportedLocales":  []any{"en"},
		"theme":             identifier("skeleton", "theme"),
		"entryRoute":        "home",
		"plugins":           []any{"home"},
		"environments":      []any{map[string]any{"key": "production", "name": "Production"}},
		"securityProfile":   "standard",
		"minRuntimeVersion": "0.2.0",
		"sync": map[string]any{
			"activation": "atSafePoint",
			"startup":    map[string]any{"mode": "useCacheThenSync"},
		},
	}
}

// validate checks the schema-bound keys of a fragment by placing them in an
// app document, and returns the diagnostics located in file.
func validate(frag map[string]any, file string) plxerr.Diagnostics {
	internal := func(err error) plxerr.Diagnostics {
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{File: file}, "%v", err)}
	}
	v, err := schema.NewValidator()
	if err != nil {
		return internal(err)
	}
	doc := skeleton()
	for _, k := range []string{"dataSources", "types", "variables"} {
		if val, ok := frag[k]; ok {
			doc[k] = val
		}
	}
	t, err := tree(doc)
	if err != nil {
		return internal(err)
	}
	var out plxerr.Diagnostics
	for _, d := range v.Validate(schema.KindApp, t, file) {
		out = append(out, plxerr.NewDiagnostic(plxerr.ImportOutputInvalid, d.Location, "%s", d.Message))
	}
	return out
}

// finish validates the fragment and returns the result; a fragment that
// fails validation is not returned.
func finish(frag map[string]any, diags plxerr.Diagnostics, file string) Result {
	if bad := validate(frag, file); len(bad) > 0 {
		return Result{Diagnostics: append(diags, bad...)}
	}
	return Result{Fragment: frag, Diagnostics: diags}
}

// field builds a field declaration.
func field(name, typ, description string, sensitive bool) map[string]any {
	f := map[string]any{"name": name, "type": typ}
	if description != "" && len(description) <= 4000 {
		f["description"] = description
	}
	if sensitive {
		f["sensitive"] = true
	}
	return f
}

// optionalInput reports whether the named input type is absent or has only
// optional fields, so an operation can run without being given any.
func (t *typeSet) optionalInput(name any) bool {
	n, ok := name.(string)
	if !ok {
		return true
	}
	d := t.decls[n]
	fields, _ := d["fields"].([]any)
	for _, f := range fields {
		if typ, _ := f.(map[string]any)["type"].(string); !strings.HasSuffix(typ, "?") {
			return false
		}
	}
	return true
}

// zeroValue generates the value of a type when no example exists: empty or
// zero scalars, empty lists and maps, null for a nullable type, and objects
// built field by field. Recursion ends in null for a nullable type and [] for
// a list, so the result is always finite and deterministic.
func (t *typeSet) zeroValue(typ string, depth int) any {
	switch {
	case strings.HasSuffix(typ, "?"):
		return nil
	case strings.HasPrefix(typ, "list<"):
		return []any{}
	case strings.HasPrefix(typ, "map<"):
		return map[string]any{}
	}
	switch typ {
	case "string":
		return ""
	case "int", "double":
		return float64(0)
	case "bool":
		return false
	case "decimal":
		return "0"
	case "date":
		return "1970-01-01"
	case "dateTime":
		return "1970-01-01T00:00:00Z"
	case "duration":
		return "PT0S"
	}
	d := t.decls[typ]
	if members, ok := d["enum"].([]any); ok && len(members) > 0 {
		return members[0]
	}
	fields, ok := d["fields"].([]any)
	if !ok || depth > 8 {
		return nil
	}
	out := map[string]any{}
	for _, f := range fields {
		fm := f.(map[string]any)
		out[fm["name"].(string)] = t.zeroValue(fm["type"].(string), depth+1)
	}
	return out
}
