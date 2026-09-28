// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
)

// Densities are the pixel densities variants are made for (CMP-030).
// An uploaded image is taken to be drawn at 3×.
var Densities = []int{1, 2, 3}

// Encoder settings. They are part of the output's identity: changing one
// changes every variant's bytes and so needs a new pipeline version.
const (
	webpQuality = 85
	avifQuality = 60
	avifSpeed   = 6
)

// Variant is one transcoded form of an image.
type Variant struct {
	MediaType string
	Density   int
	Width     int
	Height    int
	Data      []byte
}

// Info describes an image.
type Info struct {
	Width, Height int
	// Animated images are kept as uploaded: their frames are not
	// transcoded.
	Animated bool
}

// ErrTooLarge is returned for an image with more pixels than allowed.
var ErrTooLarge = fmt.Errorf("media: the image has too many pixels")

// Raster reports whether a media type is a raster image the pipeline
// transcodes.
func Raster(mediaType string) bool {
	return mediaType == PNG || mediaType == JPEG || mediaType == GIF || mediaType == WebP
}

// Transcode makes the variants of a raster image: WebP and AVIF at each
// density, largest first (CMP-030). Re-encoding from pixels leaves every
// piece of metadata behind. An image with more than maxPixels pixels is
// refused before it is decoded; an animated one is described but not
// transcoded.
func (c *Codecs) Transcode(ctx context.Context, data []byte, mediaType string, maxPixels int64) (Info, []Variant, error) {
	info, lossless, err := describe(data, mediaType)
	if err != nil {
		return Info{}, nil, err
	}
	if int64(info.Width)*int64(info.Height) > maxPixels {
		return info, nil, fmt.Errorf("%w: %d×%d is above asset.imagePixels = %d", ErrTooLarge, info.Width, info.Height, maxPixels)
	}
	if info.Animated {
		return info, nil, nil
	}
	src, err := c.decode(ctx, data, mediaType)
	if err != nil {
		return info, nil, err
	}
	quality := float32(webpQuality)
	if lossless {
		quality = -1
	}
	var out []Variant
	for i := len(Densities) - 1; i >= 0; i-- {
		d := Densities[i]
		w, h := max(1, (src.W*d+1)/3), max(1, (src.H*d+1)/3)
		px := resize(src, w, h)
		webp, err := c.encodeWebP(ctx, px, quality)
		if err != nil {
			return info, nil, err
		}
		avif, err := c.encodeAVIF(ctx, px, avifQuality, avifSpeed)
		if err != nil {
			return info, nil, err
		}
		out = append(out,
			Variant{MediaType: WebP, Density: d, Width: w, Height: h, Data: webp},
			Variant{MediaType: AVIF, Density: d, Width: w, Height: h, Data: avif})
	}
	return info, out, nil
}

// describe reads an image's size, whether it is animated, and whether it
// should be encoded losslessly — graphics (PNG, GIF, lossless WebP) are,
// photographs are not — without decoding its pixels.
func describe(data []byte, mediaType string) (Info, bool, error) {
	switch mediaType {
	case WebP:
		return describeWebP(data)
	case GIF:
		cfg, err := gif.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return Info{}, false, fmt.Errorf("%w: %w", errMalformed, err)
		}
		return Info{Width: cfg.Width, Height: cfg.Height, Animated: gifFrames(data) > 1}, true, nil
	case PNG, JPEG:
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return Info{}, false, fmt.Errorf("%w: %w", errMalformed, err)
		}
		return Info{Width: cfg.Width, Height: cfg.Height, Animated: mediaType == PNG && bytes.Contains(data, []byte("acTL"))}, mediaType == PNG, nil
	}
	return Info{}, false, fmt.Errorf("%w: %s is not a raster image", ErrUnsupported, mediaType)
}

// describeWebP reads the canvas size from a WebP file's first chunk.
func describeWebP(data []byte) (Info, bool, error) {
	bad := fmt.Errorf("%w: the WebP header is malformed", errMalformed)
	if len(data) < 30 {
		return Info{}, false, bad
	}
	chunk := data[12:]
	switch string(chunk[:4]) {
	case "VP8X":
		w := 1 + (int(chunk[12]) | int(chunk[13])<<8 | int(chunk[14])<<16)
		h := 1 + (int(chunk[15]) | int(chunk[16])<<8 | int(chunk[17])<<16)
		return Info{Width: w, Height: h, Animated: chunk[8]&0x02 != 0}, bytes.Contains(data, []byte("VP8L")), nil
	case "VP8L":
		if chunk[8] != 0x2f {
			return Info{}, false, bad
		}
		bits := binary.LittleEndian.Uint32(chunk[9:])
		return Info{Width: int(bits&0x3fff) + 1, Height: int(bits>>14&0x3fff) + 1}, true, nil
	case "VP8 ":
		if !bytes.Equal(chunk[11:14], []byte{0x9d, 0x01, 0x2a}) {
			return Info{}, false, bad
		}
		return Info{Width: int(binary.LittleEndian.Uint16(chunk[14:]) & 0x3fff), Height: int(binary.LittleEndian.Uint16(chunk[16:]) & 0x3fff)}, false, nil
	}
	return Info{}, false, bad
}

// gifFrames counts the images of a GIF by walking its blocks, without
// decoding any.
func gifFrames(data []byte) int {
	if len(data) < 13 {
		return 0
	}
	p := 13
	if data[10]&0x80 != 0 {
		p += 3 << (int(data[10]&7) + 1)
	}
	frames := 0
	for p < len(data) {
		switch data[p] {
		case 0x21:
			end, err := skipSubBlocks(data, p+2)
			if err != nil {
				return frames
			}
			p = end
		case 0x2c:
			if p+10 > len(data) {
				return frames
			}
			q := p + 10
			if data[p+9]&0x80 != 0 {
				q += 3 << (int(data[p+9]&7) + 1)
			}
			end, err := skipSubBlocks(data, q+1)
			if err != nil {
				return frames
			}
			frames++
			p = end
		default:
			return frames
		}
	}
	return frames
}

// decode decodes an image to pixels.
func (c *Codecs) decode(ctx context.Context, data []byte, mediaType string) (*RGBA, error) {
	if mediaType == WebP {
		return c.decodeWebP(ctx, data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMalformed, err)
	}
	return fromImage(img), nil
}

// Describe reads a raster image's size and whether it is animated,
// without decoding its pixels.
func Describe(data []byte, mediaType string) (Info, error) {
	info, _, err := describe(data, mediaType)
	return info, err
}
