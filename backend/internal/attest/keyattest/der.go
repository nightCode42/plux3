// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import "errors"

// The authorisation list uses EXPLICIT context-specific tags whose numbers
// exceed 30, which golang.org/x/crypto/cryptobyte cannot read. This file is
// the small, bounded reader for exactly that: DER identifier octets in
// high-tag-number form and definite lengths.

const (
	classMask         = 0xc0
	classContext      = 0x80
	constructedBit    = 0x20
	highTagMarker     = 0x1f
	minHighTagNumber  = 31
	maxHighTagOctets  = 4
	maxLengthOctets   = 4
	shortFormMaxValue = 0x7f
)

var errBadTLV = errors.New("malformed DER element")

// tlv is one DER element with its identifier decoded.
type tlv struct {
	class       byte
	constructed bool
	number      uint32
	content     []byte
}

// isContextConstructed reports whether the element is a constructed
// context-specific one, which is how every explicit authorisation tag is
// encoded.
func (t tlv) isContextConstructed() bool {
	return t.class == classContext && t.constructed
}

// readTLV reads the first element of b and returns it with the remainder.
func readTLV(b []byte) (tlv, []byte, error) {
	if len(b) < 2 {
		return tlv{}, nil, errBadTLV
	}
	id := b[0]
	t := tlv{
		class:       id & classMask,
		constructed: id&constructedBit != 0,
		number:      uint32(id & highTagMarker),
	}
	b = b[1:]
	if t.number == highTagMarker {
		var err error
		if t.number, b, err = readHighTagNumber(b); err != nil {
			return tlv{}, nil, err
		}
	}
	length, b, err := readLength(b)
	if err != nil {
		return tlv{}, nil, err
	}
	if uint64(length) > uint64(len(b)) {
		return tlv{}, nil, errBadTLV
	}
	t.content = b[:length]
	return t, b[length:], nil
}

// readHighTagNumber reads the base-128 tag number that follows an identifier
// octet whose low five bits are all set (X.690 section 8.1.2.4).
func readHighTagNumber(b []byte) (uint32, []byte, error) {
	var n uint32
	for i := 0; i < maxHighTagOctets && i < len(b); i++ {
		if i == 0 && b[0] == 0x80 {
			return 0, nil, errBadTLV
		}
		n = n<<7 | uint32(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			if n < minHighTagNumber {
				return 0, nil, errBadTLV
			}
			return n, b[i+1:], nil
		}
	}
	return 0, nil, errBadTLV
}

// readLength reads a definite DER length in its minimal form.
func readLength(b []byte) (uint32, []byte, error) {
	if len(b) == 0 {
		return 0, nil, errBadTLV
	}
	first := b[0]
	b = b[1:]
	if first <= shortFormMaxValue {
		return uint32(first), b, nil
	}
	octets := int(first & 0x7f)
	if octets == 0 || octets > maxLengthOctets || octets > len(b) || b[0] == 0 {
		return 0, nil, errBadTLV
	}
	var n uint32
	for _, c := range b[:octets] {
		n = n<<8 | uint32(c)
	}
	if n <= shortFormMaxValue {
		return 0, nil, errBadTLV
	}
	return n, b[octets:], nil
}
