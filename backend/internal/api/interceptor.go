// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"

	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Call is what an interceptor sees of one RPC, whether unary or
// streaming, so that every rule applies to both kinds alike.
type Call struct {
	// Procedure is the full method name, "/plux.v1.AppService/CreateApp".
	Procedure string
	// Header is the request's header.
	Header http.Header
	// PeerAddress is the network address of the immediate peer.
	PeerAddress string
	// ResponseHeader is written to the response. For a stream it is the
	// stream's own header, so it must be set before the first message.
	ResponseHeader http.Header
}

// Around wraps one call: it may inspect the call, change the context,
// refuse, or observe the result of next.
type Around func(ctx context.Context, c Call, next func(context.Context) error) error

// Deps are what the standard chain needs. Each field may be nil, and the
// interceptor that uses it is then left out, so a test can build the
// chain it wants.
type Deps struct {
	Log     *slog.Logger
	Metrics *observability.Metrics
	Tracer  *observability.Tracer
	// Before are interceptors that run inside error translation and
	// before the handler: authentication, rate limits, idempotency. They
	// run in order.
	Before []Around
}

// Interceptors returns the chain, outermost first:
//
//  1. recover — a panic becomes an internal error, never a dropped
//     connection;
//  2. request ID — every log record and every error carries it;
//  3. tracing — one span per call, continuing the caller's trace
//     (OBS-001);
//  4. metrics — plux_rpc_requests_total and _duration_seconds
//     (OBS-002);
//  5. errors — domain errors become Connect errors (SRV-006);
//  6. the Before interceptors — authentication, rate limits and
//     idempotency, which refuse before any work is done.
//
// Every interceptor wraps unary and streaming calls alike: a stream is
// no less subject to authorisation, limits or error translation.
func Interceptors(d Deps) []connect.Interceptor {
	out := []connect.Interceptor{interceptor(recovery(d.Log)), interceptor(requestID())}
	if d.Tracer != nil {
		out = append(out, interceptor(tracing(d.Tracer)))
	}
	if d.Metrics != nil {
		out = append(out, interceptor(metrics(d.Metrics)))
	}
	out = append(out, interceptor(translate(d.Log)))
	for _, a := range d.Before {
		out = append(out, interceptor(a))
	}
	return out
}

// interceptor adapts an Around to both halves of connect.Interceptor.
type interceptor Around

// responseHeaderKey carries the response header map of a unary call
// through the chain, so that every layer writes into one map and the
// outermost layer applies it once the error has been translated.
type responseHeaderKey struct{}

// WrapUnary applies the interceptor to a unary call.
func (a interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		header, inner := ctx.Value(responseHeaderKey{}).(http.Header)
		if !inner {
			header = http.Header{}
			ctx = context.WithValue(ctx, responseHeaderKey{}, header)
		}
		c := Call{
			Procedure:      req.Spec().Procedure,
			Header:         req.Header(),
			PeerAddress:    req.Peer().Addr,
			ResponseHeader: header,
		}
		var resp connect.AnyResponse
		err := a(ctx, c, func(ctx context.Context) error {
			var err error
			resp, err = next(ctx, req)
			return err
		})
		if !inner {
			applyHeader(header, resp, err)
		}
		return resp, err
	}
}

// applyHeader copies the collected headers onto the response, or onto
// the error when there is no response, so a request ID or a Retry-After
// reaches the caller either way.
func applyHeader(header http.Header, resp connect.AnyResponse, err error) {
	var target http.Header
	var connErr *connect.Error
	switch {
	case resp != nil:
		target = resp.Header()
	case errors.As(err, &connErr):
		target = connErr.Meta()
	default:
		return
	}
	for k, vs := range header {
		for _, v := range vs {
			target.Add(k, v)
		}
	}
}

// WrapStreamingClient passes client streams through unchanged; the
// server does not call itself.
func (interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler applies the interceptor to a streaming call.
func (a interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		c := Call{
			Procedure:      conn.Spec().Procedure,
			Header:         conn.RequestHeader(),
			PeerAddress:    conn.Peer().Addr,
			ResponseHeader: conn.ResponseHeader(),
		}
		return a(ctx, c, func(ctx context.Context) error { return next(ctx, conn) })
	}
}

