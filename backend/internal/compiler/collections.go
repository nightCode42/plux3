// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// dbFeature is required by a bundle that declares a collection or runs a
// local database action, so an older runtime refuses the bundle instead of
// failing every write (BND-008, ADR-0049). The local database first runs in
// runtime 0.3.0.
const dbFeature = "db"

// dbRuntimes lists the runtime each revision of dbFeature first shipped in.
var dbRuntimes = []string{"0.3.0"}

// dbActions are the actions that read or write the local database.
var dbActions = map[string]bool{
	"dbInsert": true, "dbUpdate": true, "dbUpsert": true, "dbDelete": true, "dbQuery": true,
	"kvGet": true, "kvSet": true, "kvRemove": true,
}

// recordScope adds `record` to the scope of a dbQuery condition: an object
// with the fields of the collection the step names (DB-006).
func (t *typer) recordScope(g *graph, st schema.Step, s *scope) *scope {
	c := t.u.collectionOf(g.plugin, literalString(st.Input["collection"]))
	if c == nil {
		return s // reported by the collection reference
	}
	var name strings.Builder
	name.WriteString("PluxRecord")
	for _, part := range strings.Split(c.Key, "-") {
		name.WriteString(upperFirst(part))
	}
	return s.with("record", name.String()).withTypes(map[string]pxl.TypeSpec{name.String(): objectType(recordFields(c))})
}

// emptyValued are the types a reset can fill with an empty value.
var emptyValued = map[string]bool{"string": true, "int": true, "double": true, "bool": true, "list": true, "map": true}

// StoredField is a field of a collection as a bundle declares it.
type StoredField struct {
	Name string
	Type string
}

// StoredMigration is a collection's plan from one version to the next.
type StoredMigration struct {
	// From is the version the plan starts from.
	From int
	// Rename maps new field names to the names they had.
	Rename map[string]string
	// Drop and Reset name fields of the older version.
	Drop  []string
	Reset []string
}

// StoredCollection is a local collection as a bundle declares it: what
// devices store, and what publishing compares with the previous release
// (DB-005).
type StoredCollection struct {
	// ID is the collection's UUID, which names its storage.
	ID  string
	Key string
	// Version is the schema version, 1 when the document omits it.
	Version    int
	Fields     []StoredField
	PrimaryKey []string
	Indexes    [][]string
	Migrations []StoredMigration
	// File and Ptr locate the declaration; empty for collections read from
	// a bundle.
	File string
	Ptr  string
}

// CollectionSet is the collections of one bundle and the IDs of those it
// dropped.
type CollectionSet struct {
	Collections []StoredCollection
	Dropped     []string
}

// storedCollection converts a declared collection.
func storedCollection(c schema.Collection, file, ptr string) StoredCollection {
	out := StoredCollection{ID: c.ID, Key: c.Key, Version: 1, PrimaryKey: c.PrimaryKey, Indexes: c.Indexes, File: file, Ptr: ptr}
	if c.Version != nil {
		out.Version = int(*c.Version)
	}
	for _, f := range c.Fields {
		out.Fields = append(out.Fields, StoredField{Name: f.Name, Type: f.Type})
	}
	for _, m := range c.Migrations {
		out.Migrations = append(out.Migrations, StoredMigration{From: int(m.From), Rename: m.Rename, Drop: m.Drop, Reset: m.Reset})
	}
	return out
}

// collectionVersion is the version a collection declares.
func collectionVersion(c schema.Collection) int64 {
	if c.Version != nil {
		return *c.Version
	}
	return 1
}

// checkCollectionDecl checks what the fields' shape cannot: primary key
// types, migration plans, dropped collections and the count (DB-004,
// DB-005), and requires the db feature.
func (u *unit) checkCollectionDecl(pl *plugin, cols []schema.Collection, dropped []string, file string) {
	set := CollectionSet{Dropped: slices.Clone(dropped)}
	declared := map[string]bool{}
	for i, c := range cols {
		ptr := plxerr.Pointer("collections", strconv.Itoa(i))
		u.useRevision(dbFeature, dbRuntimes, 1, vctx{file: file, ptr: ptr, pl: pl})
		declared[c.ID] = true
		u.checkCollectionKey(pl, c, file, ptr)
		u.checkCollectionPlans(pl, c, file, ptr)
		set.Collections = append(set.Collections, storedCollection(c, file, ptr))
	}
	for i, id := range dropped {
		if declared[id] {
			u.report(plxerr.CollectionPlanInvalid, file, plxerr.Pointer("droppedCollections", strconv.Itoa(i)), "collection %s is declared, so it cannot also be dropped", id)
		}
	}
	if limit := u.opts.Limits.Get(limits.DBCollectionsPerPlugin); int64(len(cols)) > limit {
		u.report(plxerr.LimitExceeded, file, "/collections", "%d collections, over db.collectionsPerPlugin = %d", len(cols), limit)
	}
	if len(cols) > 0 || len(dropped) > 0 {
		u.collections[pl] = set
	}
}

