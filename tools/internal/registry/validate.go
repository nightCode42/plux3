// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// platforms lists the platforms in canonical order.
var platforms = []string{"android", "ios"}

// semverLiteral matches a semantic version without pre-release or build.
var semverLiteral = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// index resolves the named types of the registry.
type index struct {
	types map[string]*ValueType
	enums map[string]*Enum
}

// check runs every semantic check and computes the coverage.
func check(r *Registry, p *problems) {
	ix := &index{types: map[string]*ValueType{}, enums: map[string]*Enum{}}
	for _, t := range r.Types {
		ix.types[t.Name] = t
	}
	for _, e := range r.Enums {
		if _, clash := ix.types[e.Name]; clash {
			p.add(e.File, "the name %s is also a value type", e.Name)
		}
		ix.enums[e.Name] = e
	}
	uniqueIDs(p, r.Widgets, func(w *Widget) (string, string, uint32) { return w.File, w.Type, w.ID })
	uniqueIDs(p, r.Types, func(t *ValueType) (string, string, uint32) { return t.File, t.Name, t.ID })
	uniqueIDs(p, r.Enums, func(e *Enum) (string, string, uint32) { return e.File, e.Name, e.ID })
	uniqueIDs(p, r.Actions, func(a *Action) (string, string, uint32) { return a.File, a.Name, a.ID })
	for _, w := range r.Widgets {
		ix.checkWidget(w, p)
	}
	for _, t := range r.Types {
		ix.checkValueType(t, p)
	}
	for _, e := range r.Enums {
		checkRevisions(p, e.File, e.Revision, e.Revisions)
		checkSentence(p, e.File, "description", e.Description)
		checkExclusions(p, e.File, e.Excluded)
		m := members{p: p, file: e.File, revision: e.Revision}
		for _, v := range e.Values {
			m.add("value", v.Name, v.ID, v.Revision, v.Deprecated, v.Description)
		}
	}
	for _, a := range r.Actions {
		ix.checkAction(a, p)
	}
	r.Coverage, r.EnumCoverage = coverage(r, p)
}

// uniqueIDs reports entries of one kind that share an ID.
func uniqueIDs[T any](p *problems, entries []*T, key func(*T) (file, name string, id uint32)) {
	seen := map[uint32]string{}
	for _, e := range entries {
		file, name, id := key(e)
		if other, dup := seen[id]; dup {
			p.add(file, "ID %d is also used by %s", id, other)
		}
		seen[id] = name
	}
}

// checkWidget checks one descriptor.
func (ix *index) checkWidget(w *Widget, p *problems) {
	checkWidgetEntry(w, p)
	params := typeParams(w.TypeParameters)
	ix.checkWidgetMembers(w, p, params)
	for _, tp := range w.TypeParameters {
		if !params[tp] {
			p.add(w.File, "type parameter %s is never used", tp)
		}
	}
}

// checkWidgetEntry checks the descriptor's own properties and structure.
func checkWidgetEntry(w *Widget, p *problems) {
	if want := "layer" + strconv.Itoa(w.Layer); !strings.Contains(w.File, "/"+want+"/") {
		p.add(w.File, "a layer %d descriptor belongs in %s/%s", w.Layer, WidgetsDir, want)
	}
	switch {
	case w.Layer == 1 && w.Flutter == nil && w.Category != "structure":
		p.add(w.File, "a Layer 1 widget without a Flutter counterpart must be a structural primitive (category structure)")
	case w.Layer == 2 && w.Flutter != nil:
		p.add(w.File, "a Layer 2 component has no Flutter counterpart")
	case w.Flutter == nil && (len(w.Excluded) > 0 || w.hasFlutterMapping()):
		p.add(w.File, "without a Flutter counterpart, members map no Flutter parameters and nothing is excluded")
	}
	checkRevisions(p, w.File, w.Revision, w.Revisions)
	checkSentence(p, w.File, "description", w.Description)
	checkInOrder(p, w.File, "platform", w.Platforms, platforms)
	checkExclusions(p, w.File, w.Excluded)
	if !slices.Contains(roles, w.Accessibility.Role) {
		p.add(w.File, "unknown accessibility role %q", w.Accessibility.Role)
	}
	if w.Deprecated != nil {
		checkDeprecation(p, w.File, w.Type, 1, w.Revision, w.Deprecated)
	}
	if w.Children != nil && len(w.Slots) > 0 {
		p.add(w.File, "a widget takes children or named slots, never both; make the list a list slot (SCH-023)")
	}
	if c := w.Children; c != nil && c.Min != nil && c.Max != nil && *c.Min > *c.Max {
		p.add(w.File, "children: min %d exceeds max %d", *c.Min, *c.Max)
	}
}

