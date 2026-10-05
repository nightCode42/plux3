// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"math/big"
	"slices"
	"strconv"
	"strings"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/pxl/phone"
	"github.com/nightCode42/plux3/backend/internal/pxl/regex"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// formsFeature is required by a bundle that declares a form or runs a
// form action (STA-020, ADR-0047): a runtime without forms refuses the
// bundle instead of rendering fields it cannot validate. Forms first run
// in runtime 0.3.0 (ADR-0040's pattern for navigation.guards).
const formsFeature = "forms"

// formsRuntimes lists the runtime each revision of formsFeature first
// shipped in.
var formsRuntimes = []string{"0.3.0"}

// form is a checked form of a page or component.
type form struct {
	id     [16]byte
	name   string
	fields []*formField
}

// formField is a checked form field.
type formField struct {
	name       string
	typ        string
	initial    *value
	validators []*formValidator
}

// formValidator is a checked validator; maxScale and maxIntegerDigits
// are -1 when absent.
type formValidator struct {
	kind             fbs.ValidatorKind
	message          string
	min, max         *value
	pattern, region  string
	maxScale         int32
	maxIntegerDigits int32
	rule             *pxl.Program
	graph            *graph
	debounceMs       uint32
}

// validatorKinds maps a document validator kind to the bundle's.
var validatorKinds = map[schema.FormValidatorKind]fbs.ValidatorKind{
	schema.FormValidatorKindRequired: fbs.ValidatorKindRequired, schema.FormValidatorKindLength: fbs.ValidatorKindLength,
	schema.FormValidatorKindRange: fbs.ValidatorKindRange, schema.FormValidatorKindRegex: fbs.ValidatorKindRegex,
	schema.FormValidatorKindEmail: fbs.ValidatorKindEmail, schema.FormValidatorKindPhone: fbs.ValidatorKindPhone,
	schema.FormValidatorKindIban: fbs.ValidatorKindIban, schema.FormValidatorKindDateRange: fbs.ValidatorKindDateRange,
	schema.FormValidatorKindDecimalPrecision: fbs.ValidatorKindDecimalPrecision, schema.FormValidatorKindCustom: fbs.ValidatorKindCustom,
	schema.FormValidatorKindAsync: fbs.ValidatorKindAsync,
}

// The parts of a form's state (ADR-0047): `<form>.values.<field>` and
// `<form>.touched.<field>` are what setState may write.
const (
	formValues  = "values"
	formTouched = "touched"
)

// formTypeName is the synthesised type of a form's state; its values,
// errors and flags types add a suffix.
func formTypeName(name string) string { return "PluxForm" + upperFirst(name) }

// formTypes builds the state fields that forms add to their scope's root
// and the types they need: <form> is {values, errors, dirty, touched,
// status, valid, validating}, values holds each field's declared type,
// errors a string? per field and the flags a bool per field.
func (t *typer) formTypes(pl *plugin, forms []schema.Form, file string) ([][2]string, map[string]pxl.TypeSpec) {
	var root [][2]string
	types := map[string]pxl.TypeSpec{}
	for i, f := range forms {
		name := formTypeName(f.Name)
		var values, errs, flags [][2]string
		for j, fd := range f.Fields {
			ptr := plxerr.Pointer("forms", strconv.Itoa(i), "fields", strconv.Itoa(j), "type")
			if te := t.u.checkType(pl, fd.Type, file, ptr); te != nil {
				values = append(values, [2]string{fd.Name, te.String()})
			}
			errs = append(errs, [2]string{fd.Name, "string?"})
			flags = append(flags, [2]string{fd.Name, "bool"})
		}
		types[name+"Values"] = objectType(values)
		types[name+"Errors"] = objectType(errs)
		types[name+"Flags"] = objectType(flags)
		types[name] = objectType([][2]string{
			{formValues, name + "Values"},
			{"errors", name + "Errors"},
			{"dirty", name + "Flags"},
			{formTouched, name + "Flags"},
			{"status", "string"},
			{"valid", "bool"},
			{"validating", "bool"},
		})
		root = append(root, [2]string{f.Name, name})
	}
	return root, types
}

