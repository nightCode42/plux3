// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const ordersID = "01d0c450-6c00-7000-8000-0000000000a0"

// withSource appends a data source of type Task to the shop plugin.
func withSource(t *testing.T, m fstest.MapFS, id, name, kind, config string) {
	t.Helper()
	edit(t, m, shopFile, func(doc map[string]any) {
		doc["dataSources"] = append(doc["dataSources"].([]any), map[string]any{
			"id": id, "name": name, "kind": kind, "type": "Task",
			"mock":   map[string]any{"id": "t1", "title": "Mocked", "done": false},
			"config": raw(t, config),
		})
	})
}

const ordersStream = `{"baseUrl": "apiBaseUrl", "path": "/orders/{orderId}", "params": {"orderId": "o1"}, "subscribeMessage": {"op": "watch"}}`

// subscribeStep makes the tasks page's button press subscribe to a stream.
func subscribeStep(t *testing.T, m fstest.MapFS, stream string) {
	t.Helper()
	edit(t, m, "plugins/shop/pages/tasks.page.json", func(doc map[string]any) {
		at(t, doc, "root/slots/body/children/2/events/onPressed")["steps"] = raw(t,
			`[{"action": "subscribe", "id": "sub", "input": {"stream": "`+stream+`"}, "next": "unsub"},
			  {"action": "unsubscribe", "id": "unsub", "input": {"stream": "`+stream+`"}}]`)
	})
}

// TestStreamsRequireTheirFeature checks that a WebSocket source, a
// subscribe step and the message trigger compile, and that the bundle
// then requires data.streams.v1, first in runtime 0.3.0.
// Verifies: DAT-012, BND-008.
func TestStreamsRequireTheirFeature(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	withSource(t, m, ordersID, "orders", "websocket", ordersStream)
	subscribeStep(t, m, "orders")
	edit(t, m, shopFile, func(doc map[string]any) {
		doc["triggers"] = raw(t, `{"dataSources": {"orders": {"onMessage": {"steps": [{"id": "s", "action": "sync"}]}}}}`)
	})
	res := compileFS(m)
	onlyRaised(t, res)
	for _, f := range []string{"data.v1", "data.streams.v1"} {
		if !slices.Contains(res.Plugins[0].Features, f) {
			t.Errorf("plugin features %v lack %s", res.Plugins[0].Features, f)
		}
	}
	edit(t, m, "app.json", func(doc map[string]any) { doc["requiredFeatures"] = "reject" })
	wantDiag(t, compileFS(m), plxerr.RuntimeTooOld, shopFile, "/dataSources/3")
}

// TestSSEAndSubscriptionSourcesCompile checks an SSE source and a GraphQL
// source with a subscription.
// Verifies: DAT-012.
func TestSSEAndSubscriptionSourcesCompile(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	withSource(t, m, ordersID, "feed", "sse", `{"baseUrl": "apiBaseUrl", "path": "/feed", "auth": true}`)
	edit(t, m, shopFile, func(doc map[string]any) {
		at(t, doc, "dataSources/2/config")["subscription"] = "subscription Watch { me { name } }"
	})
	subscribeStep(t, m, "profile")
	onlyRaised(t, compileFS(m))
}

// TestInvalidStreams checks each compile-time check of streams.
// Verifies: DAT-012.
func TestInvalidStreams(t *testing.T) {
	t.Parallel()
	const cfg = "/dataSources/3/config"
	cases := []struct {
		name, kind, config string
		ptr                string
	}{
		{"query on a stream", "websocket", `{"baseUrl": "apiBaseUrl", "path": "/o", "query": "query { x }"}`, cfg + "/query"},
		{"method on a stream", "sse", `{"baseUrl": "apiBaseUrl", "path": "/o", "method": "GET"}`, cfg + "/method"},
		{"cache on a stream", "sse", `{"baseUrl": "apiBaseUrl", "path": "/o", "cache": {"policy": "networkOnly"}}`, cfg + "/cache"},
		{"operations on a stream", "websocket", `{"baseUrl": "apiBaseUrl", "path": "/o", "operations": {"x": {"method": "POST", "path": "/x"}}}`, cfg + "/operations"},
		{"message sent by SSE", "sse", `{"baseUrl": "apiBaseUrl", "path": "/o", "subscribeMessage": {}}`, cfg + "/subscribeMessage"},
		{"message with a binding", "websocket", `{"baseUrl": "apiBaseUrl", "path": "/o", "subscribeMessage": {"a": {"$expr": "1"}}}`, cfg + "/subscribeMessage"},
		{"subscription on a stream", "websocket", `{"baseUrl": "apiBaseUrl", "path": "/o", "subscription": "subscription S { a }"}`, cfg + "/subscription"},
		{"subscription on REST", "rest", `{"baseUrl": "apiBaseUrl", "path": "/o", "subscription": "subscription S { a }"}`, cfg + "/subscription"},
		{"subscription that is a query", "graphql", `{"baseUrl": "graphUrl", "path": "/graphql", "query": "query Q { a }", "subscription": "query S { a }"}`, cfg + "/subscription"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, dataDir)
			withSource(t, m, ordersID, "orders", c.kind, c.config)
			wantDiag(t, compileFS(m), plxerr.DataStreamInvalid, shopFile, c.ptr)
		})
	}
}

