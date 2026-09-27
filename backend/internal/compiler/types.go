// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// reservedPrefix starts the names of the types the compiler declares for
// the roots of expressions; documents cannot declare such names.
const reservedPrefix = "Plux"

// universe holds every named type an expression or declaration can use:
// the registry's value types and enums, the compiler's root types, and
// the types the app and each plugin declare.
type universe struct {
	base   map[string]pxl.TypeSpec
	app    map[string]pxl.TypeSpec
	plugin map[*plugin]map[string]pxl.TypeSpec
}

// newUniverse collects the named types and checks the declared ones.
func newUniverse(u *unit) *universe {
	t := &universe{base: baseTypes(), app: map[string]pxl.TypeSpec{}, plugin: map[*plugin]map[string]pxl.TypeSpec{}}
	t.app = u.declareTypes(t, nil, u.project.App.Doc.Types, "app.json")
	for _, pl := range u.plugins {
		t.plugin[pl] = u.declareTypes(t, pl, pl.doc.Types, pl.file)
	}
	return t
}

// baseTypes are the registry types and the compiler's root types.
func baseTypes() map[string]pxl.TypeSpec {
	out := map[string]pxl.TypeSpec{}
	for _, vt := range registry.ValueTypes() {
		fields := map[string]string{}
		for _, f := range vt.Fields {
			fields[f.Name] = f.Type
		}
		out[vt.Name] = pxl.TypeSpec{Fields: fields}
	}
	for _, e := range registry.Enums() {
		members := make([]string, len(e.Values))
		for i, v := range e.Values {
			members[i] = v.Name
		}
		out[e.Name] = pxl.TypeSpec{Enum: members}
	}
	levels := []string{string(schema.AssuranceLevelAL0), string(schema.AssuranceLevelAL1), string(schema.AssuranceLevelAL2), string(schema.AssuranceLevelAL3)}
	kinds := []string{
		string(schema.ErrorKindNetwork), string(schema.ErrorKindHTTP), string(schema.ErrorKindTimeout), string(schema.ErrorKindValidation),
		string(schema.ErrorKindFunction), string(schema.ErrorKindPermission), string(schema.ErrorKindCancelled), string(schema.ErrorKindCustom),
	}
	out["PluxPlatform"] = pxl.TypeSpec{Enum: []string{"android", "ios"}}
	out["PluxSizeClass"] = pxl.TypeSpec{Enum: []string{"compact", "medium", "expanded"}}
	out["PluxAssuranceLevel"] = pxl.TypeSpec{Enum: levels}
	out["PluxErrorKind"] = pxl.TypeSpec{Enum: kinds}
	out["PluxDevice"] = pxl.TypeSpec{Fields: map[string]string{
		"platform": "PluxPlatform", "osVersion": "string", "locale": "string", "textScale": "double",
		"darkMode": "bool", "sizeClass": "PluxSizeClass", "assuranceLevel": "PluxAssuranceLevel",
	}}
	out["PluxActionError"] = pxl.TypeSpec{Fields: map[string]string{"kind": "PluxErrorKind", "message": "string"}}
	return out
}

// declareTypes checks the types a document declares and returns the valid
// ones; pl is nil for the app.
func (u *unit) declareTypes(t *universe, pl *plugin, decls []schema.TypeDecl, file string) map[string]pxl.TypeSpec {
	out := map[string]pxl.TypeSpec{}
	names := u.newKeys("type")
	for i, d := range decls {
		ptr := plxerr.Pointer("types", strconv.Itoa(i))
		_, inBase := t.base[d.Name]
		_, inApp := t.app[d.Name]
		switch {
		case strings.HasPrefix(d.Name, reservedPrefix) || inBase || primitives[d.Name] || d.Name == "list" || d.Name == "map":
			u.report(plxerr.DuplicateKey, file, ptr+"/name", "type name %q is built in or reserved", d.Name)
			continue
		case pl != nil && inApp:
			u.report(plxerr.DuplicateKey, file, ptr+"/name", "type %q is already declared by the app", d.Name)
			continue
		}
		names.claim(d.Name, file, ptr+"/name")
		if len(d.Enum) > 0 {
			members := u.newKeys("enum member")
			for j, m := range d.Enum {
				members.claim(m, file, ptr+"/enum/"+strconv.Itoa(j))
			}
			out[d.Name] = pxl.TypeSpec{Enum: slices.Clone(d.Enum)}
			continue
		}
		out[d.Name] = pxl.TypeSpec{Fields: map[string]string{}}
	}
	for i, d := range decls {
		spec, ok := out[d.Name]
		if !ok || spec.Fields == nil {
			continue
		}
		fields := u.newKeys("field")
		for j, f := range d.Fields {
			fptr := plxerr.Pointer("types", strconv.Itoa(i), "fields", strconv.Itoa(j))
			fields.claim(f.Name, file, fptr+"/name")
			if u.checkTypeIn(t, pl, out, f.Type, nil, file, fptr+"/type") != nil {
				spec.Fields[f.Name] = f.Type
			}
		}
	}
	return out
}

