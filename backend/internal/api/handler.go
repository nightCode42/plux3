// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage/idempotency"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Handlers are the ConnectRPC services of this phase. They authenticate
// (through the interceptor), resolve the principal, translate between
// the wire and the domain, and delegate; nothing here decides what is
// allowed or stores anything (L-1).
type Handlers struct {
	Auth        *auth.Service
	Tenancy     *tenancy.Service
	Documents   *document.Service
	Releases    *release.Service
	Idempotency *idempotency.Store
	Pages       *Pages
	// Limiter counts calls per principal and organisation when an
	// organisation tightens api.requestsPerMinute (SRV-065, LIM-002).
	Limiter RateLimiter
	// Limits are the installation's limits.
	Limits limits.Set
	// Now is the clock; nil uses time.Now.
	Now func() time.Time

	// orgLimits caches each organisation's effective principal allowance
	// for a minute, so the tightening costs one query a minute rather
	// than one a call.
	mu        sync.Mutex
	orgLimits map[string]orgLimit
}

// orgLimit is one cached allowance.
type orgLimit struct {
	perMinute int64
	until     time.Time
}

// now returns the current time.
func (h *Handlers) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// identity returns the authenticated caller.
func identity(ctx context.Context) (auth.Identity, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return auth.Identity{}, plxerr.New(plxerr.AuthenticationRequired, "this call needs a session or an access token")
	}
	return id, nil
}

// principal resolves the caller in the organisation the call acts in:
// the one its token is bound to, or the one named by the request or the
// X-Plux-Organization header, which must agree when both are given.
func (h *Handlers) principal(ctx context.Context, header http.Header, requestOrg string) (auth.Principal, error) {
	id, err := identity(ctx)
	if err != nil {
		return auth.Principal{}, err
	}
	org := requestOrg
	if named := header.Get(OrganizationHeader); named != "" {
		if org != "" && org != named {
			return auth.Principal{}, plxerr.New(plxerr.InvalidStructure,
				"the request and the %s header name different organisations", OrganizationHeader)
		}
		org = named
	}
	p, err := h.Auth.Resolve(ctx, id, org)
	if err != nil {
		return auth.Principal{}, err //nolint:wrapcheck // a domain error
	}
	if err := h.organisationRate(ctx, p); err != nil {
		return auth.Principal{}, err
	}
	return p, nil
}

// organisationRate applies an organisation's tighter allowance of calls
// per principal, when it has one (SRV-065, LIM-002). The installation's
// allowance is applied by the authentication interceptor to every call.
func (h *Handlers) organisationRate(ctx context.Context, p auth.Principal) error {
	if h.Limiter.Count == nil || h.Tenancy == nil {
		return nil
	}
	h.mu.Lock()
	cached, ok := h.orgLimits[p.OrganizationID]
	h.mu.Unlock()
	if !ok || h.now().After(cached.until) {
		set, err := h.Tenancy.OrganizationLimits(ctx, p.OrganizationID)
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		cached = orgLimit{perMinute: set.Get(limits.APIRequestsPerMinute), until: h.now().Add(time.Minute)}
		h.mu.Lock()
		if h.orgLimits == nil {
			h.orgLimits = map[string]orgLimit{}
		}
		h.orgLimits[p.OrganizationID] = cached
		h.mu.Unlock()
	}
	if cached.perMinute >= h.Limits.Get(limits.APIRequestsPerMinute) {
		return nil
	}
	_, err := h.Limiter.Allow(ctx, "rate:principal:"+p.OrganizationID+":"+p.Kind+":"+p.ID, cached.perMinute)
	return err
}

// Replayed is set on a response that repeats an earlier call with the
// same idempotency key (SRV-005).
const Replayed = "Idempotent-Replayed"

// mutate runs a state-changing call, honouring its idempotency key
// (SRV-005): the first call with a key acts and its result is kept for
// 24 hours; a repeat returns the kept result without acting. A call made
// without a credential — a sign-in, a device poll — is never replayed,
// because its result is itself a credential; its key is ignored.
func mutate[Req, Res any, PReq interface {
	*Req
	proto.Message
}, PRes interface {
	*Res
	proto.Message
}](ctx context.Context, h *Handlers, req *connect.Request[Req], fn func(context.Context) (*Res, error)) (*connect.Response[Res], error) {
	key := req.Header().Get(IdempotencyHeader)
	id, authenticated := IdentityFrom(ctx)
	if key == "" || !authenticated || h.Idempotency == nil {
		res, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(res), nil
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(PReq(req.Msg))
	if err != nil {
		return nil, fmt.Errorf("api: encode a request: %w", err)
	}
	call := idempotency.Call{Subject: id.Kind + ":" + id.ID, Key: key, Procedure: req.Spec().Procedure, Request: body}
	kept, done, err := h.Idempotency.Begin(ctx, call)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	if done {
		res := new(Res)
		if err := proto.Unmarshal(kept, PRes(res)); err != nil {
			return nil, fmt.Errorf("api: decode a kept result: %w", err)
		}
		out := connect.NewResponse(res)
		out.Header().Set(Replayed, "true")
		return out, nil
	}
	res, err := fn(ctx)
	if err != nil {
		_ = h.Idempotency.Abandon(ctx, call)
		return nil, err
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(PRes(res))
	if err != nil {
		_ = h.Idempotency.Abandon(ctx, call)
		return nil, fmt.Errorf("api: encode a result: %w", err)
	}
	if err := h.Idempotency.Complete(ctx, call, encoded); err != nil {
		// The call acted; failing it now would invite a retry that acts
		// again. The key stays claimed, so a retry is refused instead.
		return connect.NewResponse(res), nil //nolint:nilerr // see above
	}
	return connect.NewResponse(res), nil
}

// read runs a call that changes nothing.
func read[Res any](ctx context.Context, fn func(context.Context) (*Res, error)) (*connect.Response[Res], error) {
	res, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// ts converts a time for the wire; the zero time is absent.
func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
