// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package delta computes and applies section-level bundle deltas
// (ADR-0003, REL-021).
//
// A delta is an 80-byte header followed by one instruction per section of
// the new bundle, in directory order. All integers are little-endian.
//
//	header       magic "PXDL", version u16, bundle kind u16, section count
//	             u32, flags u32 (zero), old bundle hash [32], new bundle
//	             hash [32]
//	instruction  id [16], kind u16, op u8, reserved u8 (zero), section size
//	             u64, payload length u32, payload
//
// The operations are reuse (the payload is the SHA-256 of an old section
// with the same kind and ID), whole (the payload is a zstd frame of the
// section) and patch (the payload is the old section's SHA-256 followed by
// a zstd frame compressed with that section as a raw dictionary — zstd's
// --patch-from). Apply lays the sections out with the ordinary container
// encoder and refuses a result whose bundle hash is not the one named in
// the header (PLX-3011): a delta is never trusted, only verified.
package delta

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/klauspost/compress/zstd"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Layout constants.
const (
	magic       = "PXDL"
	version     = 1
	headerSize  = 80
	instrSize   = 32
	hashSize    = sha256.Size
	opReuse     = 0
	opWhole     = 1
	opPatch     = 2
	rawDictID   = 0
	maxSections = 1 << 16
	// minWindow is the least memory a decoder is allowed, so small frames
	// decode; the output is still bounded by the section size.
	minWindow = 1 << 17
)

// FullBundleRatio is the share of the full compressed bundle above which a
// device is told to download the bundle instead of the delta (REL-023).
const FullBundleRatio = 0.6

// Worthwhile reports whether a delta of deltaSize bytes should be served
// in place of a compressed bundle of bundleSize bytes (REL-023).
func Worthwhile(deltaSize, bundleSize int64) bool {
	return float64(deltaSize) <= FullBundleRatio*float64(bundleSize)
}

// Header is the part of a delta that names what it connects.
type Header struct {
	Kind     bundle.Kind
	Sections int
	Old, New [hashSize]byte
}

// Diff returns the delta that turns the bundle old into the bundle new.
// Both must be well-formed containers; they are read without verifying
// section contents, which the caller has already done at publish.
func Diff(old, new []byte) ([]byte, error) {
	a, err := read(old)
	if err != nil {
		return nil, err
	}
	b, err := read(new)
	if err != nil {
		return nil, err
	}
	whole, err := zstd.NewWriter(nil, encoderOptions()...)
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	defer func() { _ = whole.Close() }()
	byHash := make(map[[hashSize]byte]bool, len(a.Sections))
	for _, s := range a.Sections {
		byHash[s.Hash] = true
	}
	var out bytes.Buffer
	out.WriteString(magic)
	out.Write(binary.LittleEndian.AppendUint16(nil, version))
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(b.Kind)))
	out.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(b.Sections)))) //nolint:gosec // G115: bounded by the container.
	out.Write(make([]byte, 4))                                                // flags
	out.Write(a.Hash[:])
	out.Write(b.Hash[:])
	for _, s := range b.Sections {
		op, payload, err := instruction(a, byHash, whole, s)
		if err != nil {
			return nil, err
		}
		out.Write(s.ID[:])
		out.Write(binary.LittleEndian.AppendUint16(nil, uint16(s.Kind)))
		out.Write([]byte{op, 0})
		out.Write(binary.LittleEndian.AppendUint64(nil, uint64(len(s.Data))))  //nolint:gosec // G115: lengths are non-negative.
		out.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))) //nolint:gosec // G115: bounded by the section size.
		out.Write(payload)
	}
	return out.Bytes(), nil
}

// instruction chooses the smallest encoding of the new section s.
func instruction(a *bundle.Bundle, byHash map[[hashSize]byte]bool, whole *zstd.Encoder, s bundle.Section) (byte, []byte, error) {
	if byHash[s.Hash] {
		return opReuse, s.Hash[:], nil
	}
	best := whole.EncodeAll(s.Data, nil)
	op := byte(opWhole)
	if base, ok := a.Section(s.Kind, s.ID); ok && len(base.Data) > 0 {
		patch, err := patchFrame(base.Data, s.Data)
		if err != nil {
			return 0, nil, err
		}
		if hashSize+len(patch) < len(best) {
			op, best = opPatch, append(append(make([]byte, 0, hashSize+len(patch)), base.Hash[:]...), patch...)
		}
	}
	return op, best, nil
}

// patchFrame compresses data with base as a raw dictionary.
func patchFrame(base, data []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil, append(encoderOptions(), zstd.WithEncoderDictRaw(rawDictID, base))...)
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	defer func() { _ = enc.Close() }()
	return enc.EncodeAll(data, nil), nil
}

// encoderOptions make the output depend on the input only (CMP-002).
func encoderOptions() []zstd.EOption {
	return []zstd.EOption{
		zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
		zstd.WithZeroFrames(true),
	}
}

// read parses a container's header and directory. Section contents are
// not verified: Diff runs on bundles the server built, and Apply checks
// the result's hash.
func read(data []byte) (*bundle.Bundle, error) {
	b, err := bundle.ReadStructure(data)
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	return b, nil
}

// ReadHeader parses a delta's header.
func ReadHeader(d []byte) (Header, error) {
	if len(d) < headerSize || string(d[:4]) != magic {
		return Header{}, malformed("not a delta")
	}
	if v := binary.LittleEndian.Uint16(d[4:]); v != version {
		return Header{}, malformed("version %d is not supported", v)
	}
	if !allZero(d[12:16]) {
		return Header{}, malformed("unknown flags")
	}
	n := binary.LittleEndian.Uint32(d[8:])
	if n > maxSections {
		return Header{}, malformed("%d sections", n)
	}
	h := Header{Kind: bundle.Kind(binary.LittleEndian.Uint16(d[6:])), Sections: int(n)}
	copy(h.Old[:], d[16:48])
	copy(h.New[:], d[48:80])
	return h, nil
}

