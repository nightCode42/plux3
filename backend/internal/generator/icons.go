// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// maxIconSide bounds the source icon, so a resize stays within memory.
const maxIconSide = 4096

// decodeIcon reads a square PNG of at least 1024 pixels a side, the size
// of the largest icon a store asks for.
func decodeIcon(data []byte) (image.Image, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the icon is not a PNG: %w", err)
	}
	if cfg.Width != cfg.Height || cfg.Width < 1024 || cfg.Width > maxIconSide {
		return nil, fmt.Errorf("the icon is %dx%d; it must be square, from 1024 to %d pixels a side", cfg.Width, cfg.Height, maxIconSide)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the icon is not a PNG: %w", err)
	}
	return img, nil
}

// defaultIcon is the icon of an app that gives none: a light disc on the
// splash colour, drawn with integer arithmetic only.
func defaultIcon(bg color.NRGBA) image.Image {
	const side = 1024
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	disc := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	if int(bg.R)+int(bg.G)+int(bg.B) > 3*0xc0 {
		disc = color.NRGBA{R: 0x5b, G: 0x3d, B: 0xf5, A: 0xff}
	}
	const r = side * 3 / 10
	for y := range side {
		for x := range side {
			dx, dy := 2*x+1-side, 2*y+1-side
			if dx*dx+dy*dy <= 4*r*r {
				img.SetNRGBA(x, y, disc)
			} else {
				img.SetNRGBA(x, y, bg)
			}
		}
	}
	return img
}

// resize scales the square src to side×side by area averaging: each
// target pixel is the coverage-weighted mean of the source pixels under
// it, computed in integers so every machine produces the same bytes. With
// opaque, it is composited over bg first (iOS icons have no alpha).
func resize(img image.Image, side int, opaque bool, bg color.NRGBA) ([]byte, error) {
	b := img.Bounds()
	s := b.Dx()
	src := image.NewRGBA64(image.Rect(0, 0, s, s))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	// Source pixel i covers [i*side, (i+1)*side), target pixel x covers
	// [x*s, (x+1)*s), both in units of 1/(s*side) of the image.
	type span struct{ first, last int }
	spans := make([]span, side)
	for x := range side {
		spans[x] = span{x * s / side, ((x+1)*s - 1) / side}
	}
	overlap := func(x, i int) uint64 {
		lo, hi := max(x*s, i*side), min((x+1)*s, (i+1)*side)
		return uint64(hi - lo) //nolint:gosec // hi > lo within a span
	}
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	total := uint64(s) * uint64(s) //nolint:gosec // s is positive and bounded
	for y := range side {
		for x := range side {
			var r, g, bl, a uint64
			for j := spans[y].first; j <= spans[y].last; j++ {
				wy := overlap(y, j)
				for i := spans[x].first; i <= spans[x].last; i++ {
					w := wy * overlap(x, i)
					p := src.RGBA64At(i, j) // premultiplied, 16-bit
					r += w * uint64(p.R)
					g += w * uint64(p.G)
					bl += w * uint64(p.B)
					a += w * uint64(p.A)
				}
			}
			r, g, bl, a = (r+total/2)/total, (g+total/2)/total, (bl+total/2)/total, (a+total/2)/total
			if opaque {
				// Over the background: c + bg×(1−a), in 16-bit units.
				inv := 0xffff - a
				r += (uint64(bg.R) * 0x101 * inv) / 0xffff
				g += (uint64(bg.G) * 0x101 * inv) / 0xffff
				bl += (uint64(bg.B) * 0x101 * inv) / 0xffff
				a = 0xffff
			}
			out.SetNRGBA(x, y, unpremultiply(r, g, bl, a))
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, out); err != nil {
		return nil, fmt.Errorf("encode a %d-pixel icon: %w", side, err)
	}
	return buf.Bytes(), nil
}

// unpremultiply turns 16-bit premultiplied channels into an 8-bit
// straight colour.
func unpremultiply(r, g, b, a uint64) color.NRGBA {
	if a == 0 {
		return color.NRGBA{}
	}
	ch := func(c uint64) uint8 { return uint8(min((c*0xff+a/2)/a, 0xff)) }         //nolint:gosec // clamped to 0xff
	return color.NRGBA{R: ch(r), G: ch(g), B: ch(b), A: uint8((a + 0x80) / 0x101)} //nolint:gosec // a <= 0xffff
}
