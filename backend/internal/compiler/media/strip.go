// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"regexp"
)

// errMalformed is returned for a file whose structure does not hold.
var errMalformed = errors.New("media: the file is malformed")

// Strip removes metadata from a file of a media type (SRV-060): camera,
// location, author and editing data, comments and embedded profiles. It
// keeps what the image needs to render the same. A JPEG whose EXIF
// orientation turns it is turned before the orientation is dropped, so
// it still displays upright.
func Strip(data []byte, mediaType string) ([]byte, error) {
	switch mediaType {
	case PNG:
		return stripPNG(data)
	case JPEG:
		return stripJPEG(data)
	case GIF:
		return stripGIF(data)
	case WebP:
		return stripWebP(data)
	case SVG:
		return svgMetadata.ReplaceAll(data, nil), nil
	default:
		return data, nil
	}
}

// pngKeep lists the PNG chunks that affect rendering.
var pngKeep = map[string]bool{
	"IHDR": true, "PLTE": true, "IDAT": true, "IEND": true, "tRNS": true,
	"gAMA": true, "cHRM": true, "sRGB": true, "sBIT": true,
	"acTL": true, "fcTL": true, "fdAT": true, // animation
}

func stripPNG(data []byte) ([]byte, error) {
	const sig = 8
	if len(data) < sig {
		return nil, fmt.Errorf("%w: the PNG signature is truncated", errMalformed)
	}
	out := append([]byte(nil), data[:sig]...)
	for p := sig; ; {
		if len(data)-p < 12 {
			return nil, fmt.Errorf("%w: a PNG chunk is truncated", errMalformed)
		}
		n := int(binary.BigEndian.Uint32(data[p:]))
		if n < 0 || n > len(data)-p-12 {
			return nil, fmt.Errorf("%w: a PNG chunk runs past the file", errMalformed)
		}
		kind := string(data[p+4 : p+8])
		chunk := data[p : p+12+n]
		if crc32.ChecksumIEEE(chunk[4:8+n]) != binary.BigEndian.Uint32(chunk[8+n:]) {
			return nil, fmt.Errorf("%w: a PNG chunk's checksum is wrong", errMalformed)
		}
		if pngKeep[kind] {
			out = append(out, chunk...)
		}
		p += 12 + n
		if kind == "IEND" {
			return out, nil
		}
	}
}

func stripJPEG(data []byte) ([]byte, error) {
	if o := jpegOrientation(data); o > 1 {
		return rotateJPEG(data, o)
	}
	out := []byte{0xff, 0xd8}
	p := 2
	for {
		if len(data)-p < 4 || data[p] != 0xff {
			return nil, fmt.Errorf("%w: a JPEG segment is malformed", errMalformed)
		}
		marker := data[p+1]
		if marker == 0xda { // start of scan: the rest is image data
			return append(out, data[p:]...), nil
		}
		n := int(binary.BigEndian.Uint16(data[p+2:]))
		if n < 2 || n > len(data)-p-2 {
			return nil, fmt.Errorf("%w: a JPEG segment runs past the file", errMalformed)
		}
		// APP1–APP13 and APP15 hold EXIF, XMP, ICC profiles and editor data;
		// COM is a comment. APP0 (JFIF) and APP14 (Adobe colour transform)
		// affect decoding and stay.
		metadata := (marker >= 0xe1 && marker <= 0xef && marker != 0xee) || marker == 0xfe
		if !metadata {
			out = append(out, data[p:p+2+n]...)
		}
		p += 2 + n
	}
}

// jpegOrientation reads the EXIF orientation of a JPEG, 1 when absent.
func jpegOrientation(data []byte) int {
	for p := 2; len(data)-p >= 4 && data[p] == 0xff; {
		marker := data[p+1]
		n := int(binary.BigEndian.Uint16(data[p+2:]))
		if marker == 0xda || n < 2 || n > len(data)-p-2 {
			return 1
		}
		seg := data[p+4 : p+2+n]
		if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return exifOrientation(seg[6:])
		}
		p += 2 + n
	}
	return 1
}

// exifOrientation reads tag 0x0112 from the first IFD of a TIFF header.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd > len(t)-2 {
		return 1
	}
	count := int(order.Uint16(t[ifd:]))
	for i := range count {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) == 0x0112 {
			o := int(order.Uint16(t[e+8:]))
			if o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// rotateJPEG applies an EXIF orientation and re-encodes the image, which
// also leaves every metadata segment behind.
func rotateJPEG(data []byte, orientation int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMalformed, err)
	}
	src := fromImage(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, orient(src, orientation).image(), &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("media: %w", err)
	}
	return buf.Bytes(), nil
}

// image returns the pixels as an image.Image.
func (px *RGBA) image() image.Image {
	return &image.NRGBA{Pix: px.Pix, Stride: px.W * 4, Rect: image.Rect(0, 0, px.W, px.H)}
}

