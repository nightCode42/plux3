// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package objects_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// s3Stub is an in-process S3-compatible service covering exactly what
// the S3 backend uses: PUT, GET (whole and ranged), HEAD and DELETE in
// path style. It ignores the signature, because what is under test is
// the backend's behaviour, not the SDK's signing; MinIO in CI exercises
// the real protocol (QA-005).
type s3Stub struct {
	mu      sync.Mutex
	objects map[string]stubObject
}

// stubObject is one stored object.
type stubObject struct {
	data     []byte
	mediaTyp string
	cache    string
	modTime  time.Time
}

// newS3Stub returns a running stub and its base URL.
func newS3Stub() (*s3Stub, *httptest.Server) {
	s := &s3Stub{objects: map[string]stubObject{}}
	return s, httptest.NewServer(s)
}

// key strips the bucket from a path-style request.
func key(path string) (bucket, key string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/")
	bucket, key, found := strings.Cut(trimmed, "/")
	return bucket, key, found && key != ""
}

// ServeHTTP implements the subset of the S3 API the backend uses.
func (s *s3Stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, k, ok := key(r.URL.Path)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		body := make([]byte, r.ContentLength)
		if _, err := readFull(r, body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.objects[k] = stubObject{
			data:     body,
			mediaTyp: r.Header.Get("Content-Type"),
			cache:    r.Header.Get("Cache-Control"),
			modTime:  time.Now().UTC(),
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodHead, http.MethodGet:
		o, found := s.objects[k]
		if !found {
			writeNoSuchKey(w, r.Method)
			return
		}
		data := o.data
		status := http.StatusOK
		if rng := r.Header.Get("Range"); rng != "" {
			var err error
			if data, err = slice(o.data, rng); err != nil {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			status = http.StatusPartialContent
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes %s/%d", strings.TrimPrefix(rng, "bytes="), len(o.data)))
		}
		if o.mediaTyp != "" {
			w.Header().Set("Content-Type", o.mediaTyp)
		}
		if o.cache != "" {
			w.Header().Set("Cache-Control", o.cache)
		}
		w.Header().Set("Last-Modified", o.modTime.Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(status)
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	case http.MethodDelete:
		delete(s.objects, k)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// count reports how many objects the stub holds.
func (s *s3Stub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// cacheControl returns the Cache-Control a key was stored with.
func (s *s3Stub) cacheControl(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[k].cache
}

// writeNoSuchKey answers with the error the SDK maps to NoSuchKey.
func writeNoSuchKey(w http.ResponseWriter, method string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNotFound)
	if method == http.MethodGet {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`))
	}
}

// slice applies a "bytes=from-to" or "bytes=from-" range.
func slice(data []byte, rng string) ([]byte, error) {
	spec := strings.TrimPrefix(rng, "bytes=")
	fromText, toText, _ := strings.Cut(spec, "-")
	from, err := strconv.Atoi(fromText)
	if err != nil || from < 0 || from > len(data) {
		return nil, fmt.Errorf("bad range %q", rng)
	}
	to := len(data) - 1
	if toText != "" {
		if to, err = strconv.Atoi(toText); err != nil || to < from {
			return nil, fmt.Errorf("bad range %q", rng)
		}
	}
	if to >= len(data) {
		to = len(data) - 1
	}
	return data[from : to+1], nil
}

// readFull fills b from the request body.
func readFull(r *http.Request, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		read, err := r.Body.Read(b[n:])
		n += read
		if err != nil {
			if n == len(b) {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}