// checkConstructorProp checks a prop that selects a named constructor: a
// bool that is neither required nor bindable, mapping no parameter, on a
// widget that mirrors that constructor.
func checkConstructorProp(p *problems, loc string, w *Widget, prop Prop) {
	switch {
	case prop.Type != "bool" || prop.Required || prop.IsBindable():
		p.add(loc, "a prop selecting constructor %q is an optional bool that is not bindable", prop.Constructor)
	case len(prop.Flutter) > 0:
		p.add(loc, "a prop selecting constructor %q maps no Flutter parameter", prop.Constructor)
	case w.Flutter == nil || !slices.Contains(w.Flutter.Constructors, prop.Constructor):
		p.add(loc, "constructor %q is not among the widget's Flutter constructors", prop.Constructor)
	}
}

// checkWidgetMembers checks props, events and slots, recording the type
// parameters they use in params.
func (ix *index) checkWidgetMembers(w *Widget, p *problems, params map[string]bool) {
	m := members{p: p, file: w.File, revision: w.Revision}
	for _, prop := range w.Props {
		m.add("prop", prop.Name, prop.ID, prop.Revision, prop.Deprecated, prop.Description)
		loc := w.File + ": prop " + prop.Name
		if t, ok := ix.resolve(p, loc, prop.Type, params); ok {
			ix.checkDefault(p, loc, t, prop.Default, prop.Required)
			checkConstraints(p, loc, t, prop.Constraints, prop.Default)
		}
		if prop.Constructor != "" {
			checkConstructorProp(p, loc, w, prop)
		}
	}
	for _, e := range w.Events {
		m.add("event", e.Name, e.ID, e.Revision, e.Deprecated, e.Description)
		if e.Payload != "" {
			ix.resolve(p, w.File+": event "+e.Name, e.Payload, params)
		}
	}
	for _, s := range w.Slots {
		m.add("slot", s.Name, s.ID, s.Revision, s.Deprecated, s.Description)
		if s.List && s.Template {
			p.add(w.File, "slot %s: a template slot holds one node, built per item", s.Name)
		}
	}
}

// typeParams returns the declared type parameters, none used yet.
func typeParams(names []string) map[string]bool {
	params := make(map[string]bool, len(names))
	for _, n := range names {
		params[n] = false
	}
	return params
}

// hasFlutterMapping reports whether any member names a Flutter parameter.
func (w *Widget) hasFlutterMapping() bool {
	for _, m := range w.Props {
		if len(m.Flutter) > 0 {
			return true
		}
	}
	for _, m := range w.Events {
		if len(m.Flutter) > 0 {
			return true
		}
	}
	for _, m := range w.Slots {
		if len(m.Flutter) > 0 {
			return true
		}
	}
	return w.Children != nil && len(w.Children.Flutter) > 0
}

// checkValueType checks one value type.
func (ix *index) checkValueType(vt *ValueType, p *problems) {
	checkRevisions(p, vt.File, vt.Revision, vt.Revisions)
	checkSentence(p, vt.File, "description", vt.Description)
	m := members{p: p, file: vt.File, revision: vt.Revision}
	for _, f := range vt.Fields {
		m.add("field", f.Name, f.ID, f.Revision, f.Deprecated, f.Description)
		loc := vt.File + ": field " + f.Name
		if t, ok := ix.resolve(p, loc, f.Type, nil); ok {
			ix.checkDefault(p, loc, t, f.Default, f.Required)
		}
	}
	seen := map[string]bool{}
	for _, c := range vt.Constants {
		if seen[c.Name] {
			p.add(vt.File, "constant %s is declared twice", c.Name)
		}
		seen[c.Name] = true
		v, err := decodeLiteral(c.Value)
		if err == nil {
			err = ix.checkObject(v, vt, false)
		}
		if err != nil {
			p.add(vt.File, "constant %s: %v", c.Name, err)
		}
	}
	checkExclusions(p, vt.File, vt.Excluded)
	if len(vt.Flutter) == 0 && len(vt.Excluded) > 0 {
		p.add(vt.File, "without a Flutter counterpart, nothing is excluded")
	}
}

// Closed vocabularies of the registry, in canonical order; the JSON
// Schemas in schema/json/registry/ list the same values.
var (
	roles = []string{
		"none", "button", "checkbox", "radio", "switch", "slider", "textField", "image", "text", "header", "list", "progress",
	}
	categories = []string{
		"navigation", "feedback", "state", "forms", "data", "localDb", "compute", "control", "analytics", "app",
		"device", "security", "animation", "host", "plux", "payments",
	}
	effects = []string{"navigation", "state", "network", "storage", "device", "ui", "telemetry", "security", "host"}
)

