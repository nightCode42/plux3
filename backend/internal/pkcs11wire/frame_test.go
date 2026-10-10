// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
)

// frame builds raw frame bytes with an arbitrary declared length.
func frame(declared uint32, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, declared)
	return append(out, body...)
}

// Verifies: SEC-120.
func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()
	want := &pkcs11pb.Request{Request: &pkcs11pb.Request_Sign{Sign: &pkcs11pb.SignRequest{
		KeyRef: "pkcs11:object=plux-targets", Digest: []byte{1, 2, 3}, Algorithm: pkcs11pb.Algorithm_ALGORITHM_ED25519,
	}}}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, want); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if got := binary.BigEndian.Uint32(buf.Bytes()); int(got) != buf.Len()-4 {
		t.Errorf("declared length %d, payload %d", got, buf.Len()-4)
	}
	var got pkcs11pb.Request
	if err := ReadMessage(&buf, &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !proto.Equal(want, &got) {
		t.Errorf("round trip = %v, want %v", &got, want)
	}
}

// Verifies: SEC-120.
func TestWriteRefusesAnOversizedMessage(t *testing.T) {
	t.Parallel()
	big := &pkcs11pb.Request{Request: &pkcs11pb.Request_Sign{Sign: &pkcs11pb.SignRequest{Digest: make([]byte, MaxMessageSize+1)}}}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, big); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("err = %v, want ErrMessageTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes of a refused message", buf.Len())
	}
}

// Verifies: SEC-120.
func TestReadRefusesFramesThatAreOversizedTruncatedOrUndecodable(t *testing.T) {
	t.Parallel()
	valid, err := proto.Marshal(&pkcs11pb.Response{Response: &pkcs11pb.Response_Error{Error: &pkcs11pb.Error{Message: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"length beyond the bound", frame(MaxMessageSize+1, nil), ErrMessageTooLarge},
		{"length of four gigabytes", frame(1<<32-1, nil), ErrMessageTooLarge},
		{"empty stream", nil, io.EOF},
		{"truncated length", []byte{0, 0}, io.ErrUnexpectedEOF},
		{"truncated payload", frame(uint32(len(valid)), valid[:len(valid)-1]), io.ErrUnexpectedEOF}, //nolint:gosec // a few bytes
		{"payload missing", frame(5, nil), io.ErrUnexpectedEOF},
		{"not protobuf", frame(3, []byte{0xff, 0xff, 0xff}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ReadMessage(bytes.NewReader(tc.in), &pkcs11pb.Request{})
			switch {
			case err == nil:
				t.Fatal("accepted")
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Verifies: SEC-120.
func TestReadLeavesTrailingBytesForTheNextFrame(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	for _, msg := range []string{"a", "b"} {
		if err := WriteMessage(&buf, &pkcs11pb.Error{Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"a", "b"} {
		var got pkcs11pb.Error
		if err := ReadMessage(&buf, &got); err != nil || got.GetMessage() != want {
			t.Fatalf("got %q, %v; want %q", got.GetMessage(), err, want)
		}
	}
	if err := ReadMessage(&buf, &pkcs11pb.Error{}); !errors.Is(err, io.EOF) {
		t.Errorf("after the last frame: %v, want EOF", err)
	}
}
