// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"slices"
	"strings"
)

// ClassCoverage is the coverage of one Flutter class by a widget or value
// type (WGT-003).
type ClassCoverage struct {
	// Owner is "widget" or "type".
	Owner string
	// Name is the widget type or value type name.
	Name string
	// Class is the Flutter class covered.
	Class FlutterClass
	// Params lists every parameter of the covered constructors, in order of
	// first appearance.
	Params []ParamCoverage
}

// ParamCoverage is how one Flutter parameter is covered: by a member, or
// by an exclusion with a reason.
type ParamCoverage struct {
	Parameter
	// Constructors lists the constructors declaring the parameter.
	Constructors []string
	// Members are the covering members, such as "prop padding", "event
	// onPressed", "slot child", "children" or "field all"; several members
	// may compose one parameter, such as the grid delegate. Empty when
	// excluded.
	Members []string
	// Exclusion is set when the parameter is excluded.
	Exclusion *Exclusion
}

// EnumCoverage is the coverage of one Flutter enum by a mirrored enum.
type EnumCoverage struct {
	// Name is the registry enum.
	Name string
	// Flutter is the enum mirrored.
	Flutter FlutterEnum
	// Values lists the Flutter values in declaration order.
	Values []ValueCoverage
}

// ValueCoverage is how one Flutter enum value is covered.
type ValueCoverage struct {
	FlutterEnumValue
	// Exclusion is set when the value is excluded; otherwise a registry
	// value of the same name covers it.
	Exclusion *Exclusion
}

// mapping is one member's claim on Flutter parameter names.
type mapping struct {
	member string
	names  []string
}

// coverage joins the registry with the Flutter snapshot. Every parameter
// of every mirrored constructor, and every value of every mirrored enum,
// must be covered — by members or by one exclusion whose reason is true,
// never both — and every mapping and exclusion must name something that
// exists (WGT-003).
func coverage(r *Registry, p *problems) ([]ClassCoverage, []EnumCoverage) {
	var classes []ClassCoverage
	for _, w := range r.Widgets {
		if w.Flutter != nil {
			classes = append(classes, coverClasses(r.API, p, w.File, "widget", w.Type, []FlutterClass{*w.Flutter}, widgetMappings(w), w.Excluded)...)
		}
	}
	for _, t := range r.Types {
		var maps []mapping
		for _, f := range t.Fields {
			maps = append(maps, mapping{"field " + f.Name, f.Flutter})
		}
		classes = append(classes, coverClasses(r.API, p, t.File, "type", t.Name, t.Flutter, maps, t.Excluded)...)
	}
	var enums []EnumCoverage
	for _, e := range r.Enums {
		if e.Flutter != nil {
			enums = append(enums, coverEnum(r.API, p, e))
		} else if len(e.Excluded) > 0 {
			p.add(e.File, "without a Flutter counterpart, nothing is excluded")
		}
	}
	return classes, enums
}

// widgetMappings lists the Flutter parameters each member of w covers.
func widgetMappings(w *Widget) []mapping {
	var out []mapping
	for _, m := range w.Props {
		out = append(out, mapping{"prop " + m.Name, m.Flutter})
	}
	for _, m := range w.Events {
		out = append(out, mapping{"event " + m.Name, m.Flutter})
	}
	for _, m := range w.Slots {
		out = append(out, mapping{"slot " + m.Name, m.Flutter})
	}
	if w.Children != nil {
		out = append(out, mapping{"children", w.Children.Flutter})
	}
	return out
}

// claims indexes the Flutter parameter names an entry's members cover and
// excludes.
type claims struct {
	members    map[string][]string
	exclusions map[string]*Exclusion
}

// newClaims indexes maps and excluded, reporting duplicate and conflicting
// claims.
func newClaims(p *problems, file string, maps []mapping, excluded []Exclusion) claims {
	c := claims{members: map[string][]string{}, exclusions: map[string]*Exclusion{}}
	for _, m := range maps {
		for _, n := range m.names {
			if slices.Contains(c.members[n], m.member) {
				p.add(file, "%s names Flutter parameter %s twice", m.member, n)
				continue
			}
			c.members[n] = append(c.members[n], m.member)
		}
	}
	for i := range excluded {
		e := &excluded[i]
		if _, dup := c.exclusions[e.Flutter]; dup {
			p.add(file, "Flutter parameter %s is excluded twice", e.Flutter)
		}
		if m, ok := c.members[e.Flutter]; ok {
			p.add(file, "Flutter parameter %s is both excluded and covered by %s", e.Flutter, strings.Join(m, ", "))
		}
		c.exclusions[e.Flutter] = e
	}
	return c
}

