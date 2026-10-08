// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg

import (
	"maps"
	"reflect"
	"slices"
)

// diff returns the RFC 7396 merge patch that turns from into to, and
// whether there is any difference. Objects are compared key by key; a key
// that disappeared becomes null; arrays and scalars replace the whole
// value. The trees are those of the jcs package (nil, bool, json.Number,
// float64, string, []any, map[string]any).
//
// A merge patch cannot set a value to null, so the trees must not hold
// null: the device document never does.
func diff(from, to any) (any, bool) {
	fromObject, fromIsObject := from.(map[string]any)
	toObject, toIsObject := to.(map[string]any)
	if !fromIsObject || !toIsObject {
		return to, !reflect.DeepEqual(from, to)
	}
	patch := map[string]any{}
	for _, k := range slices.Sorted(maps.Keys(fromObject)) {
		if _, kept := toObject[k]; !kept {
			patch[k] = nil
		}
	}
	for _, k := range slices.Sorted(maps.Keys(toObject)) {
		old, existed := fromObject[k]
		if !existed {
			patch[k] = toObject[k]
			continue
		}
		if sub, changed := diff(old, toObject[k]); changed {
			patch[k] = sub
		}
	}
	return patch, len(patch) > 0
}
