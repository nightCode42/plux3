// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package observability

import "context"

// contextKey is this package's private context key type.
type contextKey int

const requestIDKey contextKey = iota + 1

// WithRequestID returns a context carrying the request's ID, which every
// log record and every error surface reports (OBS-003).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID of the context, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
