// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// Op is one RFC 6902 operation. Value is the JSON value of add, replace
// and test, already parsed.
type Op struct {
	Op    string
	Path  string
	From  string
	Value any
}

// patchError is the refusal of a patch that does not apply.
func patchError(i int, format string, args ...any) error {
	return plxerr.New(plxerr.InvalidStructure, "patch operation %d: %s", i, fmt.Sprintf(format, args...))
}

// Apply applies a patch to a parsed document and returns the result. The
// patch is atomic: an operation that fails leaves the input untouched,
// because the patch works on a copy (RFC 6902 §5).
func Apply(doc any, ops []Op) (any, error) {
	out := clone(doc)
	for i, op := range ops {
		var err error
		if out, err = applyOp(out, op); err != nil {
			return nil, patchError(i, "%v", err)
		}
	}
	return out, nil
}

// applyOp applies one operation to a document the patch owns.
func applyOp(out any, op Op) (any, error) {
	var err error
	switch op.Op {
	case "add":
		return add(out, op.Path, clone(op.Value))
	case "remove":
		out, _, err = remove(out, op.Path)
		return out, err
	case "replace":
		if op.Path == "" {
			return clone(op.Value), nil
		}
		if out, _, err = remove(out, op.Path); err != nil {
			return nil, err
		}
		return add(out, op.Path, clone(op.Value))
	case "move":
		if op.Path == op.From {
			return out, nil
		}
		if strings.HasPrefix(op.Path, op.From+"/") {
			return nil, fmt.Errorf("cannot move %q into itself", op.From)
		}
		var moved any
		if out, moved, err = remove(out, op.From); err != nil {
			return nil, err
		}
		return add(out, op.Path, moved)
	case "copy":
		v, err := get(out, op.From)
		if err != nil {
			return nil, err
		}
		return add(out, op.Path, clone(v))
	case "test":
		v, err := get(out, op.Path)
		if err != nil {
			return nil, err
		}
		if !equal(v, op.Value) {
			return nil, fmt.Errorf("the value at %q differs", op.Path)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown operation %q", op.Op)
	}
}

// tokens splits a JSON Pointer (RFC 6901).
func tokens(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("%q is not a JSON Pointer", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

// index reads an array index; "-" is the end, allowed only when adding.
func index(token string, n int, adding bool) (int, error) {
	if adding && token == "-" {
		return n, nil
	}
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, fmt.Errorf("%q is not an array index", token)
	}
	i, err := strconv.Atoi(token)
	limit := n - 1
	if adding {
		limit = n
	}
	if err != nil || i < 0 || i > limit {
		return 0, fmt.Errorf("index %q is out of range", token)
	}
	return i, nil
}

// get returns the value at a pointer.
func get(doc any, pointer string) (any, error) {
	parts, err := tokens(pointer)
	if err != nil {
		return nil, err
	}
	cur := doc
	for _, t := range parts {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[t]
			if !ok {
				return nil, fmt.Errorf("%q does not exist", pointer)
			}
			cur = v
		case []any:
			i, err := index(t, len(c), false)
			if err != nil {
				return nil, err
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("%q does not exist", pointer)
		}
	}
	return cur, nil
}

// add sets a value at a pointer, inserting into arrays.
func add(doc any, pointer string, v any) (any, error) {
	parts, err := tokens(pointer)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return v, nil
	}
	parent, err := get(doc, joinPointer(parts[:len(parts)-1]))
	if err != nil {
		return nil, err
	}
	last := parts[len(parts)-1]
	switch p := parent.(type) {
	case map[string]any:
		p[last] = v
		return doc, nil
	case []any:
		i, err := index(last, len(p), true)
		if err != nil {
			return nil, err
		}
		grown := slices.Insert(p, i, v)
		return set(doc, parts[:len(parts)-1], grown)
	default:
		return nil, fmt.Errorf("the parent of %q is not a container", pointer)
	}
}

// remove deletes the value at a pointer and returns it.
func remove(doc any, pointer string) (any, any, error) {
	parts, err := tokens(pointer)
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("the whole document cannot be removed")
	}
	parent, err := get(doc, joinPointer(parts[:len(parts)-1]))
	if err != nil {
		return nil, nil, err
	}
	last := parts[len(parts)-1]
	switch p := parent.(type) {
	case map[string]any:
		v, ok := p[last]
		if !ok {
			return nil, nil, fmt.Errorf("%q does not exist", pointer)
		}
		delete(p, last)
		return doc, v, nil
	case []any:
		i, err := index(last, len(p), false)
		if err != nil {
			return nil, nil, err
		}
		v := p[i]
		shrunk := slices.Delete(slices.Clone(p), i, i+1)
		out, err := set(doc, parts[:len(parts)-1], shrunk)
		return out, v, err
	default:
		return nil, nil, fmt.Errorf("the parent of %q is not a container", pointer)
	}
}

// set replaces the value at a path of tokens; arrays are values, so a
// changed array must be stored back into its parent.
func set(doc any, parts []string, v any) (any, error) {
	if len(parts) == 0 {
		return v, nil
	}
	parent, err := get(doc, joinPointer(parts[:len(parts)-1]))
	if err != nil {
		return nil, err
	}
	last := parts[len(parts)-1]
	switch p := parent.(type) {
	case map[string]any:
		p[last] = v
	case []any:
		i, err := index(last, len(p), false)
		if err != nil {
			return nil, err
		}
		p[i] = v
	}
	return doc, nil
}

// joinPointer renders tokens as a JSON Pointer.
func joinPointer(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// clone deep-copies a parsed JSON value.
func clone(v any) any {
	switch c := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(c))
		for k, x := range c {
			out[k] = clone(x)
		}
		return out
	case []any:
		out := make([]any, len(c))
		for i, x := range c {
			out[i] = clone(x)
		}
		return out
	default:
		return v
	}
}

// equal compares two JSON values by their canonical form, so 1.0 and 1
// are the same number.
func equal(a, b any) bool {
	ca, errA := jcs.Marshal(a)
	cb, errB := jcs.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ca, cb)
}

// Diff returns a patch that turns a into b. Objects are compared key by
// key; an array whose length changed is replaced whole, which is always
// correct and keeps the patch readable.
func Diff(a, b any) []Op {
	var ops []Op
	diff("", a, b, &ops)
	return ops
}

// diff appends the operations that turn a into b at a pointer.
func diff(at string, a, b any, ops *[]Op) {
	if equal(a, b) {
		return
	}
	ma, aObj := a.(map[string]any)
	mb, bObj := b.(map[string]any)
	if aObj && bObj {
		for _, k := range slices.Sorted(maps.Keys(ma)) {
			if _, ok := mb[k]; !ok {
				*ops = append(*ops, Op{Op: "remove", Path: at + joinPointer([]string{k})})
			}
		}
		for _, k := range slices.Sorted(maps.Keys(mb)) {
			p := at + joinPointer([]string{k})
			if va, ok := ma[k]; ok {
				diff(p, va, mb[k], ops)
			} else {
				*ops = append(*ops, Op{Op: "add", Path: p, Value: mb[k]})
			}
		}
		return
	}
	la, aArr := a.([]any)
	lb, bArr := b.([]any)
	if aArr && bArr && len(la) == len(lb) {
		for i := range la {
			diff(at+"/"+strconv.Itoa(i), la[i], lb[i], ops)
		}
		return
	}
	*ops = append(*ops, Op{Op: "replace", Path: at, Value: b})
}
