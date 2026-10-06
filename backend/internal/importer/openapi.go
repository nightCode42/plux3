// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"errors"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// methods lists the HTTP methods a data-source operation can use, in the
// order operations of one path are imported (DAT-001).
var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// LoadOpenAPI parses and validates an OpenAPI 3.x document in JSON or YAML.
// Every problem is reported as a diagnostic located in file.
func LoadOpenAPI(ctx context.Context, file string, data []byte) (*openapi3.T, plxerr.Diagnostics) {
	bad := func(format string, args ...any) (*openapi3.T, plxerr.Diagnostics) {
		return nil, plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.ImportDocumentInvalid, plxerr.Location{File: file}, format, args...)}
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		return bad("%v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return bad("the document declares openapi %q; only OpenAPI 3.x can be imported", doc.OpenAPI)
	}
	if err := doc.Validate(ctx); err != nil {
		return bad("%v", err)
	}
	return doc, nil
}

// ImportOpenAPI reads an OpenAPI 3.x document and returns one REST data
// source with a typed operation per path and method (DAT-002). An operation
// that uses a construct without a Plux type is reported and left out.
func ImportOpenAPI(ctx context.Context, file string, data []byte) Result {
	doc, diags := LoadOpenAPI(ctx, file, data)
	if doc == nil {
		return Result{Diagnostics: diags}
	}
	c := &oaConv{file: file, doc: doc, types: newTypeSet(), names: map[string]bool{}}
	title := ""
	if doc.Info != nil {
		title = doc.Info.Title
	}
	if title == "" {
		title = strings.TrimSuffix(path.Base(file), path.Ext(file))
	}
	srcName := lowerCamel(title)
	ops, mocks, opDiags := c.operations()
	diags = append(diags, opDiags...)
	variable := srcName + "BaseUrl"
	read := firstRead(ops, func(cfg map[string]any) bool {
		return cfg["method"] == "GET" && cfg["output"] != nil && c.types.optionalInput(cfg["input"])
	})
	if read == "" {
		diags = append(diags, plxerr.NewDiagnostic(plxerr.ImportConstructUnsupported, plxerr.Location{File: file, Path: "/paths"},
			"no read operation to bind: a source needs a GET operation without required parameters and with a JSON response; no source is written"))
		return finish(map[string]any{"dataSources": []any{}}, diags, file)
	}
	rc := ops[read].(map[string]any)
	delete(ops, read)
	if mocks[read] == nil {
		mocks[read] = c.types.zeroValue(rc["output"].(string), 0)
	}
	config := map[string]any{"baseUrl": variable, "method": "GET", "path": rc["path"]}
	if rc["auth"] == true {
		config["auth"] = true
	}
	if len(ops) > 0 {
		config["operations"] = ops
	}
	source := map[string]any{
		"id":          identifier("openapi", srcName),
		"name":        srcName,
		"kind":        "rest",
		"type":        rc["output"],
		"mock":        mocks[read],
		"config":      config,
		"description": describe(doc.Info != nil, func() string { return doc.Info.Description }),
	}
	delete(mocks, read)
	if source["description"] == "" {
		delete(source, "description")
	}
	if len(mocks) > 0 {
		source["x-operationMocks"] = mocks
	}
	frag := map[string]any{
		"dataSources": []any{source},
		"types":       c.types.list(),
		"variables":   []any{field(variable, "string", "Base URL of the "+title+" API.", false)},
	}
	if u := baseURL(doc); u != "" {
		frag["baseUrls"] = map[string]any{variable: u}
	}
	return finish(frag, diags, file)
}