// formHandlers compiles the forms' custom rules, each over `value` (the
// field's value) and `form` (the form's values), and records the payload
// of each asynchronous validator's graph: the field's value. It runs
// before the page's graphs are type-checked, as lifecycle handlers do.
func (t *typer) formHandlers(pl *plugin, pg *page, forms []schema.Form, file, from string, s *scope) {
	for i, f := range forms {
		valuesType := formTypeName(f.Name) + "Values"
		values := s.synth[valuesType]
		for j, fd := range f.Fields {
			te, err := parseTypeExpr(fd.Type)
			if err != nil {
				continue // reported by formTypes
			}
			for k, v := range fd.Validators {
				ptr := plxerr.Pointer("forms", strconv.Itoa(i), "fields", strconv.Itoa(j), "validators", strconv.Itoa(k))
				if v.Rule != nil && v.Kind == schema.FormValidatorKindCustom {
					rs := &scope{
						plugin: pl,
						roots:  map[string]string{"value": te.String(), "form": valuesType},
						synth:  map[string]pxl.TypeSpec{valuesType: values},
						ids:    map[string]map[string]string{},
					}
					t.compile(v.Rule.Expr, rs, from, file, ptr+"/rule")
				}
				if v.Graph != "" && v.Kind == schema.FormValidatorKindAsync {
					if pl == nil {
						t.u.report(plxerr.FormAsyncValidatorInvalid, file, ptr+"/$graph", "a shared component has no graphs to run an asynchronous validator")
						continue
					}
					if g := t.u.graphRef(pl, pg, v.Graph, from, file, ptr+"/$graph"); g != nil {
						t.useGraph(g, te.String(), file, ptr+"/$graph")
					}
				}
			}
		}
	}
}

// graphForms returns the forms a graph's form actions and form state
// paths may name: its page's, or its component's.
func (u *unit) graphForms(g *graph) (root string, forms []schema.Form) {
	switch {
	case g.page != nil:
		return "page", g.page.doc.Forms
	case g.component != nil:
		return "component", g.component.doc.Forms
	}
	comps := u.shared
	if g.plugin != nil {
		comps = g.plugin.components
	}
	for _, c := range comps {
		if c.file == g.file {
			return "component", c.doc.Forms
		}
	}
	return "", nil
}

// formNamed finds a form by name.
func formNamed(forms []schema.Form, name string) *schema.Form {
	for i := range forms {
		if forms[i].Name == name {
			return &forms[i]
		}
	}
	return nil
}

// formStatePath checks a state path that names a form's state; isForm is
// false for any other path. Only setState may write, and only a field's
// value or touched flag (PLX-1167).
func (u *unit) formStatePath(g *graph, path string, c vctx) (v *value, isForm bool) {
	root, rest, _ := strings.Cut(path, ".")
	owner, forms := u.graphForms(g)
	name, sub, _ := strings.Cut(rest, ".")
	f := formNamed(forms, name)
	if f == nil || root != owner {
		return nil, false
	}
	action := g.steps[stepIndex(c.ptr)].Action
	part, field, _ := strings.Cut(sub, ".")
	known := slices.ContainsFunc(f.Fields, func(fd schema.FormField) bool { return fd.Name == field })
	if action != "setState" || (part != formValues && part != formTouched) || !known {
		u.report(plxerr.FormWriteInvalid, c.file, c.ptr, "%s cannot write %s: only setState writes a form field's value or touched flag", action, path)
		return nil, true
	}
	return &value{kind: fbs.ValueKindString, s: path}, true
}

// formRef resolves the form a form action names (ref "form").
func (u *unit) formRef(g *graph, name string) bool {
	_, forms := u.graphForms(g)
	return formNamed(forms, name) != nil
}

// formOutput is the output type of a submitForm step: the form's values,
// or "" when the form is unknown.
func (u *unit) formOutput(g *graph, st schema.Step) string {
	_, forms := u.graphForms(g)
	f := formNamed(forms, literalString(st.Input["form"]))
	if f == nil {
		return ""
	}
	return formTypeName(f.Name) + "Values?"
}

// checkForms checks the forms of a page or component (STA-020): unique
// names, initial values, and each validator's options against its kind
// and its field's type.
func (u *unit) checkForms(pl *plugin, forms []schema.Form, state []schema.StateEntry, file string) []*form {
	if len(forms) == 0 {
		return nil
	}
	u.useRevision(formsFeature, formsRuntimes, 1, vctx{file: file, ptr: "/forms", pl: pl})
	taken := map[string]bool{}
	for _, st := range state {
		taken[st.Name] = true
	}
	var out []*form
	for i, f := range forms {
		ptr := plxerr.Pointer("forms", strconv.Itoa(i))
		if taken[f.Name] {
			u.report(plxerr.FormNameConflict, file, ptr+"/name", "form %q has the name of another form or state entry of its scope", f.Name)
		}
		taken[f.Name] = true
		o := &form{id: uuidBytes(f.ID), name: f.Name}
		fields := map[string]bool{}
		for j, fd := range f.Fields {
			fptr := ptr + plxerr.Pointer("fields", strconv.Itoa(j))
			if fields[fd.Name] {
				u.report(plxerr.FormNameConflict, file, fptr+"/name", "form %q has two fields named %q", f.Name, fd.Name)
			}
			fields[fd.Name] = true
			if ff := u.checkFormField(pl, &fd, file, fptr); ff != nil {
				o.fields = append(o.fields, ff)
			}
		}
		out = append(out, o)
	}
	return out
}

