// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata")

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// golden compares got with testdata/<name>, rewriting it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run with -update to rewrite it\n%s", name, got)
	}
}

func codes(ds plxerr.Diagnostics) []plxerr.Code {
	var out []plxerr.Code
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func mustJSON(t *testing.T, r Result) []byte {
	t.Helper()
	if r.Fragment == nil {
		t.Fatalf("no fragment: %v", r.Diagnostics)
	}
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestImportOpenAPIGoldens_DAT_002(t *testing.T) {
	for _, tc := range []struct{ in, golden string }{
		{"petstore31.yaml", "petstore31.golden.json"},
		{"todo30.json", "todo30.golden.json"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			r := ImportOpenAPI(tc.in, read(t, tc.in))
			if len(r.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %v", r.Diagnostics)
			}
			golden(t, tc.golden, mustJSON(t, r))
		})
	}
}

// TestImportOpenAPITypes_DAT_002 checks the shape of the result beyond the
// goldens: one operation per path and method, the base URL as a variable,
// nullable and password fields, and design-time mocks from examples.
func TestImportOpenAPITypes_DAT_002(t *testing.T) {
	r := ImportOpenAPI("petstore31.yaml", read(t, "petstore31.yaml"))
	src := r.Fragment["dataSources"].([]any)[0].(map[string]any)
	ops := src["config"].(map[string]any)["operations"].(map[string]any)
	for _, name := range []string{"createPet", "getPet", "deletePetsByPetId"} {
		if ops[name] == nil {
			t.Errorf("operation %s missing from %v", name, sortedKeys(ops))
		}
	}
	if got := src["config"].(map[string]any)["baseUrl"]; got != "petStoreBaseUrl" {
		t.Errorf("baseUrl = %v", got)
	}
	if got := r.Fragment["baseUrls"].(map[string]any)["petStoreBaseUrl"]; got != "https://eu.petstore.example.com/v1" {
		t.Errorf("base URL hint = %v", got)
	}
	// listPets is the source's own read: its path, type and example are the source's.
	if ops["listPets"] != nil || ops["getPet"].(map[string]any)["auth"] != nil || src["config"].(map[string]any)["auth"] != true ||
		src["type"] != "list<Pet>" || src["config"].(map[string]any)["path"] != "/pets" || src["mock"] == nil {
		t.Error("auth must follow the operation's security requirement")
	}
	raw := string(mustJSON(t, r))
	for _, want := range []string{`"type": "string?"`, `"type": "list<Pet>"`, `"type": "date?"`, `"sensitive": true`, `"x-operationMocks"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("output lacks %s", want)
		}
	}
}

func TestImportOpenAPIReportsUnsupportedConstructs_DAT_002(t *testing.T) {
	r := ImportOpenAPI("unsupported.yaml", read(t, "unsupported.yaml"))
	if len(r.Diagnostics) != 4 {
		t.Fatalf("diagnostics = %v", r.Diagnostics)
	}
	for _, d := range r.Diagnostics {
		if d.Code != plxerr.ImportConstructUnsupported || d.File != "unsupported.yaml" || !strings.HasPrefix(d.Path, "/paths/") {
			t.Errorf("diagnostic %+v is not a located unsupported construct", d)
		}
	}
	cfg := r.Fragment["dataSources"].([]any)[0].(map[string]any)["config"].(map[string]any)
	if cfg["path"] != "/good" || cfg["operations"] != nil {
		t.Errorf("only the supported operation is imported, as the source's read: %v", cfg)
	}
	golden(t, "unsupported.golden.json", mustJSON(t, r))
}

func TestImportOpenAPIRejectsInvalidDocuments_DAT_002(t *testing.T) {
	for name, in := range map[string]string{
		"not a document": "{{{",
		"swagger two":    `{"swagger": "2.0", "info": {"title": "x", "version": "1"}, "paths": {}}`,
		"no info":        `{"openapi": "3.0.0", "paths": {}}`,
	} {
		r := ImportOpenAPI("bad.json", []byte(in))
		if r.Fragment != nil || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != plxerr.ImportDocumentInvalid {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestImportIsDeterministic_DAT_002(t *testing.T) {
	first := mustJSON(t, ImportOpenAPI("petstore31.yaml", read(t, "petstore31.yaml")))
	for range 5 {
		if got := mustJSON(t, ImportOpenAPI("petstore31.yaml", read(t, "petstore31.yaml"))); !bytes.Equal(got, first) {
			t.Fatal("re-importing unchanged input changed the output")
		}
	}
	schema, ops := Source{"schema.graphql", read(t, "schema.graphql")}, []Source{{"ops.graphql", read(t, "ops.graphql")}}
	g1 := mustJSON(t, ImportGraphQL(schema, ops))
	if g2 := mustJSON(t, ImportGraphQL(schema, ops)); !bytes.Equal(g1, g2) {
		t.Fatal("re-importing unchanged GraphQL input changed the output")
	}
}

func TestImportGraphQLGoldens_DAT_002(t *testing.T) {
	r := ImportGraphQL(Source{"schema.graphql", read(t, "schema.graphql")}, []Source{{"ops.graphql", read(t, "ops.graphql")}})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	golden(t, "graphql.golden.json", mustJSON(t, r))
	var frag map[string]any
	if err := json.Unmarshal(mustJSON(t, r), &frag); err != nil {
		t.Fatal(err)
	}
	ops := frag["dataSources"].([]any)[0].(map[string]any)["config"].(map[string]any)["operations"].(map[string]any)
	for _, n := range []string{"getUser", "renameUser"} {
		if ops[n] == nil {
			t.Errorf("operation %s missing", n)
		}
	}
}

func TestImportGraphQLValidatesAgainstTheSchema_DAT_002(t *testing.T) {
	r := ImportGraphQL(Source{"schema.graphql", read(t, "schema.graphql")}, []Source{{"bad.graphql", read(t, "bad.graphql")}})
	var invalid, unsupported int
	for _, d := range r.Diagnostics {
		switch d.Code {
		case plxerr.ImportOperationInvalid:
			invalid++
			if d.File != "bad.graphql" {
				t.Errorf("diagnostic without its file: %+v", d)
			}
		case plxerr.ImportConstructUnsupported:
			unsupported++
		}
	}
	// "nope" is not a field; the anonymous operation has no name.
	if invalid != 2 || unsupported != 2 {
		t.Errorf("invalid=%d unsupported=%d: %v", invalid, unsupported, r.Diagnostics)
	}
	if !strings.Contains(r.Diagnostics[0].Message, "bad.graphql:3:") {
		t.Errorf("message does not locate the error: %s", r.Diagnostics[0].Message)
	}
	cfg := r.Fragment["dataSources"].([]any)[0].(map[string]any)["config"].(map[string]any)
	if q, _ := cfg["query"].(string); !strings.Contains(q, "query Fine") || cfg["operations"] != nil {
		t.Errorf("only the valid operation is imported, as the source's read: %v", cfg)
	}
}

// TestImportWithoutAReadOperationWritesNoSource_DAT_002 checks that a source
// is never written with a placeholder request.
func TestImportWithoutAReadOperationWritesNoSource_DAT_002(t *testing.T) {
	r := ImportGraphQL(Source{"schema.graphql", read(t, "schema.graphql")}, []Source{{"ops.graphql", []byte("mutation Rename($input: RenameInput!) { renameUser(input: $input) { id } }")}})
	if srcs := r.Fragment["dataSources"].([]any); len(srcs) != 0 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != plxerr.ImportConstructUnsupported {
		t.Errorf("%+v", r)
	}
	o := ImportOpenAPI("odd.yaml", []byte("openapi: 3.0.3\ninfo: {title: T, version: '1'}\npaths:\n  /x/{id}:\n    get:\n      parameters:\n        - {name: id, in: path, required: true, schema: {type: string}}\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema: {type: object, properties: {a: {type: string}}}\n"))
	if srcs := o.Fragment["dataSources"].([]any); len(srcs) != 0 || len(o.Diagnostics) != 1 {
		t.Errorf("%+v", o)
	}
}

func TestImportGraphQLRejectsABrokenSchema_DAT_002(t *testing.T) {
	r := ImportGraphQL(Source{"s.graphql", []byte("type Query {")}, nil)
	if r.Fragment != nil || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != plxerr.ImportDocumentInvalid {
		t.Errorf("%+v", r)
	}
}

func serve(t *testing.T, file string, seed uint64) *httptest.Server {
	t.Helper()
	m, diags := NewMock(file, read(t, file), seed)
	if m == nil {
		t.Fatal(diags)
	}
	s := httptest.NewServer(m)
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestMockServesExamplesAndGeneratedValues_TST_004(t *testing.T) {
	s := serve(t, "petstore31.yaml", 1)
	status, body := get(t, "GET", s.URL+"/v1/pets/anything")
	if status != 200 || !strings.Contains(body, `"nickname":"Rexy"`) {
		t.Errorf("example response = %d %s", status, body)
	}
	status, body = get(t, "POST", s.URL+"/pets")
	var pet map[string]any
	if status != 201 || json.Unmarshal([]byte(body), &pet) != nil || pet["id"] == nil || pet["status"] == nil {
		t.Errorf("generated response = %d %s", status, body)
	}
	if status, _ = get(t, "DELETE", s.URL+"/pets/1"); status != 204 {
		t.Errorf("delete status = %d", status)
	}
	if status, _ = get(t, "PATCH", s.URL+"/pets"); status != 405 {
		t.Errorf("patch status = %d", status)
	}
	if status, _ = get(t, "GET", s.URL+"/nothing"); status != 404 {
		t.Errorf("unknown path status = %d", status)
	}
}

// TestMockIsDeterministicForASeed_TST_004 checks that the same seed gives the
// same bodies in any order of requests, and another seed another body.
func TestMockIsDeterministicForASeed_TST_004(t *testing.T) {
	paths := []string{"/todos", "/todos/7"}
	bodies := func(seed uint64, order []string) map[string]string {
		s := serve(t, "todo30.json", seed)
		out := map[string]string{}
		for _, p := range order {
			method := "GET"
			if strings.HasSuffix(p, "7") {
				method = "PUT"
			}
			_, out[p] = get(t, method, s.URL+p)
		}
		return out
	}
	a := bodies(42, paths)
	b := bodies(42, []string{paths[1], paths[0]})
	if a["/todos"] != b["/todos"] || a["/todos/7"] != b["/todos/7"] {
		t.Errorf("same seed, different bodies: %v %v", a, b)
	}
	if c := bodies(43, paths); c["/todos"] == a["/todos"] {
		t.Errorf("another seed gave the same generated body %s", c["/todos"])
	}
}

func TestMockRejectsInvalidDocuments_TST_004(t *testing.T) {
	if m, diags := NewMock("x.json", []byte("{"), 1); m != nil || len(diags) != 1 {
		t.Errorf("%v %v", m, diags)
	}
}
