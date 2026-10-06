// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"testing"
)

// pipeConn returns the server's end of a connection, and the client's.
func pipeConn(t *testing.T) (*wsConn, net.Conn) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return &wsConn{conn: server, br: bufio.NewReader(server)}, client
}

// readFrom runs readMessage while the client writes raw bytes, and returns
// what the server's side saw and the frames it sent back.
func readFrom(t *testing.T, raw ...[]byte) (op byte, data []byte, sent []byte, err error) {
	t.Helper()
	c, client := pipeConn(t)
	type result struct {
		op   byte
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		op, data, err := c.readMessage()
		done <- result{op, data, err}
	}()
	// The client writes in the background: the server stops reading at the
	// first violation, which leaves the rest of the bytes unread.
	go func() {
		for _, b := range raw {
			if _, err := client.Write(b); err != nil {
				return
			}
		}
	}()
	replies := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 256)
		for {
			n, err := client.Read(tmp)
			buf.Write(tmp[:n])
			if err != nil {
				replies <- buf.Bytes()
				return
			}
		}
	}()
	r := <-done
	_ = c.conn.Close()
	return r.op, r.data, <-replies, r.err
}

func TestReadMessage(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 70_000)
	tests := []struct {
		name     string
		frames   [][]byte
		wantOp   byte
		wantData string
		wantErr  error
		wantSent []byte // the frames the server answers with
	}{
		{"a text frame", [][]byte{maskedFrame(opText, true, []byte("hello"))}, opText, "hello", nil, nil},
		{"a binary frame", [][]byte{maskedFrame(opBinary, true, []byte{1, 2})}, opBinary, "\x01\x02", nil, nil},
		{"a 16-bit length", [][]byte{maskedFrame(opText, true, bytes.Repeat([]byte("a"), 300))}, opText, string(bytes.Repeat([]byte("a"), 300)), nil, nil},
		{"fragments", [][]byte{maskedFrame(opText, false, []byte("he")), maskedFrame(opContinuation, false, []byte("l")), maskedFrame(opContinuation, true, []byte("lo"))}, opText, "hello", nil, nil},
		{"a ping between fragments", [][]byte{maskedFrame(opText, false, []byte("he")), maskedFrame(opPing, true, []byte("p")), maskedFrame(opContinuation, true, []byte("llo"))}, opText, "hello", nil, []byte{0x80 | opPong, 1, 'p'}},
		{"an unsolicited pong", [][]byte{maskedFrame(opPong, true, nil), maskedFrame(opText, true, []byte("ok"))}, opText, "ok", nil, nil},
		{"the peer's close", [][]byte{maskedFrame(opClose, true, []byte{0x03, 0xE8})}, 0, "", errWSClosed, []byte{0x80 | opClose, 2, 0x03, 0xE8}},
		{"a frame without a mask", [][]byte{{0x80 | opText, 2, 'h', 'i'}}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"reserved bits", [][]byte{append([]byte{0x80 | 0x40 | opText}, maskedFrame(opText, true, []byte("x"))[1:]...)}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"a continuation without a message", [][]byte{maskedFrame(opContinuation, true, []byte("x"))}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"a text frame inside a fragmented message", [][]byte{maskedFrame(opText, false, []byte("a")), maskedFrame(opText, true, []byte("b"))}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"a fragmented control frame", [][]byte{maskedFrame(opPing, false, nil)}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"an unknown opcode", [][]byte{maskedFrame(0x3, true, nil)}, 0, "", errWSProtocol, []byte{0x80 | opClose, 2, 0x03, 0xEA}},
		{"a message over the limit", [][]byte{maskedFrame(opText, true, big)[:14]}, 0, "", errWSTooBig, []byte{0x80 | opClose, 2, 0x03, 0xF1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op, data, sent, err := readFrom(t, tc.frames...)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (op != tc.wantOp || string(data) != tc.wantData) {
				t.Fatalf("got opcode %d %q, want %d %q", op, data, tc.wantOp, tc.wantData)
			}
			if !bytes.Equal(sent, tc.wantSent) {
				t.Fatalf("the server sent %x, want %x", sent, tc.wantSent)
			}
		})
	}
}

func TestReadMessageRefusesFragmentsOverTheLimit(t *testing.T) {
	half := bytes.Repeat([]byte("x"), 40_000)
	_, _, sent, err := readFrom(t,
		maskedFrame(opText, false, half), maskedFrame(opContinuation, false, half))
	if !errors.Is(err, errWSTooBig) || !bytes.Equal(sent, []byte{0x80 | opClose, 2, 0x03, 0xF1}) {
		t.Fatalf("error %v, sent %x", err, sent)
	}
}

func TestWriteFrameLengths(t *testing.T) {
	for _, n := range []int{0, 125, 126, 65_535, 65_536} {
		c, client := pipeConn(t)
		payload := bytes.Repeat([]byte("p"), n)
		go func() { _ = c.writeText(payload) }()
		op, got := readServerFrame(t, client)
		if op != opText || !bytes.Equal(got, payload) {
			t.Fatalf("length %d: opcode %d, %d bytes", n, op, len(got))
		}
	}
}

func TestHeaderHasToken(t *testing.T) {
	h := http.Header{"Connection": {"keep-alive, Upgrade"}, "Upgrade": {"WebSocket"}}
	if !headerHasToken(h, "Connection", "upgrade") || !headerHasToken(h, "Upgrade", "websocket") {
		t.Fatal("tokens are matched ignoring case, in lists")
	}
	if headerHasToken(h, "Connection", "close") || headerHasToken(h, "Missing", "x") {
		t.Fatal("matched a token that is not there")
	}
}
