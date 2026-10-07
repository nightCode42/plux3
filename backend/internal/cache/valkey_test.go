// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// respServer is an in-process RESP2 server covering the commands the
// Valkey backend sends. It lets the client be tested end to end without
// a Valkey process; CI also runs the suite against a real one (QA-005).
type respServer struct {
	addr string
	ln   net.Listener

	mu     sync.Mutex
	values map[string]respValue
	conns  map[net.Conn]struct{}
	// seen counts the commands received, by upper-case name.
	seen map[string]int
	// auths lists the passwords received by AUTH.
	auths []string
	// password, when set, is the only password AUTH accepts.
	password string
	// sentinelMaster is the host:port that SENTINEL
	// get-master-addr-by-name returns; empty means an unknown master.
	sentinelMaster string
	// replica makes ROLE answer "slave".
	replica bool
	// readonly makes every write answer -READONLY.
	readonly bool
}

// respValue is one stored value with its expiry.
type respValue struct {
	data    []byte
	expires time.Time
}

// newRESPServer starts a server that stops with the test.
func newRESPServer(t testing.TB) *respServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &respServer{
		addr:   ln.Addr().String(),
		ln:     ln,
		values: map[string]respValue{},
		conns:  map[net.Conn]struct{}{},
		seen:   map[string]int{},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns[c] = struct{}{}
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		s.kill()
		<-done
	})
	return s
}

// kill stops listening and closes every open connection, like a node
// that has crashed.
func (s *respServer) kill() {
	_ = s.ln.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		_ = c.Close()
	}
}

// count returns how many times a command has been received.
func (s *respServer) count(command string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[command]
}

// setSentinelMaster changes the master address that this server, acting
// as a Sentinel, reports.
func (s *respServer) setSentinelMaster(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentinelMaster = addr
}

// setRole makes ROLE answer "slave" (true) or "master" (false) and, for
// a replica, makes writes answer -READONLY.
func (s *respServer) setRole(replica bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replica = replica
	s.readonly = replica
}

// setReadonly makes writes answer -READONLY while ROLE still says
// "master", the window just after a demotion.
func (s *respServer) setReadonly(readonly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readonly = readonly
}

// serve answers commands until the connection closes.
func (s *respServer) serve(c net.Conn) {
	defer func() {
		_ = c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	r := bufio.NewReader(c)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if _, err := c.Write(s.reply(args)); err != nil {
			return
		}
	}
}

