// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package media prepares asset files: it tells what a file is from its
// bytes, never its name (SRV-060); strips metadata; packages Lottie as
// dotLottie (CMP-033); and transcodes raster images to WebP and AVIF at
// three densities (CMP-030) with codecs that run on WebAssembly.
// Everything it produces depends on its input alone, so the same upload
// gives the same bytes on every machine.
package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Media types of assets (the document model's enumeration).
const (
	PNG       = "image/png"
	JPEG      = "image/jpeg"
	WebP      = "image/webp"
	GIF       = "image/gif"
	SVG       = "image/svg+xml"
	TTF       = "font/ttf"
	OTF       = "font/otf"
	DotLottie = "application/lottie+zip"
	Rive      = "application/riv"
	// AVIF is produced as a variant, never accepted as an upload.
	AVIF = "image/avif"
	// lottieJSON is a Lottie animation as uploaded; it is packaged as
	// dotLottie before it is stored (CMP-033).
	lottieJSON = "application/json+lottie"
)

// ErrUnsupported is returned for a file that is none of the accepted
// kinds, whatever its name says.
var ErrUnsupported = errors.New("media: not a supported asset type")

// Sniff tells what a file is from its content. A Lottie animation in
// JSON is reported as DotLottie, the form it is stored in.
func Sniff(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return PNG, nil
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return JPEG, nil
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return GIF, nil
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return WebP, nil
	case bytes.HasPrefix(data, []byte{0, 1, 0, 0}), bytes.HasPrefix(data, []byte("true")):
		return TTF, nil
	case bytes.HasPrefix(data, []byte("OTTO")):
		return OTF, nil
	case bytes.HasPrefix(data, []byte("RIVE")):
		return Rive, nil
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return DotLottie, nil
	case isLottie(data):
		return lottieJSON, nil
	case isSVG(data):
		return SVG, nil
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		return "", fmt.Errorf("%w: upload AVIF and HEIF images as PNG or JPEG; the server produces AVIF itself", ErrUnsupported)
	}
	return "", ErrUnsupported
}

// isLottie reports whether data is a Lottie animation: a JSON object with
// a version, a frame rate, a size and layers.
func isLottie(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n\ufeff")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var head struct {
		V      *string          `json:"v"`
		FR     *json.Number     `json:"fr"`
		W      *json.Number     `json:"w"`
		H      *json.Number     `json:"h"`
		Layers *json.RawMessage `json:"layers"`
	}
	if err := json.Unmarshal(trimmed, &head); err != nil {
		return false
	}
	return head.V != nil && head.FR != nil && head.W != nil && head.H != nil && head.Layers != nil
}

// isSVG reports whether data is UTF-8 text whose first element is svg.
func isSVG(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	s := bytes.TrimPrefix(data, []byte("\ufeff"))
	for {
		s = bytes.TrimLeft(s, " \t\r\n")
		switch {
		case bytes.HasPrefix(s, []byte("<?")):
			end := bytes.Index(s, []byte("?>"))
			if end < 0 {
				return false
			}
			s = s[end+2:]
		case bytes.HasPrefix(s, []byte("<!--")):
			end := bytes.Index(s, []byte("-->"))
			if end < 0 {
				return false
			}
			s = s[end+3:]
		case bytes.HasPrefix(s, []byte("<!DOCTYPE")), bytes.HasPrefix(s, []byte("<!doctype")):
			end := bytes.IndexByte(s, '>')
			if end < 0 {
				return false
			}
			s = s[end+1:]
		default:
			return bytes.HasPrefix(s, []byte("<svg")) && len(s) > 4 && (s[4] == ' ' || s[4] == '>' || s[4] == '\n' || s[4] == '\t' || s[4] == '\r')
		}
	}
}