// operations converts every operation of the document's paths: the
// configurations by name, the design-time mocks by name, and the
// diagnostics of what was left out.
func (c *oaConv) operations() (ops, mocks map[string]any, diags plxerr.Diagnostics) {
	ops, mocks = map[string]any{}, map[string]any{}
	if c.doc.Paths == nil {
		return ops, mocks, nil
	}
	paths := c.doc.Paths.Map()
	for _, p := range sortedKeys(paths) {
		item := paths[p]
		all := item.Operations()
		diags = append(diags, c.unsupportedMethods(p, all)...)
		for _, m := range methods {
			if all[m] == nil {
				continue
			}
			ptr := plxerr.Pointer("paths", p, strings.ToLower(m))
			name, cfg, mock, err := c.operation(p, m, item, all[m], ptr)
			if err == nil && ops[name] != nil {
				err = unsupportedAt(ptr, "the operation name %q is already used by another operation", name)
			}
			if err != nil {
				diags = append(diags, plxerr.NewDiagnostic(plxerr.ImportConstructUnsupported,
					plxerr.Location{File: c.file, Path: errPointer(err, ptr)},
					"%s %s: %v; the operation is left out", m, p, err))
				continue
			}
			ops[name] = cfg
			if mock != nil {
				mocks[name] = mock
			}
		}
	}
	return ops, mocks, diags
}

// unsupportedMethods reports the operations of a path whose HTTP method a
// data source cannot use.
func (c *oaConv) unsupportedMethods(p string, all map[string]*openapi3.Operation) plxerr.Diagnostics {
	var diags plxerr.Diagnostics
	for _, m := range sortedKeys(all) {
		if !slices.Contains(methods, m) {
			diags = append(diags, plxerr.NewDiagnostic(plxerr.ImportConstructUnsupported,
				plxerr.Location{File: c.file, Path: plxerr.Pointer("paths", p, strings.ToLower(m))},
				"%s %s: a data source operation is GET, POST, PUT, PATCH or DELETE; the operation is left out", m, p))
		}
	}
	return diags
}

// limit returns f() when ok, cut to the description limit of the schema.
func describe(ok bool, f func() string) string {
	if !ok {
		return ""
	}
	s := strings.TrimSpace(f())
	if len(s) > 4000 {
		return ""
	}
	return s
}

// errPointer returns the pointer an unsupported error carries, or def.
func errPointer(err error, def string) string {
	var u *unsupported
	if errors.As(err, &u) && u.ptr != "" {
		return u.ptr
	}
	return def
}

// baseURL returns the first server's URL with its variables replaced by
// their defaults, or "".
func baseURL(doc *openapi3.T) string {
	if len(doc.Servers) == 0 {
		return ""
	}
	s := doc.Servers[0]
	u := s.URL
	for _, k := range sortedKeys(s.Variables) {
		u = strings.ReplaceAll(u, "{"+k+"}", s.Variables[k].Default)
	}
	if parsed, err := url.Parse(u); err != nil || parsed.Host == "" {
		return ""
	}
	return strings.TrimSuffix(u, "/")
}

// oaConv converts the operations of one document.
type oaConv struct {
	file  string
	doc   *openapi3.T
	types *typeSet
	names map[string]bool
}

// opCtx is the state of one operation's conversion: the types it declares
// are merged into the document's only when the operation converts whole.
type opCtx struct {
	name    string
	pending map[string]map[string]any
}

// operation converts one operation to its configuration and design-time mock.
func (c *oaConv) operation(p, method string, item *openapi3.PathItem, op *openapi3.Operation, ptr string) (string, map[string]any, any, error) {
	name := operationName(p, method, op)
	oc := &opCtx{name: upperCamel(name), pending: map[string]map[string]any{}}
	cfg := map[string]any{"method": method, "path": p}

	fields, err := c.inputFields(oc, method, item, op, ptr)
	if err != nil {
		return "", nil, nil, err
	}
	for _, ph := range placeholderNames(p) {
		if !slices.ContainsFunc(fields, func(f map[string]any) bool { return f["name"] == ph }) {
			return "", nil, nil, unsupportedAt(ptr, "the path placeholder {%s} has no path parameter of a valid Plux name", ph)
		}
	}
	if len(fields) > 0 {
		in := oc.name + "Input"
		oc.pending[in] = map[string]any{"name": in, "fields": toAny(fields)}
		cfg["input"] = in
	}
	out, mock, err := c.output(oc, op, ptr)
	if err != nil {
		return "", nil, nil, err
	}
	if out != "" {
		cfg["output"] = out
	}
	if c.secured(op) {
		cfg["auth"] = true
	}
	if err := c.types.merge(oc.pending); err != nil {
		return "", nil, nil, unsupportedAt(ptr, "%v", err)
	}
	return name, cfg, mock, nil
}