// checkFormField checks a field's initial value and validators.
func (u *unit) checkFormField(pl *plugin, fd *schema.FormField, file, ptr string) *formField {
	te, err := parseTypeExpr(fd.Type)
	if err != nil {
		return nil // reported by formTypes
	}
	out := &formField{name: fd.Name, typ: fd.Type}
	switch {
	case len(fd.Initial) > 0:
		out.initial = u.checkRaw(literalCtx(pl, file, ptr+"/initial"), fd.Initial, te)
	case !te.nullable:
		u.report(plxerr.FormFieldInitialMissing, file, ptr, "field %q has no initial value and its type %s is not nullable", fd.Name, fd.Type)
	}
	for k := range fd.Validators {
		if v := u.checkValidator(pl, &fd.Validators[k], te, file, ptr+plxerr.Pointer("validators", strconv.Itoa(k))); v != nil {
			out.validators = append(out.validators, v)
		}
	}
	return out
}

// validatorOptions lists the options each kind takes.
var validatorOptions = map[schema.FormValidatorKind][]string{
	schema.FormValidatorKindLength:           {"min", "max"},
	schema.FormValidatorKindRange:            {"min", "max"},
	schema.FormValidatorKindDateRange:        {"min", "max"},
	schema.FormValidatorKindRegex:            {"pattern"},
	schema.FormValidatorKindPhone:            {"region"},
	schema.FormValidatorKindDecimalPrecision: {"maxScale", "maxIntegerDigits"},
	schema.FormValidatorKindCustom:           {"rule"},
	schema.FormValidatorKindAsync:            {"$graph", "debounceMs"},
}

// presentOptions lists the options a validator carries.
func presentOptions(v *schema.FormValidator) []string {
	var out []string
	for _, o := range []struct {
		name string
		set  bool
	}{
		{"min", len(v.Min) > 0},
		{"max", len(v.Max) > 0},
		{"pattern", v.Pattern != ""},
		{"region", v.Region != ""},
		{"maxScale", v.MaxScale != nil},
		{"maxIntegerDigits", v.MaxIntegerDigits != nil},
		{"rule", v.Rule != nil},
		{"$graph", v.Graph != ""},
		{"debounceMs", v.DebounceMs != nil},
	} {
		if o.set {
			out = append(out, o.name)
		}
	}
	return out
}

// validatorTypes lists the field types each kind applies to; a kind not
// listed applies to every type.
var validatorTypes = map[schema.FormValidatorKind][]string{
	schema.FormValidatorKindLength:           {"string", "list"},
	schema.FormValidatorKindRange:            {"int", "double", "decimal", "string"},
	schema.FormValidatorKindDecimalPrecision: {"int", "double", "decimal", "string"},
	schema.FormValidatorKindDateRange:        {"date", "dateTime"},
	schema.FormValidatorKindRegex:            {"string"},
	schema.FormValidatorKindEmail:            {"string"},
	schema.FormValidatorKindPhone:            {"string"},
	schema.FormValidatorKindIban:             {"string"},
}

