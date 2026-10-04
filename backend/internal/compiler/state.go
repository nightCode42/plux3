// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// The state engine's features (STA-*, BND-008), first in runtime 0.3.0: a
// bundle that writes state, keeps it beyond its scope or computes it
// requires them, so an older runtime refuses the bundle instead of
// failing its writes, dropping its stored values or its computed entries
// (ADR-0040's pattern).
const (
	stateWriteFeature       = "state.write"
	statePersistenceFeature = "state.persistence"
	stateComputedFeature    = "state.computed"
)

// stateRuntimes lists the runtime each revision of the state features
// first shipped in.
var stateRuntimes = []string{"0.3.0"}

// stateWrites are the actions that write state (Appendix D).
var stateWrites = map[string]bool{"setState": true, "patchState": true, "resetState": true}

// stored reports whether a persistence keeps values beyond their scope
// instance: session, persisted and secure entries carry a type
// fingerprint and need migrations when their type changes (STA-040).
func stored(p fbs.Persistence) bool { return p != fbs.PersistenceMemory }

// StoredEntry is a session, persisted or secure state entry: what devices
// keep, and what publishing compares with the previous release (STA-040).
type StoredEntry struct {
	// ID is the entry's UUID, which keys its stored value.
	ID string
	// Name and Type are the entry's name and type expression.
	Name string
	Type string
	// Fingerprint is the version of the entry's type: a hash of its
	// structure, with named types expanded.
	Fingerprint string
	// MigrationFrom is the fingerprint of the type the entry's migration
	// reads, or "".
	MigrationFrom string
	// Reset reports a migration that starts from the default.
	Reset bool
	// File and Ptr locate the declaration; empty for entries read from a
	// bundle.
	File string
	Ptr  string
}

