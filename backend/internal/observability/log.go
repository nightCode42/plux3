// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Sensitive marks a value that must never reach a log, a trace or an
// error message. A type implements it by declaring itself sensitive; the
// handler then logs the placeholder instead of the value (SEC-092).
type Sensitive interface {
	// Redacted returns what may be logged in the value's place.
	Redacted() string
}

// redactedKeys are attribute names whose value is always replaced,
// whatever its type. The match is case-insensitive and on substrings, so
// "databaseURL" and "X-Api-Key" are both caught.
var redactedKeys = []string{
	"password", "passphrase", "secret", "token", "credential", "authorization",
	"cookie", "apikey", "api_key", "privatekey", "private_key", "signature",
	"proof", "assertion", "jwt", "session", "url",
}

// placeholder replaces a redacted value.
const placeholder = "[redacted]"

// redact reports whether an attribute's key demands redaction.
func redact(key string) bool {
	k := strings.ToLower(key)
	for _, r := range redactedKeys {
		if strings.Contains(k, r) {
			return true
		}
	}
	return false
}

// handler wraps another slog.Handler and redacts as described above.
type handler struct {
	inner slog.Handler
}

// Enabled reports whether the inner handler handles this level.
func (h handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

// Handle redacts the record's attributes and adds the trace and span IDs
// of the context, then passes it on (OBS-001, OBS-003).
func (h handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(clean(a))
		return true
	})
	if id := RequestID(ctx); id != "" {
		out.AddAttrs(slog.String("requestID", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		out.AddAttrs(slog.String("traceID", sc.TraceID().String()), slog.String("spanID", sc.SpanID().String()))
	}
	if err := h.inner.Handle(ctx, out); err != nil {
		return fmt.Errorf("observability: write a log record: %w", err)
	}
	return nil
}

// WithAttrs redacts the attributes before the inner handler stores them.
func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		cleaned[i] = clean(a)
	}
	return handler{inner: h.inner.WithAttrs(cleaned)}
}

// WithGroup opens a group on the inner handler.
func (h handler) WithGroup(name string) slog.Handler { return handler{inner: h.inner.WithGroup(name)} }

// clean replaces an attribute's value when its key or its type demands it,
// and recurses into groups.
func clean(a slog.Attr) slog.Attr {
	if redact(a.Key) {
		return slog.String(a.Key, placeholder)
	}
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		attrs := v.Group()
		cleaned := make([]any, len(attrs))
		for i, in := range attrs {
			cleaned[i] = clean(in)
		}
		return slog.Group(a.Key, cleaned...)
	}
	if s, ok := v.Any().(Sensitive); ok {
		return slog.String(a.Key, s.Redacted())
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// LogOptions configures NewLogger.
type LogOptions struct {
	// Level is "debug", "info", "warn" or "error".
	Level string
	// Format is "json" for deployments or "text" for a terminal.
	Format string
	// Role labels every record with the role of this process.
	Role string
	// Version labels every record with the build version.
	Version string
}

// NewLogger builds the process logger (OBS-003). An unknown level is
// treated as info rather than refused: the configuration has already been
// validated, and a logger is needed to report anything at all.
func NewLogger(w io.Writer, opts LogOptions) *slog.Logger {
	var level slog.Level
	switch opts.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	ho := &slog.HandlerOptions{Level: level}
	var inner slog.Handler = slog.NewJSONHandler(w, ho)
	if opts.Format == "text" {
		inner = slog.NewTextHandler(w, ho)
	}
	l := slog.New(handler{inner: inner})
	if opts.Role != "" {
		l = l.With(slog.String("role", opts.Role))
	}
	if opts.Version != "" {
		l = l.With(slog.String("version", opts.Version))
	}
	return l
}
