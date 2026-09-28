// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Valkey is the shared backend for installations with several replicas.
// It speaks RESP2 over a small pool of connections, which is all the
// handful of commands this cache needs (ADR-0007).
type Valkey struct {
	dial     func(context.Context) (net.Conn, error)
	password string
	database string
	timeout  time.Duration

	mu     sync.Mutex
	closed bool
	idle   []*conn
	// maxIdle bounds the pool; extra connections are closed after use.
	maxIdle int
}

// conn is one pooled connection with its buffered reader.
type conn struct {
	net.Conn
	r *bufio.Reader
}

// NewValkey connects lazily to the server named by a redis:// or
// rediss:// URL. No connection is made here, so a server starts even
// while the cache is briefly unreachable; /readyz reports that.
func NewValkey(rawURL string) (*Valkey, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("cache: the Valkey URL is not valid")
	}
	secure := false
	switch u.Scheme {
	case "redis", "valkey":
	case "rediss", "valkeys":
		secure = true
	default:
		return nil, fmt.Errorf("cache: %q is not a redis:// or rediss:// URL", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(host, "6379")
	}
	password, _ := u.User.Password()
	database := strings.TrimPrefix(u.Path, "/")
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	v := &Valkey{
		password: password,
		database: database,
		timeout:  5 * time.Second,
		// Every API call makes one or two counter calls (SRV-065), so the
		// pool must cover the server's concurrency; a small pool turned
		// each busy moment into a dial and a close per call (NFR-020).
		maxIdle: 256,
	}
	v.dial = func(ctx context.Context) (net.Conn, error) {
		if secure {
			return (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", host)
		}
		return dialer.DialContext(ctx, "tcp", host)
	}
	return v, nil
}

// get returns a pooled connection, opening one when the pool is empty.
func (v *Valkey) get(ctx context.Context) (*conn, error) {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil, ErrClosed
	}
	if n := len(v.idle); n > 0 {
		c := v.idle[n-1]
		v.idle = v.idle[:n-1]
		v.mu.Unlock()
		return c, nil
	}
	v.mu.Unlock()

	raw, err := v.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("cache: connect to Valkey: %w", err)
	}
	c := &conn{Conn: raw, r: bufio.NewReader(raw)}
	if v.password != "" {
		if _, err := v.do(ctx, c, "AUTH", v.password); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	if v.database != "" && v.database != "0" {
		if _, err := v.do(ctx, c, "SELECT", v.database); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	return c, nil
}

// put returns a connection to the pool, or closes it.
func (v *Valkey) put(c *conn, broken bool) {
	if broken {
		_ = c.Close()
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || len(v.idle) >= v.maxIdle {
		_ = c.Close()
		return
	}
	v.idle = append(v.idle, c)
}

// call runs one command on a pooled connection.
func (v *Valkey) call(ctx context.Context, args ...string) (any, error) {
	c, err := v.get(ctx)
	if err != nil {
		return nil, err
	}
	reply, err := v.do(ctx, c, args...)
	// A protocol or I/O failure leaves the connection in an unknown
	// state, so it is closed rather than reused. A server-side error
	// reply does not.
	var replyErr replyError
	v.put(c, err != nil && !errors.As(err, &replyErr))
	return reply, err
}

// do writes a command and reads its reply.
func (v *Valkey) do(ctx context.Context, c *conn, args ...string) (any, error) {
	deadline := time.Now().Add(v.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.Write([]byte(b.String())); err != nil {
		return nil, fmt.Errorf("cache: write: %w", err)
	}
	return readReply(c.r)
}

// replyError is an error the server returned.
type replyError struct{ msg string }

// Error returns the server's message.
func (e replyError) Error() string { return "cache: " + e.msg }

// readReply reads one RESP2 value.
func readReply(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("cache: read: %w", err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return nil, errors.New("cache: empty reply")
	}
	body := line[1:]
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return nil, replyError{msg: body}
	case ':':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cache: %q is not an integer reply", body)
		}
		return n, nil
	case '$':
		return readBulk(r, body)
	case '*':
		return readArray(r, body)
	default:
		return nil, fmt.Errorf("cache: unexpected reply %q", line[0])
	}
}

// readBulk reads a bulk string of the declared length; a negative
// length is the null reply.
func readBulk(r *bufio.Reader, length string) (any, error) {
	n, err := strconv.Atoi(length)
	if err != nil {
		return nil, fmt.Errorf("cache: %q is not a length", length)
	}
	if n < 0 {
		return nil, nil
	}
	buf := make([]byte, n+2) // the value and its CRLF
	if err := readFull(r, buf); err != nil {
		return nil, fmt.Errorf("cache: read: %w", err)
	}
	return buf[:n], nil
}

// readArray reads an array of the declared length.
func readArray(r *bufio.Reader, length string) (any, error) {
	n, err := strconv.Atoi(length)
	if err != nil {
		return nil, fmt.Errorf("cache: %q is not a length", length)
	}
	if n < 0 {
		return nil, nil
	}
	out := make([]any, n)
	for i := range out {
		if out[i], err = readReply(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// readFull fills b.
func readFull(r *bufio.Reader, b []byte) error {
	if _, err := io.ReadFull(r, b); err != nil {
		return fmt.Errorf("cache: read: %w", err)
	}
	return nil
}

// seconds renders a lifetime for EX, rounding up so that a sub-second
// lifetime is not dropped to zero.
func seconds(ttl time.Duration) string {
	s := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		s++
	}
	if s < 1 {
		s = 1
	}
	return strconv.FormatInt(s, 10)
}

// Get returns a value and whether it was present.
func (v *Valkey) Get(ctx context.Context, key string) ([]byte, bool, error) {
	reply, err := v.call(ctx, "GET", key)
	if err != nil {
		return nil, false, err
	}
	if reply == nil {
		return nil, false, nil
	}
	b, ok := reply.([]byte)
	if !ok {
		return nil, false, errors.New("cache: GET did not return a value")
	}
	return b, true, nil
}

// Set stores a value with a lifetime.
func (v *Valkey) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("cache: a value needs a lifetime")
	}
	_, err := v.call(ctx, "SET", key, string(value), "EX", seconds(ttl))
	return err
}

// SetNX stores a value only if the key is absent.
func (v *Valkey) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, errors.New("cache: a value needs a lifetime")
	}
	reply, err := v.call(ctx, "SET", key, string(value), "EX", seconds(ttl), "NX")
	if err != nil {
		return false, err
	}
	// A refused SET NX returns a null reply.
	return reply != nil, nil
}

// Increment adds one to a counter and sets its lifetime when it is
// created.
func (v *Valkey) Increment(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, errors.New("cache: a counter needs a lifetime")
	}
	reply, err := v.call(ctx, "INCR", key)
	if err != nil {
		return 0, err
	}
	n, ok := reply.(int64)
	if !ok {
		return 0, errors.New("cache: INCR did not return a number")
	}
	if n == 1 {
		if _, err := v.call(ctx, "EXPIRE", key, seconds(ttl)); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Delete removes a key.
func (v *Valkey) Delete(ctx context.Context, key string) error {
	_, err := v.call(ctx, "DEL", key)
	return err
}

// Ping reports whether the server answers.
func (v *Valkey) Ping(ctx context.Context) error {
	_, err := v.call(ctx, "PING")
	return err
}

// Close closes every pooled connection.
func (v *Valkey) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	for _, c := range v.idle {
		_ = c.Close()
	}
	v.idle = nil
	return nil
}
