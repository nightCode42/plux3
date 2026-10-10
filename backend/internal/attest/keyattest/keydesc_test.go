// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"
)

func TestParseKeyDescription(t *testing.T) {
	// Verifies: SEC-002.
	spec := validSpec()
	spec.extraSoftTLV = explicitTLV(1_000_000, []byte{0x02, 0x01, 0x05})
	kd, err := parseKeyDescription(encodeKD(spec))
	if err != nil {
		t.Fatalf("parseKeyDescription: %v", err)
	}
	if kd.attestationVersion != 300 || kd.keyMintVersion != 300 || !bytes.Equal(kd.challenge, testChallenge) {
		t.Errorf("header = %+v", kd)
	}
	rot := kd.hardware.rootOfTrust
	if rot == nil || !rot.deviceLocked || rot.state != BootVerified {
		t.Errorf("rootOfTrust = %+v", rot)
	}
	if kd.software.rootOfTrust != nil {
		t.Error("rootOfTrust read from the software list")
	}
	if kd.hardware.osPatchLevel == nil || *kd.hardware.osPatchLevel != 20260905 {
		t.Errorf("osPatchLevel = %v", kd.hardware.osPatchLevel)
	}
	if a := kd.software.appID; a == nil || len(a.packages) != 1 || a.packages[0] != testPackage {
		t.Errorf("appID = %+v", a)
	}
}

func TestParseKeyDescriptionBootStates(t *testing.T) {
	// Verifies: SEC-002.
	want := []BootState{BootVerified, BootSelfSigned, BootUnverified, BootFailed}
	for state, w := range want {
		spec := validSpec()
		spec.root = &rotSpec{locked: true, state: int64(state)}
		kd, err := parseKeyDescription(encodeKD(spec))
		if err != nil || kd.hardware.rootOfTrust.state != w {
			t.Errorf("state %d: %+v, %v", state, kd, err)
		}
	}
}

// seq wraps the given encoded elements in a SEQUENCE.
func seq(parts ...[]byte) []byte {
	return build(func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			for _, p := range parts {
				b.AddBytes(p)
			}
		})
	})
}

func derInt(v int64) []byte {
	return build(func(b *cryptobyte.Builder) { b.AddASN1Int64(v) })
}

func derEnum(v int64) []byte {
	return build(func(b *cryptobyte.Builder) { b.AddASN1Enum(v) })
}

func derOctets(v []byte) []byte {
	return build(func(b *cryptobyte.Builder) { b.AddASN1OctetString(v) })
}

func TestParseKeyDescriptionMalformed(t *testing.T) {
	// Verifies: SEC-002.
	empty := seq()
	header := func(parts ...[]byte) []byte {
		return seq(append([][]byte{derInt(3), derEnum(1), derInt(3), derEnum(1), derOctets(testChallenge), derOctets(nil)}, parts...)...)
	}
	badRoot := explicitTLV(704, seq(derOctets(nil), derInt(1), derEnum(0)))
	cases := map[string][]byte{
		"version is an enumeration":   seq(derEnum(3), derEnum(1), derInt(3), derEnum(1), derOctets(nil), derOctets(nil), empty, empty),
		"level is an integer":         seq(derInt(3), derInt(1), derInt(3), derEnum(1), derOctets(nil), derOctets(nil), empty, empty),
		"challenge is an integer":     seq(derInt(3), derEnum(1), derInt(3), derEnum(1), derInt(5), derOctets(nil), empty, empty),
		"unknown level":               seq(derInt(3), derEnum(1), derInt(3), derEnum(7), derOctets(nil), derOctets(nil), empty, empty),
		"missing lists":               header(),
		"missing hardware list":       header(empty),
		"root of trust wrong types":   header(empty, seq(badRoot)),
		"unknown boot state":          header(empty, seq(explicitTLV(704, seq(derOctets(nil), []byte{0x01, 0x01, 0xff}, derEnum(9))))),
		"duplicate root of trust":     header(empty, seq(explicitTLV(704, encodeRoot(rotSpec{})), explicitTLV(704, encodeRoot(rotSpec{})))),
		"patch level not an integer":  header(empty, seq(explicitTLV(706, derOctets(nil)))),
		"app id not octets":           header(seq(explicitTLV(709, derInt(1))), empty),
		"app id content malformed":    header(seq(explicitTLV(709, derOctets([]byte{0x30, 0x05}))), empty),
		"truncated authorisation tag": header(seq(), []byte{0x30, 0x04, 0xbf, 0x85, 0x40, 0x09}),
		"tag number not minimal":      header(seq(), seq(append([]byte{0xbf, 0x80, 0x05}, 0x00))),
	}
	for name, der := range cases {
		t.Run(name, func(t *testing.T) {
			if kd, err := parseKeyDescription(der); err == nil {
				t.Errorf("accepted: %+v", kd)
			}
		})
	}
}

func TestReadTLV(t *testing.T) {
	// Verifies: SEC-002.
	valid := []struct {
		name    string
		in      []byte
		number  uint32
		content string
		rest    int
	}{
		{"low tag", []byte{0xa1, 0x01, 'x'}, 1, "x", 0},
		{"tag 31", []byte{0xbf, 0x1f, 0x01, 'x', 0xff}, 31, "x", 1},
		{"tag 704", []byte{0xbf, 0x85, 0x40, 0x02, 'a', 'b'}, 704, "ab", 0},
		{"long length", append([]byte{0xbf, 0x85, 0x40, 0x81, 0x80}, make([]byte, 128)...), 704, string(make([]byte, 128)), 0},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			el, rest, err := readTLV(tt.in)
			if err != nil || el.number != tt.number || string(el.content) != tt.content || len(rest) != tt.rest {
				t.Errorf("readTLV = %+v, %d, %v", el, len(rest), err)
			}
			if !el.isContextConstructed() {
				t.Error("not context constructed")
			}
		})
	}
	invalid := map[string][]byte{
		"empty":                   {},
		"one byte":                {0xa1},
		"high tag below 31":       {0xbf, 0x05, 0x00},
		"leading 0x80":            {0xbf, 0x80, 0x1f, 0x00},
		"tag number too long":     {0xbf, 0x81, 0x81, 0x81, 0x81, 0x01, 0x00},
		"tag number cut":          {0xbf, 0x85},
		"indefinite length":       {0xa1, 0x80, 0x00, 0x00},
		"non-minimal long length": {0xa1, 0x81, 0x05, 1, 2, 3, 4, 5},
		"length with zero lead":   {0xa1, 0x82, 0x00, 0x80},
		"length too wide":         {0xa1, 0x85, 0x01, 0x00, 0x00, 0x00, 0x00},
		"content past the end":    {0xa1, 0x05, 0x00},
	}
	for name, in := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readTLV(in); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func FuzzParseKeyDescription(f *testing.F) {
	valid := encodeKD(validSpec())
	f.Add(valid)
	f.Add(valid[:len(valid)/2])
	f.Add([]byte{})
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{0x30, 0x84, 0xff, 0xff, 0xff, 0xff})
	soft := validSpec()
	soft.appInHW = true
	f.Add(encodeKD(soft))
	f.Fuzz(func(t *testing.T, der []byte) {
		kd, err := parseKeyDescription(der)
		if (kd == nil) == (err == nil) {
			t.Fatalf("kd=%v err=%v", kd, err)
		}
	})
}
