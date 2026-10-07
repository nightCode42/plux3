// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"bytes"
	"testing"
	"time"
)

// stepClock is a clock that only moves when a test moves it.
type stepClock struct{ t time.Time }

func (c *stepClock) now() time.Time          { return c.t }
func (c *stepClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testNonces(t *testing.T, c *stepClock) *Nonces {
	t.Helper()
	n, err := NewNonces(bytes.Repeat([]byte{7}, 32), time.Minute, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Verifies: SEC-024.
func TestNewNoncesValidates(t *testing.T) {
	clock := &stepClock{t: testNow}
	tests := []struct {
		name     string
		key      []byte
		rotation time.Duration
		now      func() time.Time
		ok       bool
	}{
		{"valid", make([]byte, 32), time.Minute, clock.now, true},
		{"long key", make([]byte, 64), 5 * time.Minute, clock.now, true},
		{"short key", make([]byte, 31), time.Minute, clock.now, false},
		{"nil key", nil, time.Minute, clock.now, false},
		{"zero rotation", make([]byte, 32), 0, clock.now, false},
		{"negative rotation", make([]byte, 32), -time.Second, clock.now, false},
		{"rotation above five minutes", make([]byte, 32), 5*time.Minute + time.Nanosecond, clock.now, false},
		{"no clock", make([]byte, 32), time.Minute, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := NewNonces(tc.key, tc.rotation, tc.now)
			if (err == nil) != tc.ok || (err == nil) != (n != nil) {
				t.Fatalf("NewNonces = %v, %v; want ok=%v", n, err, tc.ok)
			}
		})
	}
}

// Verifies: SEC-024.
func TestNonceEpochs(t *testing.T) {
	clock := &stepClock{t: testNow}
	n := testNonces(t, clock)
	first := n.Current()

	if !n.Valid(first) {
		t.Fatal("current nonce refused")
	}
	if got := n.Current(); got != first {
		t.Fatal("nonce changed within its epoch")
	}

	// Staying inside the epoch keeps the nonce valid and unchanged.
	clock.t = time.Unix(testNow.Unix()/60*60, 0).UTC().Add(59 * time.Second)
	if n.Current() != first || !n.Valid(first) {
		t.Fatal("nonce changed before the epoch ended")
	}

	clock.advance(time.Second) // next epoch
	if n.Current() == first {
		t.Fatal("nonce did not rotate")
	}
	if !n.Valid(first) {
		t.Fatal("previous epoch refused")
	}
	if !n.Valid(n.Current()) {
		t.Fatal("new current refused")
	}

	clock.advance(time.Minute) // two epochs after first
	if n.Valid(first) {
		t.Fatal("nonce two epochs old accepted")
	}

	// A nonce from the future is not valid either.
	future := n.Current()
	clock.t = clock.t.Add(-3 * time.Minute)
	if n.Valid(future) {
		t.Fatal("nonce from a later epoch accepted")
	}
}

// Verifies: SEC-024.
func TestNonceRejectsForgeries(t *testing.T) {
	clock := &stepClock{t: testNow}
	n := testNonces(t, clock)
	good := n.Current()

	flipped := []byte(good)
	if flipped[len(flipped)-1] == 'A' {
		flipped[len(flipped)-1] = 'B'
	} else {
		flipped[len(flipped)-1] = 'A'
	}
	otherKey, err := NewNonces(bytes.Repeat([]byte{8}, 32), time.Minute, clock.now)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, nonce string }{
		{"empty", ""},
		{"not base64", "!!!!"},
		{"too short", good[:len(good)-4]},
		{"too long", good + "AAAA"},
		{"padded", good + "="},
		{"mac altered", string(flipped)},
		{"zero mac", b64(append(make([]byte, 7), 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0))},
		{"other key", otherKey.Current()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if n.Valid(tc.nonce) {
				t.Fatalf("Valid(%q) = true", tc.nonce)
			}
		})
	}
}

// Verifies: SEC-024.
func TestNonceIsStatelessAcrossReplicas(t *testing.T) {
	clock := &stepClock{t: testNow}
	a, b := testNonces(t, clock), testNonces(t, clock)
	if !b.Valid(a.Current()) {
		t.Fatal("a replica refused a nonce another replica issued")
	}
}

func TestNonceKeyIsCopiedAndEpochsClamp(t *testing.T) {
	clock := &stepClock{t: testNow}
	key := bytes.Repeat([]byte{7}, 32)
	n, err := NewNonces(key, time.Minute, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	want := n.Current()
	key[0] ^= 0xff
	if n.Current() != want {
		t.Fatal("changing the caller's key changed the nonces")
	}

	clock.t = time.Unix(-100, 0)
	if !n.Valid(n.Current()) {
		t.Fatal("a clock before 1970 broke the nonce")
	}
}