// recovery turns a panic into an internal error and logs the stack, so
// one broken handler cannot take the process down (SRV-007).
func recovery(log *slog.Logger) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) (err error) {
		defer func() {
			if r := recover(); r != nil {
				incident := observability.RequestID(ctx)
				if log != nil {
					log.ErrorContext(ctx, "panic in a handler",
						slog.String("procedure", c.Procedure),
						slog.String("panic", fmt.Sprint(r)),
						slog.String("stack", string(debug.Stack())))
				}
				err = internal(incident)
			}
		}()
		return next(ctx)
	}
}

// requestID puts an identifier in the context and echoes it in the
// response, so a caller can quote it when reporting a problem.
func requestID() Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		id := c.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 || strings.ContainsAny(id, "\r\n") {
			id = newID()
		}
		c.ResponseHeader.Set("X-Request-Id", id)
		return next(observability.WithRequestID(ctx, id))
	}
}

// newID returns a random request identifier.
func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Randomness is not available; a fixed marker is better than a
		// failed request, because the identifier is only for tracing.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// tracing starts one span per call, continuing the caller's trace when
// the request carries W3C Trace Context (OBS-001).
func tracing(t *observability.Tracer) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		ctx = t.Propagator.Extract(ctx, propagation.HeaderCarrier(c.Header))
		service, method := split(c.Procedure)
		ctx, span := t.Start(ctx, c.Procedure,
			attribute.String("rpc.system", "connect_rpc"),
			attribute.String("rpc.service", service),
			attribute.String("rpc.method", method))
		defer span.End()
		err := next(ctx)
		if err != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("rpc.connect_rpc.error_code", connect.CodeOf(err).String()))
		}
		return err
	}
}

// metrics records the catalogued RPC metrics (OBS-002, Appendix G.1).
func metrics(m *observability.Metrics) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		start := time.Now()
		err := next(ctx)
		service, method := split(c.Procedure)
		code := "ok"
		if err != nil {
			code = connect.CodeOf(err).String()
		}
		m.RPC(service, method, code, time.Since(start))
		return err
	}
}

// translate turns a domain error into a Connect error and logs the
// unrecognised ones with their incident identifier (SRV-006).
func translate(log *slog.Logger) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		err := next(ctx)
		if err == nil {
			return nil
		}
		incident := observability.RequestID(ctx)
		out := Error(err, incident)
		if log != nil && connect.CodeOf(out) == connect.CodeInternal {
			log.ErrorContext(ctx, "unhandled failure",
				slog.String("procedure", c.Procedure),
				slog.String("incident", incident),
				slog.String("error", err.Error()))
		}
		return out
	}
}

// RateLimit refuses a call before any work is done when decide says so,
// and tells the caller when to retry (SRV-065).
func RateLimit(decide func(ctx context.Context, c Call) (retryAfter time.Duration, err error)) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		retryAfter, err := decide(ctx, c)
		if err != nil {
			if retryAfter > 0 {
				c.ResponseHeader.Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds()+0.999)))
			}
			return err
		}
		return next(ctx)
	}
}

// split cuts "/plux.v1.AppService/CreateApp" into its service and
// method, for the metric labels of Appendix G.1.
func split(procedure string) (service, method string) {
	trimmed := strings.TrimPrefix(procedure, "/")
	service, method, found := strings.Cut(trimmed, "/")
	if !found {
		return trimmed, ""
	}
	return service, method
}

// RateLimiter counts calls per key in the shared cache and refuses once
// the allowance is spent (SRV-065). The key is the principal, the device
// or the address, chosen by the caller of Allow.
type RateLimiter struct {
	// Count increments the counter for a key and returns its new value.
	Count func(ctx context.Context, key string, window time.Duration) (int64, error)
	// Window is how long an allowance lasts.
	Window time.Duration
}

// Allow reports whether a key may make one more call within limit, and
// the wait before retrying when it may not.
func (r RateLimiter) Allow(ctx context.Context, key string, limit int64) (time.Duration, error) {
	n, err := r.Count(ctx, key, r.Window)
	if err != nil {
		// A cache failure must not close the API: the limit is a
		// protection, not a correctness property (ADR-0007). The error
		// is deliberately dropped rather than returned.
		return 0, nil //nolint:nilerr // failing open is the decision
	}
	if n > limit {
		return r.Window, fmt.Errorf("%w", plxerr.New(plxerr.RateLimited,
			"more than %d calls in %s", limit, r.Window))
	}
	return 0, nil
}
