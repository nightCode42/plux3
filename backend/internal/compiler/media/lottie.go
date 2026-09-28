// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
)

// dotLottieEpoch is the modification time every entry is written with, so
// that packaging the same animation gives the same bytes.
var dotLottieEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// maxDotLottieEntries bounds the files one dotLottie may hold.
const maxDotLottieEntries = 256

// Package prepares a file of a sniffed type for storage and returns it
// with the media type it is stored under: a Lottie animation becomes a
// dotLottie archive (CMP-033), a dotLottie archive is checked and
// rewritten canonically, and every other file is returned as it is.
// maxSize bounds what an archive may expand to.
func Package(data []byte, sniffed string, maxSize int64) ([]byte, string, error) {
	switch sniffed {
	case lottieJSON:
		out, err := packLottie(map[string][]byte{"animations/animation.json": data}, []string{"animation"})
		return out, DotLottie, err
	case DotLottie:
		files, ids, err := readDotLottie(data, maxSize)
		if err != nil {
			return nil, "", err
		}
		out, err := packLottie(files, ids)
		return out, DotLottie, err
	default:
		return data, sniffed, nil
	}
}

// readDotLottie reads a dotLottie archive, refusing one that expands past
// maxSize, holds unsafe paths, or has no animation.
func readDotLottie(data []byte, maxSize int64) (map[string][]byte, []string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: not a zip archive", errMalformed)
	}
	if len(r.File) > maxDotLottieEntries {
		return nil, nil, fmt.Errorf("%w: the archive holds more than %d files", errMalformed, maxDotLottieEntries)
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range r.File {
		name := f.Name
		if strings.HasSuffix(name, "/") {
			continue
		}
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") || strings.Contains(name, "\\") {
			return nil, nil, fmt.Errorf("%w: the archive holds an unsafe path %q", errMalformed, name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errMalformed, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxSize-total+1))
		_ = rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errMalformed, err)
		}
		total += int64(len(b))
		if total > maxSize {
			return nil, nil, fmt.Errorf("%w: the archive expands past %d bytes", errMalformed, maxSize)
		}
		files[name] = b
	}
	var manifest struct {
		Animations []struct {
			ID string `json:"id"`
		} `json:"animations"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || len(manifest.Animations) == 0 {
		return nil, nil, fmt.Errorf("%w: the archive has no manifest.json listing an animation", errMalformed)
	}
	ids := make([]string, 0, len(manifest.Animations))
	for _, a := range manifest.Animations {
		body, ok := files["animations/"+a.ID+".json"]
		if !ok || !isLottie(body) {
			return nil, nil, fmt.Errorf("%w: animation %q is missing or not Lottie", errMalformed, a.ID)
		}
		ids = append(ids, a.ID)
	}
	delete(files, "manifest.json")
	return files, ids, nil
}

// packLottie writes a dotLottie archive: the manifest first, then every
// file in name order, deflated, with fixed times.
func packLottie(files map[string][]byte, ids []string) ([]byte, error) {
	type animation struct {
		ID string `json:"id"`
	}
	m := struct {
		Version    string      `json:"version"`
		Generator  string      `json:"generator"`
		Animations []animation `json:"animations"`
	}{Version: "1", Generator: "Plux"}
	for _, id := range ids {
		m.Animations = append(m.Animations, animation{ID: id})
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("media: %w", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, body []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: dotLottieEpoch})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		_, err = w.Write(body)
		return err //nolint:wrapcheck // wrapped below
	}
	if err := write("manifest.json", manifest); err != nil {
		return nil, fmt.Errorf("media: package: %w", err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := write(name, files[name]); err != nil {
			return nil, fmt.Errorf("media: package: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("media: package: %w", err)
	}
	return buf.Bytes(), nil
}
