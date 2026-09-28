// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration written as a Go duration string, such as
// "5m" or "750ms".
type Duration time.Duration

// UnmarshalJSON reads a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"5m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(d.String())
	if err != nil {
		return nil, fmt.Errorf("marshal duration: %w", err)
	}
	return b, nil
}

// Duration returns the value as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String returns the duration in the form it is written in.
func (d Duration) String() string { return time.Duration(d).String() }

// byteUnits are the suffixes Bytes accepts, longest first so that "MiB"
// is matched before "B".
var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10},
	{"MiB", 1 << 20},
	{"GiB", 1 << 30},
	{"TiB", 1 << 40},
	{"KB", 1000},
	{"MB", 1000 * 1000},
	{"GB", 1000 * 1000 * 1000},
	{"TB", 1000 * 1000 * 1000 * 1000},
	{"B", 1},
}

// Bytes is a size written with a binary or decimal unit, such as "20MiB",
// "1.5GB" or "512" (bytes).
type Bytes int64

// ParseBytes reads a size. Fractions are allowed and must resolve to a
// whole number of bytes.
func ParseBytes(s string) (Bytes, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("size must not be empty")
	}
	factor := int64(1)
	for _, u := range byteUnits {
		if rest, ok := strings.CutSuffix(t, u.suffix); ok {
			factor, t = u.factor, strings.TrimSpace(rest)
			break
		}
	}
	if n, err := strconv.ParseInt(t, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("size %q must not be negative", s)
		}
		if n > (1<<62)/factor {
			return 0, fmt.Errorf("size %q is too large", s)
		}
		return Bytes(n * factor), nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: want a number with an optional unit such as \"20MiB\"", s)
	}
	if f < 0 {
		return 0, fmt.Errorf("size %q must not be negative", s)
	}
	exact := f * float64(factor)
	n := int64(exact)
	if exact != float64(n) {
		return 0, fmt.Errorf("size %q is not a whole number of bytes", s)
	}
	return Bytes(n), nil
}

// UnmarshalJSON reads a size written as a string or as a plain number of
// bytes.
func (b *Bytes) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("size must be a string such as \"20MiB\" or a number of bytes: %w", err)
		}
		if n < 0 {
			return fmt.Errorf("size %d must not be negative", n)
		}
		*b = Bytes(n)
		return nil
	}
	v, err := ParseBytes(s)
	if err != nil {
		return err
	}
	*b = v
	return nil
}

// MarshalJSON writes the size as a plain number of bytes.
func (b Bytes) MarshalJSON() ([]byte, error) {
	out, err := json.Marshal(int64(b))
	if err != nil {
		return nil, fmt.Errorf("marshal size: %w", err)
	}
	return out, nil
}

// Int64 returns the size in bytes.
func (b Bytes) Int64() int64 { return int64(b) }

// String returns the size with the largest binary unit that divides it
// exactly, so that a value read back is the value written.
func (b Bytes) String() string {
	n := int64(b)
	for _, u := range []struct {
		suffix string
		factor int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n != 0 && n%u.factor == 0 {
			return strconv.FormatInt(n/u.factor, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

// Secret is a configured value that must never be printed: a password, a
// client secret or a token. It reads from the file like a string.
type Secret string

// String hides the value, so that a Secret cannot be logged by accident
// (OBS-003, SEC-092).
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "[redacted]"
}

// GoString hides the value in %#v output as well.
func (s Secret) GoString() string { return `"` + s.String() + `"` }

// MarshalJSON hides the value, so that a marshalled Config carries no
// secrets.
func (s Secret) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(s.String())
	if err != nil {
		return nil, fmt.Errorf("marshal secret: %w", err)
	}
	return b, nil
}

// Value returns the secret itself. Every call site is a place where the
// value leaves the type, which makes them easy to review.
func (s Secret) Value() string { return string(s) }