// Roles returns the accessibility roles of widgets.
func Roles() []string { return slices.Clone(roles) }

// ActionCategories returns the categories of Appendix D.
func ActionCategories() []string { return slices.Clone(categories) }

// ActionEffects returns the effects an action may declare.
func ActionEffects() []string { return slices.Clone(effects) }

// Platforms returns the platforms a widget may support.
func Platforms() []string { return slices.Clone(platforms) }

// checkAction checks one action descriptor.
func (ix *index) checkAction(a *Action, p *problems) {
	checkSentence(p, a.File, "description", a.Description)
	checkInOrder(p, a.File, "effect", a.Effects, effects)
	if !slices.Contains(categories, a.Category) {
		p.add(a.File, "unknown category %q", a.Category)
	}
	params := map[string]bool{}
	for _, tp := range a.TypeParams {
		if _, dup := params[tp.Name]; dup {
			p.add(a.File, "type parameter %s is declared twice", tp.Name)
		}
		params[tp.Name] = false
		checkSentence(p, a.File, "type parameter "+tp.Name+" description", tp.Description)
	}
	ix.checkInputs(a, p, params)
	if a.Output != "" {
		ix.resolve(p, a.File+": output", a.Output, params)
	}
	for _, b := range a.Branches {
		if slices.Contains([]string{"next", "onSuccess", "onError"}, b) {
			p.add(a.File, "branch %s is a standard edge", b)
		}
	}
	for _, tp := range a.TypeParams {
		if !params[tp.Name] {
			p.add(a.File, "type parameter %s is never used", tp.Name)
		}
	}
}

// checkInputs checks the inputs of an action and its branchesFrom input.
func (ix *index) checkInputs(a *Action, p *problems, params map[string]bool) {
	m := members{p: p, file: a.File, revision: 1}
	for _, in := range a.Inputs {
		m.add("input", in.Name, in.ID, 0, nil, in.Description)
		loc := a.File + ": input " + in.Name
		t, ok := ix.resolve(p, loc, in.Type, params)
		if !ok {
			continue
		}
		ix.checkDefault(p, loc, t, in.Default, in.Required)
		if in.Ref != "" && t.String() != "string" {
			p.add(loc, "an input naming a %s is a string", in.Ref)
		}
		if in.Name == a.BranchesFrom && t.String() != "list<string>" {
			p.add(loc, "branchesFrom names an input of type list<string>")
		}
	}
	if a.BranchesFrom != "" && !slices.ContainsFunc(a.Inputs, func(in Input) bool { return in.Name == a.BranchesFrom }) {
		p.add(a.File, "branchesFrom names no input %s", a.BranchesFrom)
	}
}

// resolve parses a type expression and checks that its names exist. params
// holds the declared type parameters and records which are used.
func (ix *index) resolve(p *problems, loc, expr string, params map[string]bool) (Type, bool) {
	t, err := ParseType(expr)
	if err != nil {
		p.add(loc, "%v", err)
		return Type{}, false
	}
	ok := true
	t.walk(func(n Type) {
		switch n.Kind {
		case KindNamed:
			if ix.types[n.Name] == nil && ix.enums[n.Name] == nil {
				p.add(loc, "unknown type %s", n.Name)
				ok = false
			}
		case KindParam:
			if _, declared := params[n.Name]; !declared {
				p.add(loc, "undeclared type parameter %s", n.Name)
				ok = false
				return
			}
			params[n.Name] = true
		}
	})
	return t, ok
}

// checkDefault checks a default literal against its type.
func (ix *index) checkDefault(p *problems, loc string, t Type, def json.RawMessage, required bool) {
	if def == nil {
		return
	}
	if required {
		p.add(loc, "a required value has no default")
	}
	v, err := decodeLiteral(def)
	if err == nil {
		err = ix.checkLiteral(v, t)
	}
	if err != nil {
		p.add(loc, "default: %v", err)
	}
}

// checkConstraints checks that constraints fit the type and the default.
func checkConstraints(p *problems, loc string, t Type, c *Constraints, def json.RawMessage) {
	if c == nil {
		return
	}
	checkConstraintKinds(p, loc, t, c)
	if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
		p.add(loc, "min %v exceeds max %v", *c.Min, *c.Max)
	}
	if c.MinLength != nil && c.MaxLength != nil && *c.MinLength > *c.MaxLength {
		p.add(loc, "minLength %d exceeds maxLength %d", *c.MinLength, *c.MaxLength)
	}
	var n float64
	if def != nil && numeric(t) && json.Unmarshal(def, &n) == nil {
		if c.Min != nil && n < *c.Min || c.Max != nil && n > *c.Max {
			p.add(loc, "default %v is outside [min, max]", n)
		}
	}
}