// checkCollectionKey checks that primary key fields are non-null strings or
// ints (DB-004).
func (u *unit) checkCollectionKey(pl *plugin, c schema.Collection, file, ptr string) {
	types := map[string]string{}
	for _, f := range c.Fields {
		types[f.Name] = f.Type
	}
	for j, k := range c.PrimaryKey {
		t, ok := types[k]
		if !ok {
			continue // reported by checkCollections
		}
		if te, err := parseTypeExpr(t); err == nil && !te.nullable && (te.name == "string" || te.name == "int") {
			continue
		}
		u.report(plxerr.CollectionKeyTypeInvalid, file, ptr+plxerr.Pointer("primaryKey", strconv.Itoa(j)), "primary key field %q of collection %q is %s, not a non-null string or int", k, c.Key, t)
	}
}

// checkCollectionPlans checks a collection's migration plans on their own
// (DB-005): versions, and that they name fields they can act on.
func (u *unit) checkCollectionPlans(pl *plugin, c schema.Collection, file, ptr string) {
	version := collectionVersion(c)
	types := map[string]string{}
	for _, f := range c.Fields {
		types[f.Name] = f.Type
	}
	seen := map[int64]bool{}
	for i, m := range c.Migrations {
		mptr := ptr + plxerr.Pointer("migrations", strconv.Itoa(i))
		bad := func(sub, format string, args ...any) {
			u.report(plxerr.CollectionPlanInvalid, file, mptr+sub, format, args...)
		}
		if m.From >= version {
			bad("/from", "a migration from version %d cannot lead to version %d", m.From, version)
		}
		if seen[m.From] {
			bad("/from", "two migrations start from version %d", m.From)
		}
		seen[m.From] = true
		for j, d := range m.Drop {
			if _, still := types[d]; still {
				bad(plxerr.Pointer("drop", strconv.Itoa(j)), "field %q is dropped but collection %q still declares it", d, c.Key)
			}
		}
		for j, r := range m.Reset {
			t, ok := types[r]
			if !ok {
				bad(plxerr.Pointer("reset", strconv.Itoa(j)), "collection %q has no field %q to reset", c.Key, r)
				continue
			}
			if te, err := parseTypeExpr(t); err == nil && !te.nullable && !emptyValued[te.name] {
				bad(plxerr.Pointer("reset", strconv.Itoa(j)), "field %q is a non-null %s, which has no empty value to reset to; make it nullable", r, t)
			}
		}
		for _, to := range sortedKeys(m.Rename) {
			if _, ok := types[to]; !ok {
				bad(plxerr.Pointer("rename", to), "collection %q has no field %q", c.Key, to)
			}
		}
	}
}

// collectionOf finds the collection a key names for a plugin: its own,
// then the app's (DB-004).
func (u *unit) collectionOf(pl *plugin, key string) *schema.Collection {
	if pl != nil {
		for i := range pl.doc.Collections {
			if pl.doc.Collections[i].Key == key {
				return &pl.doc.Collections[i]
			}
		}
	}
	cols := u.project.App.Doc.Collections
	for i := range cols {
		if cols[i].Key == key {
			return &cols[i]
		}
	}
	return nil
}

// recordFields lists a collection's fields as an object type's fields:
// what `record` is in a dbQuery condition.
func recordFields(c *schema.Collection) [][2]string {
	out := make([][2]string, len(c.Fields))
	for i, f := range c.Fields {
		out[i] = [2]string{f.Name, f.Type}
	}
	return out
}

