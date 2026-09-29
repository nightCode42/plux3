// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/nightCode42/plux3/backend/internal/icons"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/pxl/decimal"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// maxJSONInt is the largest integer a literal may write: the exact range
// of I-JSON (document-model.md §3).
const maxJSONInt = 1<<53 - 1

// vctx is where a value is checked.
type vctx struct {
	file string
	ptr  string
	// scope is the expression scope; nil where bindings are not allowed,
	// such as defaults and mocks.
	scope *scope
	pl    *plugin
	// code reports a mismatch: PLX-1106 for props, PLX-1117 otherwise.
	code plxerr.Code
	// from is the referring entity, for the reference graph.
	from string
	// constraints restrict a literal (PLX-1121).
	constraints *registry.Constraints
}

// at returns the context of a nested value.
func (c vctx) at(tokens ...string) vctx {
	c.ptr += plxerr.Pointer(tokens...)
	c.constraints = nil
	return c
}

// decodeJSON decodes a raw value keeping numbers exact.
func decodeJSON(raw json.RawMessage) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// checkRaw checks a raw JSON value against a type expression.
func (u *unit) checkRaw(c vctx, raw json.RawMessage, t *texpr) *value {
	v, ok := decodeJSON(raw)
	if !ok {
		u.report(c.code, c.file, c.ptr, "not a JSON value")
		return nil
	}
	return u.check(c, v, t)
}

// check checks a decoded value against t and returns its encoded form, or
// nil after reporting a problem.
func (u *unit) check(c vctx, v any, t *texpr) *value {
	if obj, ok := v.(map[string]any); ok {
		if t.name == iconType && isBinding(obj) {
			u.report(plxerr.UnknownIcon, c.file, c.ptr, "an icon is written literally, so that its glyph is delivered")
			return nil
		}
		if b := u.binding(c, obj, t); b != nil || isBinding(obj) {
			return b
		}
	}
	if v == nil {
		if t.nullable {
			return &value{kind: fbs.ValueKindNull}
		}
		u.report(c.code, c.file, c.ptr, "null is not a %s", t)
		return nil
	}
	switch {
	case t.name == "list":
		return u.checkList(c, v, t)
	case t.name == "map":
		return u.checkMap(c, v, t)
	case t.name == "asset":
		u.report(c.code, c.file, c.ptr, "an asset is written {\"$asset\": \"<asset-id>\"}")
		return nil
	case t.name == "route":
		return u.checkRoute(c, v)
	case primitives[t.name]:
		return u.checkPrimitive(c, v, t)
	}
	if vt, ok := registry.LookupValueType(t.name); ok {
		return u.checkValueType(c, v, &vt)
	}
	if e, ok := registry.LookupEnum(t.name); ok {
		return u.checkRegistryEnum(c, v, &e)
	}
	if spec, ok := u.declared(c.pl, t.name); ok {
		if spec.Fields == nil {
			return u.checkDeclaredEnum(c, v, t.name, spec.Enum)
		}
		return u.checkObject(c, v, t.name, spec.Fields)
	}
	u.report(plxerr.UnknownType, c.file, c.ptr, "type %q is not declared", t.name)
	return nil
}

// declared looks a type up among the app's and the plugin's.
func (u *unit) declared(pl *plugin, name string) (pxl.TypeSpec, bool) {
	if spec, ok := u.types.app[name]; ok {
		return spec, true
	}
	spec, ok := u.types.plugin[pl][name]
	return spec, ok
}

