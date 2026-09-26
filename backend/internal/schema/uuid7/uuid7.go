// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package uuid7 parses, validates and generates the UUIDv7 identifiers of
// Plux documents (RFC 9562 §5.7, SCH-002, ADR-0025).
//
// Documents use the canonical lower-case 8-4-4-4-12 form only, so that equal
// identifiers are equal strings. The compiler never generates identifiers —
// generation is used when a template is copied with fresh identifiers
// (SCH-031) — and the clock and entropy of a Generator are injected, so tests
// are deterministic.
package uuid7

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// UUID is a 128-bit identifier in network byte order.
type UUID [16]byte

// Pattern is the regular expression, for JSON Schema, of a canonical UUIDv7.
const Pattern = `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`

// Parse parses the canonical form of a UUIDv7: lower-case hexadecimal in
// 8-4-4-4-12 groups, version 7 and the RFC 9562 variant.
func Parse(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, fmt.Errorf("uuid7.Parse: %q is not in 8-4-4-4-12 form", s)
	}
	hexDigits := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	for i := 0; i < len(hexDigits); i++ {
		if c := hexDigits[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return u, fmt.Errorf("uuid7.Parse: %q contains %q; use lower-case hexadecimal", s, c)
		}
	}
	if _, err := hex.Decode(u[:], []byte(hexDigits)); err != nil {
		return u, fmt.Errorf("uuid7.Parse: %w", err)
	}
	if u.Version() != 7 {
		return UUID{}, fmt.Errorf("uuid7.Parse: %q is version %d, want 7", s, u.Version())
	}
	if u[8]&0xC0 != 0x80 {
		return UUID{}, fmt.Errorf("uuid7.Parse: %q does not have the RFC 9562 variant", s)
	}
	return u, nil
}

// String returns the canonical lower-case 8-4-4-4-12 form.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// MarshalText encodes the canonical form.
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText decodes the canonical form of a UUIDv7.
func (u *UUID) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// Version returns the version field.
func (u UUID) Version() int { return int(u[6] >> 4) }

// IsZero reports whether u is the nil UUID.
func (u UUID) IsZero() bool { return u == UUID{} }

// Time returns the creation time encoded in the first 48 bits, with
// millisecond precision.
func (u UUID) Time() time.Time {
	ms := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 | int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	return time.UnixMilli(ms).UTC()
}

// Generator creates UUIDv7 values that are strictly increasing for one
// generator: within a millisecond the 12-bit rand_a field is a counter
// (RFC 9562 §6.2, method 1), and when it overflows the timestamp advances by
// one millisecond. It is safe for concurrent use.
type Generator struct {
	now     func() time.Time
	entropy io.Reader

	mu      sync.Mutex // guards lastMS and counter
	lastMS  uint64
	counter uint16
}

// NewGenerator returns a generator reading the clock from now and random
// bits from entropy, e.g. time.Now and crypto/rand.Reader.
func NewGenerator(now func() time.Time, entropy io.Reader) *Generator {
	return &Generator{now: now, entropy: entropy}
}

// maxTimestamp is the largest 48-bit millisecond timestamp.
const maxTimestamp = 1<<48 - 1

// New returns the next identifier.
func (g *Generator) New() (UUID, error) {
	var u UUID
	if _, err := io.ReadFull(g.entropy, u[6:]); err != nil {
		return UUID{}, fmt.Errorf("uuid7.New: read entropy: %w", err)
	}
	ms, counter, err := g.next(binary.BigEndian.Uint16(u[6:8]) & 0x0FFF)
	if err != nil {
		return UUID{}, err
	}
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(u[0:6], ts[2:8])
	binary.BigEndian.PutUint16(u[6:8], 0x7000|counter)
	u[8] = 0x80 | u[8]&0x3F
	return u, nil
}

// next returns the timestamp and rand_a counter for a new identifier, seeding
// the counter with seed at the start of each millisecond.
func (g *Generator) next(seed uint16) (uint64, uint16, error) {
	now := g.now().UnixMilli()
	if now < 0 || now > maxTimestamp {
		return 0, 0, errors.New("uuid7.New: clock outside the 48-bit UUIDv7 range")
	}
	ms := uint64(now)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case ms > g.lastMS:
		g.lastMS, g.counter = ms, seed&0x7FF // leave headroom for increments
	case g.counter < 0xFFF:
		g.counter++
	default:
		if g.lastMS == maxTimestamp {
			return 0, 0, errors.New("uuid7.New: timestamp exhausted")
		}
		g.lastMS++
		g.counter = seed & 0x7FF
	}
	return g.lastMS, g.counter, nil
}