func toAny(fields []map[string]any) []any {
	out := make([]any, len(fields))
	for i, f := range fields {
		out[i] = f
	}
	return out
}

// operationName is the operationId, or the method and path, in lowerCamel.
func operationName(p, method string, op *openapi3.Operation) string {
	if op.OperationID != "" {
		return lowerCamel(op.OperationID)
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, "{") {
			b.WriteString(" by ")
		}
		b.WriteString(" " + seg)
	}
	return lowerCamel(b.String())
}

// placeholderNames lists the {name} placeholders of a path.
func placeholderNames(p string) []string {
	var out []string
	for rest := p; ; {
		i := strings.Index(rest, "{")
		j := strings.Index(rest, "}")
		if i < 0 || j < i {
			return out
		}
		out = append(out, rest[i+1:j])
		rest = rest[j+1:]
	}
}

// secured reports whether the operation requires authentication.
func (c *oaConv) secured(op *openapi3.Operation) bool {
	req := c.doc.Security
	if op.Security != nil {
		req = *op.Security
	}
	return slices.ContainsFunc(req, func(r openapi3.SecurityRequirement) bool { return len(r) > 0 })
}

// inputFields builds the operation's input fields from its path and query
// parameters, or from its JSON request body (DAT-001: the parameters of a
// call become the path, then the query of a GET or the body of a POST).
func (c *oaConv) inputFields(oc *opCtx, method string, item *openapi3.PathItem, op *openapi3.Operation, ptr string) ([]map[string]any, error) {
	params := map[string]*openapi3.Parameter{}
	for _, list := range []openapi3.Parameters{item.Parameters, op.Parameters} {
		for _, pr := range list {
			if pr != nil && pr.Value != nil {
				params[pr.Value.In+" "+pr.Value.Name] = pr.Value
			}
		}
	}
	readsQuery := method == "GET" || method == "DELETE"
	fs := &fieldSet{ptr: ptr, seen: map[string]bool{}}
	for _, key := range sortedKeys(params) {
		f, err := c.parameterField(oc, params[key], method, readsQuery, ptr)
		if err != nil {
			return nil, err
		}
		if f == nil {
			continue
		}
		if err := fs.add(f); err != nil {
			return nil, err
		}
	}
	if err := c.bodyFields(oc, op, method, readsQuery, ptr, fs); err != nil {
		return nil, err
	}
	return fs.fields, nil
}

// fieldSet collects the input fields of an operation and refuses a name
// declared twice.
type fieldSet struct {
	ptr    string
	seen   map[string]bool
	fields []map[string]any
}

func (fs *fieldSet) add(f map[string]any) error {
	n := f["name"].(string)
	if fs.seen[n] {
		return unsupportedAt(fs.ptr, "the input field %q is declared twice", n)
	}
	fs.seen[n] = true
	fs.fields = append(fs.fields, f)
	return nil
}

