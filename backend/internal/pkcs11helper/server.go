// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/pkcs11wire"
)

// Defaults for Options.
const (
	// DefaultTimeout is the per-request deadline.
	DefaultTimeout = 10 * time.Second
	// DefaultMaxConns is the number of connections served at once.
	DefaultMaxConns = 16
	// writeGrace is the time to deliver an answer, even a timeout, after
	// the request's own deadline.
	writeGrace = 2 * time.Second
	// signatureSize is the length of both algorithms' signatures.
	signatureSize = 64
	// digestSize is the length of a SHA-256 digest.
	digestSize = 32
)

// Options configures a Server.
type Options struct {
	// Timeout is the deadline for one request, from accept to the end of
	// the token's work; the answer then has a short grace to be written.
	// 0 uses DefaultTimeout.
	Timeout time.Duration
	// MaxConns bounds the connections served at once; 0 uses
	// DefaultMaxConns. Further clients wait in the listen backlog.
	MaxConns int
	// Log receives failures, never keys or PINs; nil discards.
	Log *slog.Logger
}

// Server answers pkcs11wire requests with a Token.
type Server struct {
	token    Token
	timeout  time.Duration
	maxConns int
	log      *slog.Logger
}

// NewServer returns a server over a token.
func NewServer(token Token, opts Options) *Server {
	s := &Server{token: token, timeout: opts.Timeout, maxConns: opts.MaxConns, log: opts.Log}
	if s.timeout <= 0 {
		s.timeout = DefaultTimeout
	}
	if s.maxConns <= 0 {
		s.maxConns = DefaultMaxConns
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return s
}

// Serve accepts connections on l until ctx is done, then closes l and
// waits for the requests in flight. It returns nil on a clean stop.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, s.maxConns)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		conn, err := l.Accept()
		if err != nil {
			<-slots
			if ctx.Err() != nil {
				return nil
			}
			return err //nolint:wrapcheck // the listener's own error
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			s.serveConn(ctx, conn)
		}()
	}
}

// serveConn answers the one request of a connection.
func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return
	}
	var req pkcs11pb.Request
	if err := pkcs11wire.ReadMessage(conn, &req); err != nil {
		if errors.Is(err, io.EOF) {
			return // a liveness probe
		}
		s.log.WarnContext(ctx, "pkcs11 helper: unreadable request", slog.Any("error", err))
		_ = pkcs11wire.WriteMessage(conn, failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "the request is malformed"))
		return
	}
	resp := s.Respond(ctx, &req)
	if err := conn.SetWriteDeadline(time.Now().Add(writeGrace)); err != nil {
		return
	}
	if err := pkcs11wire.WriteMessage(conn, resp); err != nil {
		s.log.WarnContext(ctx, "pkcs11 helper: the response was not delivered", slog.Any("error", err))
	}
}

// Respond answers one request. A token call that outlives ctx is
// abandoned: the answer is a timeout, and the call finishes on its own
// goroutine, since a PKCS#11 call cannot be cancelled.
func (s *Server) Respond(ctx context.Context, req *pkcs11pb.Request) *pkcs11pb.Response {
	done := make(chan *pkcs11pb.Response, 1)
	go func() { done <- s.respond(ctx, req) }()
	select {
	case resp := <-done:
		return resp
	case <-ctx.Done():
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "the token did not answer in time")
	}
}

// respond dispatches a request by its variant.
func (s *Server) respond(ctx context.Context, req *pkcs11pb.Request) *pkcs11pb.Response {
	switch r := req.GetRequest().(type) {
	case *pkcs11pb.Request_Sign:
		return s.sign(ctx, r.Sign)
	case *pkcs11pb.Request_PublicKey:
		return s.publicKey(ctx, r.PublicKey)
	case *pkcs11pb.Request_Wrap:
		return s.wrap(ctx, r.Wrap)
	case *pkcs11pb.Request_Unwrap:
		return s.unwrap(ctx, r.Unwrap)
	default:
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "the request names no operation")
	}
}

