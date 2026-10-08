// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// dataFeature is required by a bundle that declares a REST or GraphQL
// data source: a runtime without the data layer refuses the bundle, so a
// page never shows a source that nothing loads (BND-008, ADR-0048). The
// data layer first runs in runtime 0.3.0.
const dataFeature = "data"

// dataRuntimes lists the runtime each revision of dataFeature first
// shipped in.
var dataRuntimes = []string{"0.3.0"}

// dataConfig is the configuration of a REST or GraphQL data source
// (DAT-001, ADR-0048), as docs/reference/data-sources.md describes it.
type dataConfig struct {
	BaseURL      string                      `json:"baseUrl"`
	Method       string                      `json:"method"`
	Path         string                      `json:"path"`
	Query        string                      `json:"query"`
	Params       map[string]json.RawMessage  `json:"params"`
	Headers      map[string]string           `json:"headers"`
	Auth         bool                        `json:"auth"`
	Select       string                      `json:"select"`
	ResponseType string                      `json:"responseType"`
	Transform    json.RawMessage             `json:"transform"`
	Cache        *cacheConfig                `json:"cache"`
	Pagination   *pageConfig                 `json:"pagination"`
	Mocks        *mockConfig                 `json:"mocks"`
	Operations   map[string]*operationConfig `json:"operations"`

	// Subscription is a GraphQL source's subscription document (DAT-012);
	// SubscribeMessage is the message a WebSocket source sends after each
	// connection; OfflineCapable marks the source's mutations for the
	// outbox (DAT-020).
	Subscription     string          `json:"subscription"`
	SubscribeMessage json.RawMessage `json:"subscribeMessage"`
	OfflineCapable   bool            `json:"offlineCapable"`
}

// cacheConfig is a source's caching (DAT-010).
type cacheConfig struct {
	Policy     string `json:"policy"`
	TTLSeconds int64  `json:"ttlSeconds"`
	Key        string `json:"key"`
	Encrypted  bool   `json:"encrypted"`
}

// pageConfig is a source's pagination (DAT-011).
type pageConfig struct {
	Style       string `json:"style"`
	PageSize    int64  `json:"pageSize"`
	SizeParam   string `json:"sizeParam"`
	CursorParam string `json:"cursorParam"`
	NextCursor  string `json:"nextCursor"`
	PageParam   string `json:"pageParam"`
	FirstPage   *int64 `json:"firstPage"`
	OffsetParam string `json:"offsetParam"`
	HasMore     string `json:"hasMore"`
}

// mockConfig holds the mock states beyond success, which is the source's
// mock (DAT-080).
type mockConfig struct {
	Empty json.RawMessage `json:"empty"`
	Error *mockError      `json:"error"`
}

// mockError is the error a source's error mock fails with.
type mockError struct {
	Kind    string `json:"kind"`
	Status  int64  `json:"status"`
	Message string `json:"message"`
}

// operationConfig is an operation apiCall runs (DAT-001, DAT-002).
type operationConfig struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Auth    *bool             `json:"auth"`
	Input   string            `json:"input"`
	Output  string            `json:"output"`
	Select  string            `json:"select"`

	// OfflineCapable overrides the source's setting for this mutation
	// (DAT-020); Transfer makes the operation a file upload or download
	// (DAT-031).
	OfflineCapable *bool           `json:"offlineCapable"`
	Transfer       *transferConfig `json:"transfer"`
}

// parsedConfig is a decoded configuration or why it could not be read.
type parsedConfig struct {
	cfg *dataConfig
	err error
}

