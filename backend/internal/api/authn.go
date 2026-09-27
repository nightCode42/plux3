// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// The names a credential travels under (SEC-101, api.md §4).
const (
	// SessionCookie holds a browser session. The __Host- prefix makes the
	// browser refuse it unless it is Secure, has Path=/ and no Domain.
	SessionCookie = "__Host-plux_session"
	// CSRFHeader carries the session's CSRF token on every call made
	// with the cookie.
	CSRFHeader = "X-CSRF-Token"
	// OrganizationHeader names the organisation a session's call acts in.
	OrganizationHeader = "X-Plux-Organization"
	// IdempotencyHeader carries the idempotency key of a mutating call.
	IdempotencyHeader = "Idempotency-Key"
)

// Authenticator resolves the two kinds of credential a caller presents.
type Authenticator interface {
	AuthenticateSession(ctx context.Context, secret string) (auth.Identity, error)
	AuthenticateToken(ctx context.Context, secret string) (auth.Identity, error)
}

// identityKey carries the caller's identity in a context.
type identityKey struct{}

// IdentityFrom returns the authenticated caller, when there is one.
func IdentityFrom(ctx context.Context) (auth.Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(auth.Identity)
	return id, ok
}

// WithIdentity returns a context carrying an identity, for tests and
// for calls the server makes on its own behalf.
func WithIdentity(ctx context.Context, id auth.Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// Authentication resolves the caller's credential and puts the identity
// in the context (SEC-101, SRV-064). Every procedure needs one except
// those listed as public, which authenticate by what they carry — a
// password, a device code, an invitation, a CI identity token.
//
// A call made with the session cookie must echo the session's CSRF token
// in the X-CSRF-Token header; with SameSite=Strict this makes a forged
// cross-site request fail twice over. A bearer token is not sent by a
// browser on its own, so it needs no CSRF token.
//
// It also records what the audit log keeps about the call — the client
// address, the user agent and the request ID — and counts the call
// against the principal's rate limit (SRV-065).
func Authentication(a Authenticator, public map[string]bool, trusted []netip.Prefix, limiter RateLimiter, perPrincipal int64) Around {
	return func(ctx context.Context, c Call, next func(context.Context) error) error {
		ctx = audit.WithRequest(ctx, audit.Request{
			SourceIP:  ClientAddress(c.PeerAddress, c.Header, trusted),
			UserAgent: truncate(c.Header.Get("User-Agent"), 512),
			RequestID: observability.RequestID(ctx),
		})
		id, found, err := credential(ctx, a, c.Header)
		if err != nil {
			return err
		}
		if !found {
			if public[c.Procedure] {
				return next(ctx)
			}
			return plxerr.New(plxerr.AuthenticationRequired, "this call needs a session or an access token")
		}
		if limiter.Count != nil {
			retry, err := limiter.Allow(ctx, "rate:principal:"+id.Kind+":"+id.ID, perPrincipal)
			if err != nil {
				if retry > 0 {
					c.ResponseHeader.Set("Retry-After", retryAfterHeader(retry))
				}
				return err
			}
		}
		return next(WithIdentity(ctx, id))
	}
}

// credential reads and checks whichever credential the request carries.
// A request carrying both is refused: which one acts would be a guess.
func credential(ctx context.Context, a Authenticator, h http.Header) (auth.Identity, bool, error) {
	bearer, hasBearer := bearerToken(h)
	session, hasSession := cookie(h, SessionCookie)
	switch {
	case hasBearer && hasSession:
		return auth.Identity{}, false, plxerr.New(plxerr.AuthenticationRequired, "send a session cookie or a bearer token, not both")
	case hasBearer:
		id, err := a.AuthenticateToken(ctx, bearer)
		return id, err == nil, err //nolint:wrapcheck // a domain error
	case hasSession:
		id, err := a.AuthenticateSession(ctx, session)
		if err != nil {
			return auth.Identity{}, false, err //nolint:wrapcheck // a domain error
		}
		got := h.Get(CSRFHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(id.CSRFToken)) != 1 {
			return auth.Identity{}, false, plxerr.New(plxerr.PermissionDenied, "the %s header does not match the session", CSRFHeader)
		}
		return id, true, nil
	default:
		return auth.Identity{}, false, nil
	}
}

// bearerToken returns the token of an "Authorization: Bearer" header.
func bearerToken(h http.Header) (string, bool) {
	v := h.Get("Authorization")
	scheme, token, ok := strings.Cut(v, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// cookie returns a named cookie's value from a request header.
func cookie(h http.Header, name string) (string, bool) {
	r := http.Request{Header: h}
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// SessionCookieHeader renders the Set-Cookie value that hands a session
// to a browser (SEC-101).
func SessionCookieHeader(secret string, expires time.Time, now time.Time) string {
	c := http.Cookie{
		Name: SessionCookie, Value: secret, Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: max(int(expires.Sub(now).Seconds()), 1),
	}
	return c.String()
}

// ClearSessionCookieHeader renders the Set-Cookie value that removes
// the session cookie.
func ClearSessionCookieHeader() string {
	c := http.Cookie{
		Name: SessionCookie, Value: "", Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	}
	return c.String()
}

// truncate shortens a header value for the audit log.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