// publicKey answers a public key request.
func (s *Server) publicKey(ctx context.Context, req *pkcs11pb.PublicKeyRequest) *pkcs11pb.Response {
	pub, fail := s.lookup(ctx, req.GetKeyRef())
	if fail != nil {
		return fail
	}
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_PublicKey{PublicKey: &pkcs11pb.PublicKeyResponse{
		PublicKeyDer: pub.DER, Algorithm: pub.Algorithm, KeyId: pub.KeyID,
	}}}
}

// sign answers a sign request: it reads the key first, so that an
// algorithm that does not fit the key is refused before the token signs.
func (s *Server) sign(ctx context.Context, req *pkcs11pb.SignRequest) *pkcs11pb.Response {
	alg := req.GetAlgorithm()
	if fail := checkDigest(alg, req.GetDigest()); fail != nil {
		return fail
	}
	sel, err := ParseKeyRef(req.GetKeyRef())
	if err != nil {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, err.Error())
	}
	pub, fail := s.publicOf(ctx, sel)
	if fail != nil {
		return fail
	}
	if pub.Algorithm != alg {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM, "the algorithm does not match the key")
	}
	sig, err := s.token.Sign(sel, alg, req.GetDigest())
	if err != nil {
		return s.tokenFailure(ctx, "sign", err)
	}
	if len(sig) != signatureSize {
		s.log.ErrorContext(ctx, "pkcs11 helper: the token returned a signature of the wrong length", slog.Int("length", len(sig)))
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "the token returned a malformed signature")
	}
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Sign{Sign: &pkcs11pb.SignResponse{Signature: sig, KeyId: pub.KeyID}}}
}

// checkDigest refuses an unknown algorithm and a digest of the wrong
// size for it.
func checkDigest(alg pkcs11pb.Algorithm, digest []byte) *pkcs11pb.Response {
	switch alg {
	case pkcs11pb.Algorithm_ALGORITHM_ED25519:
		return nil // the whole message, bounded by the frame
	case pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256:
		if len(digest) != digestSize {
			return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "an ECDSA P-256 digest is 32 bytes")
		}
		return nil
	case pkcs11pb.Algorithm_ALGORITHM_UNSPECIFIED:
		fallthrough
	default:
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM, "the algorithm is not supported")
	}
}

// lookup parses a key reference and reads its public key.
func (s *Server) lookup(ctx context.Context, ref string) (Public, *pkcs11pb.Response) {
	sel, err := ParseKeyRef(ref)
	if err != nil {
		return Public{}, failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, err.Error())
	}
	return s.publicOf(ctx, sel)
}

// publicOf reads and decodes a selected key's public half.
func (s *Server) publicOf(ctx context.Context, sel Selector) (Public, *pkcs11pb.Response) {
	attrs, err := s.token.PublicKey(sel)
	if err != nil {
		return Public{}, s.tokenFailure(ctx, "read a public key", err)
	}
	pub, err := attrs.Decode()
	if err != nil {
		s.log.ErrorContext(ctx, "pkcs11 helper: unusable public key", slog.Any("error", err))
		return Public{}, failure(pkcs11pb.ErrorCode_ERROR_CODE_UNSUPPORTED_ALGORITHM, "the key is not Ed25519 or ECDSA P-256")
	}
	return pub, nil
}

// tokenFailure maps a token error to a response. Only the two selector
// errors are described to the client; anything else is logged here and
// reported as an internal failure, so token detail stays in the helper.
func (s *Server) tokenFailure(ctx context.Context, op string, err error) *pkcs11pb.Response {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_KEY_NOT_FOUND, "no such key")
	case errors.Is(err, ErrAmbiguousKey):
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "the key reference matches more than one key")
	case errors.Is(err, ErrDecryptFailed):
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_DECRYPT_FAILED, "the wrapped key does not decrypt")
	default:
		s.log.ErrorContext(ctx, "pkcs11 helper: the token failed", slog.String("operation", op), slog.Any("error", err))
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "the token failed")
	}
}

// failure is an error response.
func failure(code pkcs11pb.ErrorCode, message string) *pkcs11pb.Response {
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Error{Error: &pkcs11pb.Error{Code: code, Message: message}}}
}
