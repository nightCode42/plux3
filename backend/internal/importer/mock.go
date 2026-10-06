// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// maxGenerateDepth bounds how deep generated values nest, which also ends
// recursive schemas.
const maxGenerateDepth = 5

// mockRoute is one operation the mock serves.
type mockRoute struct {
	method   string
	segments []string
	status   int
	media    *openapi3.MediaType
	hasBody  bool
	salt     uint64
}

// Mock serves the operations of an OpenAPI document with the examples it
// carries, or with values generated from its schemas. The same seed always
// produces the same responses, whatever the order of requests (TST-004).
type Mock struct {
	routes []mockRoute
	prefix string
	seed   uint64
}

// NewMock builds a mock from an OpenAPI 3.x document.
func NewMock(ctx context.Context, file string, data []byte, seed uint64) (*Mock, plxerr.Diagnostics) {
	doc, diags := LoadOpenAPI(ctx, file, data)
	if doc == nil {
		return nil, diags
	}
	m := &Mock{seed: seed}
	if len(doc.Servers) > 0 {
		if u, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(doc.Servers[0].URL, "{", ""), "}", "")); err == nil {
			m.prefix = strings.TrimSuffix(u.Path, "/")
		}
	}
	if doc.Paths == nil {
		return m, nil
	}
	for _, p := range sortedKeys(doc.Paths.Map()) {
		all := doc.Paths.Map()[p].Operations()
		for _, method := range sortedKeys(all) {
			m.routes = append(m.routes, newRoute(p, method, all[method]))
		}
	}
	// Literal paths win over templated ones: fewer placeholders first.
	slices.SortStableFunc(m.routes, func(a, b mockRoute) int {
		return placeholders(a.segments) - placeholders(b.segments)
	})
	return m, nil
}

func placeholders(segments []string) int {
	n := 0
	for _, s := range segments {
		if strings.HasPrefix(s, "{") {
			n++
		}
	}
	return n
}

