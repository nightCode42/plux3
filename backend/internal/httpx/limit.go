// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package httpx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrTooLarge is returned when a body is bigger than the limit.
var ErrTooLarge = errors.New("httpx: the body is too large")

// LimitReader returns a reader that fails with ErrTooLarge once more
// than max bytes have been read, instead of truncating silently
// (SEC-104).
func LimitReader(r io.Reader, max int64) io.Reader {
	return &limited{r: io.LimitReader(r, max+1), max: max}
}

// limited counts what it has read and refuses to exceed the limit.
type limited struct {
	r    io.Reader
	max  int64
	read int64
}

// Read reads from the wrapped reader.
func (l *limited) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.read += int64(n)
	if l.read > l.max {
		return n, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, l.max)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("httpx: read: %w", err)
	}
	//nolint:wrapcheck // io.EOF must reach the caller unchanged
	return n, err
}

// ReadAll reads a whole body, refusing one larger than max.
func ReadAll(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(LimitReader(r, max))
	if err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	return b, nil
}

// MaxBytes bounds every request body a handler reads, and answers 413
// rather than letting a handler allocate the difference (SEC-104).
func MaxBytes(next http.Handler, max int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > max {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, max)
		next.ServeHTTP(w, r)
	})
}
