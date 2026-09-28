// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// This file decodes the subset of CBOR (RFC 8949) that WebAuthn uses:
// attestation objects and COSE keys. It accepts definite lengths only,
// bounds nesting and item counts, and reads maps into Go maps keyed by
// int64 or string. Anything else — tags, floats, indefinite lengths — is
// refused, since no authenticator response needs it.

// errCBOR is returned for input this decoder refuses.
var errCBOR = errors.New("auth: malformed CBOR")

const (
	cborMaxDepth = 8
	cborMaxItems = 256
)

// cborDecoder reads one CBOR item after another from a buffer.
type cborDecoder struct {
	data  []byte
	pos   int
	items int
}

// decodeCBOR decodes the first item of data and returns it with the
// number of bytes it took.
func decodeCBOR(data []byte) (any, int, error) {
	d := &cborDecoder{data: data}
	v, err := d.item(0)
	if err != nil {
		return nil, 0, err
	}
	return v, d.pos, nil
}

// head reads an item's major type and argument.
func (d *cborDecoder) head() (byte, uint64, error) {
	if d.pos >= len(d.data) {
		return 0, 0, fmt.Errorf("%w: truncated", errCBOR)
	}
	b := d.data[d.pos]
	d.pos++
	major, info := b>>5, b&0x1f
	var n int
	switch {
	case info < 24:
		return major, uint64(info), nil
	case info == 24:
		n = 1
	case info == 25:
		n = 2
	case info == 26:
		n = 4
	case info == 27:
		n = 8
	default:
		return 0, 0, fmt.Errorf("%w: unsupported additional information %d", errCBOR, info)
	}
	if len(d.data)-d.pos < n {
		return 0, 0, fmt.Errorf("%w: truncated", errCBOR)
	}
	var buf [8]byte
	copy(buf[8-n:], d.data[d.pos:d.pos+n])
	d.pos += n
	return major, binary.BigEndian.Uint64(buf[:]), nil
}

// item decodes one item at a nesting depth.
func (d *cborDecoder) item(depth int) (any, error) {
	if depth > cborMaxDepth {
		return nil, fmt.Errorf("%w: nested too deeply", errCBOR)
	}
	d.items++
	if d.items > cborMaxItems {
		return nil, fmt.Errorf("%w: too many items", errCBOR)
	}
	major, arg, err := d.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		if arg > math.MaxInt64 {
			return nil, fmt.Errorf("%w: integer out of range", errCBOR)
		}
		return int64(arg), nil
	case 1:
		if arg > math.MaxInt64 {
			return nil, fmt.Errorf("%w: integer out of range", errCBOR)
		}
		return -1 - int64(arg), nil
	case 2, 3:
		remaining := len(d.data) - d.pos
		if arg > uint64(remaining) { //nolint:gosec // remaining is not negative
			return nil, fmt.Errorf("%w: truncated string", errCBOR)
		}
		n := int(arg) //nolint:gosec // bounded by the input's length above
		b := d.data[d.pos : d.pos+n]
		d.pos += n
		if major == 3 {
			return string(b), nil
		}
		return b, nil
	case 4:
		return d.array(arg, depth)
	case 5:
		return d.mapping(arg, depth)
	case 7:
		switch arg {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22:
			return nil, nil
		}
	}
	return nil, fmt.Errorf("%w: unsupported major type %d", errCBOR, major)
}

func (d *cborDecoder) array(n uint64, depth int) (any, error) {
	if n > cborMaxItems {
		return nil, fmt.Errorf("%w: too many items", errCBOR)
	}
	out := make([]any, 0, n)
	for range n {
		v, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (d *cborDecoder) mapping(n uint64, depth int) (any, error) {
	if n > cborMaxItems {
		return nil, fmt.Errorf("%w: too many items", errCBOR)
	}
	out := make(map[any]any, n)
	for range n {
		k, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}
		switch k.(type) {
		case int64, string:
		default:
			return nil, fmt.Errorf("%w: a map key is neither an integer nor a text string", errCBOR)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("%w: a repeated map key", errCBOR)
		}
		v, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}
