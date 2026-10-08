// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// MaxMessageSize bounds a frame's payload. It is read before the payload
// is allocated, so a peer cannot make either side buffer more.
const MaxMessageSize = 64 << 10

// headerSize is the length prefix of a frame.
const headerSize = 4

// ErrMessageTooLarge is returned for a frame whose payload exceeds
// MaxMessageSize.
var ErrMessageTooLarge = errors.New("pkcs11wire: message exceeds 64 KiB")

// WriteMessage writes m as one frame, in a single Write.
func WriteMessage(w io.Writer, m proto.Message) error {
	body, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("pkcs11wire: encode a message: %w", err)
	}
	if len(body) > MaxMessageSize {
		return ErrMessageTooLarge
	}
	frame := make([]byte, headerSize+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body))) //nolint:gosec // bounded by MaxMessageSize above
	copy(frame[headerSize:], body)
	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("pkcs11wire: write a message: %w", err)
	}
	return nil
}

// ReadMessage reads one frame into m. It returns io.EOF when the peer
// closed before sending any byte, io.ErrUnexpectedEOF (wrapped) for a
// frame cut short, and ErrMessageTooLarge for a length beyond the bound.
func ReadMessage(r io.Reader, m proto.Message) error {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("pkcs11wire: read a message length: %w", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > MaxMessageSize {
		return ErrMessageTooLarge
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("pkcs11wire: read a message: %w", err)
	}
	if err := proto.Unmarshal(body, m); err != nil {
		return fmt.Errorf("pkcs11wire: decode a message: %w", err)
	}
	return nil
}