// parameterField converts a path, query, header or cookie parameter to an
// input field; it returns nil for a header or cookie parameter that the
// data source need not send.
func (c *oaConv) parameterField(oc *opCtx, pr *openapi3.Parameter, method string, readsQuery bool, ptr string) (map[string]any, error) {
	switch pr.In {
	case openapi3.ParameterInHeader, openapi3.ParameterInCookie:
		if pr.Required && !strings.EqualFold(pr.Name, "authorization") {
			return nil, unsupportedAt(ptr, "the required %s parameter %q cannot be sent by a data source", pr.In, pr.Name)
		}
		return nil, nil
	case openapi3.ParameterInQuery:
		if !readsQuery {
			return nil, unsupportedAt(ptr, "the query parameter %q on a %s: a data source sends the parameters of a %s as its body", pr.Name, method, method)
		}
	}
	if !nameShapeOK(pr.Name) {
		return nil, unsupportedAt(ptr, "the parameter %q is not a valid Plux name (lowerCamelCase); a name cannot be changed without changing the request", pr.Name)
	}
	if pr.Schema == nil {
		return nil, unsupportedAt(ptr, "the parameter %q has no schema", pr.Name)
	}
	t, err := c.typeOf(oc, pr.Schema, ptr, oc.name+upperCamel(pr.Name), nil)
	if err != nil {
		return nil, err
	}
	required := pr.Required || pr.In == openapi3.ParameterInPath
	return field(pr.Name, optional(t, required), pr.Description, false), nil
}

// bodyFields adds the fields of the operation's JSON request body to fs.
func (c *oaConv) bodyFields(oc *opCtx, op *openapi3.Operation, method string, readsQuery bool, ptr string, fs *fieldSet) error {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	if readsQuery {
		return unsupportedAt(ptr, "a %s has a request body, which a data source cannot send", method)
	}
	media := jsonMedia(op.RequestBody.Value.Content)
	if media == nil || media.Schema == nil {
		return unsupportedAt(ptr, "the request body is not JSON with a schema")
	}
	bodyName, stack := oc.name+"Body", []string(nil)
	if rn := refName(media.Schema); rn != "" {
		bodyName, stack = rn, []string{rn}
	}
	bodyFields, err := c.objectFields(oc, media.Schema, ptr, bodyName, stack)
	if err != nil {
		return err
	}
	for _, f := range bodyFields {
		if err := fs.add(f); err != nil {
			return err
		}
	}
	return nil
}

func nameShapeOK(s string) bool {
	return s != "" && len(s) <= maxNameLength && s == lowerCamelStrict(s)
}

// lowerCamelStrict returns s when it already is a valid lowerCamelCase name
// and a different string otherwise.
func lowerCamelStrict(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return "-"
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return "-"
		}
	}
	return s
}

// optional makes a type nullable unless required.
func optional(t string, required bool) string {
	if required || strings.HasSuffix(t, "?") {
		return t
	}
	return t + "?"
}

// jsonMedia returns the JSON media type of a content map, or nil.
func jsonMedia(content openapi3.Content) *openapi3.MediaType {
	for _, k := range sortedKeys(content) {
		mt := strings.ToLower(strings.TrimSpace(strings.SplitN(k, ";", 2)[0]))
		if mt == "application/json" || strings.HasSuffix(mt, "+json") {
			return content[k]
		}
	}
	return nil
}