// collectionsOf returns the collections of each bundle, by bundle key.
func (u *unit) collectionsOf() map[string]CollectionSet {
	out := map[string]CollectionSet{}
	for pl, set := range u.collections {
		key := ""
		if pl != nil {
			key = pl.doc.Key
		}
		out[key] = set
	}
	return out
}

// StoredCollections lists the collections a compiled bundle declares and
// the IDs it drops (DB-005). Publishing reads the previous release's
// bundle with it.
func StoredCollections(data []byte) (set CollectionSet, err error) {
	defer func() {
		if r := recover(); r != nil {
			set, err = CollectionSet{}, fmt.Errorf("compiler: malformed bundle: %v", r)
		}
	}()
	b, err := bundle.Read(data, bundle.ReadOptions{Limits: limits.Defaults(), Supports: func(string) bool { return true }})
	if err != nil {
		return CollectionSet{}, fmt.Errorf("compiler: %w", err)
	}
	strs := bundleStrings(b)
	str := func(i uint32) string {
		if int(i) < len(strs) {
			return strs[i]
		}
		return ""
	}
	for _, s := range b.Sections {
		if s.Kind != bundle.SectionSchemas {
			continue
		}
		sc := fbs.GetRootAsSchemas(s.Data, 0)
		var c fbs.Collection
		for i := range sc.CollectionsLength() {
			if sc.Collections(&c, i) {
				set.Collections = append(set.Collections, readCollection(&c, str))
			}
		}
		var id fbs.Uuid
		for i := range sc.DroppedCollectionsLength() {
			if sc.DroppedCollections(&id, i) {
				set.Dropped = append(set.Dropped, uuidString(uuidOfHalves(id.Hi(), id.Lo())))
			}
		}
	}
	slices.SortFunc(set.Collections, func(a, b StoredCollection) int { return strings.Compare(a.ID, b.ID) })
	slices.Sort(set.Dropped)
	return set, nil
}

// readCollection reads one collection table.
func readCollection(c *fbs.Collection, str func(uint32) string) StoredCollection {
	var id [16]byte
	if u := c.Id(nil); u != nil {
		id = uuidOfHalves(u.Hi(), u.Lo())
	}
	out := StoredCollection{ID: uuidString(id), Key: str(c.Key()), Version: max(1, int(c.Version()))}
	var p fbs.Param
	for i := range c.FieldsLength() {
		if c.Fields(&p, i) {
			out.Fields = append(out.Fields, StoredField{Name: str(p.Name()), Type: str(p.Type())})
		}
	}
	for i := range c.PrimaryKeyLength() {
		out.PrimaryKey = append(out.PrimaryKey, str(c.PrimaryKey(i)))
	}
	var ix fbs.Index
	for i := range c.IndexesLength() {
		if !c.Indexes(&ix, i) {
			continue
		}
		var fields []string
		for j := range ix.FieldsLength() {
			fields = append(fields, str(ix.Fields(j)))
		}
		out.Indexes = append(out.Indexes, fields)
	}
	var m fbs.CollectionMigration
	for i := range c.MigrationsLength() {
		if !c.Migrations(&m, i) {
			continue
		}
		sm := StoredMigration{From: int(m.From())}
		var r fbs.FieldRename
		for j := range m.RenameLength() {
			if m.Rename(&r, j) {
				if sm.Rename == nil {
					sm.Rename = map[string]string{}
				}
				sm.Rename[str(r.To())] = str(r.From())
			}
		}
		for j := range m.DropLength() {
			sm.Drop = append(sm.Drop, str(m.Drop(j)))
		}
		for j := range m.ResetLength() {
			sm.Reset = append(sm.Reset, str(m.Reset(j)))
		}
		out.Migrations = append(out.Migrations, sm)
	}
	return out
}

// sameSchema reports whether two versions of a collection store the same
// fields, types, key and indexes.
func sameSchema(a, b StoredCollection) bool {
	return slices.Equal(a.Fields, b.Fields) && slices.Equal(a.PrimaryKey, b.PrimaryKey) &&
		slices.EqualFunc(a.Indexes, b.Indexes, slices.Equal)
}