// newRoute picks the response a route serves: the lowest 2xx status.
func newRoute(p, method string, op *openapi3.Operation) mockRoute {
	h := fnv.New64a()
	_, _ = h.Write([]byte(method + " " + p))
	r := mockRoute{method: method, segments: strings.Split(strings.Trim(p, "/"), "/"), status: http.StatusOK, salt: h.Sum64()}
	if op.Responses == nil {
		return r
	}
	var codes []string
	for code := range op.Responses.Map() {
		if n, err := strconv.Atoi(code); err == nil && n >= 200 && n < 300 {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return r
	}
	slices.Sort(codes)
	r.status, _ = strconv.Atoi(codes[0])
	if resp := op.Responses.Map()[codes[0]]; resp != nil && resp.Value != nil {
		if media := jsonMedia(resp.Value.Content); media != nil {
			r.media, r.hasBody = media, true
		}
	}
	return r
}

// ServeHTTP answers a request from the first route that matches.
func (m *Mock) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	segs := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if m.prefix != "" && strings.HasPrefix(req.URL.Path, m.prefix+"/") {
		segs = strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, m.prefix), "/"), "/")
	}
	pathMatched := false
	for _, r := range m.routes {
		if !matches(r.segments, segs) {
			continue
		}
		pathMatched = true
		if r.method != req.Method {
			continue
		}
		m.respond(w, r)
		return
	}
	status, msg := http.StatusNotFound, "no operation at this path"
	if pathMatched {
		status, msg = http.StatusMethodNotAllowed, "no operation for this method"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func matches(template, segs []string) bool {
	if len(template) != len(segs) {
		return false
	}
	for i, t := range template {
		if !strings.HasPrefix(t, "{") && t != segs[i] {
			return false
		}
	}
	return true
}

// respond writes the route's example, or a value generated from its schema.
func (m *Mock) respond(w http.ResponseWriter, r mockRoute) {
	if !r.hasBody || r.status == http.StatusNoContent {
		w.WriteHeader(r.status)
		return
	}
	var v any
	if ex := exampleOf(r.media); ex != nil {
		v = ex
	} else if r.media.Schema != nil {
		g := &generator{rng: rand.New(rand.NewPCG(m.seed, r.salt))} //nolint:gosec // G404: reproducible mock data, not a secret.
		v = g.value(r.media.Schema, 0)
	}
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "the example cannot be encoded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.status)
	_, _ = w.Write(body)
}

// generator makes values from schemas with a seeded random source.
type generator struct {
	rng *rand.Rand
}

// value generates a value valid for the schema. Properties are visited in
// sorted order so that the sequence of random draws does not depend on map
// iteration.
func (g *generator) value(ref *openapi3.SchemaRef, depth int) any {
	if ref == nil || ref.Value == nil {
		return nil
	}
	s := ref.Value
	if s.Example != nil {
		return s.Example
	}
	if len(s.Enum) > 0 {
		return s.Enum[g.rng.IntN(len(s.Enum))]
	}
	if v, ok := g.composed(s, depth); ok {
		return v
	}
	switch schemaKind(s) {
	case "string":
		return g.str(s)
	case "integer":
		lo, hi := bounds(s, 1, 1000)
		return int64(lo) + g.rng.Int64N(int64(hi)-int64(lo)+1)
	case "number":
		lo, hi := bounds(s, 0, 1000)
		return math.Round((lo+g.rng.Float64()*(hi-lo))*100) / 100
	case "boolean":
		return g.rng.IntN(2) == 0
	case "array":
		n := itemCount(s, depth)
		out := make([]any, 0, n)
		for range n {
			out = append(out, g.value(s.Items, depth+1))
		}
		return out
	}
	return g.object(s, depth)
}

// composed generates a value for a schema built with allOf, oneOf or
// anyOf; ok is false for any other schema.
func (g *generator) composed(s *openapi3.Schema, depth int) (v any, ok bool) {
	switch {
	case len(s.AllOf) > 0:
		merged := map[string]any{}
		for _, part := range s.AllOf {
			if obj, isObj := g.value(part, depth).(map[string]any); isObj {
				for k, v := range obj {
					merged[k] = v
				}
			}
		}
		return merged, true
	case len(s.OneOf) > 0:
		return g.value(s.OneOf[0], depth), true
	case len(s.AnyOf) > 0:
		return g.value(s.AnyOf[0], depth), true
	}
	return nil, false
}

// schemaKind is the first type of a schema other than null; a schema with
// no type is an array when it has items and no properties, else an object.
func schemaKind(s *openapi3.Schema) string {
	if s.Type == nil {
		if len(s.Properties) == 0 && s.Items != nil {
			return "array"
		}
		return "object"
	}
	for _, t := range s.Type.Slice() {
		if t != "null" {
			return t
		}
	}
	return "object"
}

// itemCount is how many items a generated array has: two, or the schema's
// minimum when that is higher, but never more than its maximum; no items at
// the depth limit beyond the minimum.
func itemCount(s *openapi3.Schema, depth int) int {
	minItems := clampInt(s.MinItems)
	n := minItems
	if n == 0 {
		n = 2
	}
	if s.MaxItems != nil {
		n = min(n, clampInt(*s.MaxItems))
	}
	if depth >= maxGenerateDepth {
		n = minItems
	}
	return n
}

// clampInt converts u to an int, cut at the largest 32-bit value.
func clampInt(u uint64) int {
	if u > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(u)
}

func (g *generator) object(s *openapi3.Schema, depth int) any {
	out := map[string]any{}
	if depth >= maxGenerateDepth {
		return out
	}
	for _, k := range sortedKeys(s.Properties) {
		out[k] = g.value(s.Properties[k], depth+1)
	}
	if len(s.Properties) == 0 && s.AdditionalProperties.Schema != nil {
		out["key"] = g.value(s.AdditionalProperties.Schema, depth+1)
	}
	return out
}

// bounds returns the inclusive range of a numeric schema.
func bounds(s *openapi3.Schema, lo, hi float64) (float64, float64) {
	if s.Min != nil {
		lo = *s.Min
		if hi < lo {
			hi = lo + 1000
		}
	}
	if s.Max != nil {
		hi = *s.Max
		if lo > hi {
			lo = hi - 1000
		}
	}
	return lo, hi
}

// str generates a string for the schema's format.
func (g *generator) str(s *openapi3.Schema) string {
	switch s.Format {
	case "date":
		return fmt.Sprintf("2026-%02d-%02d", 1+g.rng.IntN(12), 1+g.rng.IntN(28))
	case "date-time":
		return fmt.Sprintf("2026-%02d-%02dT%02d:%02d:00Z", 1+g.rng.IntN(12), 1+g.rng.IntN(28), g.rng.IntN(24), g.rng.IntN(60))
	case "uuid":
		return fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x", g.rng.Uint32(), g.rng.Uint32()&0xffff, g.rng.Uint32()&0xfff, g.rng.Uint32()&0xfff, g.rng.Uint64()&0xffffffffffff)
	case "email":
		return fmt.Sprintf("user%d@example.com", g.rng.IntN(1000))
	case "uri", "url":
		return fmt.Sprintf("https://example.com/%d", g.rng.IntN(1000))
	}
	out := "string" + strconv.Itoa(g.rng.IntN(1000))
	for uint64(len(out)) < s.MinLength {
		out += "x"
	}
	if s.MaxLength != nil && uint64(len(out)) > *s.MaxLength {
		out = out[:*s.MaxLength]
	}
	return out
}
