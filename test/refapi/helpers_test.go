// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// handshakeKey is the nonce of RFC 6455's handshake example, whose accept
// key the tests know.
var handshakeKey = base64.StdEncoding.EncodeToString([]byte("the sample nonce"))

// testServer starts the API over TLS, as httptest does, and returns it with
// a client that trusts it.
func testServer(t *testing.T, o Options) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(New(o))
	t.Cleanup(srv.Close)
	return srv
}

// reply is a response read to its end.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// json decodes the body into v.
func (r reply) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("the body is not JSON: %v\n%s", err, r.body)
	}
}

// errorCode is the code of the JSON error the body holds.
func (r reply) errorCode(t *testing.T) string {
	t.Helper()
	var e errorBody
	r.json(t, &e)
	return e.Error.Code
}

// call sends a request and reads the whole response.
func call(t *testing.T, srv *httptest.Server, method, path, token, body string, headers ...string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: res.StatusCode, header: res.Header, body: data}
}

// signIn logs the demo user in and returns the token.
func signIn(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	r := call(t, srv, http.MethodPost, "/bank/v1/login", "", `{"username":"demo","password":"demo1234"}`)
	if r.status != http.StatusOK {
		t.Fatalf("login: %d %s", r.status, r.body)
	}
	var out struct{ Token string }
	r.json(t, &out)
	return out.Token
}

// testWS is a WebSocket client, enough to test the server with.
type testWS struct {
	conn net.Conn
	br   *bufio.Reader
}

// dialWS connects to path and completes the handshake; it returns the
// handshake's response too, whose status is 101 when it worked.
func dialWS(t *testing.T, srv *httptest.Server, path string) (*testWS, *handshake) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	dialer := &tls.Dialer{Config: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(t.Context(), "tcp", strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", handshakeKey)
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return &testWS{conn: conn, br: br}, &handshake{StatusCode: res.StatusCode, Header: res.Header}
}

// handshake is what a WebSocket handshake answered with.
type handshake struct {
	StatusCode int
	Header     http.Header
}

// frameLength is the payload length of a frame as an int; the test client
// does not read frames longer than 2 GiB.
func frameLength(t *testing.T, length uint64) int {
	t.Helper()
	if length > math.MaxInt32 {
		t.Fatalf("a frame of %d bytes is too long for the test client", length)
		return 0
	}
	return int(length)
}

// maskedFrame encodes a client frame.
func maskedFrame(op byte, fin bool, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		out = append(out, 0x80|byte(n))
	case n <= 0xFFFF:
		out = binary.BigEndian.AppendUint16(append(out, 0x80|126), uint16(n))
	default:
		out = binary.BigEndian.AppendUint64(append(out, 0x80|127), uint64(n))
	}
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	out = append(out, mask[:]...)
	for i, c := range payload {
		out = append(out, c^mask[i%4])
	}
	return out
}

// send writes a masked frame.
func (c *testWS) send(t *testing.T, op byte, fin bool, payload []byte) {
	t.Helper()
	if _, err := c.conn.Write(maskedFrame(op, fin, payload)); err != nil {
		t.Fatal(err)
	}
}

// recv reads one server frame.
func (c *testWS) recv(t *testing.T) (op byte, payload []byte) {
	t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return readServerFrame(t, c.br)
}

// readServerFrame reads an unmasked frame from r.
func readServerFrame(t *testing.T, r io.Reader) (op byte, payload []byte) {
	t.Helper()
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if head[1]&0x80 != 0 {
		t.Fatal("the server masked a frame")
	}
	n := int(head[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			t.Fatal(err)
		}
		n = int(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			t.Fatal(err)
		}
		n = frameLength(t, binary.BigEndian.Uint64(b[:]))
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatal(err)
	}
	return head[0] & 0x0F, payload
}

// closeCode is the status code of a close frame's payload.
func closeCode(t *testing.T, payload []byte) uint16 {
	t.Helper()
	if len(payload) < 2 {
		t.Fatalf("a close frame without a code: %x", payload)
	}
	return binary.BigEndian.Uint16(payload)
}

// bodyOf is the JSON of v as a string.
func bodyOf(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