// checkConstraintKinds checks that each constraint applies to the type.
func checkConstraintKinds(p *problems, loc string, t Type, c *Constraints) {
	if (c.Min != nil || c.Max != nil) && !numeric(t) {
		p.add(loc, "min and max constrain numbers, not %s", t)
	}
	if (c.MinLength != nil || c.MaxLength != nil) && !sized(t) {
		p.add(loc, "minLength and maxLength constrain strings and lists, not %s", t)
	}
	if c.Pattern == "" {
		return
	}
	if t.Kind != KindPrimitive || t.Name != "string" {
		p.add(loc, "pattern constrains strings, not %s", t)
	}
	if _, err := regexp.Compile(c.Pattern); err != nil {
		p.add(loc, "pattern: %v", err)
	}
}

// members checks the members of one entry: names unique across kinds, IDs
// unique within a kind, revisions within the entry's revision.
type members struct {
	p        *problems
	file     string
	revision int
	names    map[string]string
	ids      map[string]string
}

// add records one member. A zero revision means 1.
func (m *members) add(kind, name string, id uint32, revision int, dep *Deprecation, doc string) {
	if m.names == nil {
		m.names, m.ids = map[string]string{}, map[string]string{}
	}
	if other, dup := m.names[name]; dup {
		m.p.add(m.file, "%s %s: the name is also used by a %s", kind, name, other)
	}
	m.names[name] = kind
	key := kind + "#" + strconv.FormatUint(uint64(id), 10)
	if other, dup := m.ids[key]; dup {
		m.p.add(m.file, "%s %s: ID %d is also used by %s", kind, name, id, other)
	}
	m.ids[key] = name
	if revision == 0 {
		revision = 1
	}
	if revision > m.revision {
		m.p.add(m.file, "%s %s: revision %d is newer than the entry's revision %d", kind, name, revision, m.revision)
	}
	if dep != nil {
		checkDeprecation(m.p, m.file, kind+" "+name, revision, m.revision, dep)
	}
	if doc != "" {
		checkSentence(m.p, m.file, kind+" "+name+" description", doc)
	}
}

// checkDeprecation checks that a deprecation falls between the revision
// that added the member and the entry's current revision.
func checkDeprecation(p *problems, file, what string, added, current int, d *Deprecation) {
	if d.Revision < added || d.Revision > current {
		p.add(file, "%s: deprecated in revision %d, outside revisions %d–%d", what, d.Revision, added, current)
	}
	checkSentence(p, file, what+" deprecation message", d.Message)
}

// checkExclusions checks that exclusion notes are sentences.
func checkExclusions(p *problems, file string, excluded []Exclusion) {
	for _, e := range excluded {
		if e.Note != "" {
			checkSentence(p, file, "exclusion of "+e.Flutter, e.Note)
		}
	}
}

// checkRevisions checks that revisions lists 1…revision in order, each
// with a runtime version no older than the previous one.
func checkRevisions(p *problems, file string, revision int, revisions []Revision) {
	if len(revisions) != revision {
		p.add(file, "revisions must list revisions 1 to %d", revision)
		return
	}
	var prev []int
	for i, r := range revisions {
		if r.Revision != i+1 {
			p.add(file, "revisions[%d] must be revision %d", i, i+1)
		}
		v, ok := parseSemver(r.Runtime)
		if !ok {
			p.add(file, "revisions[%d]: %q is not a semantic version", i, r.Runtime)
			continue
		}
		if prev != nil && slices.Compare(v, prev) < 0 {
			p.add(file, "revisions[%d]: runtime %s is older than the previous revision's", i, r.Runtime)
		}
		prev = v
	}
}

// parseSemver parses MAJOR.MINOR.PATCH.
func parseSemver(s string) ([]int, bool) {
	if !semverLiteral.MatchString(s) {
		return nil, false
	}
	out := make([]int, 3)
	for i, part := range strings.Split(s, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// checkSentence checks that documentation text is a sentence.
func checkSentence(p *problems, file, what, s string) {
	if s = strings.TrimSpace(s); s == "" || !strings.HasSuffix(s, ".") {
		p.add(file, "%s must be a sentence ending with a full stop", what)
	}
}

// checkInOrder checks that values are distinct members of allowed, in
// allowed's order, so every list has one spelling.
func checkInOrder(p *problems, file, what string, values, allowed []string) {
	last := -1
	for _, v := range values {
		i := slices.Index(allowed, v)
		switch {
		case i < 0:
			p.add(file, "unknown %s %q", what, v)
		case i <= last:
			p.add(file, "%ss must be distinct and in the order %s", what, strings.Join(allowed, ", "))
		}
		last = max(last, i)
	}
}
