// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"regexp"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// dataEvent is a trigger a data source declares.
type dataEvent struct {
	kind fbs.TriggerKind
	eh   *schema.EventHandler
	name string
}

// dataEvents lists the triggers of a data source, in a fixed order.
func dataEvents(ds schema.DataSourceTriggers) []dataEvent {
	return []dataEvent{
		{fbs.TriggerKindDataLoaded, ds.OnLoaded, "onLoaded"},
		{fbs.TriggerKindDataFailed, ds.OnFailed, "onFailed"},
		{fbs.TriggerKindDataMessage, ds.OnMessage, "onMessage"},
		{fbs.TriggerKindDataProgress, ds.OnProgress, "onProgress"},
		{fbs.TriggerKindOutboxSynced, ds.OnSynced, "onSynced"},
		{fbs.TriggerKindOutboxFailed, ds.OnSyncFailed, "onSyncFailed"},
		{fbs.TriggerKindOutboxConflict, ds.OnConflict, "onConflict"},
	}
}

// dataIOPayload returns the type of `event` for the triggers of streams,
// transfers and the outbox: a stream's message has the source's type, a
// transfer's progress and an outbox entry's result are synthesised
// objects (DAT-012, DAT-020, DAT-031).
func (t *typer) dataIOPayload(tr *trigger, s *scope) (string, bool) {
	src := sourceNamed(s.sources, tr.name)
	if src == nil {
		t.u.report(plxerr.InvalidTrigger, tr.file, tr.ptr, "no data source %q is visible here", tr.name)
		return "", false
	}
	var payload string
	var fields [][2]string
	switch tr.kind {
	case fbs.TriggerKindDataMessage:
		return src.typ, true
	case fbs.TriggerKindDataProgress:
		payload = "PluxTransferProgress"
		fields = [][2]string{{"operation", "string"}, {"sent", "int"}, {"total", "int"}}
	default:
		payload = "PluxOutboxEntry"
		fields = [][2]string{{"operation", "string"}, {"key", "string"}, {"status", "int"}}
	}
	tr.graph.eventTypes = map[string]pxl.TypeSpec{payload: objectType(fields)}
	return payload, true
}

// The features of the data layer's second half (ADR-0048, P5 R5): a bundle
// with a stream, an offline mutation or a file transfer requires the
// runtime that runs it, first in runtime 0.3.0.
const (
	streamsFeature   = "data.streams"
	outboxFeature    = "data.outbox"
	transfersFeature = "data.transfers"
)

// ioRuntimes lists the runtime each revision of the streams, outbox and
// transfers features first shipped in.
var ioRuntimes = []string{"0.3.0"}

// The directions of a transfer.
const (
	transferUpload   = "upload"
	transferDownload = "download"
)

// transferConfig makes an operation a file upload or download (DAT-031):
// the input field fileParam names the file (the path of the file to
// upload, the plain name a download is saved under).
type transferConfig struct {
	Kind        string `json:"kind"`
	Body        string `json:"body"`
	FileParam   string `json:"fileParam"`
	Field       string `json:"field"`
	ContentType string `json:"contentType"`
}

