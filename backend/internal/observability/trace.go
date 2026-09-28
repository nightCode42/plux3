// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TraceOptions configures NewTracer.
type TraceOptions struct {
	// Endpoint is the OTLP collector; empty disables exporting, and the
	// tracer becomes a no-op that still propagates context.
	Endpoint string
	// SampleRatio is the fraction of root traces recorded, 0 to 1.
	SampleRatio float64
	// Service names this process in the exported resource.
	Service string
	// Role and Version label the resource.
	Role    string
	Version string
}

// Tracer is the process's tracer and the shutdown that flushes it.
type Tracer struct {
	// Tracer starts spans. It is never nil.
	Tracer trace.Tracer
	// Propagator carries W3C Trace Context across process boundaries, so
	// a trace started in Studio, the CLI or a device continues through
	// the api and worker roles (OBS-001).
	Propagator propagation.TextMapPropagator

	shutdown func(context.Context) error
}

// NewTracer builds the tracer. With no endpoint it returns a no-op
// tracer, so nothing in the server needs to know whether tracing is on.
func NewTracer(ctx context.Context, opts TraceOptions) (*Tracer, error) {
	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	if opts.Endpoint == "" {
		return &Tracer{
			Tracer:     noop.NewTracerProvider().Tracer(opts.Service),
			Propagator: prop,
			shutdown:   func(context.Context) error { return nil },
		}, nil
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(opts.Service),
		semconv.ServiceVersion(opts.Version),
		attribute.String("plux.role", opts.Role),
	))
	if err != nil {
		return nil, fmt.Errorf("trace resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.SampleRatio))),
	)
	return &Tracer{Tracer: tp.Tracer(opts.Service), Propagator: prop, shutdown: tp.Shutdown}, nil
}

// Shutdown flushes pending spans, bounded by the context's deadline.
func (t *Tracer) Shutdown(ctx context.Context) error { return t.shutdown(ctx) }

// Start begins a span and returns the context carrying it.
func (t *Tracer) Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.Tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// Timed runs f as a span and records the error it returns, so that a
// failing job or stage is visible in the trace.
func (t *Tracer) Timed(ctx context.Context, name string, f func(context.Context) error) error {
	ctx, span := t.Tracer.Start(ctx, name)
	defer span.End()
	start := time.Now()
	err := f(ctx)
	span.SetAttributes(attribute.Float64("plux.duration_seconds", time.Since(start).Seconds()))
	if err != nil {
		span.RecordError(err)
	}
	return err
}
