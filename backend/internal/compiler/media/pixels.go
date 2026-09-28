// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"image"
	"image/draw"
)

// RGBA is an image as 8-bit red, green, blue and alpha, not
// premultiplied, row by row with no padding: what the codecs take.
type RGBA struct {
	Pix  []byte
	W, H int
}

// fromImage converts a decoded image.
func fromImage(img image.Image) *RGBA {
	b := img.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	return &RGBA{Pix: n.Pix, W: b.Dx(), H: b.Dy()}
}

// resize scales an image to w×h by area averaging with alpha weighting:
// each output pixel is the mean of the source area it covers, each
// source pixel weighted by the fraction of it inside that area and by
// its opacity, so transparent pixels lend no colour to their
// neighbours. It uses integer arithmetic only, so it gives the same
// bytes on every platform (CMP-002, CI-006). It is meant for
// downscaling; asking for the same size returns a copy.
func resize(src *RGBA, w, h int) *RGBA {
	out := &RGBA{Pix: make([]byte, w*h*4), W: w, H: h}
	if w == src.W && h == src.H {
		copy(out.Pix, src.Pix)
		return out
	}
	// Source pixel i spans [i*w, (i+1)*w) in units of 1/(src.W*w) of the
	// row; output pixel j spans [j*src.W, (j+1)*src.W). The overlap of
	// the two is the weight, so the weights of one output pixel sum to
	// src.W, and likewise vertically.
	xs := spans(src.W, w)
	ys := spans(src.H, h)
	for oy := range h {
		for ox := range w {
			var sa, sr, sg, sb, sw uint64
			for _, y := range ys[oy] {
				row := y.index * src.W * 4
				for _, x := range xs[ox] {
					wt := x.weight * y.weight
					p := src.Pix[row+x.index*4:]
					a := uint64(p[3]) * wt
					sa += a
					sr += uint64(p[0]) * a
					sg += uint64(p[1]) * a
					sb += uint64(p[2]) * a
					sw += wt
				}
			}
			q := out.Pix[(oy*w+ox)*4:]
			// Each is a weighted mean of bytes, so it fits in a byte.
			q[3] = byte((sa + sw/2) / sw) //nolint:gosec // a mean of bytes
			if sa > 0 {
				q[0] = byte((sr + sa/2) / sa) //nolint:gosec // a mean of bytes
				q[1] = byte((sg + sa/2) / sa) //nolint:gosec // a mean of bytes
				q[2] = byte((sb + sa/2) / sa) //nolint:gosec // a mean of bytes
			}
		}
	}
	return out
}

// tap is one source pixel's weight in an output pixel.
type tap struct {
	index  int
	weight uint64
}

// spans lists, for each of n output pixels along an axis of m source
// pixels, the source pixels it covers and by how much.
func spans(m, n int) [][]tap {
	out := make([][]tap, n)
	for j := range n {
		lo, hi := j*m, (j+1)*m
		for i := lo / n; i < m && i*n < hi; i++ {
			a, b := max(lo, i*n), min(hi, (i+1)*n)
			if b > a {
				out[j] = append(out[j], tap{index: i, weight: uint64(b - a)}) //nolint:gosec // b > a
			}
		}
	}
	return out
}