// coverClasses computes the coverage of classes by one registry entry. A
// member or exclusion may cover a parameter name in several classes; it
// must exist in at least one.
func coverClasses(api *FlutterAPI, p *problems, file, owner, name string, classes []FlutterClass, maps []mapping, excluded []Exclusion) []ClassCoverage {
	c := newClaims(p, file, maps, excluded)
	seen := map[string]bool{}
	var out []ClassCoverage
	for _, class := range classes {
		cc, ok := classParams(api, p, file, class)
		if !ok {
			continue
		}
		cc.Owner, cc.Name = owner, name
		for i := range cc.Params {
			pc := &cc.Params[i]
			seen[pc.Name] = true
			pc.Members, pc.Exclusion = c.members[pc.Name], c.exclusions[pc.Name]
			switch {
			case len(pc.Members) == 0 && pc.Exclusion == nil:
				p.add(file, "%s.%s is neither supported nor excluded (WGT-003)", class.Class, pc.Name)
			case pc.Exclusion != nil && pc.Exclusion.Reason == ReasonDeprecated && !pc.Deprecated:
				p.add(file, "%s.%s is excluded as deprecated but Flutter does not deprecate it", class.Class, pc.Name)
			}
		}
		out = append(out, cc)
	}
	for _, n := range sortedKeys(c.members) {
		if !seen[n] {
			p.add(file, "%s maps Flutter parameter %s, which no mirrored constructor declares", strings.Join(c.members[n], ", "), n)
		}
	}
	for _, n := range sortedKeys(c.exclusions) {
		if !seen[n] {
			p.add(file, "excluded Flutter parameter %s is declared by no mirrored constructor", n)
		}
	}
	return out
}

// classParams lists the parameters of the mirrored constructors of class
// in order of first appearance, merging parameters several constructors
// declare.
func classParams(api *FlutterAPI, p *problems, file string, class FlutterClass) (ClassCoverage, bool) {
	extracted, ok := api.Classes[class.Key()]
	if !ok {
		p.add(file, "%s is missing from %s; run 'make widgets-api'", class.Key(), APIFile)
		return ClassCoverage{}, false
	}
	cc := ClassCoverage{Class: class}
	index := map[string]int{}
	for _, ctor := range class.Constructors {
		params, ok := extracted.Constructors[ctor]
		if !ok {
			p.add(file, "constructor %q of %s is missing from %s; run 'make widgets-api'", ctor, class.Key(), APIFile)
			continue
		}
		for _, prm := range params {
			if i, ok := index[prm.Name]; ok {
				cc.Params[i].Constructors = append(cc.Params[i].Constructors, ctor)
				cc.Params[i].Deprecated = cc.Params[i].Deprecated || prm.Deprecated
				continue
			}
			index[prm.Name] = len(cc.Params)
			cc.Params = append(cc.Params, ParamCoverage{Parameter: prm, Constructors: []string{ctor}})
		}
	}
	return cc, true
}

// coverEnum computes the coverage of a mirrored enum.
func coverEnum(api *FlutterAPI, p *problems, e *Enum) EnumCoverage {
	ec := EnumCoverage{Name: e.Name, Flutter: *e.Flutter}
	values, ok := api.Enums[e.Flutter.Key()]
	if !ok {
		p.add(e.File, "%s is missing from %s; run 'make widgets-api'", e.Flutter.Key(), APIFile)
		return ec
	}
	exclusions := map[string]*Exclusion{}
	for i := range e.Excluded {
		exclusions[e.Excluded[i].Flutter] = &e.Excluded[i]
	}
	flutter := map[string]bool{}
	for _, v := range values {
		flutter[v.Name] = true
		covered := slices.ContainsFunc(e.Values, func(x EnumValue) bool { return x.Name == v.Name })
		vc := ValueCoverage{FlutterEnumValue: v, Exclusion: exclusions[v.Name]}
		switch {
		case covered && vc.Exclusion != nil:
			p.add(e.File, "%s.%s is both a value and excluded", e.Flutter.Enum, v.Name)
		case !covered && vc.Exclusion == nil:
			p.add(e.File, "%s.%s is neither supported nor excluded (WGT-003)", e.Flutter.Enum, v.Name)
		case vc.Exclusion != nil && vc.Exclusion.Reason == ReasonDeprecated && !v.Deprecated:
			p.add(e.File, "%s.%s is excluded as deprecated but Flutter does not deprecate it", e.Flutter.Enum, v.Name)
		}
		ec.Values = append(ec.Values, vc)
	}
	for _, v := range e.Values {
		if !flutter[v.Name] {
			p.add(e.File, "value %s is not a value of the mirrored enum %s", v.Name, e.Flutter.Enum)
		}
	}
	for _, n := range sortedKeys(exclusions) {
		if !flutter[n] {
			p.add(e.File, "excluded value %s is not a value of %s", n, e.Flutter.Enum)
		}
	}
	return ec
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, strings.Compare)
	return keys
}