// reply runs one command and returns its RESP encoding.
func (s *respServer) reply(args []string) []byte {
	if len(args) == 0 {
		return []byte("-ERR empty command\r\n")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.ToUpper(args[0])
	s.seen[name]++
	if s.readonly && (name == "SET" || name == "INCR" || name == "EXPIRE" || name == "DEL") {
		return []byte("-READONLY You can't write against a read only replica.\r\n")
	}
	switch name {
	case "AUTH":
		s.auths = append(s.auths, args[1])
		if s.password != "" && args[1] != s.password {
			return []byte("-WRONGPASS invalid username-password pair\r\n")
		}
		return []byte("+OK\r\n")
	case "SENTINEL":
		return s.sentinelReply()
	case "ROLE":
		if s.replica {
			return []byte("*5\r\n$5\r\nslave\r\n$9\r\n127.0.0.1\r\n:6379\r\n$9\r\nconnected\r\n:0\r\n")
		}
		return []byte("*3\r\n$6\r\nmaster\r\n:0\r\n*0\r\n")
	case "PING", "SELECT":
		return []byte("+OK\r\n")
	case "GET":
		v, ok := s.live(args[1])
		if !ok {
			return []byte("$-1\r\n")
		}
		return bulk(v.data)
	case "SET":
		return s.set(args)
	case "INCR":
		v, _ := s.live(args[1])
		n, _ := strconv.ParseInt(string(v.data), 10, 64)
		n++
		expires := v.expires
		if expires.IsZero() {
			expires = time.Now().Add(time.Hour)
		}
		s.values[args[1]] = respValue{data: []byte(strconv.FormatInt(n, 10)), expires: expires}
		return []byte(fmt.Sprintf(":%d\r\n", n))
	case "EXPIRE":
		v, ok := s.live(args[1])
		if !ok {
			return []byte(":0\r\n")
		}
		secs, _ := strconv.Atoi(args[2])
		v.expires = time.Now().Add(time.Duration(secs) * time.Second)
		s.values[args[1]] = v
		return []byte(":1\r\n")
	case "DEL":
		delete(s.values, args[1])
		return []byte(":1\r\n")
	default:
		return []byte("-ERR unknown command\r\n")
	}
}

// sentinelReply answers SENTINEL get-master-addr-by-name.
func (s *respServer) sentinelReply() []byte {
	if s.sentinelMaster == "" {
		return []byte("*-1\r\n")
	}
	host, port, _ := net.SplitHostPort(s.sentinelMaster)
	return []byte(fmt.Sprintf("*2\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(host), host, len(port), port))
}

// set applies SET key value [EX n] [NX].
func (s *respServer) set(args []string) []byte {
	key, value := args[1], []byte(args[2])
	ttl := time.Hour
	nx := false
	for i := 3; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "EX":
			i++
			if i < len(args) {
				secs, _ := strconv.Atoi(args[i])
				ttl = time.Duration(secs) * time.Second
			}
		case "NX":
			nx = true
		}
	}
	if nx {
		if _, ok := s.live(key); ok {
			return []byte("$-1\r\n")
		}
	}
	s.values[key] = respValue{data: value, expires: time.Now().Add(ttl)}
	return []byte("+OK\r\n")
}

// live returns an unexpired value.
func (s *respServer) live(key string) (respValue, bool) {
	v, ok := s.values[key]
	if !ok || !v.expires.After(time.Now()) {
		delete(s.values, key)
		return respValue{}, false
	}
	return v, true
}

// bulk encodes a bulk string.
func bulk(b []byte) []byte {
	return append([]byte(fmt.Sprintf("$%d\r\n", len(b))), append(b, '\r', '\n')...)
}

// readCommand reads one RESP array of bulk strings.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, errors.New("not a command")
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := range args {
		if line, err = r.ReadString('\n'); err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimRight(line, "\r\n")[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if err := readFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:size])
	}
	return args, nil
}

func TestValkeyURLs(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"redis://localhost:6379", "rediss://user:pw@localhost/1", "valkey://h:1"} {
		if _, err := NewValkey(raw); err != nil {
			t.Errorf("NewValkey(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{"http://localhost", "://", "amqp://h"} {
		if _, err := NewValkey(raw); err == nil {
			t.Errorf("NewValkey(%q) was accepted", raw)
		}
	}
}

func TestValkeyReportsAnUnreachableServer(t *testing.T) {
	t.Parallel()
	v, err := NewValkey("redis://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Ping(t.Context()); err == nil {
		t.Error("an unreachable server answered")
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Ping(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("Ping after Close = %v", err)
	}
}

func TestValkeyAuthenticatesAndSelects(t *testing.T) {
	t.Parallel()
	srv := newRESPServer(t)
	v, err := NewValkey("redis://user:secret@" + srv.addr + "/3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	if err := v.Ping(t.Context()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestValkeyReportsServerErrors(t *testing.T) {
	t.Parallel()
	srv := newRESPServer(t)
	v, err := NewValkey("redis://" + srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	if _, err := v.call(t.Context(), "NONSENSE"); err == nil {
		t.Error("an unknown command succeeded")
	}
	// The connection is kept after a server-side error, so the next call
	// still works.
	if err := v.Ping(t.Context()); err != nil {
		t.Errorf("Ping after an error reply: %v", err)
	}
}

func TestSecondsRoundsUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{{time.Second, "1"}, {1500 * time.Millisecond, "2"}, {time.Millisecond, "1"}, {90 * time.Second, "90"}} {
		if got := seconds(tc.in); got != tc.want {
			t.Errorf("seconds(%s) = %s; want %s", tc.in, got, tc.want)
		}
	}
}
