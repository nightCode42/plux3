// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package objects stores bundles, deltas, assets and exports under
// content-addressed keys (SRV-023, ADR-0007).
//
// Every object is immutable: its key contains the SHA-256 of its bytes,
// so writing the same content twice is a no-op and a stored object can
// be cached for ever by a CDN (REL-024). Two backends implement the same
// interface — S3-compatible storage for deployments, and the local
// filesystem for development and tests.
package objects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("objects: not found")

// Kind groups objects by what they are, and becomes the first segment of
// the key. It is a closed set so that a typo cannot create a new prefix.
type Kind string

// The kinds of object this phase stores.
const (
	KindBundle Kind = "bundles"
	KindDelta  Kind = "deltas"
	KindAsset  Kind = "assets"
	KindExport Kind = "exports"
)

// kinds lists the valid kinds, for validation and for tests.
var kinds = []Kind{KindBundle, KindDelta, KindAsset, KindExport}

// hexDigest matches a lower-case SHA-256.
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Key is the storage key of an object: "<kind>/<first two hex digits>/
// <digest>". The fan-out keeps any one directory small on the filesystem
// backend and spreads keys across prefixes on S3.
func Key(k Kind, digest string) (string, error) {
	if !hexDigest.MatchString(digest) {
		return "", fmt.Errorf("objects: %q is not a lower-case SHA-256", digest)
	}
	for _, valid := range kinds {
		if k == valid {
			return fmt.Sprintf("%s/%s/%s", k, digest[:2], digest), nil
		}
	}
	return "", fmt.Errorf("objects: unknown kind %q", k)
}

// safeKey matches the only shape a key may have: lower-case segments
// with no traversal, exactly as Key produces them. Validating the shape
// rather than sanitising it means a key that is not one this package
// made is refused instead of being silently rewritten.
var safeKey = regexp.MustCompile(`^[a-z]+(/[0-9a-z]+)+$`)

// ValidKey reports whether a key is well formed.
func ValidKey(key string) error {
	if !safeKey.MatchString(key) {
		return fmt.Errorf("objects: %q is not a valid key", key)
	}
	return nil
}

// Digest returns the SHA-256 of b as lower-case hex, which is what a key
// is built from.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Info describes a stored object.
type Info struct {
	Key  string
	Size int64
	// MediaType is what the object is served with.
	MediaType string
	// ModTime is when the object was written.
	ModTime time.Time
}

// Store is the object store. Implementations are safe for concurrent
// use.
type Store interface {
	// Put stores data under key. Storing the same key twice is a no-op,
	// because the key commits to the content.
	Put(ctx context.Context, key string, data []byte, mediaType string) (Info, error)
	// Get returns the object's bytes.
	Get(ctx context.Context, key string) ([]byte, Info, error)
	// Open returns a reader over a byte range; n < 0 reads to the end.
	// It is what serving a range request uses (REL-024).
	Open(ctx context.Context, key string, offset, n int64) (io.ReadCloser, Info, error)
	// Stat returns an object's metadata.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes an object. Deleting what is not there succeeds.
	Delete(ctx context.Context, key string) error
	// URL returns a location a client may fetch the object from: the CDN
	// URL when one is configured, otherwise a signed URL valid for ttl.
	// An empty string means the caller must serve the bytes itself
	// (DEP-041).
	URL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// checkRange rejects a range that cannot be served, and returns the
// number of bytes to read.
func checkRange(size, offset, n int64) (int64, error) {
	if offset < 0 || offset > size {
		return 0, fmt.Errorf("objects: offset %d is outside the object's %d bytes", offset, size)
	}
	remaining := size - offset
	if n < 0 || n > remaining {
		return remaining, nil
	}
	return n, nil
}