// checkValidator checks one validator against its kind and the field's
// type, and lowers it.
func (u *unit) checkValidator(pl *plugin, v *schema.FormValidator, te *texpr, file, ptr string) *formValidator {
	if types, ok := validatorTypes[v.Kind]; ok && !slices.Contains(types, te.name) {
		u.report(plxerr.FormValidatorNotApplicable, file, ptr+"/kind", "a %s validator does not apply to a %s field", v.Kind, te)
		return nil
	}
	allowed := validatorOptions[v.Kind]
	bad := false
	for _, o := range presentOptions(v) {
		if !slices.Contains(allowed, o) {
			u.report(plxerr.FormValidatorOptions, file, ptr+"/"+o, "a %s validator takes no %s", v.Kind, o)
			bad = true
		}
	}
	if bad {
		return nil
	}
	out := &formValidator{kind: validatorKinds[v.Kind], message: v.Message, maxScale: -1, maxIntegerDigits: -1}
	need := func(ok bool, what string) bool {
		if !ok {
			u.report(plxerr.FormValidatorOptions, file, ptr, "a %s validator needs %s", v.Kind, what)
		}
		return ok
	}
	switch v.Kind {
	case schema.FormValidatorKindLength:
		if !need(len(v.Min) > 0 || len(v.Max) > 0, "min or max") {
			return nil
		}
		if !u.checkBounds(pl, v, &texpr{name: "int"}, file, ptr, out) {
			return nil
		}
		for _, b := range []*value{out.min, out.max} {
			if b != nil && b.i < 0 {
				u.report(plxerr.FormValidatorOptions, file, ptr, "a length cannot be negative")
				return nil
			}
		}
	case schema.FormValidatorKindRange:
		// A number field's bounds have its type; numeric text's are decimals.
		bound := &texpr{name: te.name}
		if te.name == "string" {
			bound.name = "decimal"
		}
		if !need(len(v.Min) > 0 || len(v.Max) > 0, "min or max") || !u.checkBounds(pl, v, bound, file, ptr, out) {
			return nil
		}
	case schema.FormValidatorKindDateRange:
		if !need(len(v.Min) > 0 || len(v.Max) > 0, "min or max") || !u.checkBounds(pl, v, &texpr{name: te.name}, file, ptr, out) {
			return nil
		}
	case schema.FormValidatorKindRegex:
		if !need(v.Pattern != "", "a pattern") {
			return nil
		}
		if _, err := regex.Compile(v.Pattern, pxl.LimitsFrom(u.opts.Limits).Regex); err != nil {
			u.report(plxerr.FormPatternInvalid, file, ptr+"/pattern", "%s at code point %d: %s", err.Kind, err.Offset, err.Message)
			return nil
		}
		out.pattern = v.Pattern
	case schema.FormValidatorKindPhone:
		if v.Region != "" && !phone.Known(v.Region) {
			u.report(plxerr.FormPhoneRegionUnknown, file, ptr+"/region", "the phone table knows no region %q", v.Region)
			return nil
		}
		out.region = v.Region
	case schema.FormValidatorKindDecimalPrecision:
		if !need(v.MaxScale != nil || v.MaxIntegerDigits != nil, "maxScale or maxIntegerDigits") {
			return nil
		}
		if v.MaxScale != nil {
			out.maxScale = int32(min(*v.MaxScale, 1<<31-1)) //nolint:gosec // G115: clamped.
		}
		if v.MaxIntegerDigits != nil {
			out.maxIntegerDigits = int32(min(*v.MaxIntegerDigits, 1<<31-1)) //nolint:gosec // G115: clamped.
		}
	case schema.FormValidatorKindCustom:
		if !need(v.Rule != nil, "a rule") {
			return nil
		}
		val := u.exprValue(vctx{file: file, ptr: ptr + "/rule", pl: pl, code: plxerr.ValueTypeMismatch}, &texpr{name: "bool"})
		if val == nil {
			return nil
		}
		out.rule = val.prog
	case schema.FormValidatorKindAsync:
		if !need(v.Graph != "", "a graph") {
			return nil
		}
		g := u.graphs[v.Graph]
		if g == nil || pl == nil || g.plugin != pl {
			return nil // reported by formHandlers
		}
		if !u.checkAsyncGraph(g, file, ptr+"/$graph") {
			return nil
		}
		out.graph = g
		if v.DebounceMs != nil {
			out.debounceMs = uint32(min(*v.DebounceMs, 1<<32-1)) //nolint:gosec // G115: clamped.
		}
	}
	return out
}

// checkAsyncGraph checks an asynchronous validator's graph: it takes the
// field's value as its event, declares no inputs, and returns a bool
// (true when valid) or a string? (a message, null when valid).
func (u *unit) checkAsyncGraph(g *graph, file, ptr string) bool {
	if g.doc == nil {
		return false
	}
	if len(g.doc.Inputs) > 0 {
		u.report(plxerr.FormAsyncValidatorInvalid, file, ptr, "graph %q declares inputs, which a validator cannot pass", g.key)
		return false
	}
	switch g.doc.Output {
	case "bool", "string?", "string":
		return true
	}
	u.report(plxerr.FormAsyncValidatorInvalid, file, ptr, "graph %q must declare the output bool or string?, not %q", g.key, g.doc.Output)
	return false
}

