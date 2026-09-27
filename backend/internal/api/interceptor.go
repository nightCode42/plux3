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
	"runtime/debug"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"

	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Deps are what the interceptors need. Each field may be nil, and the
// interceptor that uses it is then left out, so a test can build the
// chain it wants.
type Deps struct {
	Log     *slog.Logger
	Metrics *observability.Metrics
	Tracer  *observability.Tracer
	// RateLimit decides whether a call may proceed. It returns the wait
	// before retrying when it refuses (SRV-065).
	RateLimit func(ctx context.Context, procedure string) (retryAfter time.Duration, err error)
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
//  6. rate limit — refuses before any work is done (SRV-065).
//
// Authentication, authorisation and idempotency join the chain between
// the rate limit and the handler when identity lands.
func Interceptors(d Deps) []connect.Interceptor {
	out := []connect.Interceptor{recovery(d.Log), requestID()}
	if d.Tracer != nil {
		out = append(out, tracing(d.Tracer))
	}
	if d.Metrics != nil {
		out = append(out, metrics(d.Metrics))
	}
	out = append(out, translate(d.Log))
	if d.RateLimit != nil {
		out = append(out, rateLimit(d.RateLimit))
	}
	return out
}

// unary adapts a function into an interceptor that only wraps unary
// calls and leaves streams to the handler.
type unary func(context.Context, connect.AnyRequest, connect.UnaryFunc) (connect.AnyResponse, error)

// WrapUnary applies the function.
func (f unary) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return f(ctx, req, next)
	}
}

// WrapStreamingClient passes client streams through unchanged; the
// server does not call itself.
func (unary) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler passes server streams through unchanged.
func (unary) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// recovery turns a panic into an internal error and logs the stack, so
// one broken handler cannot take the process down (SRV-007).
func recovery(log *slog.Logger) connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (resp connect.AnyResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				incident := observability.RequestID(ctx)
				if log != nil {
					log.ErrorContext(ctx, "panic in a handler",
						slog.String("procedure", req.Spec().Procedure),
						slog.String("panic", fmt.Sprint(r)),
						slog.String("stack", string(debug.Stack())))
				}
				err = internal(incident)
			}
		}()
		return next(ctx, req)
	})
}

// requestID puts an identifier in the context and echoes it in the
// response, so a caller can quote it when reporting a problem.
func requestID() connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (connect.AnyResponse, error) {
		id := req.Header().Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			id = newID()
		}
		ctx = observability.WithRequestID(ctx, id)
		resp, err := next(ctx, req)
		if resp != nil {
			resp.Header().Set("X-Request-Id", id)
		}
		return resp, err
	})
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
func tracing(t *observability.Tracer) connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (connect.AnyResponse, error) {
		ctx = t.Propagator.Extract(ctx, propagation.HeaderCarrier(req.Header()))
		service, method := split(req.Spec().Procedure)
		ctx, span := t.Start(ctx, req.Spec().Procedure,
			attribute.String("rpc.system", "connect_rpc"),
			attribute.String("rpc.service", service),
			attribute.String("rpc.method", method))
		defer span.End()
		resp, err := next(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("rpc.connect_rpc.error_code", connect.CodeOf(err).String()))
		}
		return resp, err
	})
}

// metrics records the catalogued RPC metrics (OBS-002, Appendix G.1).
func metrics(m *observability.Metrics) connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (connect.AnyResponse, error) {
		start := time.Now()
		resp, err := next(ctx, req)
		service, method := split(req.Spec().Procedure)
		code := "ok"
		if err != nil {
			code = connect.CodeOf(err).String()
		}
		m.RPC(service, method, code, time.Since(start))
		return resp, err
	})
}

// translate turns a domain error into a Connect error and logs the
// unrecognised ones with their incident identifier (SRV-006).
func translate(log *slog.Logger) connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		if err == nil {
			return resp, nil
		}
		incident := observability.RequestID(ctx)
		out := Error(err, incident)
		if log != nil && connect.CodeOf(out) == connect.CodeInternal {
			log.ErrorContext(ctx, "unhandled failure",
				slog.String("procedure", req.Spec().Procedure),
				slog.String("incident", incident),
				slog.String("error", err.Error()))
		}
		return resp, out
	})
}

// rateLimit refuses a call before any work is done, and tells the caller
// when to retry (SRV-065).
func rateLimit(decide func(context.Context, string) (time.Duration, error)) connect.Interceptor {
	return unary(func(ctx context.Context, req connect.AnyRequest, next connect.UnaryFunc) (connect.AnyResponse, error) {
		retryAfter, err := decide(ctx, req.Spec().Procedure)
		if err != nil {
			out := Error(err, observability.RequestID(ctx))
			var connErr *connect.Error
			if errors.As(out, &connErr) && retryAfter > 0 {
				connErr.Meta().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds()+0.999)))
			}
			return nil, out
		}
		return next(ctx, req)
	})
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
	// Limit is how many calls one key may make in a window.
	Limit int64
	// Window is how long an allowance lasts.
	Window time.Duration
}

// Allow reports whether a key may make one more call, and the wait
// before retrying when it may not.
func (r RateLimiter) Allow(ctx context.Context, key string) (time.Duration, error) {
	n, err := r.Count(ctx, key, r.Window)
	if err != nil {
		// A cache failure must not close the API: the limit is a
		// protection, not a correctness property (ADR-0007). The error
		// is deliberately dropped rather than returned.
		return 0, nil //nolint:nilerr // failing open is the decision
	}
	if n > r.Limit {
		return r.Window, fmt.Errorf("%w", plxerr.New(plxerr.RateLimited,
			"more than %d calls in %s", r.Limit, r.Window))
	}
	return 0, nil
}
