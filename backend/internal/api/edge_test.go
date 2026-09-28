// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

func pages(t *testing.T, now func() time.Time) *api.Pages {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	p, err := api.NewPages(key, 50, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Verifies: SRV-004.
// A page token continues exactly where the last page ended, and is
// refused when tampered with, forged, stale, or presented for another
// procedure, scope, filter or ordering.
func TestPageTokensAreOpaqueAndBound(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	p := pages(t, func() time.Time { return now })
	page := &pluxv1.Page{PageSize: 2, Filter: `key = "a"`}
	last := storage.Cursor{Key: "k", Time: now.Add(-time.Hour), Sequence: 9, ID: "id"}
	token := p.Next("/proc", "org", page, 2, 2, last)
	if token == "" {
		t.Fatal("a full page has no continuation")
	}
	if p.Next("/proc", "org", page, 1, 2, last) != "" {
		t.Error("a short page has a continuation")
	}
	page.PageToken = token
	got, size, err := p.Request("/proc", "org", page)
	if err != nil || size != 2 || got.Key != "k" || got.ID != "id" || got.Sequence != 9 || !got.Time.Equal(last.Time) {
		t.Fatalf("Request = %+v, %d, %v", got, size, err)
	}
	refused := func(name, procedure, scope string, pg *pluxv1.Page) {
		t.Helper()
		if _, _, err := p.Request(procedure, scope, pg); err == nil {
			t.Errorf("%s: the token was accepted", name)
		}
	}
	refused("another procedure", "/other", "org", page)
	refused("another scope", "/proc", "other-org", page)
	refused("another filter", "/proc", "org", &pluxv1.Page{PageToken: token, Filter: `key = "b"`})
	refused("another ordering", "/proc", "org", &pluxv1.Page{PageToken: token, Filter: `key = "a"`, OrderBy: "key desc"})
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	raw[3] ^= 1
	refused("tampered", "/proc", "org", &pluxv1.Page{PageToken: base64.RawURLEncoding.EncodeToString(raw), Filter: page.Filter})
	refused("not base64", "/proc", "org", &pluxv1.Page{PageToken: "!!", Filter: page.Filter})
	refused("short", "/proc", "org", &pluxv1.Page{PageToken: "AAAA", Filter: page.Filter})
	forger := pages(t, func() time.Time { return now })
	refused("forged", "/proc", "org", &pluxv1.Page{PageToken: forger.Next("/proc", "org", page, 2, 2, last), Filter: page.Filter})

	// Sizes: unset and oversized become the maximum; negative is refused.
	for _, tc := range []struct{ in, want int32 }{{0, 50}, {10, 10}, {5000, 50}} {
		if _, size, err := p.Request("/proc", "org", &pluxv1.Page{PageSize: tc.in}); err != nil || size != tc.want {
			t.Errorf("page_size %d = %d, %v; want %d", tc.in, size, err, tc.want)
		}
	}
	if _, _, err := p.Request("/proc", "org", &pluxv1.Page{PageSize: -1}); err == nil {
		t.Error("a negative page size was accepted")
	}
	if _, err := api.NewPages([]byte("short"), 1, nil); err == nil {
		t.Error("a short key was accepted")
	}
	if _, err := api.NewPages(make([]byte, 32), 0, nil); err == nil {
		t.Error("a zero page size was accepted")
	}
}

// Verifies: SRV-004.
// A token expires a day after it was issued.
func TestPageTokensExpire(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	p := pages(t, func() time.Time { return now })
	page := &pluxv1.Page{}
	page.PageToken = p.Next("/proc", "org", page, 1, 1, storage.Cursor{Key: "k"})
	now = now.Add(25 * time.Hour)
	if _, _, err := p.Request("/proc", "org", page); err == nil {
		t.Error("a day-old token was accepted")
	}
}

// Verifies: SRV-004.
// Filters are conjunctions of equalities on declared fields; orderings
// are the list's own; read masks keep only the named fields.
func TestFiltersOrderingsAndMasks(t *testing.T) {
	t.Parallel()
	query := api.Query{OrderBy: "key", Filters: map[string]func(api.Item) string{
		"key":  func(m api.Item) string { return m.(*pluxv1.App).GetKey() },
		"name": func(m api.Item) string { return m.(*pluxv1.App).GetName() },
	}}
	clauses, err := query.Parse(&pluxv1.Page{Filter: `key = "shop" AND name = "Shop"`, OrderBy: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if !query.Keep(clauses, &pluxv1.App{Key: "shop", Name: "Shop"}) || query.Keep(clauses, &pluxv1.App{Key: "shop", Name: "Other"}) {
		t.Error("the filter kept the wrong items")
	}
	for _, bad := range []*pluxv1.Page{
		{OrderBy: "name"},
		{Filter: `colour = "red"`},
		{Filter: `key shop`},
		{Filter: `key = shop`},
	} {
		if _, err := query.Parse(bad); err == nil {
			t.Errorf("Parse(%+v) was accepted", bad)
		}
	}
	items := []*pluxv1.App{{Id: "1", Key: "a", Name: "A"}, {Id: "2", Key: "b", Name: "B"}}
	if err := api.Mask(&fieldmaskpb.FieldMask{Paths: []string{"id", "key"}}, items); err != nil {
		t.Fatal(err)
	}
	if items[0].GetName() != "" || items[1].GetKey() != "b" {
		t.Errorf("masked items = %+v", items)
	}
	if err := api.Mask(&fieldmaskpb.FieldMask{Paths: []string{"colour"}}, items); err == nil {
		t.Error("a mask naming an unknown field was accepted")
	}
	if err := api.Mask(nil, items); err != nil {
		t.Error(err)
	}
}

// fakeAuthenticator accepts one session and one token.
type fakeAuthenticator struct{}

func (fakeAuthenticator) AuthenticateSession(_ context.Context, secret string) (auth.Identity, error) {
	if secret != "plux_ses_good" { //nolint:gosec // a test value
		return auth.Identity{}, plxerr.New(plxerr.AuthenticationRequired, "no")
	}
	return auth.Identity{Kind: auth.KindUser, ID: "u", UserID: "u", CSRFToken: "plux_csrf_good"}, nil //nolint:gosec // a test value
}

func (fakeAuthenticator) AuthenticateToken(_ context.Context, secret string) (auth.Identity, error) {
	if secret != "plux_pat_good" { //nolint:gosec // a test value
		return auth.Identity{}, plxerr.New(plxerr.AuthenticationRequired, "no")
	}
	return auth.Identity{Kind: auth.KindToken, ID: "t", OrganizationID: "o", Scopes: []auth.Permission{}}, nil
}

// Verifies: SEC-101, SRV-064, SRV-065.
// A call carries a session cookie with its CSRF token, or a bearer
// token, never both; public procedures need neither; every
// authenticated call counts against its principal.
func TestAuthenticationAtTheEdge(t *testing.T) {
	t.Parallel()
	var seen auth.Identity
	var request audit.Request
	counts := map[string]int64{}
	limiter := api.RateLimiter{Window: time.Minute, Count: func(_ context.Context, key string, _ time.Duration) (int64, error) {
		counts[key]++
		return counts[key], nil
	}}
	around := api.Authentication(fakeAuthenticator{}, map[string]bool{"/public": true}, nil, limiter, 2)
	run := func(procedure string, header http.Header) error {
		return around(context.Background(), api.Call{Procedure: procedure, Header: header, PeerAddress: "198.51.100.1:5000", ResponseHeader: http.Header{}},
			func(ctx context.Context) error {
				seen, _ = api.IdentityFrom(ctx)
				request = audit.RequestFrom(ctx)
				return nil
			})
	}
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			out.Add(kv[i], kv[i+1])
		}
		return out
	}
	if err := run("/public", h("User-Agent", "cli")); err != nil || seen.Kind != "" || request.SourceIP != "198.51.100.1" || request.UserAgent != "cli" {
		t.Errorf("a public call: %v, %+v, %+v", err, seen, request)
	}
	if err := run("/private", h()); plxCode(err) != plxerr.AuthenticationRequired {
		t.Errorf("no credential: %v", err)
	}
	if err := run("/private", h("Authorization", "Bearer plux_pat_good")); err != nil || seen.Kind != auth.KindToken {
		t.Errorf("a bearer token: %v, %+v", err, seen)
	}
	if err := run("/private", h("Authorization", "Bearer plux_pat_bad")); plxCode(err) != plxerr.AuthenticationRequired {
		t.Errorf("a bad bearer token: %v", err)
	}
	cookie := api.SessionCookie + "=plux_ses_good"
	if err := run("/private", h("Cookie", cookie)); plxCode(err) != plxerr.PermissionDenied {
		t.Errorf("a session without its CSRF token: %v", err)
	}
	if err := run("/private", h("Cookie", cookie, api.CSRFHeader, "plux_csrf_bad")); plxCode(err) != plxerr.PermissionDenied {
		t.Errorf("a session with the wrong CSRF token: %v", err)
	}
	if err := run("/private", h("Cookie", cookie, api.CSRFHeader, "plux_csrf_good")); err != nil || seen.Kind != auth.KindUser {
		t.Errorf("a session with its CSRF token: %v, %+v", err, seen)
	}
	if err := run("/private", h("Cookie", cookie, api.CSRFHeader, "plux_csrf_good", "Authorization", "Bearer plux_pat_good")); plxCode(err) != plxerr.AuthenticationRequired {
		t.Errorf("both credentials: %v", err)
	}
	// The allowance is two calls a minute: the second is served, the
	// third refused.
	if err := run("/private", h("Cookie", cookie, api.CSRFHeader, "plux_csrf_good")); err != nil {
		t.Errorf("the session's second call: %v", err)
	}
	if err := run("/private", h("Cookie", cookie, api.CSRFHeader, "plux_csrf_good")); plxCode(err) != plxerr.RateLimited {
		t.Errorf("over the principal's allowance: %v", err)
	}
	set := api.SessionCookieHeader("plux_ses_x", time.Now().Add(time.Hour), time.Now())
	for _, want := range []string{"__Host-plux_session=plux_ses_x", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	if clear := api.ClearSessionCookieHeader(); !strings.Contains(clear, "Max-Age=0") {
		t.Errorf("clearing cookie = %q", clear)
	}
}

// plxCode returns an error's Plux code.
func plxCode(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