var (
	paramName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	formFieldName   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	mediaType       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*/[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*$`)
	uploadMethods   = []string{"POST", "PUT", "PATCH"}
	mutatingMethods = []string{"POST", "PUT", "PATCH", "DELETE"}
)

// isStreamKind reports whether a source kind is a stream the runtime
// opens: WebSocket or server-sent events (DAT-012).
func isStreamKind(k schema.DataSourceKind) bool {
	return k == schema.DataSourceKindWebsocket || k == schema.DataSourceKindSSE
}

// streamSource finds the source a `subscribe` step names in a graph's
// scope: a WebSocket or SSE source, or a GraphQL source with a
// subscription.
func (u *unit) streamSource(g *graph, name string) *schema.DataSource {
	for _, s := range u.dataSourcesOf(g) {
		if s.Name != name {
			continue
		}
		if isStreamKind(s.Kind) {
			return s
		}
		if s.Kind == schema.DataSourceKindGraphql {
			if p := u.dataConfigOf(s); p.cfg != nil && p.cfg.Subscription != "" {
				return s
			}
		}
		return nil
	}
	return nil
}

// isMutation reports whether an operation changes data on the server: a
// REST operation by its method, a GraphQL one by its document.
func isMutation(op *operationConfig, graphql bool) bool {
	if graphql {
		return strings.HasPrefix(strings.TrimSpace(op.Query), "mutation")
	}
	return slices.Contains(mutatingMethods, op.Method)
}

// checkStreamAndOutbox checks what a source declares for the streams and
// the outbox: a stream's configuration, a GraphQL subscription and the
// offline-capable mutations (DAT-012, DAT-020).
func (u *unit) checkStreamAndOutbox(pl *plugin, s *schema.DataSource, c *dataConfig, file, ptr, cptr string) {
	ctx := vctx{file: file, ptr: ptr, pl: pl}
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataStreamInvalid, file, cptr+sub, format, args...)
	}
	switch {
	case isStreamKind(s.Kind):
		u.useRevision(streamsFeature, ioRuntimes, 1, ctx)
		u.checkStream(s, c, bad)
	case s.Kind == schema.DataSourceKindGraphql && c.Subscription != "":
		u.useRevision(streamsFeature, ioRuntimes, 1, ctx)
		if !strings.HasPrefix(strings.TrimSpace(c.Subscription), "subscription") {
			bad("/subscription", "a GraphQL subscription needs a subscription document")
		}
	case c.Subscription != "":
		bad("/subscription", "only a GraphQL source has a subscription")
	}
	if c.SubscribeMessage != nil && s.Kind != schema.DataSourceKindWebsocket {
		bad("/subscribeMessage", "only a WebSocket source sends a message after connecting")
	}
	u.checkOffline(pl, s, c, file, cptr)
}

// checkStream checks the properties a WebSocket or SSE source does not
// have.
func (u *unit) checkStream(s *schema.DataSource, c *dataConfig, bad func(sub, format string, args ...any)) {
	for _, p := range []struct {
		set  bool
		name string
	}{
		{c.Query != "", "query"},
		{c.Method != "", "method"},
		{c.Subscription != "", "subscription"},
		{len(c.Operations) > 0, "operations"},
		{c.Cache != nil, "cache"},
		{c.Pagination != nil, "pagination"},
		{c.OfflineCapable, "offlineCapable"},
	} {
		if p.set {
			bad("/"+p.name, "a %s source has no %s: it receives messages", s.Kind, p.name)
		}
	}
	if hasExpr(c.SubscribeMessage) {
		bad("/subscribeMessage", "the message sent after connecting is a literal")
	}
}

// checkOffline checks the mutations marked offlineCapable and raises the
// outbox feature for those the outbox queues (DAT-020).
func (u *unit) checkOffline(pl *plugin, s *schema.DataSource, c *dataConfig, file, cptr string) {
	graphql := s.Kind == schema.DataSourceKindGraphql
	for _, name := range sortedKeys(c.Operations) {
		op := c.Operations[name]
		if op == nil {
			continue
		}
		optr := cptr + plxerr.Pointer("operations", name)
		mutation := isMutation(op, graphql)
		explicit := op.OfflineCapable != nil && *op.OfflineCapable
		offline := explicit || (op.OfflineCapable == nil && c.OfflineCapable)
		switch {
		case explicit && !mutation:
			u.report(plxerr.DataOutboxInvalid, file, optr+"/offlineCapable", "operation %q does not mutate, so there is nothing to replay", name)
		case explicit && op.Transfer != nil:
			u.report(plxerr.DataOutboxInvalid, file, optr+"/offlineCapable", "operation %q is a file transfer, which the outbox does not queue", name)
		case offline && mutation && op.Transfer == nil:
			u.useRevision(outboxFeature, ioRuntimes, 1, vctx{file: file, ptr: optr, pl: pl})
		}
	}
}

// checkTransfer checks an operation that uploads or downloads a file
// (DAT-031).
func (u *unit) checkTransfer(pl *plugin, op *operationConfig, graphql bool, file, ptr string) {
	t := op.Transfer
	if t == nil {
		return
	}
	tptr := ptr + "/transfer"
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataTransferInvalid, file, tptr+sub, format, args...)
	}
	u.useRevision(transfersFeature, ioRuntimes, 1, vctx{file: file, ptr: ptr, pl: pl})
	if graphql {
		bad("", "only a REST operation transfers files")
		return
	}
	switch t.Kind {
	case transferUpload:
		checkUpload(op, t, bad)
	case transferDownload:
		if op.Method != "GET" {
			bad("/kind", "a download reads with GET, not %s", op.Method)
		}
		if t.Body != "" || t.Field != "" || t.ContentType != "" {
			bad("", "a download has no body, field or contentType")
		}
	default:
		bad("/kind", "%q is not a transfer: upload or download", t.Kind)
	}
	u.checkFileParam(pl, op, t, tptr, file)
}

// checkUpload checks an upload's method, body mode and its details.
func checkUpload(op *operationConfig, t *transferConfig, bad func(sub, format string, args ...any)) {
	if !slices.Contains(uploadMethods, op.Method) {
		bad("/kind", "an upload sends with POST, PUT or PATCH, not %s", op.Method)
	}
	switch t.Body {
	case "", "multipart":
		if t.ContentType != "" {
			bad("/contentType", "a multipart upload sets its own content type")
		}
		if t.Field != "" && !formFieldName.MatchString(t.Field) {
			bad("/field", "%q is not a form field name", t.Field)
		}
	case "raw":
		if t.Field != "" {
			bad("/field", "a raw upload has no form field")
		}
		if t.ContentType != "" && !mediaType.MatchString(t.ContentType) {
			bad("/contentType", "%q is not a media type", t.ContentType)
		}
	default:
		bad("/body", "%q is not a body mode: multipart or raw", t.Body)
	}
}

// checkFileParam checks that the transfer's file parameter is a string
// field of the operation's input.
func (u *unit) checkFileParam(pl *plugin, op *operationConfig, t *transferConfig, tptr, file string) {
	bad := func(sub, format string, args ...any) {
		u.report(plxerr.DataTransferInvalid, file, tptr+sub, format, args...)
	}
	if !paramName.MatchString(t.FileParam) {
		bad("/fileParam", "fileParam names the input field holding the file")
		return
	}
	if op.Input == "" {
		bad("/fileParam", "a transfer needs an input type with the field %q", t.FileParam)
		return
	}
	spec, ok := u.declared(pl, op.Input)
	if !ok {
		return
	}
	if spec.Fields[t.FileParam] != "string" {
		bad("/fileParam", "input type %s has no string field %q", op.Input, t.FileParam)
	}
}