// checkBounds checks a validator's min and max against t and that min is
// not above max.
func (u *unit) checkBounds(pl *plugin, v *schema.FormValidator, t *texpr, file, ptr string, out *formValidator) bool {
	ok := true
	if len(v.Min) > 0 {
		out.min = u.checkRaw(literalCtx(pl, file, ptr+"/min"), v.Min, t)
		ok = out.min != nil
	}
	if len(v.Max) > 0 {
		out.max = u.checkRaw(literalCtx(pl, file, ptr+"/max"), v.Max, t)
		ok = ok && out.max != nil
	}
	if ok && out.min != nil && out.max != nil && boundOf(out.min).Cmp(boundOf(out.max)) > 0 {
		u.report(plxerr.FormValidatorOptions, file, ptr+"/min", "min is above max")
		return false
	}
	return ok
}

// boundOf is a bound as a rational: an int, a double, a date's days, a
// date-time's microseconds, or a decimal.
func boundOf(v *value) *big.Rat {
	switch v.kind {
	case fbs.ValueKindDouble:
		if r := new(big.Rat).SetFloat64(v.d); r != nil {
			return r
		}
		return new(big.Rat)
	case fbs.ValueKindDecimal:
	default:
		return new(big.Rat).SetInt64(v.i)
	}
	n := new(big.Int).SetBytes(v.unscaled)
	if len(v.unscaled) > 0 && v.unscaled[0]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(v.unscaled))))
	}
	return new(big.Rat).SetFrac(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(v.scale)), nil))
}

// forms encodes forms; 0 when there are none, so bundles without forms
// keep their bytes.
func (e *valueEnc) forms(fs []*form) flatbuffers.UOffsetT {
	if len(fs) == 0 {
		return 0
	}
	b := e.b
	offs := make([]flatbuffers.UOffsetT, len(fs))
	for i, f := range fs {
		fields := make([]flatbuffers.UOffsetT, len(f.fields))
		for j, fd := range f.fields {
			fields[j] = e.formField(fd)
		}
		fv := offsetVector(b, fields)
		name := e.strs.of(f.name)
		fbs.FormStart(b)
		hi, lo := uuidHalves(f.id)
		fbs.FormAddId(b, fbs.CreateUuid(b, hi, lo))
		fbs.FormAddName(b, name)
		fbs.FormAddFields(b, fv)
		offs[i] = fbs.FormEnd(b)
	}
	return offsetVector(b, offs)
}

// formField encodes a field and its validators.
func (e *valueEnc) formField(fd *formField) flatbuffers.UOffsetT {
	b := e.b
	vs := make([]flatbuffers.UOffsetT, len(fd.validators))
	for k, v := range fd.validators {
		vs[k] = e.formValidator(v)
	}
	vv := offsetVector(b, vs)
	initial := e.value(fd.initial)
	name, typ := e.strs.of(fd.name), e.strs.of(fd.typ)
	fbs.FormFieldStart(b)
	fbs.FormFieldAddName(b, name)
	fbs.FormFieldAddType(b, typ)
	if initial != 0 {
		fbs.FormFieldAddInitial(b, initial)
	}
	fbs.FormFieldAddValidators(b, vv)
	return fbs.FormFieldEnd(b)
}

// formValidator encodes a validator.
func (e *valueEnc) formValidator(v *formValidator) flatbuffers.UOffsetT {
	b := e.b
	lo, hi := e.value(v.min), e.value(v.max)
	var message, pattern, region uint32
	if v.message != "" {
		message = e.strs.of(v.message)
	}
	if v.pattern != "" {
		pattern = e.strs.of(v.pattern)
	}
	if v.region != "" {
		region = e.strs.of(v.region)
	}
	var rule uint64
	if v.rule != nil {
		rule = e.u.program(e.o, v.rule)
	}
	fbs.FormValidatorStart(b)
	fbs.FormValidatorAddKind(b, v.kind)
	if message != 0 {
		fbs.FormValidatorAddMessage(b, message)
	}
	if lo != 0 {
		fbs.FormValidatorAddMin(b, lo)
	}
	if hi != 0 {
		fbs.FormValidatorAddMax(b, hi)
	}
	if pattern != 0 {
		fbs.FormValidatorAddPattern(b, pattern)
	}
	if region != 0 {
		fbs.FormValidatorAddRegion(b, region)
	}
	fbs.FormValidatorAddMaxScale(b, v.maxScale)
	fbs.FormValidatorAddMaxIntegerDigits(b, v.maxIntegerDigits)
	if rule != 0 {
		fbs.FormValidatorAddRule(b, rule)
	}
	if v.graph != nil {
		gh, gl := uuidHalves(v.graph.id)
		fbs.FormValidatorAddGraph(b, fbs.CreateUuid(b, gh, gl))
	}
	if v.debounceMs != 0 {
		fbs.FormValidatorAddDebounceMs(b, v.debounceMs)
	}
	return fbs.FormValidatorEnd(b)
}
