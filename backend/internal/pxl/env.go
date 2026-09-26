// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// EnvSpec declares what an expression can use at one use site (PXL-002):
// named types and the roots with their types. It is the JSON form used by
// the conformance vectors; the compiler builds it from the documents.
type EnvSpec struct {
	// Types declares enums and object types by name.
	Types map[string]TypeSpec `json:"types,omitempty"`
	// Roots maps each root, such as "page" or "item", to a type expression.
	Roots map[string]string `json:"roots"`
}

// TypeSpec declares an enum (Enum) or an object type (Fields).
type TypeSpec struct {
	Enum   []string          `json:"enum,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Env is a checked EnvSpec. It is immutable and safe for concurrent use.
type Env struct {
	types map[string]*NamedType
	roots map[string]*Type
}

// NowRoot is the root holding the evaluation's frozen current time.
const NowRoot = "now"

// NewEnv checks spec and resolves its types. The root "now" of type
// dateTime is always available.
func NewEnv(spec EnvSpec) (*Env, error) {
	e := &Env{types: map[string]*NamedType{}, roots: map[string]*Type{}}
	for _, def := range stdEnums {
		e.types[def.name] = &NamedType{Name: def.name, Members: def.values}
	}
	errs := e.declare(spec.Types)
	errs = append(errs, e.resolveFields(spec.Types)...)
	for _, root := range sortedKeys(spec.Roots) {
		t, err := parseType(spec.Roots[root], e.lookup, nil)
		if err != nil || root == NowRoot || !isIdent(root) {
			errs = append(errs, fmt.Errorf("root %q: invalid name or type: %w", root, err))
			continue
		}
		e.roots[root] = t
	}
	e.roots[NowRoot] = typeDateTime
	if len(errs) > 0 {
		return nil, fmt.Errorf("pxl.NewEnv: %w", errors.Join(errs...))
	}
	return e, nil
}

// declare adds the declared enums and object types, without field types.
func (e *Env) declare(types map[string]TypeSpec) []error {
	var errs []error
	for _, name := range sortedKeys(types) {
		ts := types[name]
		if _, builtin := e.types[name]; builtin || primitiveNames[name] != 0 || !isTypeName(name) {
			errs = append(errs, fmt.Errorf("type %q: invalid or reserved name", name))
			continue
		}
		switch {
		case len(ts.Enum) > 0 && ts.Fields == nil:
			e.types[name] = &NamedType{Name: name, Members: slices.Clone(ts.Enum)}
		case len(ts.Enum) == 0 && ts.Fields != nil:
			e.types[name] = &NamedType{Name: name, Fields: map[string]*Type{}, FieldOrder: sortedKeys(ts.Fields)}
		default:
			errs = append(errs, fmt.Errorf("type %q: declare either enum members or fields", name))
		}
	}
	return errs
}

// resolveFields parses the field types of the declared object types, which
// may refer to each other.
func (e *Env) resolveFields(types map[string]TypeSpec) []error {
	var errs []error
	for _, name := range sortedKeys(types) {
		n := e.types[name]
		if n == nil || n.IsEnum() || n.Name != name {
			continue
		}
		for _, f := range n.FieldOrder {
			t, err := parseType(types[name].Fields[f], e.lookup, nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("type %s, field %s: %w", name, f, err))
				continue
			}
			n.Fields[f] = t
		}
	}
	return errs
}

// lookup resolves a declared or built-in type name.
func (e *Env) lookup(name string) (*NamedType, bool) {
	n, ok := e.types[name]
	return n, ok
}

// ParseType parses a type expression against the environment's types.
func (e *Env) ParseType(expr string) (*Type, error) {
	return parseType(expr, e.lookup, nil)
}

// Root returns the type of a root.
func (e *Env) Root(name string) (*Type, bool) {
	t, ok := e.roots[name]
	return t, ok
}

// isIdent reports whether s is a PXL identifier.
func isIdent(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	for i := range len(s) {
		if !isLetter(s[i]) && !isDigit(s[i]) {
			return false
		}
	}
	return !isKeyword(s)
}

// isTypeName reports whether s is an UpperCamelCase type name.
func isTypeName(s string) bool {
	return isIdent(s) && s[0] >= 'A' && s[0] <= 'Z' && !strings.Contains(s, "_")
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
