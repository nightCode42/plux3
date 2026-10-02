// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteDir writes the project into dir, which must be new or empty
// (GEN-003).
func WriteDir(dir string, files []File) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read %s: %w", dir, err)
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty", dir)
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, f.Data, f.Mode); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}
	return nil
}

// Zip is the project as one archive under root/ (GEN-003): entries in path
// order, the zip format's earliest timestamp, fixed modes and no extra
// fields, so the same project gives the same bytes.
func Zip(root string, files []File) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: root + "/" + f.Path, Method: zip.Deflate}
		// 1980-01-01 00:00, set through the DOS fields: a Modified time
		// would add an extended-timestamp field.
		h.ModifiedDate = 1<<5 | 1 //nolint:staticcheck // SA1019: the DOS field, so no extra field is written
		h.SetMode(f.Mode)
		fw, err := w.CreateHeader(h)
		if err != nil {
			return nil, fmt.Errorf("zip %s: %w", f.Path, err)
		}
		if _, err := fw.Write(f.Data); err != nil {
			return nil, fmt.Errorf("zip %s: %w", f.Path, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip: %w", err)
	}
	return buf.Bytes(), nil
}