// isBinding reports whether an object is one of the binding forms.
func isBinding(obj map[string]any) bool {
	for _, k := range []string{"$expr", "$token", "$t", "$asset"} {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

// binding checks a binding (SCH-011); it returns nil when obj is not one.
func (u *unit) binding(c vctx, obj map[string]any, t *texpr) *value {
	if !isBinding(obj) {
		return nil
	}
	if c.scope == nil {
		u.report(c.code, c.file, c.ptr, "only a literal is allowed here")
		return nil
	}
	switch {
	case obj["$expr"] != nil:
		return u.exprValue(c, t)
	case obj["$token"] != nil:
		path, _ := obj["$token"].(string)
		return u.tokenValue(c, path, t)
	case obj["$t"] != nil:
		id, _ := obj["$t"].(string)
		args, _ := obj["args"].(map[string]any)
		return u.translationValue(c, id, args, t)
	default:
		id, _ := obj["$asset"].(string)
		if t.name != "asset" {
			u.report(c.code, c.file, c.ptr, "an asset is not a %s", t)
			return nil
		}
		if _, ok := u.assetIDs[id]; !ok {
			return nil // reported by resolve
		}
		return &value{kind: fbs.ValueKindAsset, uuid: uuidBytes(id)}
	}
}

// exprValue checks a compiled expression against the expected type. An
// int result where a double or decimal is expected is widened by
// compiling the conversion, so the program returns the expected type.
func (u *unit) exprValue(c vctx, t *texpr) *value {
	e, ok := u.exprs[c.file+"#"+c.ptr]
	if !ok {
		u.internalError("expression at %s#%s was not type-checked", c.file, c.ptr)
		return nil
	}
	if e.prog == nil {
		return nil // reported by typecheck
	}
	env, err := u.env(e.scope)
	if err != nil {
		u.internalError("%v", err)
		return nil
	}
	want, err := env.ParseType(t.String())
	if err != nil {
		u.report(plxerr.UnknownType, c.file, c.ptr, "type %s: %v", t, err)
		return nil
	}
	if pxl.Assignable(e.typ, want) {
		return &value{kind: fbs.ValueKindExpr, prog: e.prog}
	}
	if conv := widening(e.typ, want); conv != "" {
		prog, typ, diags := pxl.Compile(conv+"("+e.src+")", env, e.scope.options(pxl.OptionsFrom(u.opts.Limits)), plxerr.Location{File: c.file, Path: c.ptr + "/$expr"})
		if prog != nil && pxl.Assignable(typ, want) {
			return &value{kind: fbs.ValueKindExpr, prog: prog}
		}
		u.diags = append(u.diags, diags...)
	}
	u.report(c.code, c.file, c.ptr+"/$expr", "expected %s, the expression is %s", t, e.typ)
	return nil
}

// widening names the conversion from an int to a double or decimal.
func widening(from, to *pxl.Type) string {
	if from.Kind() != pxl.KindInt || from.Nullable() {
		return ""
	}
	switch to.Kind() {
	case pxl.KindDouble:
		return "double"
	case pxl.KindDecimal:
		return "decimal"
	}
	return ""
}

// tokenTypes maps a design token's $type to the prop type it can bind.
var tokenTypes = map[string]string{
	"color": "color", "dimension": "double", "number": "double", "fontFamily": "string", "fontWeight": "FontWeight",
	"duration": "duration", "typography": "TextStyle", "shadow": "BoxShadow",
}

// tokenValue checks a design-token reference.
func (u *unit) tokenValue(c vctx, path string, t *texpr) *value {
	tok, ok := u.tokens[path]
	if !ok {
		return nil // reported by resolve
	}
	if want := tokenTypes[tok.typ]; want != t.name {
		u.report(c.code, c.file, c.ptr+"/$token", "token %q is a %s token, which cannot be a %s", path, tok.typ, t)
		return nil
	}
	return &value{kind: fbs.ValueKindToken, s: path}
}

// translationValue checks a translation reference and its arguments.
func (u *unit) translationValue(c vctx, id string, args map[string]any, t *texpr) *value {
	key, ok := u.tkeys[id]
	if !ok {
		return nil // reported by resolve
	}
	if t.name != "string" {
		u.report(c.code, c.file, c.ptr, "a translation is a string, not a %s", t)
		return nil
	}
	out := &value{kind: fbs.ValueKindTranslation, uuid: uuidBytes(id)}
	declared := map[string]bool{}
	for _, f := range key.Args {
		declared[f.Name] = true
		arg, present := args[f.Name]
		if !present {
			u.report(c.code, c.file, c.ptr, "translation %q needs argument %q", key.Key, f.Name)
			continue
		}
		te, err := parseTypeExpr(f.Type)
		if err != nil {
			continue // reported where the key is declared
		}
		if v := u.check(c.at("args", f.Name), arg, te); v != nil {
			out.entries = append(out.entries, entry{key: f.Name, value: v})
		}
	}
	for _, name := range sortedKeys(args) {
		if !declared[name] {
			u.report(c.code, c.file, c.ptr+plxerr.Pointer("args", name), "translation %q has no argument %q", key.Key, name)
		}
	}
	sortEntriesByKey(out.entries)
	return out
}

// checkList checks a list literal.
func (u *unit) checkList(c vctx, v any, t *texpr) *value {
	items, ok := v.([]any)
	if !ok {
		u.report(c.code, c.file, c.ptr, "expected a list (%s)", t)
		return nil
	}
	out := &value{kind: fbs.ValueKindList}
	for i, it := range items {
		x := u.check(c.at(strconv.Itoa(i)), it, t.elem)
		if x == nil {
			return nil
		}
		out.items = append(out.items, x)
	}
	return out
}

// checkMap checks a map literal.
func (u *unit) checkMap(c vctx, v any, t *texpr) *value {
	obj, ok := v.(map[string]any)
	if !ok {
		u.report(c.code, c.file, c.ptr, "expected an object (%s)", t)
		return nil
	}
	out := &value{kind: fbs.ValueKindMap}
	for _, k := range sortedKeys(obj) {
		x := u.check(c.at(k), obj[k], t.elem)
		if x == nil {
			return nil
		}
		out.entries = append(out.entries, entry{key: k, value: x})
	}
	return out
}

// checkRoute checks a route name.
func (u *unit) checkRoute(c vctx, v any) *value {
	name, ok := v.(string)
	if !ok {
		u.report(c.code, c.file, c.ptr, "a route is written as its name")
		return nil
	}
	if _, found := u.routes[name]; !found {
		u.report(plxerr.UnknownRoute, c.file, c.ptr, "no page or native route is named %q", name)
		return nil
	}
	return &value{kind: fbs.ValueKindRoute, s: name}
}

// checkPrimitive checks a literal of a primitive type through the PXL
// value parser, which implements the literal forms of the document model.
func (u *unit) checkPrimitive(c vctx, v any, t *texpr) *value {
	typ := &texpr{name: t.name}
	env, err := u.env(&scope{roots: map[string]string{}})
	if err != nil {
		u.internalError("%v", err)
		return nil
	}
	pt, _ := env.ParseType(typ.String())
	if n, ok := v.(json.Number); ok && t.name == "int" {
		if i, err := n.Int64(); err == nil && (i > maxJSONInt || i < -maxJSONInt) {
			u.report(c.code, c.file, c.ptr, "%s is outside the exact range of JSON integers; bind larger values", n)
			return nil
		}
	}
	x, err := pxl.FromJSON(pt, v)
	if err != nil {
		u.report(c.code, c.file, c.ptr, "not a %s: %v", t, err)
		return nil
	}
	out := primitiveValue(x)
	if out.kind == fbs.ValueKindString {
		if max := u.opts.Limits.Get(limits.DocumentStringPropSize); int64(len(out.s)) > max {
			u.report(plxerr.LimitExceeded, c.file, c.ptr, "the string has %d bytes, above document.stringPropSize = %d", len(out.s), max)
			return nil
		}
	}
	u.checkConstraints(c, x)
	return out
}

// primitiveValue converts a PXL value of a primitive type.
func primitiveValue(x pxl.Value) *value {
	switch v := x.(type) {
	case bool:
		i := int64(0)
		if v {
			i = 1
		}
		return &value{kind: fbs.ValueKindBool, i: i}
	case int64:
		return &value{kind: fbs.ValueKindInt, i: v}
	case float64:
		return &value{kind: fbs.ValueKindDouble, d: v}
	case string:
		return &value{kind: fbs.ValueKindString, s: v}
	case decimal.Decimal:
		return &value{kind: fbs.ValueKindDecimal, unscaled: twosComplement(v.Unscaled()), scale: uint32(v.Scale())} //nolint:gosec // G115: scales are bounded by the digits limit.
	case pxl.Money:
		return &value{kind: fbs.ValueKindMoney, unscaled: twosComplement(v.Amount.Unscaled()), scale: uint32(v.Amount.Scale()), s: v.Currency} //nolint:gosec // G115: as above.
	case pxl.Date:
		return &value{kind: fbs.ValueKindDate, i: int64(v)}
	case pxl.DateTime:
		return &value{kind: fbs.ValueKindDateTime, i: v.Millis * 1000, offset: int16(v.Offset)} //nolint:gosec // G115: offsets are within ±1439 minutes.
	case pxl.Duration:
		return &value{kind: fbs.ValueKindDuration, i: int64(v) * 1000}
	case pxl.Color:
		rgba := uint32(v)
		return &value{kind: fbs.ValueKindColor, i: int64(rgba&0xff)<<24 | int64(rgba>>8)}
	}
	return &value{kind: fbs.ValueKindNull}
}

// twosComplement returns the minimal big-endian two's-complement bytes.
func twosComplement(x *big.Int) []byte {
	if x.Sign() >= 0 {
		b := x.Bytes()
		if len(b) > 0 && b[0]&0x80 != 0 {
			b = append([]byte{0}, b...)
		}
		return b
	}
	n := (x.BitLen() + 8) / 8 // one sign bit more than the magnitude needs
	m := new(big.Int).Lsh(big.NewInt(1), uint(8*n))
	b := m.Add(m, x).Bytes()
	for len(b) < n {
		b = append([]byte{0xff}, b...)
	}
	if len(b) > 1 && b[0] == 0xff && b[1]&0x80 != 0 {
		b = b[1:]
	}
	return b
}

// checkConstraints applies a descriptor's constraints to a literal.
func (u *unit) checkConstraints(c vctx, x pxl.Value) {
	k := c.constraints
	if k == nil {
		return
	}
	var num float64
	isNum := true
	switch v := x.(type) {
	case int64:
		num = float64(v)
	case float64:
		num = v
	default:
		isNum = false
	}
	if isNum && (k.Min.Set && num < k.Min.Value || k.Max.Set && num > k.Max.Value) {
		u.report(plxerr.ConstraintViolation, c.file, c.ptr, "%v is outside %s", num, bounds(k.Min, k.Max))
	}
	s, isStr := x.(string)
	if !isStr {
		return
	}
	n := float64(utf8.RuneCountInString(s))
	if k.MinLength.Set && n < k.MinLength.Value || k.MaxLength.Set && n > k.MaxLength.Value {
		u.report(plxerr.ConstraintViolation, c.file, c.ptr, "the length %v is outside %s", n, bounds(k.MinLength, k.MaxLength))
	}
	if k.Pattern != "" {
		if re, err := regexp.Compile(k.Pattern); err == nil && !re.MatchString(s) {
			u.report(plxerr.ConstraintViolation, c.file, c.ptr, "%q does not match %s", s, k.Pattern)
		}
	}
}

// bounds formats a range.
func bounds(lo, hi registry.Bound) string {
	l, h := "-∞", "∞"
	if lo.Set {
		l = strconv.FormatFloat(lo.Value, 'g', -1, 64)
	}
	if hi.Set {
		h = strconv.FormatFloat(hi.Value, 'g', -1, 64)
	}
	return "[" + l + ", " + h + "]"
}

// checkRegistryEnum checks a value of a registry enum, encoded by ID.
func (u *unit) checkRegistryEnum(c vctx, v any, e *registry.Enum) *value {
	name, _ := v.(string)
	ev, ok := e.Value(name)
	if !ok {
		u.report(c.code, c.file, c.ptr, "%v is not a value of %s", v, e.Name)
		return nil
	}
	u.useRevision("enum."+e.Name, e.Runtimes, ev.Revision, c)
	u.deprecation(ev.Deprecated, e.Name+"."+name, c)
	return &value{kind: fbs.ValueKindEnum, i: int64(ev.ID)}
}

// checkDeclaredEnum checks a value of a declared enum, encoded by name
// and position.
func (u *unit) checkDeclaredEnum(c vctx, v any, name string, members []string) *value {
	s, _ := v.(string)
	for i, m := range members {
		if m == s {
			return &value{kind: fbs.ValueKindEnum, i: int64(i), s: s}
		}
	}
	u.report(c.code, c.file, c.ptr, "%v is not a value of %s", v, name)
	return nil
}

// checkValueType checks an object literal or constant of a registry value
// type; fields are keyed by permanent ID (BND-011).
func (u *unit) checkValueType(c vctx, v any, vt *registry.ValueType) *value {
	obj, ok := constantOrObject(v, vt)
	if !ok {
		u.report(c.code, c.file, c.ptr, "expected an object or a constant of %s", vt.Name)
		return nil
	}
	out := &value{kind: fbs.ValueKindObject, valueType: vt.ID}
	u.useRevision("type."+vt.Name, vt.Runtimes, 1, c)
	for _, f := range vt.Fields {
		raw, present := obj[f.Name]
		if !present {
			if f.Required {
				u.report(c.code, c.file, c.ptr, "%s needs field %q", vt.Name, f.Name)
			}
			continue
		}
		if f.Revision > 1 {
			u.useRevision("type."+vt.Name, vt.Runtimes, f.Revision, c.at(f.Name))
		}
		u.deprecation(f.Deprecated, vt.Name+"."+f.Name, c.at(f.Name))
		te, err := parseTypeExpr(f.Type)
		if err != nil {
			u.internalError("registry field %s.%s: %v", vt.Name, f.Name, err)
			return nil
		}
		x := u.check(c.at(f.Name), raw, te)
		if x == nil {
			return nil
		}
		out.entries = append(out.entries, entry{id: f.ID, value: x})
	}
	if name, unknown := unknownField(obj, vt); unknown {
		u.report(c.code, c.file, c.ptr+plxerr.Pointer(name), "%s has no field %q", vt.Name, name)
		return nil
	}
	if vt.Name == iconType && !u.useIcon(c, out, vt) {
		return nil
	}
	return out
}

// enumName is the name of registry enum typ's value id.
func enumName(typ string, id int64) string {
	e, _ := registry.LookupEnum(typ)
	for _, v := range e.Values {
		if int64(v.ID) == id {
			return v.Name
		}
	}
	return ""
}

// iconType is the value type of an icon (THM-005).
const iconType = "IconData"

// useIcon checks that an icon names one of its set's glyphs literally,
// and records it for the icon font of the bundle that uses it (THM-005).
func (u *unit) useIcon(c vctx, v *value, vt *registry.ValueType) bool {
	nameField, _ := vt.Field("name")
	setField, _ := vt.Field("set")
	name, set := "", icons.Material
	for _, e := range v.entries {
		switch {
		case e.id == nameField.ID && e.value.kind == fbs.ValueKindString:
			name = e.value.s
		case e.id == setField.ID && e.value.kind == fbs.ValueKindEnum:
			set = icons.Set(enumName(setField.Type, e.value.i))
		default:
			u.report(plxerr.UnknownIcon, c.file, c.ptr, "an icon is written literally, so that its glyph is delivered")
			return false
		}
	}
	if _, ok := icons.Lookup(set, name); !ok {
		u.report(plxerr.UnknownIcon, c.file, c.ptr+plxerr.Pointer("name"), "the %s icon set has no icon %q", set, name)
		return false
	}
	used := u.icons[c.pl]
	if used == nil {
		used = map[icons.Set]map[string]bool{}
		u.icons[c.pl] = used
	}
	if used[set] == nil {
		used[set] = map[string]bool{}
	}
	used[set][name] = true
	return true
}

// constantOrObject resolves a value type's constant by name, or returns
// the object literal.
func constantOrObject(v any, vt *registry.ValueType) (map[string]any, bool) {
	if name, ok := v.(string); ok {
		for _, k := range vt.Constants {
			if k.Name == name {
				v, _ = decodeJSON(json.RawMessage(k.Value))
				break
			}
		}
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

// unknownField returns the first field of obj that vt does not declare.
func unknownField(obj map[string]any, vt *registry.ValueType) (string, bool) {
	for _, name := range sortedKeys(obj) {
		if _, known := vt.Field(name); !known {
			return name, true
		}
	}
	return "", false
}

// checkObject checks an object literal of a declared type; fields are
// keyed by name.
func (u *unit) checkObject(c vctx, v any, name string, fields map[string]string) *value {
	obj, ok := v.(map[string]any)
	if !ok {
		u.report(c.code, c.file, c.ptr, "expected an object of type %s", name)
		return nil
	}
	out := &value{kind: fbs.ValueKindObject}
	for _, f := range sortedKeys(fields) {
		te, err := parseTypeExpr(fields[f])
		if err != nil {
			return nil
		}
		raw, present := obj[f]
		if !present {
			if !te.nullable {
				u.report(c.code, c.file, c.ptr, "%s needs field %q", name, f)
				return nil
			}
			continue
		}
		x := u.check(c.at(f), raw, te)
		if x == nil {
			return nil
		}
		out.entries = append(out.entries, entry{key: f, value: x})
	}
	for _, k := range sortedKeys(obj) {
		if _, known := fields[k]; !known {
			u.report(c.code, c.file, c.ptr+plxerr.Pointer(k), "%s has no field %q", name, k)
			return nil
		}
	}
	return out
}

// sortEntriesByKey orders entries by key.
func sortEntriesByKey(es []entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j].key < es[j-1].key; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

// deprecation warns about a deprecated member (PLX-1122).
func (u *unit) deprecation(d *registry.Deprecation, what string, c vctx) {
	if d == nil {
		return
	}
	u.report(plxerr.DeprecatedMember, c.file, c.ptr, "%s is deprecated since revision %d: %s", what, d.Revision, d.Message)
}
