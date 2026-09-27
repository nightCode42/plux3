// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package uuid7

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// fixedClock returns a clock that reads *t.
func fixedClock(t *time.Time) func() time.Time { return func() time.Time { return *t } }

// endless returns an entropy source repeating b.
type endless struct{ b byte }

// Read fills p with the repeated byte.
func (e endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = e.b
	}
	return len(p), nil
}

// TestParseAcceptsOnlyCanonicalUUIDv7_SCH_002 checks the accepted form.
// Verifies: SCH-002.
func TestParseAcceptsOnlyCanonicalUUIDv7_SCH_002(t *testing.T) {
	t.Parallel()
	valid := "01928c3a-7b2e-7c4d-8e5f-0123456789ab"
	u, err := Parse(valid)
	if err != nil || u.String() != valid || u.Version() != 7 {
		t.Fatalf("Parse(%q) = %v, %v", valid, u, err)
	}
	for _, bad := range []string{
		"", "01928c3a7b2e7c4d8e5f0123456789ab", "01928C3A-7B2E-7C4D-8E5F-0123456789AB",
		"01928c3a-7b2e-4c4d-8e5f-0123456789ab", // version 4
		"01928c3a-7b2e-7c4d-ce5f-0123456789ab", // wrong variant
		"01928c3a-7b2e-7c4d-8e5f-0123456789ag", "{1928c3a-7b2e-7c4d-8e5f-0123456789ab}",
		"01928c3a_7b2e-7c4d-8e5f-0123456789ab",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	if !regexp.MustCompile(Pattern).MatchString(valid) {
		t.Error("Pattern rejects a valid UUIDv7")
	}
}

// TestTextMarshalling checks the encoding.TextMarshaler round trip.
func TestTextMarshalling(t *testing.T) {
	t.Parallel()
	var u UUID
	if err := u.UnmarshalText([]byte("01928c3a-7b2e-7c4d-8e5f-0123456789ab")); err != nil {
		t.Fatal(err)
	}
	text, _ := u.MarshalText()
	if string(text) != "01928c3a-7b2e-7c4d-8e5f-0123456789ab" || u.IsZero() || !(UUID{}).IsZero() {
		t.Errorf("round trip gave %s", text)
	}
	if err := u.UnmarshalText([]byte("nope")); err == nil {
		t.Error("invalid text accepted")
	}
}

// TestGeneratorProducesValidIncreasingIDs_SCH_031 checks version, variant,
// embedded time and strict ordering within and across milliseconds.
// Verifies: SCH-002, SCH-031.
func TestGeneratorProducesValidIncreasingIDs_SCH_031(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	g := NewGenerator(fixedClock(&now), endless{0xFF})
	var prev string
	for i := range 5000 { // crosses the 12-bit counter overflow
		if i == 2500 {
			now = now.Add(-time.Second) // a clock step backwards keeps order
		}
		u, err := g.New()
		if err != nil {
			t.Fatal(err)
		}
		s := u.String()
		if _, err := Parse(s); err != nil {
			t.Fatalf("generated invalid ID %s: %v", s, err)
		}
		if s <= prev {
			t.Fatalf("ID %d not increasing: %s after %s", i, s, prev)
		}
		prev = s
	}
	first, _ := NewGenerator(fixedClock(&now), endless{0}).New()
	if !first.Time().Equal(now.Truncate(time.Millisecond)) {
		t.Errorf("Time() = %v, want %v", first.Time(), now)
	}
}

// TestGeneratorIsSafeForConcurrentUse checks uniqueness under -race.
func TestGeneratorIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g := NewGenerator(fixedClock(&now), endless{0x42})
	var mu sync.Mutex
	seen := map[UUID]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				u, err := g.New()
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				seen[u] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(seen) != 1600 {
		t.Errorf("got %d unique IDs, want 1600", len(seen))
	}
}

// TestGeneratorReportsFailures checks entropy and clock errors.
func TestGeneratorReportsFailures(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if _, err := NewGenerator(fixedClock(&now), bytes.NewReader(nil)).New(); err == nil {
		t.Error("empty entropy accepted")
	}
	past := time.UnixMilli(-1)
	if _, err := NewGenerator(fixedClock(&past), endless{1}).New(); err == nil || !strings.Contains(err.Error(), "48-bit") {
		t.Errorf("negative clock: %v", err)
	}
	end := time.UnixMilli(maxTimestamp)
	g := NewGenerator(fixedClock(&end), endless{0xFF})
	var err error
	for range 0x1000 {
		if _, err = g.New(); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("exhausted timestamp: %v", err)
	}
}

// TestStringParseRoundTrip_QA_002 states: every generated identifier
// round-trips through its text form.
// Verifies: QA-002, SCH-002.
func TestStringParseRoundTrip_QA_002(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ms := rapid.Int64Range(0, maxTimestamp).Draw(t, "ms")
		now := time.UnixMilli(ms)
		seed := rapid.Byte().Draw(t, "entropy")
		u, err := NewGenerator(fixedClock(&now), endless{seed}).New()
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(u.String())
		if err != nil || back != u || u.Time().UnixMilli() != ms {
			t.Fatalf("%v → %s → %v (%v)", u, u.String(), back, err)
		}
	})
}
