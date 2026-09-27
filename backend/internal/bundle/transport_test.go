// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: BND-007.
func TestTransportRoundTrip(t *testing.T) {
	t.Parallel()
	data, err := Encode(KindPlugin, sampleSections())
	if err != nil {
		t.Fatal(err)
	}
	packed, err := Compress(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Compress(data)
	if !bytes.Equal(packed, again) {
		t.Error("compression is not deterministic")
	}
	out, err := Decompress(packed, int64(len(data)))
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("round trip: %v", err)
	}
	_, err = Decompress(packed, int64(len(data)-1))
	wantCode(t, "declared size over the limit", err, plxerr.TransportDecodingFailed)
}

// Verifies: BND-007.
func TestTransportRejectsBombsAndCorruption(t *testing.T) {
	t.Parallel()
	// A frame without a declared size (streaming encoder).
	var stream bytes.Buffer
	w, err := zstd.NewWriter(&stream)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(make([]byte, 1<<20))
	_ = w.Close()
	_, err = Decompress(stream.Bytes(), 1<<30)
	wantCode(t, "no declared size", err, plxerr.TransportDecodingFailed)

	bomb, err := Compress(make([]byte, 64<<20))
	if err != nil {
		t.Fatal(err)
	}
	if len(bomb) > 1<<16 {
		t.Fatalf("the bomb is %d bytes", len(bomb))
	}
	_, err = Decompress(bomb, 1<<20)
	wantCode(t, "bomb", err, plxerr.TransportDecodingFailed)

	good, _ := Compress([]byte("plux bundle"))
	for name, data := range map[string][]byte{
		"not zstd":  []byte("PLUX"),
		"truncated": good[:len(good)-3],
		"corrupt":   append(append([]byte{}, good[:len(good)-1]...), good[len(good)-1]^0xff),
	} {
		_, err := Decompress(data, 1<<20)
		wantCode(t, name, err, plxerr.TransportDecodingFailed)
	}
}
