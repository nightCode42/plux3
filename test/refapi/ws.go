// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // G505: RFC 6455 defines the handshake accept key with SHA-1.
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A minimal RFC 6455 server: the handshake, text and binary messages,
// fragmentation, ping/pong and close. It serves the tracking stream and is
// no more than that: no extensions, no subprotocols.

const (
	wsGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWSMessage = 64 << 10

	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	closeNormal   = 1000
	closeProtocol = 1002
	closeTooBig   = 1009
)

var (
	// errWSClosed is returned by readMessage once the peer closed the
	// connection with a close frame.
	errWSClosed = errors.New("websocket closed by the peer")
	// errWSProtocol is returned for a frame the protocol forbids.
	errWSProtocol = errors.New("websocket protocol error")
	// errWSTooBig is returned for a message over maxWSMessage.
	errWSTooBig = errors.New("websocket message too big")
)

// wsConn is an upgraded connection.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

// upgrade completes the WebSocket handshake on w. Browsers' Origin checks
// do not apply: the clients are apps. A request that is no valid upgrade is
// answered with a 400 before the connection is taken over.
func upgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	raw, decodeErr := base64.StdEncoding.DecodeString(key)
	switch {
	case r.Method != http.MethodGet,
		!headerHasToken(r.Header, "Connection", "upgrade"),
		!headerHasToken(r.Header, "Upgrade", "websocket"),
		decodeErr != nil || len(raw) != 16:
		writeError(w, http.StatusBadRequest, "bad_handshake", "a WebSocket upgrade request is required")
		return nil, errors.New("not a WebSocket upgrade request")
	case r.Header.Get("Sec-WebSocket-Version") != "13":
		w.Header().Set("Sec-WebSocket-Version", "13")
		writeError(w, http.StatusBadRequest, "bad_version", "only WebSocket version 13 is supported")
		return nil, errors.New("unsupported WebSocket version")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no_hijack", "the connection cannot be upgraded")
		return nil, errors.New("the response writer cannot hijack")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijacking the connection: %w", err)
	}
	sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // G401: see the import.
	_, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(sum[:]))
	if err == nil {
		err = rw.Flush()
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("writing the handshake: %w", err)
	}
	return &wsConn{conn: conn, br: rw.Reader}, nil
}

// headerHasToken reports whether the comma-separated header name holds
// token, ignoring case.
func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// frame is one frame read from the peer.
type frame struct {
	fin     bool
	op      byte
	payload []byte
}

// readFrame reads one masked client frame.
func (c *wsConn) readFrame() (frame, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return frame{}, fmt.Errorf("reading a frame header: %w", err)
	}
	f := frame{fin: head[0]&0x80 != 0, op: head[0] & 0x0F}
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)
	switch {
	case head[0]&0x70 != 0, !masked:
		return frame{}, errWSProtocol
	case f.op >= opClose && (!f.fin || length > 125):
		return frame{}, errWSProtocol
	case length == 126:
		var b [2]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return frame{}, fmt.Errorf("reading a frame length: %w", err)
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case length == 127:
		var b [8]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return frame{}, fmt.Errorf("reading a frame length: %w", err)
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > maxWSMessage {
		return frame{}, errWSTooBig
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.br, mask[:]); err != nil {
		return frame{}, fmt.Errorf("reading a frame mask: %w", err)
	}
	f.payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, f.payload); err != nil {
		return frame{}, fmt.Errorf("reading a frame payload: %w", err)
	}
	for i := range f.payload {
		f.payload[i] ^= mask[i%4]
	}
	return f, nil
}

// readMessage returns the next text or binary message, answering pings and
// the peer's close along the way. A protocol violation or an oversized
// message closes the connection with the matching code.
func (c *wsConn) readMessage() (op byte, data []byte, err error) {
	var buf []byte
	var first byte
	for {
		f, err := c.readFrame()
		switch {
		case errors.Is(err, errWSProtocol):
			_ = c.close(closeProtocol)
			return 0, nil, err
		case errors.Is(err, errWSTooBig):
			_ = c.close(closeTooBig)
			return 0, nil, err
		case err != nil:
			return 0, nil, err
		}
		switch f.op {
		case opPing:
			if err := c.write(opPong, f.payload); err != nil {
				return 0, nil, err
			}
		case opPong:
		case opClose:
			code := uint16(closeNormal)
			if len(f.payload) >= 2 {
				code = binary.BigEndian.Uint16(f.payload)
			}
			_ = c.close(code)
			return 0, nil, errWSClosed
		case opText, opBinary:
			if first != 0 {
				_ = c.close(closeProtocol)
				return 0, nil, errWSProtocol
			}
			first, buf = f.op, append(buf[:0], f.payload...)
			if f.fin {
				return first, buf, nil
			}
		case opContinuation:
			if first == 0 {
				_ = c.close(closeProtocol)
				return 0, nil, errWSProtocol
			}
			if len(buf)+len(f.payload) > maxWSMessage {
				_ = c.close(closeTooBig)
				return 0, nil, errWSTooBig
			}
			buf = append(buf, f.payload...)
			if f.fin {
				return first, buf, nil
			}
		default:
			_ = c.close(closeProtocol)
			return 0, nil, errWSProtocol
		}
	}
}

// writeText sends a text message.
func (c *wsConn) writeText(b []byte) error { return c.write(opText, b) }

// write sends one unmasked, unfragmented frame.
func (c *wsConn) write(op byte, payload []byte) error {
	head := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		head = append(head, byte(n))
	case n <= 0xFFFF:
		head = binary.BigEndian.AppendUint16(append(head, 126), uint16(n))
	default:
		head = binary.BigEndian.AppendUint64(append(head, 127), uint64(n))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("setting the write deadline: %w", err)
	}
	if _, err := c.conn.Write(append(head, payload...)); err != nil {
		return fmt.Errorf("writing a frame: %w", err)
	}
	return nil
}

// close sends a close frame with code; the connection stays open so the
// peer's close can still be read.
func (c *wsConn) close(code uint16) error {
	return c.write(opClose, binary.BigEndian.AppendUint16(nil, code))
}

// shut closes the connection without a closing handshake.
func (c *wsConn) shut() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("closing the connection: %w", err)
	}
	return nil
}