// orient transforms pixels by an EXIF orientation (2 to 8).
func orient(src *RGBA, o int) *RGBA {
	w, h := src.W, src.H
	if o >= 5 {
		w, h = h, w
	}
	out := &RGBA{Pix: make([]byte, len(src.Pix)), W: w, H: h}
	for y := range src.H {
		for x := range src.W {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = src.W-1-x, y
			case 3:
				dx, dy = src.W-1-x, src.H-1-y
			case 4:
				dx, dy = x, src.H-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = src.H-1-y, x
			case 7:
				dx, dy = src.H-1-y, src.W-1-x
			case 8:
				dx, dy = y, src.W-1-x
			default:
				dx, dy = x, y
			}
			copy(out.Pix[(dy*w+dx)*4:(dy*w+dx)*4+4], src.Pix[(y*src.W+x)*4:])
		}
	}
	return out
}

func stripGIF(data []byte) ([]byte, error) {
	if len(data) < 13 {
		return nil, fmt.Errorf("%w: the GIF header is truncated", errMalformed)
	}
	p := 13
	if data[10]&0x80 != 0 {
		p += 3 << (int(data[10]&7) + 1)
	}
	if p > len(data) {
		return nil, fmt.Errorf("%w: the GIF colour table is truncated", errMalformed)
	}
	out := append([]byte(nil), data[:p]...)
	for p < len(data) {
		start := p
		switch data[p] {
		case 0x3b: // trailer
			return append(out, 0x3b), nil
		case 0x21: // extension
			end, keep, err := gifExtension(data, p)
			if err != nil {
				return nil, err
			}
			if keep {
				out = append(out, data[start:end]...)
			}
			p = end
		case 0x2c: // image descriptor
			end, err := gifImage(data, p)
			if err != nil {
				return nil, err
			}
			out = append(out, data[start:end]...)
			p = end
		default:
			return nil, fmt.Errorf("%w: an unknown GIF block", errMalformed)
		}
	}
	return nil, fmt.Errorf("%w: the GIF has no trailer", errMalformed)
}

// gifExtension reads the extension at p and reports where it ends and
// whether it affects display: graphic control, plain text and the
// looping application extensions do; comments and other application
// data do not.
func gifExtension(data []byte, p int) (int, bool, error) {
	if p+2 > len(data) {
		return 0, false, fmt.Errorf("%w: a GIF extension is truncated", errMalformed)
	}
	label := data[p+1]
	end, err := skipSubBlocks(data, p+2)
	if err != nil {
		return 0, false, err
	}
	keep := label == 0xf9 || label == 0x01
	if label == 0xff && p+14 <= len(data) && int(data[p+2]) == 11 {
		app := string(data[p+3 : p+14])
		keep = app == "NETSCAPE2.0" || app == "ANIMEXTS1.0"
	}
	return end, keep, nil
}

// gifImage returns the position after the image whose descriptor is at
// p: the descriptor, a local colour table, and the image data.
func gifImage(data []byte, p int) (int, error) {
	if p+10 > len(data) {
		return 0, fmt.Errorf("%w: a GIF image is truncated", errMalformed)
	}
	q := p + 10
	if data[p+9]&0x80 != 0 {
		q += 3 << (int(data[p+9]&7) + 1)
	}
	return skipSubBlocks(data, q+1) // after the LZW minimum code size
}

// skipSubBlocks returns the position after a chain of GIF sub-blocks.
func skipSubBlocks(data []byte, p int) (int, error) {
	for {
		if p >= len(data) {
			return 0, fmt.Errorf("%w: GIF data is truncated", errMalformed)
		}
		n := int(data[p])
		p += 1 + n
		if n == 0 {
			return p, nil
		}
	}
}

// VP8X flags for metadata the image does not need.
const (
	vp8xICC  = 0x20
	vp8xEXIF = 0x08
	vp8xXMP  = 0x04
)

func stripWebP(data []byte) ([]byte, error) {
	if len(data) < 12 || int(binary.LittleEndian.Uint32(data[4:])) > len(data)-8 {
		return nil, fmt.Errorf("%w: the WebP header is malformed", errMalformed)
	}
	out := append([]byte(nil), data[:12]...)
	for p := 12; p < len(data); {
		if len(data)-p < 8 {
			return nil, fmt.Errorf("%w: a WebP chunk is truncated", errMalformed)
		}
		kind := string(data[p : p+4])
		n := int(binary.LittleEndian.Uint32(data[p+4:]))
		size := 8 + n + n&1
		if n < 0 || size > len(data)-p {
			return nil, fmt.Errorf("%w: a WebP chunk runs past the file", errMalformed)
		}
		chunk := append([]byte(nil), data[p:p+size]...)
		p += size
		switch kind {
		case "EXIF", "XMP ", "ICCP":
			continue
		case "VP8X":
			if len(chunk) > 8 {
				chunk[8] &^= vp8xICC | vp8xEXIF | vp8xXMP
			}
		}
		out = append(out, chunk...)
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8)) //nolint:gosec // smaller than the input
	return out, nil
}

// svgMetadata matches an SVG metadata element, which holds RDF about the
// file's author and tools and nothing that is drawn.
var svgMetadata = regexp.MustCompile(`(?s)<metadata\b.*?</metadata>`)