// output returns the response type of the lowest 2xx response and its
// example, if any.
func (c *oaConv) output(oc *opCtx, op *openapi3.Operation, ptr string) (string, any, error) {
	if op.Responses == nil {
		return "", nil, nil
	}
	var codes []string
	for code := range op.Responses.Map() {
		if n, err := strconv.Atoi(code); err == nil && n >= 200 && n < 300 {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return "", nil, nil
	}
	slices.Sort(codes)
	resp := op.Responses.Map()[codes[0]]
	if resp == nil || resp.Value == nil || len(resp.Value.Content) == 0 {
		return "", nil, nil
	}
	rptr := ptr + plxerr.Pointer("responses", codes[0])
	media := jsonMedia(resp.Value.Content)
	if media == nil || media.Schema == nil {
		return "", nil, unsupportedAt(rptr, "the %s response is not JSON with a schema", codes[0])
	}
	t, err := c.typeOf(oc, media.Schema, rptr, oc.name+"Output", nil)
	if err != nil {
		return "", nil, err
	}
	return t, exampleOf(media), nil
}

// exampleOf returns a media type's example: its own, the first of its
// examples by name, or its schema's.
func exampleOf(m *openapi3.MediaType) any {
	if m.Example != nil {
		return m.Example
	}
	for _, k := range sortedKeys(m.Examples) {
		if e := m.Examples[k]; e != nil && e.Value != nil && e.Value.Value != nil {
			return e.Value.Value
		}
	}
	if m.Schema != nil && m.Schema.Value != nil {
		if m.Schema.Value.Example != nil {
			return m.Schema.Value.Example
		}
		if len(m.Schema.Value.Examples) > 0 {
			return m.Schema.Value.Examples[0]
		}
	}
	return nil
}

// refName is the component name a reference points at, or "".
func refName(r *openapi3.SchemaRef) string {
	if r.Ref == "" {
		return ""
	}
	return upperCamel(r.Ref[strings.LastIndex(r.Ref, "/")+1:])
}

// typeOf converts a schema to a type expression, declaring the object and
// enum types it needs. stack holds the references being converted, so that a
// recursive schema is reported rather than followed.
func (c *oaConv) typeOf(oc *opCtx, ref *openapi3.SchemaRef, ptr, ctx string, stack []string) (string, error) {
	if ref == nil || ref.Value == nil {
		return "", unsupportedAt(ptr, "a schema is missing")
	}
	s := ref.Value
	name := ctx
	if rn := refName(ref); rn != "" {
		if slices.Contains(stack, rn) {
			return "", unsupportedAt(ptr, "the schema %s refers to itself", rn)
		}
		stack = append(slices.Clone(stack), rn)
		name = rn
	}
	switch {
	case len(s.AllOf) > 0, len(s.OneOf) > 0, len(s.AnyOf) > 0:
		return "", unsupportedAt(ptr, "allOf, oneOf and anyOf have no Plux type")
	case s.Not != nil, len(s.PatternProperties) > 0, len(s.PrefixItems) > 0, s.If != nil, s.Const != nil:
		return "", unsupportedAt(ptr, "not, patternProperties, prefixItems, if and const have no Plux type")
	}
	kinds, nullable := schemaKinds(s)
	if len(kinds) != 1 {
		return "", unsupportedAt(ptr, "a schema with no single type has no Plux type")
	}
	var t string
	var err error
	switch kinds[0] {
	case "string":
		t, err = c.stringType(oc, s, ptr, name)
	case "integer":
		t = "int"
	case "number":
		t = "double"
	case "boolean":
		t = "bool"
	case "array":
		if s.Items == nil {
			return "", unsupportedAt(ptr, "an array without items has no Plux type")
		}
		var elem string
		elem, err = c.typeOf(oc, s.Items, ptr, name+"Item", stack)
		t = "list<" + elem + ">"
	case "object":
		t, err = c.objectType(oc, ref, ptr, name, stack)
	default:
		err = unsupportedAt(ptr, "the type %q has no Plux type", kinds[0])
	}
	if err != nil {
		return "", err
	}
	if nullable {
		t = optional(t, false)
	}
	return t, nil
}

// schemaKinds lists the types a schema allows apart from null, and whether
// it allows null. A schema with properties and no type is an object.
func schemaKinds(s *openapi3.Schema) (kinds []string, nullable bool) {
	nullable = s.Nullable
	if s.Type != nil {
		for _, t := range s.Type.Slice() {
			if t == "null" {
				nullable = true
				continue
			}
			kinds = append(kinds, t)
		}
	}
	if len(kinds) == 0 && len(s.Properties) > 0 {
		kinds = []string{"object"}
	}
	return kinds, nullable
}

// stringType converts a string schema: an enum, a date or a plain string.
func (*oaConv) stringType(oc *opCtx, s *openapi3.Schema, ptr, name string) (string, error) {
	if len(s.Enum) > 0 {
		members := make([]any, 0, len(s.Enum))
		for _, e := range s.Enum {
			m, ok := e.(string)
			if !ok || !isEnumMember(m) {
				return "", unsupportedAt(ptr, "the enum value %v is not a Plux enum member (a letter, then letters and digits)", e)
			}
			members = append(members, m)
		}
		decl := map[string]any{"name": name, "enum": members}
		if d := describe(true, func() string { return s.Description }); d != "" {
			decl["description"] = d
		}
		oc.pending[name] = decl
		return name, nil
	}
	switch s.Format {
	case "date":
		return "date", nil
	case "date-time":
		return "dateTime", nil
	case "duration":
		return "duration", nil
	case "binary", "byte":
		return "", unsupportedAt(ptr, "the string format %q has no Plux type", s.Format)
	}
	return "string", nil
}

func isEnumMember(s string) bool {
	if s == "" || len(s) > maxNameLength || ((s[0] < 'a' || s[0] > 'z') && (s[0] < 'A' || s[0] > 'Z')) {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// objectType converts an object schema to a named type or a map.
func (c *oaConv) objectType(oc *opCtx, ref *openapi3.SchemaRef, ptr, name string, stack []string) (string, error) {
	s := ref.Value
	if len(s.Properties) == 0 {
		ap := s.AdditionalProperties
		if ap.Schema != nil {
			v, err := c.typeOf(oc, ap.Schema, ptr, name+"Value", stack)
			if err != nil {
				return "", err
			}
			return "map<string," + v + ">", nil
		}
		return "", unsupportedAt(ptr, "an object without properties has no Plux type")
	}
	if s.AdditionalProperties.Schema != nil {
		return "", unsupportedAt(ptr, "an object with both properties and additionalProperties has no Plux type")
	}
	fields, err := c.objectFields(oc, ref, ptr, name, stack)
	if err != nil {
		return "", err
	}
	decl := map[string]any{"name": name, "fields": toAny(fields)}
	if d := describe(true, func() string { return s.Description }); d != "" {
		decl["description"] = d
	}
	oc.pending[name] = decl
	return name, nil
}

// objectFields converts the properties of an object schema to fields.
func (c *oaConv) objectFields(oc *opCtx, ref *openapi3.SchemaRef, ptr, name string, stack []string) ([]map[string]any, error) {
	if ref == nil || ref.Value == nil || len(ref.Value.Properties) == 0 {
		return nil, unsupportedAt(ptr, "an object without properties has no Plux type")
	}
	s := ref.Value
	if len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) > 0 {
		return nil, unsupportedAt(ptr, "allOf, oneOf and anyOf have no Plux type")
	}
	var fields []map[string]any
	for _, prop := range sortedKeys(s.Properties) {
		if !nameShapeOK(prop) {
			return nil, unsupportedAt(ptr, "the property %q is not a valid Plux name (lowerCamelCase); a name cannot be changed without changing the wire format", prop)
		}
		pr := s.Properties[prop]
		t, err := c.typeOf(oc, pr, ptr, name+upperCamel(prop), stack)
		if err != nil {
			return nil, err
		}
		desc, secret := "", false
		if pr != nil && pr.Value != nil {
			desc, secret = pr.Value.Description, pr.Value.Format == "password"
		}
		fields = append(fields, field(prop, optional(t, slices.Contains(s.Required, prop)), desc, secret))
	}
	return fields, nil
}

// firstRead returns the first operation, in name order, that can be a
// source's own read, or "".
func firstRead(ops map[string]any, ok func(map[string]any) bool) string {
	for _, name := range sortedKeys(ops) {
		if ok(ops[name].(map[string]any)) {
			return name
		}
	}
	return ""
}