// TestSubscribeNamesAStream checks that subscribe resolves only streams.
// Verifies: DAT-012.
func TestSubscribeNamesAStream(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	subscribeStep(t, m, "count")
	wantDiag(t, compileFS(m), plxerr.UnresolvedReference, "plugins/shop/pages/tasks.page.json", "/root/slots/body/children/2/events/onPressed/steps/0/input/stream")
}

// TestOfflineMutations checks that offlineCapable operations raise
// data.outbox.v1 and that only mutations may be marked.
// Verifies: DAT-020, BND-008.
func TestOfflineMutations(t *testing.T) {
	t.Parallel()
	m := project(t, dataDir)
	onSource("0", func(_ *testing.T, _, c map[string]any) {
		at(t, c, "operations/createTask")["offlineCapable"] = true
	})(t, m)
	res := compileFS(m)
	onlyRaised(t, res)
	if !slices.Contains(res.Plugins[0].Features, "data.outbox.v1") {
		t.Errorf("plugin features %v lack data.outbox.v1", res.Plugins[0].Features)
	}

	m = project(t, dataDir)
	onSource("0", func(_ *testing.T, _, c map[string]any) { c["offlineCapable"] = true })(t, m)
	res = compileFS(m)
	onlyRaised(t, res)
	if !slices.Contains(res.Plugins[0].Features, "data.outbox.v1") {
		t.Errorf("a source marked offlineCapable does not raise data.outbox.v1: %v", res.Plugins[0].Features)
	}

	m = project(t, dataDir)
	onSource("0", func(_ *testing.T, _, c map[string]any) {
		at(t, c, "operations/createTask")["method"] = "GET"
		at(t, c, "operations/createTask")["offlineCapable"] = true
	})(t, m)
	wantDiag(t, compileFS(m), plxerr.DataOutboxInvalid, shopFile, "/dataSources/0/config/operations/createTask/offlineCapable")

	m = project(t, dataDir)
	onSource("2", func(_ *testing.T, _, c map[string]any) {
		at(t, c, "operations/rename")["query"] = "query Q { me { name } }"
		at(t, c, "operations/rename")["offlineCapable"] = true
	})(t, m)
	wantDiag(t, compileFS(m), plxerr.DataOutboxInvalid, shopFile, "/dataSources/2/config/operations/rename/offlineCapable")
}

// transferCase breaks or builds a transfer on the tasks source.
type transferCase struct {
	name     string
	op       string
	wantCode plxerr.Code
	ptr      string
}

// TestTransfers checks uploads and downloads and their checks.
// Verifies: DAT-031, BND-008.
func TestTransfers(t *testing.T) {
	t.Parallel()
	const op = "/dataSources/0/config/operations/send"
	cases := []transferCase{
		{"multipart upload", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "fileParam": "path", "field": "attachment"}}`, 0, ""},
		{"raw upload", `{"method": "PUT", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "body": "raw", "fileParam": "path", "contentType": "image/png"}}`, 0, ""},
		{"download", `{"method": "GET", "path": "/files", "input": "Upload", "transfer": {"kind": "download", "fileParam": "path"}}`, 0, ""},
		{"unknown kind", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "sync", "fileParam": "path"}}`, plxerr.DataTransferInvalid, op + "/transfer/kind"},
		{"upload with GET", `{"method": "GET", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "fileParam": "path"}}`, plxerr.DataTransferInvalid, op + "/transfer/kind"},
		{"download with POST", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "download", "fileParam": "path"}}`, plxerr.DataTransferInvalid, op + "/transfer/kind"},
		{"unknown body", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "body": "chunked", "fileParam": "path"}}`, plxerr.DataTransferInvalid, op + "/transfer/body"},
		{"content type on multipart", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "fileParam": "path", "contentType": "image/png"}}`, plxerr.DataTransferInvalid, op + "/transfer/contentType"},
		{"field on a raw upload", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "body": "raw", "fileParam": "path", "field": "f"}}`, plxerr.DataTransferInvalid, op + "/transfer/field"},
		{"no file parameter", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload"}}`, plxerr.DataTransferInvalid, op + "/transfer/fileParam"},
		{"file parameter not in the input", `{"method": "POST", "path": "/files", "input": "Upload", "transfer": {"kind": "upload", "fileParam": "nope"}}`, plxerr.DataTransferInvalid, op + "/transfer/fileParam"},
		{"no input", `{"method": "POST", "path": "/files", "transfer": {"kind": "upload", "fileParam": "path"}}`, plxerr.DataTransferInvalid, op + "/transfer/fileParam"},
		{"offline upload", `{"method": "POST", "path": "/files", "input": "Upload", "offlineCapable": true, "transfer": {"kind": "upload", "fileParam": "path"}}`, plxerr.DataOutboxInvalid, op + "/offlineCapable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, dataDir)
			edit(t, m, "app.json", func(doc map[string]any) {
				types := doc["types"].([]any)
				doc["types"] = append(types, raw(t, `{"name": "Upload", "fields": [{"name": "path", "type": "string"}]}`))
			})
			onSource("0", func(_ *testing.T, _, cfg map[string]any) {
				at(t, cfg, "operations")["send"] = raw(t, c.op)
			})(t, m)
			res := compileFS(m)
			if c.wantCode != 0 {
				wantDiag(t, res, c.wantCode, shopFile, c.ptr)
				return
			}
			onlyRaised(t, res)
			if !slices.Contains(res.Plugins[0].Features, "data.transfers.v1") {
				t.Errorf("plugin features %v lack data.transfers.v1", res.Plugins[0].Features)
			}
		})
	}
}
