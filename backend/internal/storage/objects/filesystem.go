// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Filesystem stores objects in a directory. It is for development, for
// tests and for a single-node installation with no object storage; a
// deployment uses S3.
type Filesystem struct {
	root string
	// baseURL is prefixed to keys when the directory is served by a web
	// server or a CDN; empty means the server serves the bytes itself.
	baseURL string
}

// NewFilesystem returns a store rooted at dir, creating it if needed.
func NewFilesystem(dir, baseURL string) (*Filesystem, error) {
	if dir == "" {
		return nil, errors.New("objects: the filesystem backend needs a directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("objects: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("objects: create %s: %w", abs, err)
	}
	return &Filesystem{root: abs, baseURL: strings.TrimSuffix(baseURL, "/")}, nil
}

// path returns the file of a key, refusing anything that would escape
// the root.
func (f *Filesystem) path(key string) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	p := filepath.Join(f.root, filepath.FromSlash(key))
	if !strings.HasPrefix(p, f.root+string(filepath.Separator)) {
		return "", fmt.Errorf("objects: %q is not a valid key", key)
	}
	return p, nil
}

// Put writes the object, unless it is already there.
func (f *Filesystem) Put(_ context.Context, key string, data []byte, mediaType string) (Info, error) {
	p, err := f.path(key)
	if err != nil {
		return Info{}, err
	}
	if st, err := os.Stat(p); err == nil {
		return Info{Key: key, Size: st.Size(), MediaType: mediaType, ModTime: st.ModTime()}, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	// Write to a temporary file and rename, so a reader never sees a
	// partly written object.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	st, err := os.Stat(p)
	if err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	return Info{Key: key, Size: st.Size(), MediaType: mediaType, ModTime: st.ModTime()}, nil
}

// Get reads the whole object.
func (f *Filesystem) Get(ctx context.Context, key string) ([]byte, Info, error) {
	info, err := f.Stat(ctx, key)
	if err != nil {
		return nil, Info{}, err
	}
	p, err := f.path(key)
	if err != nil {
		return nil, Info{}, err
	}
	data, err := os.ReadFile(p) //nolint:gosec // p is built from a validated key under the root
	if err != nil {
		return nil, Info{}, fmt.Errorf("objects: %w", err)
	}
	return data, info, nil
}

// Open returns a reader over a byte range.
func (f *Filesystem) Open(ctx context.Context, key string, offset, n int64) (io.ReadCloser, Info, error) {
	info, err := f.Stat(ctx, key)
	if err != nil {
		return nil, Info{}, err
	}
	length, err := checkRange(info.Size, offset, n)
	if err != nil {
		return nil, Info{}, err
	}
	p, err := f.path(key)
	if err != nil {
		return nil, Info{}, err
	}
	file, err := os.Open(p) //nolint:gosec // p is built from a validated key under the root
	if err != nil {
		return nil, Info{}, fmt.Errorf("objects: %w", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, Info{}, fmt.Errorf("objects: %w", err)
	}
	info.Size = length
	return sectionCloser{Reader: io.LimitReader(file, length), closer: file}, info, nil
}

// sectionCloser closes the file a limited reader reads from.
type sectionCloser struct {
	io.Reader
	closer io.Closer
}

// Close closes the underlying file.
func (s sectionCloser) Close() error {
	if err := s.closer.Close(); err != nil {
		return fmt.Errorf("objects: %w", err)
	}
	return nil
}

// Stat returns the object's metadata.
func (f *Filesystem) Stat(_ context.Context, key string) (Info, error) {
	p, err := f.path(key)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err != nil {
		return Info{}, fmt.Errorf("objects: %w", err)
	}
	return Info{Key: key, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete removes the object.
func (f *Filesystem) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("objects: %w", err)
	}
	return nil
}

// URL returns the CDN location when one is configured. Without one the
// server serves the bytes itself, which is what an empty result means
// (DEP-041).
func (f *Filesystem) URL(_ context.Context, key string, _ time.Duration) (string, error) {
	if f.baseURL == "" {
		return "", nil
	}
	return f.baseURL + "/" + key, nil
}