// checkType checks a type expression written in a document; params are
// the type parameters in scope. It returns nil after reporting a problem
// (PLX-1115, PLX-1116).
func (u *unit) checkType(pl *plugin, expr, file, ptr string) *texpr {
	return u.checkTypeIn(u.types, pl, nil, expr, nil, file, ptr)
}

func (u *unit) checkTypeIn(t *universe, pl *plugin, extra map[string]pxl.TypeSpec, expr string, params []string, file, ptr string) *texpr {
	te, err := parseTypeExpr(expr)
	if err != nil {
		u.report(plxerr.InvalidTypeExpression, file, ptr, "%q: %v", expr, err)
		return nil
	}
	ok := true
	te.names(func(name string) {
		if !t.known(pl, name) && !slices.Contains(params, name) && !hasKey(extra, name) {
			u.report(plxerr.UnknownType, file, ptr, "type %q is not declared", name)
			ok = false
		}
	})
	if !ok {
		return nil
	}
	return te
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// known reports whether a document in pl (nil: app level) can name a
// type: a primitive or a type the app or the plugin declares. Registry
// types are named by descriptors only (document-model.md §3).
func (t *universe) known(pl *plugin, name string) bool {
	if primitives[name] || hasKey(t.app, name) {
		return true
	}
	return pl != nil && hasKey(t.plugin[pl], name)
}

// scope is what an expression site can read: the plugin whose types are
// visible, its roots with their types, and the object types the compiler
// declares for them.
type scope struct {
	plugin *plugin
	roots  map[string]string
	synth  map[string]pxl.TypeSpec
	// ids maps a state root and a name to the entry's ID, and data and a
	// name to the source's ID, for the reference graph.
	ids map[string]map[string]string
	// sources are the data sources visible as data.<name>.
	sources []sourceField
	// envKey caches key: a scope is complete before any expression is
	// compiled in it and never changes afterwards.
	envKey string
}

// with returns a copy of s with one more root.
func (s *scope) with(root, typ string) *scope {
	c := &scope{plugin: s.plugin, roots: map[string]string{}, synth: s.synth, ids: s.ids, sources: s.sources}
	for k, v := range s.roots {
		c.roots[k] = v
	}
	c.roots[root] = typ
	return c
}

// withTypes returns a copy of s with more declared types.
func (s *scope) withTypes(types map[string]pxl.TypeSpec) *scope {
	c := &scope{plugin: s.plugin, roots: s.roots, synth: map[string]pxl.TypeSpec{}, ids: s.ids, sources: s.sources}
	for k, v := range s.synth {
		c.synth[k] = v
	}
	for k, v := range types {
		c.synth[k] = v
	}
	return c
}

// key identifies the environment a scope needs.
func (s *scope) key() string {
	if s.envKey != "" {
		return s.envKey
	}
	var b strings.Builder
	if s.plugin != nil {
		b.WriteString(s.plugin.key)
	}
	for _, k := range sortedKeys(s.roots) {
		fmt.Fprintf(&b, "|%s:%s", k, s.roots[k])
	}
	for _, k := range sortedKeys(s.synth) {
		spec := s.synth[k]
		fmt.Fprintf(&b, "|%s=%v", k, spec.Enum)
		for _, f := range sortedKeys(spec.Fields) {
			fmt.Fprintf(&b, ",%s:%s", f, spec.Fields[f])
		}
	}
	s.envKey = b.String()
	return s.envKey
}

// env returns the PXL environment of a scope, built once per distinct
// scope.
func (u *unit) env(s *scope) (*pxl.Env, error) {
	key := s.key()
	if e, ok := u.envs[key]; ok {
		return e, nil
	}
	spec := pxl.EnvSpec{Types: map[string]pxl.TypeSpec{}, Roots: s.roots}
	for _, m := range []map[string]pxl.TypeSpec{u.types.base, u.types.app, u.types.plugin[s.plugin], s.synth} {
		for k, v := range m {
			spec.Types[k] = v
		}
	}
	e, err := pxl.NewEnv(spec)
	if err != nil {
		return nil, fmt.Errorf("compiler: environment %s: %w", key, err)
	}
	u.envs[key] = e
	return e, nil
}

// objectType builds a compiler-declared object type from name-type pairs;
// entries whose type did not check are left out.
func objectType(fields [][2]string) pxl.TypeSpec {
	spec := pxl.TypeSpec{Fields: map[string]string{}}
	for _, f := range fields {
		if f[1] != "" {
			spec.Fields[f[0]] = f[1]
		}
	}
	return spec
}

// upperFirst makes a name usable inside a type name.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
