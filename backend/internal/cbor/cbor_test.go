// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cbor

import (
	"bytes"
	"errors"
	"testing"
)

// Verifies: SEC-100.
// The CBOR decoder reads what authenticators write and refuses what
// they never do: tags, floats, indefinite lengths, truncation, repeated
// keys and unbounded nesting.
func TestDecode(t *testing.T) {
	t.Parallel()
	v, n, err := Decode([]byte{0xa3, 0x01, 0x02, 0x20, 0x43, 1, 2, 3, 0x63, 'f', 'm', 't', 0x82, 0xf5, 0xf6, 0xff})
	if err != nil || n != 15 {
		t.Fatalf("decode = %v, %d, %v", v, n, err)
	}
	m := v.(map[any]any)
	if m[int64(1)] != int64(2) || !bytes.Equal(m[int64(-1)].([]byte), []byte{1, 2, 3}) || len(m["fmt"].([]any)) != 2 {
		t.Errorf("decoded %v", m)
	}
	if v, _, err := Decode([]byte{0x19, 0x01, 0x00}); err != nil || v != int64(256) {
		t.Errorf("uint16 = %v, %v", v, err)
	}
	if v, _, err := Decode([]byte{0x3a, 0, 0, 0, 0}); err != nil || v != int64(-1) {
		t.Errorf("negative uint32 = %v, %v", v, err)
	}
	if v, _, err := Decode([]byte{0xf4}); err != nil || v != false {
		t.Errorf("false = %v, %v", v, err)
	}
	deep := bytes.Repeat([]byte{0x81}, 20)
	deep = append(deep, 0x00)
	for name, in := range map[string][]byte{
		"empty":             {},
		"a tag":             {0xc0, 0x00},
		"a float":           {0xf9, 0x3c, 0x00},
		"indefinite":        {0x5f},
		"truncated head":    {0x19, 0x01},
		"truncated string":  {0x43, 1},
		"a repeated key":    {0xa2, 0x01, 0x00, 0x01, 0x00},
		"a byte-string key": {0xa1, 0x41, 0x00, 0x00},
		"too deep":          deep,
		"too many items":    {0x9a, 0x00, 0x01, 0x00, 0x00},
		"a huge integer":    {0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"a huge negative":   {0x3b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"an unknown simple": {0xf0},
		"a truncated map":   {0xa1, 0x01},
		"a truncated array": {0x81},
	} {
		if _, _, err := Decode(in); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzDecode checks that no input makes the decoder panic or read
// past its input.
func FuzzDecode(f *testing.F) {
	for _, seed := range [][]byte{{0xa1, 0x01, 0x02}, {0x9f}, {0x5b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, n, err := Decode(data)
		if err == nil && n > len(data) {
			t.Fatalf("read %d of %d bytes", n, len(data))
		}
	})
}