// typeFingerprint is the version of a type (STA-040): the first eight
// bytes of the SHA-256 of its canonical structure, in hex. Renaming a
// named type keeps it; adding, removing or retyping a field, an enum
// member or nullability changes it.
func (u *unit) typeFingerprint(pl *plugin, te *texpr) string {
	var b strings.Builder
	u.canonType(&b, pl, te, map[string]bool{})
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// canonType writes the canonical structure of a type: primitives by name,
// declared and registry objects as their sorted fields, enums as their
// sorted members; a recursive reference by name.
func (u *unit) canonType(b *strings.Builder, pl *plugin, te *texpr, seen map[string]bool) {
	switch {
	case te.name == "list" || te.name == "map":
		b.WriteString(te.name + "<")
		if te.elem != nil {
			u.canonType(b, pl, te.elem, seen)
		}
		b.WriteString(">")
	case primitives[te.name]:
		b.WriteString(te.name)
	default:
		spec, ok := u.namedType(pl, te.name)
		switch {
		case !ok || seen[te.name]:
			b.WriteString("@" + te.name)
		case spec.Fields == nil:
			members := slices.Clone(spec.Enum)
			slices.Sort(members)
			b.WriteString("enum(" + strings.Join(members, "|") + ")")
		default:
			seen[te.name] = true
			b.WriteString("{")
			for _, f := range sortedKeys(spec.Fields) {
				b.WriteString(f + ":")
				if ft, err := parseTypeExpr(spec.Fields[f]); err == nil {
					u.canonType(b, pl, ft, seen)
				}
				b.WriteString(";")
			}
			b.WriteString("}")
			delete(seen, te.name)
		}
	}
	if te.nullable {
		b.WriteString("?")
	}
}

// namedType looks a named type up among the plugin's, the app's and the
// registry's.
func (u *unit) namedType(pl *plugin, name string) (pxl.TypeSpec, bool) {
	if spec, ok := u.declared(pl, name); ok {
		return spec, true
	}
	spec, ok := u.types.base[name]
	return spec, ok
}

// checkEntryStorage checks a state entry's persistence and migration and
// records what devices store (STA-003, STA-040); run is set for run
// variables, which only live in memory.
func (u *unit) checkEntryStorage(pl *plugin, st *schema.StateEntry, e *stateEntry, te *texpr, file, ptr string, run bool) {
	if (run || st.Computed != nil) && stored(e.persistence) {
		what := "a computed state entry"
		if run {
			what = "a run variable"
		}
		u.report(plxerr.StatePersistenceNotAllowed, file, ptr+"/persistence", "%s (%q) lives only in memory", what, st.Name)
		e.persistence = fbs.PersistenceMemory
	}
	if st.Computed != nil {
		u.useRevision(stateComputedFeature, stateRuntimes, 1, vctx{file: file, ptr: ptr + "/computed", pl: pl})
	}
	if stored(e.persistence) {
		e.fingerprint = u.typeFingerprint(pl, te)
		u.useRevision(statePersistenceFeature, stateRuntimes, 1, vctx{file: file, ptr: ptr + "/persistence", pl: pl})
	}
	if m := st.Migration; m != nil {
		u.checkMigration(pl, st, e, te, file, ptr)
	}
	if stored(e.persistence) {
		u.stored[pl] = append(u.stored[pl], StoredEntry{
			ID: uuidString(uuidBytes(st.ID)), Name: st.Name, Type: te.String(), Fingerprint: e.fingerprint,
			MigrationFrom: e.migrationFrom, Reset: e.migrationReset, File: file, Ptr: ptr,
		})
	}
}

// checkMigration checks a stored entry's migration: a reset, or an
// expression over `previous` of a type other than the entry's (STA-040).
func (u *unit) checkMigration(pl *plugin, st *schema.StateEntry, e *stateEntry, te *texpr, file, ptr string) {
	m, mptr := st.Migration, ptr+"/migration"
	switch {
	case !stored(e.persistence):
		u.report(plxerr.StateMigrationInvalid, file, mptr, "state %q is not stored, so it has nothing to migrate", st.Name)
	case m.Reset != nil:
		if !*m.Reset {
			u.report(plxerr.StateMigrationInvalid, file, mptr+"/reset", "a migration resets with reset: true")
			return
		}
		e.migrationReset = true
	default:
		from, err := parseTypeExpr(m.From)
		if err != nil {
			return // reported by typecheck
		}
		fp := u.typeFingerprint(pl, from)
		if fp == e.fingerprint {
			u.report(plxerr.StateMigrationInvalid, file, mptr+"/from", "state %q already has the type %s", st.Name, m.From)
			return
		}
		c := vctx{file: file, ptr: mptr + "/value", pl: pl, code: plxerr.ValueTypeMismatch}
		if v := u.exprValue(c, te); v != nil {
			e.migrationFrom, e.migrationType, e.migration = fp, from.String(), &expr{prog: v.prog}
		}
	}
}

// compileMigration type-checks a migration's expression, which reads the
// stored value as `previous` of type `from`.
func (t *typer) compileMigration(pl *plugin, st *schema.StateEntry, s *scope, from, file, ptr string) {
	m := st.Migration
	if m == nil || m.Value == nil || m.From == "" {
		return
	}
	if te := t.u.checkType(pl, m.From, file, ptr+"/migration/from"); te != nil {
		t.compile(m.Value.Expr, s.with("previous", te.String()), from, file, ptr+"/migration/value")
	}
}

// stateDecl finds the declaration of the entry a state path names in a
// graph's scope.
func (u *unit) stateDecl(g *graph, root, name string) *schema.StateEntry {
	var entries []schema.StateEntry
	switch root {
	case "app":
		entries = u.project.App.Doc.State
	case "plugin":
		if g.plugin != nil {
			entries = g.plugin.doc.State
		}
	case "page":
		if g.page != nil {
			entries = g.page.doc.State
		}
	case "component":
		comps := u.shared
		if g.plugin != nil {
			comps = g.plugin.components
		}
		for _, c := range comps {
			if c.file == g.file {
				entries = c.doc.State
			}
		}
	case "run":
		if g.doc != nil {
			entries = g.doc.State
		}
	}
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	return nil
}

// checkStateWrite checks a setState, patchState or resetState step: the
// entry is not computed, a patch names an object, and the bundle requires
// state.write (STA-002, STA-004).
func (u *unit) checkStateWrite(g *graph, action string, input map[string]json.RawMessage, ptr string) {
	u.useRevision(stateWriteFeature, stateRuntimes, 1, vctx{file: g.file, ptr: ptr + "/action", pl: g.plugin})
	root, name, ok := strings.Cut(literalString(input["path"]), ".")
	if !ok {
		return
	}
	d := u.stateDecl(g, root, name)
	if d == nil {
		return // reported by its reference
	}
	pptr := ptr + plxerr.Pointer("input", "path")
	if d.Computed != nil {
		u.report(plxerr.StateEntryReadOnly, g.file, pptr, "state %s.%s is computed, so %s cannot write it", root, name, action)
		return
	}
	if action == "patchState" {
		if _, ok := u.patchFields(g.plugin, d.Type); !ok {
			u.report(plxerr.StatePatchNotObject, g.file, pptr, "state %s.%s is a %s, not an object to patch", root, name, d.Type)
		}
	}
}

// patchFields returns the fields of a type patchState can merge into: a
// declared object type, not null.
func (u *unit) patchFields(pl *plugin, typ string) (map[string]string, bool) {
	te, err := parseTypeExpr(typ)
	if err != nil || te.nullable || te.name == "list" || te.name == "map" || primitives[te.name] {
		return nil, false
	}
	spec, ok := u.declared(pl, te.name)
	if !ok || spec.Fields == nil {
		return nil, false
	}
	return spec.Fields, true
}

// patchValue checks a patchState patch: an object literal gives any of
// the entry's fields, each of its type; a binding gives the whole object.
func (u *unit) patchValue(g *graph, raw json.RawMessage, c vctx) *value {
	st := g.steps[stepIndex(c.ptr)]
	path := literalString(st.Input["path"])
	te := statePathType(g, path)
	if te == nil {
		return u.inferred(c, raw)
	}
	fields, ok := u.patchFields(g.plugin, te.String())
	if !ok {
		return nil // reported by checkStateWrite
	}
	v, ok := decodeJSON(raw)
	if !ok {
		u.report(c.code, c.file, c.ptr, "not a JSON value")
		return nil
	}
	obj, isObj := v.(map[string]any)
	if !isObj || isBinding(obj) {
		return u.check(c, v, te)
	}
	out := &value{kind: fbs.ValueKindObject}
	for _, k := range sortedKeys(obj) {
		ft, known := fields[k]
		if !known {
			u.report(c.code, c.file, c.ptr+plxerr.Pointer(k), "%s has no field %q", te, k)
			return nil
		}
		fte, err := parseTypeExpr(ft)
		if err != nil {
			return nil
		}
		x := u.check(c.at(k), obj[k], fte)
		if x == nil {
			return nil
		}
		out.entries = append(out.entries, entry{key: k, value: x})
	}
	return out
}

// storedOf returns the stored entries of each bundle, by bundle key.
func (u *unit) storedOf() map[string][]StoredEntry {
	out := map[string][]StoredEntry{}
	for pl, es := range u.stored {
		key := ""
		if pl != nil {
			key = pl.doc.Key
		}
		out[key] = es
	}
	return out
}

// StoredState lists the stored state entries a compiled bundle declares:
// its app, plugin, page and component state that is session, persisted
// or secure (STA-040). Publishing reads the previous release's bundle
// with it.
func StoredState(data []byte) (entries []StoredEntry, err error) {
	defer func() {
		if r := recover(); r != nil {
			entries, err = nil, fmt.Errorf("compiler: malformed bundle: %v", r)
		}
	}()
	b, err := bundle.Read(data, bundle.ReadOptions{Limits: limits.Defaults(), Supports: func(string) bool { return true }})
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	var strs []string
	for _, s := range b.Sections {
		if s.Kind == bundle.SectionStrings {
			st := fbs.GetRootAsStrings(s.Data, 0)
			for i := range st.StringsLength() {
				strs = append(strs, string(st.Strings(i)))
			}
		}
	}
	str := func(i uint32) string {
		if int(i) < len(strs) {
			return strs[i]
		}
		return ""
	}
	add := func(n int, at func(*fbs.StateEntry, int) bool) {
		var e fbs.StateEntry
		for i := range n {
			if !at(&e, i) || e.Persistence() == fbs.PersistenceMemory {
				continue
			}
			var id [16]byte
			if u := e.Id(nil); u != nil {
				id = uuidOfHalves(u.Hi(), u.Lo())
			}
			entries = append(entries, StoredEntry{
				ID: uuidString(id), Name: str(e.Name()), Type: str(e.Type()), Fingerprint: str(e.Fingerprint()),
				MigrationFrom: str(e.MigrationFrom()), Reset: e.MigrationReset(),
			})
		}
	}
	for _, s := range b.Sections {
		switch s.Kind {
		case bundle.SectionSchemas:
			sc := fbs.GetRootAsSchemas(s.Data, 0)
			add(sc.StateLength(), sc.State)
		case bundle.SectionPage:
			pg := fbs.GetRootAsPage(s.Data, 0)
			add(pg.StateLength(), pg.State)
		case bundle.SectionComponent:
			c := fbs.GetRootAsComponent(s.Data, 0)
			add(c.StateLength(), c.State)
		}
	}
	slices.SortFunc(entries, func(a, b StoredEntry) int { return strings.Compare(a.ID, b.ID) })
	return entries, nil
}

// CheckStoredState compares the stored state of a bundle with the same
// bundle's in the previous release (STA-040): an entry whose type changed
// needs a migration from the previous type (PLX-1143, PLX-1144) or a
// reset. Entries added, removed or no longer stored need nothing.
func CheckStoredState(previous, current []StoredEntry) plxerr.Diagnostics {
	prev := map[string]StoredEntry{}
	for _, e := range previous {
		prev[e.ID] = e
	}
	var out plxerr.Diagnostics
	for _, e := range current {
		p, ok := prev[e.ID]
		if !ok || p.Fingerprint == "" || p.Fingerprint == e.Fingerprint || e.Reset {
			continue
		}
		loc := plxerr.Location{File: e.File, Path: e.Ptr}
		switch e.MigrationFrom {
		case "":
			out = append(out, plxerr.NewDiagnostic(plxerr.StateMigrationRequired, loc,
				"state %q was a %s in the previous release and is a %s now; declare a migration from %s or reset it", e.Name, p.Type, e.Type, p.Type))
		case p.Fingerprint:
		default:
			loc.Path += "/migration/from"
			out = append(out, plxerr.NewDiagnostic(plxerr.StateMigrationMismatch, loc,
				"state %q was a %s in the previous release; its migration reads another type", e.Name, p.Type))
		}
	}
	out.Sort()
	return out
}

// uuidOfHalves joins the two big-endian halves of an fbs.Uuid.
func uuidOfHalves(hi, lo uint64) [16]byte {
	var id [16]byte
	binary.BigEndian.PutUint64(id[:8], hi)
	binary.BigEndian.PutUint64(id[8:], lo)
	return id
}