// Apply rebuilds the new bundle from old and the delta d. The rebuilt
// bundle may be at most maxSize bytes; every section is decoded into a
// buffer of its declared size, so a hostile delta cannot make Apply
// allocate beyond it. Errors carry PLX-3012 (the delta is malformed) or
// PLX-3011 (the result is not the bundle the delta names).
func Apply(old, d []byte, maxSize int64) ([]byte, error) {
	h, err := ReadHeader(d)
	if err != nil {
		return nil, err
	}
	a, err := bundle.ReadStructure(old)
	if err != nil {
		return nil, fmt.Errorf("delta: the base: %w", err)
	}
	if a.Hash != h.Old {
		return nil, plxerr.New(plxerr.PatchHashMismatch, "the delta was made against another bundle")
	}
	byHash := make(map[[hashSize]byte][]byte, len(a.Sections))
	for _, s := range a.Sections {
		byHash[s.Hash] = s.Data
	}
	sections := make([]bundle.Section, 0, h.Sections)
	rest, budget := d[headerSize:], maxSize
	for range h.Sections {
		var s bundle.Section
		if s, rest, err = next(rest, byHash, &budget); err != nil {
			return nil, err
		}
		sections = append(sections, s)
	}
	if len(rest) != 0 {
		return nil, malformed("%d bytes after the last instruction", len(rest))
	}
	out, err := bundle.Encode(h.Kind, sections)
	if err != nil {
		return nil, malformed("%v", err)
	}
	if got, err := bundle.ReadStructure(out); err != nil || got.Hash != h.New {
		return nil, plxerr.New(plxerr.PatchHashMismatch, "the rebuilt bundle's hash is not the one the delta names")
	}
	return out, nil
}

// next decodes one instruction and returns its section and the remaining
// input; budget is what is left of the size limit.
func next(d []byte, byHash map[[hashSize]byte][]byte, budget *int64) (bundle.Section, []byte, error) {
	if len(d) < instrSize {
		return bundle.Section{}, nil, malformed("truncated instruction")
	}
	var s bundle.Section
	copy(s.ID[:], d[:16])
	s.Kind = bundle.SectionKind(binary.LittleEndian.Uint16(d[16:]))
	op := d[18]
	size := binary.LittleEndian.Uint64(d[20:])
	n := int(binary.LittleEndian.Uint32(d[28:]))
	if d[19] != 0 {
		return s, nil, malformed("reserved byte set")
	}
	if size > uint64(max(*budget, 0)) { //nolint:gosec // G115: clamped to non-negative.
		return s, nil, malformed("the sections exceed %d bytes", *budget)
	}
	*budget -= int64(size) //nolint:gosec // G115: checked against the budget above.
	d = d[instrSize:]
	if len(d) < n {
		return s, nil, malformed("truncated payload")
	}
	payload, rest := d[:n], d[n:]
	var err error
	switch op {
	case opReuse:
		s.Data, err = reuse(payload, byHash)
	case opWhole:
		s.Data, err = decode(payload, nil, size)
	case opPatch:
		if len(payload) < hashSize {
			return s, nil, malformed("truncated patch")
		}
		base, ok := byHash[[hashSize]byte(payload[:hashSize])]
		if !ok {
			return s, nil, plxerr.New(plxerr.PatchHashMismatch, "the patch's base section is not in the old bundle")
		}
		s.Data, err = decode(payload[hashSize:], base, size)
	default:
		return s, nil, malformed("unknown operation %d", op)
	}
	if err != nil {
		return s, nil, err
	}
	if uint64(len(s.Data)) != size {
		return s, nil, plxerr.New(plxerr.PatchHashMismatch, "a section decoded to %d bytes, not %d", len(s.Data), size)
	}
	s.Hash = sha256.Sum256(s.Data)
	return s, rest, nil
}

// reuse returns the old section with the hash in payload.
func reuse(payload []byte, byHash map[[hashSize]byte][]byte) ([]byte, error) {
	if len(payload) != hashSize {
		return nil, malformed("a reuse instruction carries %d bytes", len(payload))
	}
	data, ok := byHash[[hashSize]byte(payload)]
	if !ok {
		return nil, plxerr.New(plxerr.PatchHashMismatch, "a reused section is not in the old bundle")
	}
	return data, nil
}

// decode decodes one zstd frame of exactly size bytes, with base as a raw
// dictionary when it is not nil. The encoder omits the content size from
// very small frames, so the bound is the output buffer's capacity, which
// the decoder never grows (WithDecodeAllCapLimit).
func decode(frame, base []byte, size uint64) ([]byte, error) {
	var h zstd.Header
	if err := h.Decode(frame); err != nil {
		return nil, malformed("zstd frame header: %v", err)
	}
	if h.HasFCS && h.FrameContentSize != size {
		return nil, malformed("the frame declares %d bytes, the section %d", h.FrameContentSize, size)
	}
	opts := []zstd.DOption{
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(max(size, minWindow)),
		zstd.WithDecodeAllCapLimit(true),
	}
	if base != nil {
		opts = append(opts, zstd.WithDecoderDictRaw(rawDictID, base))
	}
	dec, err := zstd.NewReader(nil, opts...)
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	defer dec.Close()
	out, err := dec.DecodeAll(frame, make([]byte, 0, max(size, 1)))
	if err != nil {
		return nil, malformed("zstd: %v", err)
	}
	return out, nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func malformed(format string, args ...any) error {
	return plxerr.New(plxerr.DeltaMalformed, format, args...)
}