var (
	selectorPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.([A-Za-z_][A-Za-z0-9_]*|[0-9]+))*$`)
	placeholder      = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	headerName       = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)
	operationName    = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	cachePolicies    = []string{"networkOnly", "cacheFirst", "networkFirst", "staleWhileRevalidate"}
	pageStyles       = []string{"cursor", "page", "offset"}
	mockErrorKinds   = []string{"network", "http", "timeout", "validation", "function", "permission", "cancelled", "custom"}
	operationMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	// secretHeaderWords mark headers that carry credentials (DAT-003).
	secretHeaderWords = []string{"authorization", "cookie", "api-key", "apikey", "api_key", "token", "secret", "password"}
)

// maxTTLSeconds is the longest cache TTL, a year.
const maxTTLSeconds = 365 * 24 * 3600

// runsData reports whether the data layer runs sources of a kind in this
// milestone: REST and GraphQL (P5 R4), WebSocket and SSE streams (P5 R5).
func runsData(k schema.DataSourceKind) bool {
	return k == schema.DataSourceKindRest || k == schema.DataSourceKindGraphql || isStreamKind(k)
}

// dataConfigOf decodes a REST or GraphQL source's configuration once.
func (u *unit) dataConfigOf(s *schema.DataSource) *parsedConfig {
	if p, ok := u.dataConfigs[s.ID]; ok {
		return p
	}
	p := &parsedConfig{}
	if len(s.Config) == 0 {
		p.err = errNoConfig
	} else {
		dec := json.NewDecoder(bytes.NewReader(s.Config))
		dec.DisallowUnknownFields()
		var c dataConfig
		if err := dec.Decode(&c); err != nil {
			p.err = err
		} else {
			p.cfg = &c
		}
	}
	u.dataConfigs[s.ID] = p
	return p
}

type configError string

func (e configError) Error() string { return string(e) }

const errNoConfig = configError("the source has no config")

// dataSourcesOf lists the sources a graph's scope sees, innermost first:
// its page's, its plugin's, the app's.
func (u *unit) dataSourcesOf(g *graph) []*schema.DataSource {
	var out []*schema.DataSource
	add := func(ss []schema.DataSource) {
		for i := range ss {
			out = append(out, &ss[i])
		}
	}
	if g.page != nil {
		add(g.page.doc.DataSources)
	}
	if g.plugin != nil {
		add(g.plugin.doc.DataSources)
	}
	add(u.project.App.Doc.DataSources)
	return out
}

// operation resolves "<source>.<operation>" in a graph's scope.
func (u *unit) operation(g *graph, ref string) (*schema.DataSource, *operationConfig) {
	name, op, ok := strings.Cut(ref, ".")
	if !ok {
		return nil, nil
	}
	for _, s := range u.dataSourcesOf(g) {
		if s.Name != name {
			continue
		}
		if !runsData(s.Kind) {
			return nil, nil
		}
		p := u.dataConfigOf(s)
		if p.cfg == nil || p.cfg.Operations[op] == nil {
			return nil, nil
		}
		return s, p.cfg.Operations[op]
	}
	return nil, nil
}

// compileDataSources compiles the expressions of sources' configurations:
// parameters in the declaring scope, the transform with `response` of
// the source's response type (DAT-004).
func (t *typer) compileDataSources(pl *plugin, sources []schema.DataSource, file string, s *scope, from string) {
	for i := range sources {
		src := &sources[i]
		if !runsData(src.Kind) {
			continue
		}
		p := t.u.dataConfigOf(src)
		if p.cfg == nil {
			continue
		}
		ptr := plxerr.Pointer("dataSources", strconv.Itoa(i), "config")
		for _, k := range sortedKeys(p.cfg.Params) {
			t.compileAll(p.cfg.Params[k], s, from, file, ptr+plxerr.Pointer("params", k))
		}
		if len(p.cfg.Transform) > 0 && p.cfg.ResponseType != "" {
			if te, err := parseTypeExpr(p.cfg.ResponseType); err == nil && t.u.typeKnown(pl, te) {
				t.compileAll(p.cfg.Transform, s.with("response", te.String()), from, file, ptr+"/transform")
			}
		}
	}
}

// checkDataSource validates a REST or GraphQL source and returns its
// configuration as the bundle carries it: the document's configuration,
// the base URL of every environment as `baseUrls` and, in development
// bundles only, the source's mock as `mocks.success` (DAT-003, DAT-080).
func (u *unit) checkDataSource(pl *plugin, s *schema.DataSource, file, ptr string, sc *scope) *value {
	cptr := ptr + "/config"
	p := u.dataConfigOf(s)
	if p.cfg == nil {
		u.report(plxerr.DataSourceConfigInvalid, file, cptr, "%s source %q: %v", s.Kind, s.Name, p.err)
		return nil
	}
	c := p.cfg
	te, _ := parseTypeExpr(s.Type)
	u.useRevision(dataFeature, dataRuntimes, 1, vctx{file: file, ptr: ptr, pl: pl})
	baseURLs := u.checkBaseURL(pl, s, c, file, cptr)
	graphql := s.Kind == schema.DataSourceKindGraphql
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataSourceConfigInvalid, file, cptr+sub, format, args...)
	}
	u.checkStreamAndOutbox(pl, s, c, file, ptr, cptr)
	switch {
	case isStreamKind(s.Kind):
		u.checkPath(c.Path, c.Params, file, cptr)
	case graphql:
		checkGraphQLSource(c, bad)
	default:
		if c.Query != "" {
			bad("/query", "only a GraphQL source has a query")
		}
		if c.Method != "" && c.Method != "GET" && c.Method != "POST" {
			bad("/method", "a REST source reads with GET or POST, not %q", c.Method)
		}
		u.checkPath(c.Path, c.Params, file, cptr)
	}
	u.checkHeaders(c.Headers, file, cptr+"/headers")
	u.checkSelector(c.Select, file, cptr+"/select")
	u.checkMapping(pl, c, te, file, cptr, sc)
	u.checkCache(c.Cache, file, cptr+"/cache")
	u.checkPagination(c, te, file, cptr+"/pagination")
	u.checkMocks(pl, c.Mocks, te, file, cptr+"/mocks")
	for _, name := range sortedKeys(c.Operations) {
		u.checkOperation(pl, name, c.Operations[name], graphql, file, cptr+plxerr.Pointer("operations", name))
	}
	for _, k := range sortedKeys(c.Params) {
		if sc == nil && hasExpr(c.Params[k]) {
			bad(plxerr.Pointer("params", k), "only literals are allowed here")
		}
	}
	return u.encodeDataConfig(s, file, ptr, sc, baseURLs)
}

// typeKnown reports whether every name of a type expression is a
// primitive or a type the plugin, the app or the registry declares.
func (u *unit) typeKnown(pl *plugin, te *texpr) bool {
	if te.elem != nil {
		return (te.name == "list" || te.name == "map") && u.typeKnown(pl, te.elem)
	}
	return primitives[te.name] || u.types.known(pl, te.name) || hasKey(u.types.base, te.name)
}

// checkGraphQLSource checks a GraphQL source's document and path.
func checkGraphQLSource(c *dataConfig, bad func(sub, format string, args ...any)) {
	q := strings.TrimSpace(c.Query)
	if !strings.HasPrefix(q, "query") && !strings.HasPrefix(q, "{") {
		bad("/query", "a GraphQL source needs a query document")
	}
	if c.Method != "" {
		bad("/method", "a GraphQL source has no method: it posts its query")
	}
	if c.Path != "" && !strings.HasPrefix(c.Path, "/") {
		bad("/path", "the path must start with /")
	}
}

// hasExpr reports whether a raw value is a binding.
func hasExpr(raw json.RawMessage) bool { return bytes.Contains(raw, []byte(`"$expr"`)) }

// checkBaseURL checks that the base URL names a string variable and that
// every environment gives it an HTTPS URL on a declared domain (DAT-003,
// DAT-030); it returns the URLs by environment key.
func (u *unit) checkBaseURL(pl *plugin, s *schema.DataSource, c *dataConfig, file, cptr string) map[string]any {
	app := u.project.App.Doc
	if c.BaseURL == "" {
		u.report(plxerr.DataSourceConfigInvalid, file, cptr, "source %q needs a baseUrl: an app variable holding its base URL per environment", s.Name)
		return nil
	}
	if !slices.ContainsFunc(app.Variables, func(f schema.Field) bool { return f.Name == c.BaseURL && f.Type == "string" }) {
		u.report(plxerr.DataSourceConfigInvalid, file, cptr+"/baseUrl", "no string variable %q is declared in app.json", c.BaseURL)
		return nil
	}
	var domains []string
	if pl != nil && pl.doc.Capabilities != nil {
		domains = pl.doc.Capabilities.NetworkDomains
	}
	out := map[string]any{}
	for _, env := range app.Environments {
		var raw string
		if json.Unmarshal(env.Values[c.BaseURL], &raw) != nil {
			u.report(plxerr.DataSourceConfigInvalid, file, cptr+"/baseUrl", "environment %q gives variable %q no value", env.Key, c.BaseURL)
			continue
		}
		uri, err := url.Parse(raw)
		switch {
		case err != nil || uri.Scheme != "https" || uri.Host == "" || uri.RawQuery != "" || uri.Fragment != "" || uri.User != nil:
			u.report(plxerr.DataSourceDomainUndeclared, file, cptr+"/baseUrl", "environment %q: %q is not an HTTPS base URL without query or credentials", env.Key, raw)
			continue
		case pl != nil && !domainAllowed(uri.Hostname(), domains):
			u.report(plxerr.DataSourceDomainUndeclared, file, cptr+"/baseUrl", "environment %q: %s is not a domain plugin %q declares", env.Key, uri.Hostname(), pl.doc.Key)
			continue
		}
		out[env.Key] = strings.TrimSuffix(raw, "/")
	}
	return out
}

// domainAllowed reports whether host is a declared domain: the same name
// or under a "*." wildcard, which does not match the bare domain. The
// runtime applies the same rule before every request (SEC-080).
func domainAllowed(host string, domains []string) bool {
	h := strings.ToLower(host)
	for _, d := range domains {
		if rest, ok := strings.CutPrefix(d, "*"); ok {
			if strings.HasSuffix(h, rest) && len(h) > len(rest) {
				return true
			}
		} else if h == d {
			return true
		}
	}
	return false
}

// checkPath checks a REST path and that each placeholder is a parameter.
func (u *unit) checkPath(path string, params map[string]json.RawMessage, file, cptr string) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		u.report(plxerr.DataSourceConfigInvalid, file, cptr+"/path", "a REST path starts with / and has no query or fragment")
		return
	}
	for _, m := range placeholder.FindAllStringSubmatch(path, -1) {
		if _, ok := params[m[1]]; !ok {
			u.report(plxerr.DataSourceConfigInvalid, file, cptr+"/path", "placeholder {%s} names no parameter", m[1])
		}
	}
}

// checkHeaders refuses malformed names and headers carrying credentials.
func (u *unit) checkHeaders(h map[string]string, file, ptr string) {
	for _, name := range sortedKeys(h) {
		lower := strings.ToLower(name)
		switch {
		case !headerName.MatchString(name):
			u.report(plxerr.DataSourceConfigInvalid, file, ptr+plxerr.Pointer(name), "%q is not a header name", name)
		case slices.ContainsFunc(secretHeaderWords, func(w string) bool { return strings.Contains(lower, w) }):
			u.report(plxerr.DataSourceSecretHeader, file, ptr+plxerr.Pointer(name), "header %s carries credentials; set auth to use the auth delegate's token", name)
		}
	}
}

// checkSelector checks a dot-path selector.
func (u *unit) checkSelector(sel, file, ptr string) {
	if sel != "" && !selectorPattern.MatchString(sel) {
		u.report(plxerr.DataMappingInvalid, file, ptr, "%q is not a selector: names and indices joined by dots", sel)
	}
}

// checkMapping checks the response type and transform (DAT-004).
func (u *unit) checkMapping(pl *plugin, c *dataConfig, declared *texpr, file, cptr string, sc *scope) {
	switch {
	case c.ResponseType == "" && len(c.Transform) == 0:
		return
	case c.ResponseType == "":
		u.report(plxerr.DataMappingInvalid, file, cptr+"/transform", "a transform needs the responseType it reads as response")
		return
	case len(c.Transform) == 0:
		u.report(plxerr.DataMappingInvalid, file, cptr+"/responseType", "a responseType needs a transform to the source's type")
		return
	}
	if u.checkType(pl, c.ResponseType, file, cptr+"/responseType") == nil || declared == nil {
		return
	}
	if sc == nil || !hasExpr(c.Transform) {
		u.report(plxerr.DataMappingInvalid, file, cptr+"/transform", "a transform is a PXL expression")
		return
	}
	u.exprValue(vctx{file: file, ptr: cptr + "/transform", scope: sc, pl: pl, code: plxerr.DataMappingInvalid}, declared)
}

// checkCache checks a caching policy (DAT-010).
func (u *unit) checkCache(c *cacheConfig, file, ptr string) {
	if c == nil {
		return
	}
	if !slices.Contains(cachePolicies, c.Policy) {
		u.report(plxerr.DataSourceConfigInvalid, file, ptr+"/policy", "%q is not a cache policy: %s", c.Policy, strings.Join(cachePolicies, ", "))
		return
	}
	if c.Policy != "networkOnly" && (c.TTLSeconds < 1 || c.TTLSeconds > maxTTLSeconds) {
		u.report(plxerr.DataSourceConfigInvalid, file, ptr+"/ttlSeconds", "policy %s needs ttlSeconds from 1 to %d", c.Policy, maxTTLSeconds)
	}
}

// checkPagination checks a paginated source (DAT-011).
func (u *unit) checkPagination(c *dataConfig, declared *texpr, file, ptr string) {
	p := c.Pagination
	if p == nil {
		return
	}
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataPaginationInvalid, file, ptr+sub, format, args...)
	}
	if declared == nil || declared.name != "list" {
		bad("", "a paginated source declares a list type, not %s", declared)
	}
	if limit := u.opts.Limits.Get(limits.DataPageSize); p.PageSize < 1 || p.PageSize > limit {
		bad("/pageSize", "pageSize must be from 1 to %d (data.pageSize)", limit)
	}
	need := map[string]string{"cursor": p.CursorParam, "page": p.PageParam, "offset": p.OffsetParam}
	param := map[string]string{"cursor": "cursorParam", "page": "pageParam", "offset": "offsetParam"}
	switch {
	case !slices.Contains(pageStyles, p.Style):
		bad("/style", "%q is not a pagination style: cursor, page or offset", p.Style)
	case need[p.Style] == "":
		bad("", "style %s needs %s", p.Style, param[p.Style])
	case p.Style == "cursor" && p.NextCursor == "":
		bad("", "style cursor needs nextCursor: the selector of the next page's cursor")
	}
	if p.FirstPage != nil && *p.FirstPage < 0 {
		bad("/firstPage", "firstPage must not be negative")
	}
	u.checkSelector(p.NextCursor, file, ptr+"/nextCursor")
	u.checkSelector(p.HasMore, file, ptr+"/hasMore")
}

// checkMocks checks the empty and error mocks (DAT-080).
func (u *unit) checkMocks(pl *plugin, m *mockConfig, declared *texpr, file, ptr string) {
	if m == nil {
		return
	}
	if len(m.Empty) > 0 && declared != nil {
		u.checkRaw(literalCtx(pl, file, ptr+"/empty"), m.Empty, declared)
	}
	if e := m.Error; e != nil {
		if e.Kind != "" && !slices.Contains(mockErrorKinds, e.Kind) {
			u.report(plxerr.DataSourceConfigInvalid, file, ptr+"/error/kind", "%q is not an error kind", e.Kind)
		}
		if e.Status < 0 || e.Status > 599 {
			u.report(plxerr.DataSourceConfigInvalid, file, ptr+"/error/status", "status must be from 0 to 599")
		}
	}
}

// checkOperation checks an operation apiCall runs.
func (u *unit) checkOperation(pl *plugin, name string, op *operationConfig, graphql bool, file, ptr string) {
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataSourceConfigInvalid, file, ptr+sub, format, args...)
	}
	if !operationName.MatchString(name) {
		bad("", "operation name %q is not lowerCamelCase", name)
	}
	if op == nil {
		bad("", "operation %q is empty", name)
		return
	}
	checkOperationRequest(op, graphql, bad)
	u.checkTransfer(pl, op, graphql, file, ptr)
	if op.Input != "" {
		if te := u.checkType(pl, op.Input, file, ptr+"/input"); te != nil {
			if _, ok := u.declared(pl, te.name); !ok || te.nullable || te.elem != nil {
				bad("/input", "an operation's input is a declared object type, not %s", op.Input)
			}
		}
	} else if !graphql {
		for _, m := range placeholder.FindAllStringSubmatch(op.Path, -1) {
			bad("/path", "placeholder {%s} needs an input field", m[1])
		}
	}
	if op.Output != "" {
		u.checkType(pl, op.Output, file, ptr+"/output")
	}
	u.checkHeaders(op.Headers, file, ptr+"/headers")
	u.checkSelector(op.Select, file, ptr+"/select")
}

// checkOperationRequest checks an operation's document, or its method
// and path.
func checkOperationRequest(op *operationConfig, graphql bool, bad func(sub, format string, args ...any)) {
	if graphql {
		q := strings.TrimSpace(op.Query)
		if !strings.HasPrefix(q, "query") && !strings.HasPrefix(q, "mutation") && !strings.HasPrefix(q, "{") {
			bad("/query", "a GraphQL operation needs a query or mutation document")
		}
		if op.Method != "" || op.Path != "" {
			bad("", "a GraphQL operation has a query, not a method or path")
		}
		return
	}
	if op.Query != "" {
		bad("/query", "only a GraphQL operation has a query")
	}
	if !slices.Contains(operationMethods, op.Method) {
		bad("/method", "method must be one of %s", strings.Join(operationMethods, ", "))
	}
	if !strings.HasPrefix(op.Path, "/") || strings.ContainsAny(op.Path, "?#") {
		bad("/path", "a REST path starts with / and has no query or fragment")
	}
}

// apiCallInput reports an input given to an operation that declares
// none; it returns false when it did.
func (u *unit) apiCallInput(g *graph, c vctx) bool {
	ref := literalString(g.steps[stepIndex(c.ptr)].Input["operation"])
	if _, op := u.operation(g, ref); op != nil && op.Input == "" {
		u.report(plxerr.PropTypeMismatch, c.file, c.ptr, "operation %s declares no input", ref)
		return false
	}
	return true
}

// apiCallOutput is the output type of an apiCall step: its operation's
// declared output, nullable, or "".
func (t *typer) apiCallOutput(g *graph, st schema.Step) string {
	_, op := t.u.operation(g, literalString(st.Input["operation"]))
	if op != nil && op.Transfer != nil && op.Transfer.Kind == transferDownload {
		return "string?"
	}
	if op == nil || op.Output == "" {
		return ""
	}
	te, err := parseTypeExpr(op.Output)
	if err != nil || !t.u.typeKnown(g.plugin, te) {
		return ""
	}
	te.nullable = true
	return te.String()
}

// encodeDataConfig builds the configuration value the bundle carries.
func (u *unit) encodeDataConfig(s *schema.DataSource, file, ptr string, sc *scope, baseURLs map[string]any) *value {
	cfg, ok := decodeJSON(s.Config)
	obj, isObj := cfg.(map[string]any)
	if !ok || !isObj {
		return nil
	}
	obj["baseUrls"] = baseURLs
	// The device holds the source to its assurance level (SEC-007): the
	// level travels in the configuration the runtime reads.
	if lvl := s.RequiresAssurance; lvl != "" && lvl != schema.AssuranceLevelAL0 {
		obj["requiresAssurance"] = string(lvl)
	}
	// Mocks are for tests and development builds (DAT-080): a release
	// bundle carries none, so a release can never answer from one.
	if u.opts.Mode != Development {
		delete(obj, "mocks")
		return u.infer(vctx{file: file, ptr: ptr + "/config", scope: sc, code: plxerr.DataSourceConfigInvalid}, obj)
	}
	if mock, ok := decodeJSON(s.Mock); ok && len(s.Mock) > 0 {
		mocks, _ := obj["mocks"].(map[string]any)
		if mocks == nil {
			mocks = map[string]any{}
		}
		mocks["success"] = mock
		obj["mocks"] = mocks
	}
	return u.infer(vctx{file: file, ptr: ptr + "/config", scope: sc, code: plxerr.DataSourceConfigInvalid}, obj)
}

// checkSourceCount enforces data.sourcesPerPlugin: the app's sources, the
// plugin's and its pages' together.
func (u *unit) checkSourceCount(pl *plugin) {
	n := len(u.project.App.Doc.DataSources) + len(pl.doc.DataSources)
	for _, pg := range pl.pages {
		n += len(pg.doc.DataSources)
	}
	if limit := u.opts.Limits.Get(limits.DataSourcesPerPlugin); int64(n) > limit {
		u.report(plxerr.LimitExceeded, pl.file, "/dataSources", "plugin %q sees %d data sources, over data.sourcesPerPlugin = %d", pl.doc.Key, n, limit)
	}
}
