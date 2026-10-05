// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// databaseConfig is the configuration of a `database` data source: a
// watched query over a local collection that lists bind to and that
// updates when the collection changes (DB-006).
type databaseConfig struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
	OrderBy    string          `json:"orderBy"`
	Descending bool            `json:"descending"`
	Limit      *int64          `json:"limit"`
	Offset     *int64          `json:"offset"`
}

// filterOps are the operators of a filter's comparisons; the null tests
// take no value.
var filterOps = []string{"eq", "ne", "lt", "le", "gt", "ge", "in", "contains", "startsWith", "isNull", "notNull"}

// maxFilterDepth bounds the nesting of and, or and not.
const maxFilterDepth = 8

// badFunc reports a problem at a pointer below the source's config.
type badFunc func(sub, format string, args ...any)

// checkDatabaseSource validates a database source's configuration against
// the collection it names and requires the db feature (DB-006).
func (u *unit) checkDatabaseSource(pl *plugin, s *schema.DataSource, file, ptr string) {
	cptr := ptr + "/config"
	u.useRevision(dbFeature, dbRuntimes, 1, vctx{file: file, ptr: ptr, pl: pl})
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DatabaseSourceInvalid, file, cptr+sub, format, args...)
	}
	if len(s.Config) == 0 {
		bad("", "database source %q has no config", s.Name)
		return
	}
	var c databaseConfig
	dec := json.NewDecoder(bytes.NewReader(s.Config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		bad("", "database source %q: %v", s.Name, err)
		return
	}
	if te, err := parseTypeExpr(s.Type); err == nil && te.name != "list" {
		u.report(plxerr.DatabaseSourceInvalid, file, ptr+"/type", "database source %q returns records: its type is a list, not %s", s.Name, s.Type)
	}
	col := u.collectionOf(pl, c.Collection)
	if col == nil {
		bad("/collection", "no collection is named %q", c.Collection)
		return
	}
	kinds := map[string]string{}
	for _, f := range col.Fields {
		kinds[f.Name] = f.Type
	}
	if len(c.Filter) > 0 {
		var v any
		if err := json.Unmarshal(c.Filter, &v); err != nil {
			bad("/filter", "the filter is not JSON: %v", err)
		} else {
			checkFilter(v, kinds, 0, "/filter", func(p, msg string) { bad(p, "%s", msg) })
		}
	}
	u.checkDatabaseOrder(&c, col, kinds, bad)
}

// checkDatabaseOrder checks a database source's sort, limit and offset.
func (u *unit) checkDatabaseOrder(c *databaseConfig, col *schema.Collection, kinds map[string]string, bad badFunc) {
	if c.OrderBy != "" {
		t, ok := kinds[c.OrderBy]
		switch {
		case !ok:
			bad("/orderBy", "collection %q has no field %q", col.Key, c.OrderBy)
		case scalarKind(t) == "":
			bad("/orderBy", "field %q (%s) cannot be sorted", c.OrderBy, t)
		}
	}
	if c.Descending && c.OrderBy == "" {
		bad("/descending", "descending needs orderBy")
	}
	if max := u.opts.Limits.Get(limits.DBQueryRows); c.Limit != nil && (*c.Limit < 0 || *c.Limit > max) {
		bad("/limit", "limit %d is outside 0 to db.queryRows = %d", *c.Limit, max)
	}
	if c.Offset != nil && *c.Offset < 0 {
		bad("/offset", "offset %d is negative", *c.Offset)
	}
}

// scalarKind names how the database stores a field type, "" for types it
// cannot sort or order: lists, maps, objects and money.
func scalarKind(t string) string {
	te, err := parseTypeExpr(t)
	if err != nil {
		return ""
	}
	switch te.name {
	case "string", "date", "dateTime", "color", "asset", "route", "decimal":
		return "string"
	case "int", "duration":
		return "int"
	case "double":
		return "real"
	case "bool":
		return "bool"
	}
	return ""
}

// checkFilter checks a filter tree: known fields, operators and values of
// the field's type.
func checkFilter(v any, types map[string]string, depth int, ptr string, bad func(ptr, msg string)) {
	m, ok := v.(map[string]any)
	if !ok {
		bad(ptr, "a filter is an object")
		return
	}
	if depth > maxFilterDepth {
		bad(ptr, fmt.Sprintf("filters nest at most %d levels", maxFilterDepth))
		return
	}
	for _, k := range []string{"and", "or"} {
		if list, has := m[k]; has {
			items, isList := list.([]any)
			if !isList {
				bad(ptr+"/"+k, k+" takes a list of filters")
				return
			}
			for i, it := range items {
				checkFilter(it, types, depth+1, fmt.Sprintf("%s/%s/%d", ptr, k, i), bad)
			}
			return
		}
	}
	if not, has := m["not"]; has {
		checkFilter(not, types, depth+1, ptr+"/not", bad)
		return
	}
	checkComparison(m, types, ptr, bad)
}

// checkComparison checks one comparison of a filter.
func checkComparison(m map[string]any, types map[string]string, ptr string, bad func(ptr, msg string)) {
	field, _ := m["field"].(string)
	op, _ := m["op"].(string)
	t, known := types[field]
	switch {
	case !known:
		bad(ptr+"/field", fmt.Sprintf("no field %q in the collection", field))
		return
	case !slices.Contains(filterOps, op):
		bad(ptr+"/op", fmt.Sprintf("operator %q is not one of %v", op, filterOps))
		return
	}
	kind := scalarKind(t)
	ordered := op == "lt" || op == "le" || op == "gt" || op == "ge"
	text := op == "contains" || op == "startsWith"
	if (ordered && kind == "") || (text && kind != "string") {
		bad(ptr+"/op", fmt.Sprintf("%s does not apply to %s (%s)", op, field, t))
		return
	}
	if op == "isNull" || op == "notNull" {
		return
	}
	values := []any{m["value"]}
	if op == "in" {
		list, isList := m["value"].([]any)
		if !isList {
			bad(ptr+"/value", "in takes a list")
			return
		}
		values = list
	}
	for _, val := range values {
		nullOK := val == nil && (op == "eq" || op == "ne")
		if kind != "" && !valueFits(val, kind) && !nullOK {
			bad(ptr+"/value", fmt.Sprintf("the value compared with %s is not a %s", field, t))
			return
		}
	}
}

// valueFits reports whether a JSON value is of a scalar kind.
func valueFits(v any, kind string) bool {
	switch kind {
	case "string":
		_, ok := v.(string)
		return ok
	case "int":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "real":
		_, ok := v.(float64)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	}
	return false
}