// CheckCollections compares the collections of a bundle with the same
// bundle's in the previous release (DB-005): a changed schema raises the
// version; a change that loses data needs a plan, and every plan that
// deletes data is a warning the publisher acknowledges (SRV-051).
func CheckCollections(previous, current CollectionSet) plxerr.Diagnostics {
	prev := map[string]StoredCollection{}
	for _, p := range previous.Collections {
		prev[p.ID] = p
	}
	now := map[string]bool{}
	var out plxerr.Diagnostics
	for _, c := range current.Collections {
		now[c.ID] = true
		if p, ok := prev[c.ID]; ok {
			out = append(out, checkCollection(p, c)...)
		}
	}
	for _, p := range previous.Collections {
		if now[p.ID] {
			continue
		}
		if slices.Contains(current.Dropped, p.ID) {
			out = append(out, plxerr.NewDiagnostic(plxerr.CollectionDestructive, plxerr.Location{},
				"collection %q is dropped: devices delete its records", p.Key))
			continue
		}
		out = append(out, plxerr.NewDiagnostic(plxerr.CollectionPlanRequired, plxerr.Location{},
			"collection %q was in the previous release and is gone; list its ID %s in droppedCollections to delete its data", p.Key, p.ID))
	}
	out.Sort()
	return out
}

// checkCollection compares one collection with its previous release.
func checkCollection(p, c StoredCollection) plxerr.Diagnostics {
	loc := plxerr.Location{File: c.File, Path: c.Ptr}
	switch {
	case c.Version < p.Version:
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.CollectionVersionInvalid, loc,
			"collection %q is at version %d, below %d of the previous release", c.Key, c.Version, p.Version)}
	case c.Version == p.Version:
		if sameSchema(p, c) {
			return nil
		}
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.CollectionVersionInvalid, loc,
			"collection %q changed since the previous release but is still at version %d; raise its version", c.Key, c.Version)}
	case !slices.Equal(p.PrimaryKey, c.PrimaryKey):
		return plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.CollectionKeyChanged, loc,
			"collection %q had the primary key %v and has %v now", c.Key, p.PrimaryKey, c.PrimaryKey)}
	}
	return checkMigrated(p, c, loc)
}

// checkMigrated simulates the plans from the previous version to the
// current one on the previous fields, as a device does, and reports what
// is left unexplained.
func checkMigrated(p, c StoredCollection, loc plxerr.Location) plxerr.Diagnostics {
	fields := map[string]string{}
	for _, f := range p.Fields {
		fields[f.Name] = f.Type
	}
	resets := map[string]bool{}
	var deleted []string
	plans := slices.SortedFunc(slices.Values(c.Migrations), func(a, b StoredMigration) int { return a.From - b.From })
	for _, m := range plans {
		if m.From < p.Version || m.From >= c.Version {
			continue
		}
		for _, to := range slices.Sorted(maps.Keys(m.Rename)) {
			if t, ok := fields[m.Rename[to]]; ok {
				delete(fields, m.Rename[to])
				fields[to] = t
			}
		}
		for _, d := range m.Drop {
			if _, ok := fields[d]; ok {
				delete(fields, d)
				deleted = append(deleted, d)
			}
		}
		for _, r := range m.Reset {
			resets[r] = true
			deleted = append(deleted, r)
		}
	}
	var out plxerr.Diagnostics
	need := func(format string, args ...any) {
		out = append(out, plxerr.NewDiagnostic(plxerr.CollectionPlanRequired, loc, format, args...))
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !slices.ContainsFunc(c.Fields, func(f StoredField) bool { return f.Name == name }) {
			need("field %q of collection %q is gone; declare a migration from version %d that drops or renames it", name, c.Key, p.Version)
		}
	}
	for _, f := range c.Fields {
		old, existed := fields[f.Name]
		switch {
		case resets[f.Name]:
		case !existed && !strings.HasSuffix(f.Type, "?"):
			need("field %q is new in collection %q and not nullable; make it nullable or declare a migration that resets it", f.Name, c.Key)
		case existed && old != f.Type && old+"?" != f.Type:
			need("field %q of collection %q changes from %s to %s; declare a migration that resets it", f.Name, c.Key, old, f.Type)
		}
	}
	if len(deleted) > 0 && !out.HasErrors() {
		slices.Sort(deleted)
		out = append(out, plxerr.NewDiagnostic(plxerr.CollectionDestructive, loc,
			"collection %q: migrating from version %d deletes the values of %s", c.Key, p.Version, strings.Join(slices.Compact(deleted), ", ")))
	}
	return out
}
