// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultSentinelPort is the port a Sentinel listens on unless the URL
// names another.
const defaultSentinelPort = "26379"

// masterAttempts bounds how often one connection attempt re-resolves the
// master after a node that Sentinel named turned out not to be one. Each
// attempt asks every Sentinel at most once.
const masterAttempts = 2

// errNotMaster reports that the node Sentinel named did not confirm the
// master role, which happens briefly while a failover is in progress.
var errNotMaster = errors.New("cache: the Valkey node named by Sentinel is not a master")

// sentinelState is the Sentinel-mode part of a Valkey client (SEC-023,
// ADR-0012).
//
// Sentinel supplies only the address of the current master. Replicas are
// never read from: the rate-limit counters and the replay cache need the
// master's view, and a read from a lagging replica would let a replayed
// DPoP proof through. "Replica support" is therefore met by following
// the replica that Sentinel promotes when the master fails.
type sentinelState struct {
	name     string
	password string
	secure   bool
	dialer   *net.Dialer

	// resolveMu serialises resolution, so that a burst of callers during
	// a failover asks the Sentinels once and the rest wait for the answer.
	// It also guards addrs.
	resolveMu sync.Mutex
	// addrs are the Sentinel addresses; the one that answered last is
	// first.
	addrs []string

	// master and gen are guarded by Valkey.mu. master is empty while the
	// address is stale. gen changes whenever the master is invalidated, so
	// that a connection from before a failover is recognised and dropped.
	master string
	gen    uint64
}

// failoverError marks an error that means the master may have changed:
// a connection failure, a -READONLY or -LOADING reply. The pool has been
// dropped and the master address marked stale when it is returned.
type failoverError struct{ err error }

// Error returns the underlying message.
func (e *failoverError) Error() string { return e.err.Error() }

// Unwrap returns the underlying error.
func (e *failoverError) Unwrap() error { return e.err }

// isSentinelScheme reports whether a URL scheme selects Sentinel mode.
func isSentinelScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "redis+sentinel", "valkey+sentinel", "rediss+sentinel", "valkeys+sentinel":
		return true
	}
	return false
}

// newSentinelValkey builds a client from the part of a Sentinel URL after
// the scheme separator:
//
//	valkey+sentinel://[:password@]host1[:port],host2[:port]/<master>[/<db>][?sentinelPassword=...]
//
// The userinfo password authenticates to the master; sentinelPassword
// authenticates to the Sentinels. The rediss+sentinel and valkeys+sentinel
// schemes use TLS to the Sentinels and to the master. The URL is parsed by
// hand because url.Parse rejects a host list whose last entry has no port.
// Errors never echo the URL, which can hold passwords.
func newSentinelValkey(scheme, rest string) (*Valkey, error) {
	rest, rawQuery, _ := strings.Cut(rest, "?")
	authority, path, _ := strings.Cut(rest, "/")
	userinfo, hosts := "", authority
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		userinfo, hosts = authority[:i], authority[i+1:]
	}
	password, err := sentinelUserPassword(userinfo)
	if err != nil {
		return nil, err
	}
	addrs, err := sentinelAddrs(hosts)
	if err != nil {
		return nil, err
	}
	name, database, err := sentinelPath(path)
	if err != nil {
		return nil, err
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, errors.New("cache: the Valkey Sentinel URL query is not valid")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &Valkey{
		password: password,
		database: database,
		timeout:  5 * time.Second,
		maxIdle:  256,
		sentinel: &sentinelState{
			name:     name,
			password: query.Get("sentinelPassword"),
			secure:   strings.HasPrefix(scheme, "rediss+") || strings.HasPrefix(scheme, "valkeys+"),
			dialer:   dialer,
			addrs:    addrs,
		},
	}, nil
}

// sentinelUserPassword extracts the password from a userinfo section.
func sentinelUserPassword(userinfo string) (string, error) {
	if userinfo == "" {
		return "", nil
	}
	u, err := url.Parse("redis://" + userinfo + "@host")
	if err != nil {
		return "", errors.New("cache: the Valkey Sentinel URL credentials are not valid")
	}
	password, _ := u.User.Password()
	return password, nil
}

