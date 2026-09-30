// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package netsim

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Exchange is one request through the proxy and what it cost on the
// device's wire: request and response, headers included.
type Exchange struct {
	Method, Path string
	Status       int
	// Up and Down are the bytes the device sent and received.
	Up, Down int64
	// Start is when the proxy received the request; End when it had
	// written the response.
	Start, End time.Time
}

// Proxy is the server as a device sees it through a network: a reverse
// proxy that records every exchange, behind a Forwarder.
type Proxy struct {
	forward *Forwarder
	server  *http.Server
	ln      net.Listener
	done    chan struct{}

	mu        sync.Mutex
	exchanges []Exchange // guarded by mu
}

// Proxy starts a proxy to the server at target (an http URL) through n,
// until ctx ends or Close is called. Devices connect to URL, at listen
// (host:port; port 0 for any); the server need not be up yet. With a
// device address, every request carries it as X-Forwarded-For, so a
// server that trusts the proxy counts the devices' requests apart from
// other local clients.
func (n *Network) Proxy(ctx context.Context, listen, target, device string) (*Proxy, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("netsim.Proxy: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("netsim.Proxy: %w", err)
	}
	p := &Proxy{ln: ln, done: make(chan struct{})}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Host = r.In.Host
			if device != "" {
				r.Out.Header.Set("X-Forwarded-For", device)
			}
		},
	}
	p.server = &http.Server{
		Handler:           p.record(rp),
		ReadHeaderTimeout: time.Minute,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}
	go func() {
		defer close(p.done)
		_ = p.server.Serve(countingListener{ln})
	}()
	f, err := n.Forward(ctx, listen, ln.Addr().String())
	if err != nil {
		_ = p.server.Close()
		<-p.done
		return nil, err
	}
	p.forward = f
	return p, nil
}

// URL is the base URL devices use.
func (p *Proxy) URL() string { return "http://" + p.forward.Addr() }

// Exchanges returns the exchanges recorded since the last Reset, in the
// order their responses were written.
func (p *Proxy) Exchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Exchange(nil), p.exchanges...)
}

// Reset forgets the recorded exchanges.
func (p *Proxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exchanges = nil
}

// Close stops the proxy and its forwarder.
func (p *Proxy) Close() error {
	err := errors.Join(p.server.Close(), p.forward.Close())
	<-p.done
	return err
}

type connKey struct{}

// record wraps h: once h has answered, the exchange is recorded with the
// bytes its connection carried since the previous exchange on it. An
// HTTP/1.1 client does not pipeline, so those are this exchange's.
func (p *Proxy) record(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		c, _ := r.Context().Value(connKey{}).(*countingConn)
		ex := Exchange{Method: r.Method, Path: r.URL.Path, Status: sw.status, Start: start, End: time.Now()}
		if c != nil {
			ex.Up, ex.Down = c.take()
		}
		p.mu.Lock()
		p.exchanges = append(p.exchanges, ex)
		p.mu.Unlock()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// countingListener counts the bytes of every connection it accepts.
type countingListener struct{ net.Listener }

func (l countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err //nolint:wrapcheck // http.Server inspects Accept's errors.
	}
	return &countingConn{Conn: c}, nil
}

// countingConn counts what is read from and written to it.
type countingConn struct {
	net.Conn
	read, written atomic.Int64
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.read.Add(int64(n))
	return n, err //nolint:wrapcheck // A net.Conn's errors reach http.Server as they are.
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.written.Add(int64(n))
	return n, err //nolint:wrapcheck // A net.Conn's errors reach http.Server as they are.
}

// take returns the bytes read and written since the last take.
func (c *countingConn) take() (read, written int64) {
	return c.read.Swap(0), c.written.Swap(0)
}