// sentinelAddrs splits a comma-separated host list into host:port
// addresses, applying the default Sentinel port.
func sentinelAddrs(hosts string) ([]string, error) {
	if hosts == "" {
		return nil, errors.New("cache: the Valkey Sentinel URL names no Sentinel")
	}
	parts := strings.Split(hosts, ",")
	addrs := make([]string, 0, len(parts))
	for i, h := range parts {
		addr, err := sentinelAddr(h)
		if err != nil {
			return nil, fmt.Errorf("cache: Sentinel address %d of the Valkey URL is not valid", i+1)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// sentinelAddr normalises one Sentinel address.
func sentinelAddr(h string) (string, error) {
	if host, port, err := net.SplitHostPort(h); err == nil {
		if n, perr := strconv.ParseUint(port, 10, 16); host == "" || perr != nil || n == 0 {
			return "", errors.New("invalid address")
		}
		return h, nil
	}
	bracketed := strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]")
	host := h
	if bracketed {
		host = h[1 : len(h)-1]
	}
	if host == "" || strings.ContainsAny(host, "[]") || (!bracketed && strings.Contains(host, ":")) {
		return "", errors.New("invalid address")
	}
	return net.JoinHostPort(host, defaultSentinelPort), nil
}

// sentinelPath splits /<master>[/<db>] into the master name and the
// database number.
func sentinelPath(path string) (name, database string, err error) {
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		return "", "", errors.New("cache: the Valkey Sentinel URL path must be /<master>[/<db>]")
	}
	name, err = url.PathUnescape(parts[0])
	if err != nil || name == "" {
		return "", "", errors.New("cache: the Valkey Sentinel URL names no master")
	}
	if len(parts) == 2 && parts[1] != "" {
		if n, perr := strconv.Atoi(parts[1]); perr != nil || n < 0 {
			return "", "", errors.New("cache: the Valkey Sentinel URL database is not a number")
		}
		database = parts[1]
	}
	return name, database, nil
}

// idempotent reports whether a command may be sent again after a
// failover without changing its outcome. SET, SET NX and INCR may already
// have taken effect on the old master, so their callers decide.
func idempotent(command string) bool {
	switch command {
	case "GET", "DEL", "PING":
		return true
	}
	return false
}

// openMaster opens a connection to the current master, resolving it
// first when the address is unknown or stale.
func (v *Valkey) openMaster(ctx context.Context) (*conn, error) {
	var err error
	for range masterAttempts {
		var c *conn
		if c, err = v.dialMaster(ctx); err == nil {
			return c, nil
		}
		if !errors.Is(err, errNotMaster) {
			break
		}
	}
	return nil, err
}

// dialMaster makes one attempt: resolve, connect, authenticate and
// confirm the master role.
func (v *Valkey) dialMaster(ctx context.Context) (*conn, error) {
	s := v.sentinel
	addr, gen, err := v.masterAddress(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := s.dialTo(ctx, addr, v.timeout)
	if err != nil {
		return nil, v.failover(ctx, gen, fmt.Errorf("cache: connect to Valkey master: %w", err))
	}
	c, err := v.open(ctx, raw)
	if err != nil {
		return nil, v.failover(ctx, gen, err)
	}
	c.gen = gen
	if err := v.confirmMaster(ctx, c); err != nil {
		_ = c.Close()
		if errors.Is(err, errNotMaster) {
			v.invalidate(gen)
			return nil, err
		}
		return nil, v.failover(ctx, gen, err)
	}
	return c, nil
}

// confirmMaster checks that ROLE names the node a master. A Sentinel can
// briefly point at a node that has just been demoted.
func (v *Valkey) confirmMaster(ctx context.Context, c *conn) error {
	reply, err := v.do(ctx, c, "ROLE")
	if err != nil {
		return err
	}
	if parts, ok := reply.([]any); ok && len(parts) > 0 {
		switch role := parts[0].(type) {
		case []byte:
			if string(role) == "master" {
				return nil
			}
		case string:
			if role == "master" {
				return nil
			}
		}
	}
	return errNotMaster
}

// masterAddress returns the cached master address and its generation,
// asking the Sentinels when it is stale. Only one resolution runs at a
// time; callers that arrive meanwhile wait on resolveMu and then find
// the fresh address.
func (v *Valkey) masterAddress(ctx context.Context) (string, uint64, error) {
	s := v.sentinel
	s.resolveMu.Lock()
	defer s.resolveMu.Unlock()
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return "", 0, ErrClosed
	}
	if s.master != "" {
		addr, gen := s.master, s.gen
		v.mu.Unlock()
		return addr, gen, nil
	}
	v.mu.Unlock()

	addr, idx, err := v.querySentinels(ctx)
	if err != nil {
		return "", 0, err
	}
	// The Sentinel that answered is asked first next time.
	if idx > 0 {
		first := s.addrs[idx]
		s.addrs = slices.Insert(slices.Delete(slices.Clone(s.addrs), idx, idx+1), 0, first)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return "", 0, ErrClosed
	}
	s.master = addr
	return addr, s.gen, nil
}

// querySentinels asks the Sentinels in order and returns the first
// master address with the index of the Sentinel that gave it. The caller
// holds resolveMu.
func (v *Valkey) querySentinels(ctx context.Context) (string, int, error) {
	s := v.sentinel
	last := errors.New("no answer")
	for i, addr := range s.addrs {
		if err := ctx.Err(); err != nil {
			return "", 0, fmt.Errorf("cache: resolve the Valkey master: %w", err)
		}
		master, err := v.askSentinel(ctx, addr)
		if err != nil {
			last = err
			continue
		}
		if master == "" {
			last = errors.New("the master is unknown to it")
			continue
		}
		return master, i, nil
	}
	return "", 0, fmt.Errorf("cache: none of the %d Valkey Sentinels named the master %q: %w", len(s.addrs), s.name, last)
}

// askSentinel sends SENTINEL get-master-addr-by-name to one Sentinel. It
// returns an empty address when the Sentinel does not know the master.
// Every step is bounded by the client timeout.
func (v *Valkey) askSentinel(ctx context.Context, addr string) (string, error) {
	s := v.sentinel
	raw, err := s.dialTo(ctx, addr, v.timeout)
	if err != nil {
		return "", fmt.Errorf("cache: connect to Valkey Sentinel: %w", err)
	}
	c := &conn{Conn: raw, r: bufio.NewReader(raw)}
	defer func() { _ = c.Close() }()
	if s.password != "" {
		if _, err := v.do(ctx, c, "AUTH", s.password); err != nil {
			return "", err
		}
	}
	reply, err := v.do(ctx, c, "SENTINEL", "get-master-addr-by-name", s.name)
	if err != nil {
		return "", err
	}
	return masterFromReply(reply)
}

// masterFromReply turns a get-master-addr-by-name reply into host:port.
func masterFromReply(reply any) (string, error) {
	if reply == nil {
		return "", nil
	}
	parts, ok := reply.([]any)
	if !ok || len(parts) != 2 {
		return "", errors.New("cache: a Sentinel gave an unexpected master address")
	}
	host, hok := parts[0].([]byte)
	port, pok := parts[1].([]byte)
	if !hok || !pok || len(host) == 0 || len(port) == 0 {
		return "", errors.New("cache: a Sentinel gave an unexpected master address")
	}
	return net.JoinHostPort(string(host), string(port)), nil
}

// dialTo connects to addr, bounded by timeout.
func (s *sentinelState) dialTo(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return dialTCP(ctx, s.secure, s.dialer, addr)
}

// failover classifies an error from a connection to the master. When it
// means the master may have changed, it drops the pool, marks the cached
// address stale and returns the error as a *failoverError. Any other
// error, including one caused by the caller's context, is returned as it
// is. It does nothing for a plain server.
func (v *Valkey) failover(ctx context.Context, gen uint64, err error) error {
	if v.sentinel == nil || ctx.Err() != nil || !masterChanged(err) {
		return err
	}
	v.invalidate(gen)
	return &failoverError{err: err}
}

// masterChanged reports whether an error suggests a failover: a
// -READONLY or -LOADING reply, or any failure that is not a server-side
// error reply.
func masterChanged(err error) bool {
	var re replyError
	if errors.As(err, &re) {
		return strings.HasPrefix(re.msg, "READONLY") || strings.HasPrefix(re.msg, "LOADING")
	}
	return true
}

// invalidate drops the idle pool and marks the master address stale, but
// only if gen is still the current generation. A caller that failed on a
// connection from before an earlier invalidation must not discard the
// address another caller has resolved since.
func (v *Valkey) invalidate(gen uint64) {
	v.mu.Lock()
	s := v.sentinel
	if s.gen != gen {
		v.mu.Unlock()
		return
	}
	s.gen++
	s.master = ""
	idle := v.idle
	v.idle = nil
	v.mu.Unlock()
	for _, c := range idle {
		_ = c.Close()
	}
}
