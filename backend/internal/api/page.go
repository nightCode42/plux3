// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// Pages turns list positions into page tokens and back (SRV-004). A
// token is opaque to the caller and authenticated with an HMAC under an
// installation key, and it is bound to the procedure, the caller's
// scope, the filter and the ordering it was issued for: presenting it
// anywhere else is refused, so a token can neither be forged nor used
// to page through a list the caller did not ask for.
type Pages struct {
	key []byte
	// Size is the default and maximum page size (api.pageSize).
	Size int32
	now  func() time.Time
}

// tokenTTL bounds how long a page token may be used.
const tokenTTL = 24 * time.Hour

// NewPages returns the codec. The key must be 32 bytes.
func NewPages(key []byte, size int32, now func() time.Time) (*Pages, error) {
	if len(key) != 32 {
		return nil, errors.New("api: the page token key must be 32 bytes")
	}
	if size < 1 {
		return nil, errors.New("api: the page size must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &Pages{key: key, Size: size, now: now}, nil
}

// token is what a page token carries before it is authenticated.
type token struct {
	Binding  string `json:"b"`
	Key      string `json:"k,omitempty"`
	Time     int64  `json:"t,omitempty"`
	Sequence int64  `json:"s,omitempty"`
	ID       string `json:"i,omitempty"`
	Issued   int64  `json:"e"`
}

// binding commits to everything a token is valid for.
func binding(procedure, scope string, page *pluxv1.Page) string {
	h := sha256.New()
	for _, part := range []string{procedure, scope, page.GetFilter(), page.GetOrderBy()} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16])
}

// Request reads a list request's page: its size, clamped to the
// registry's maximum, and its position, from a verified token. scope is
// what the list is of, such as the organisation or the app.
func (p *Pages) Request(procedure, scope string, page *pluxv1.Page) (storage.Cursor, int32, error) {
	size := page.GetPageSize()
	switch {
	case size < 0:
		return storage.Cursor{}, 0, plxerr.New(plxerr.OutOfRange, "page_size must not be negative")
	case size == 0 || size > p.Size:
		size = p.Size
	}
	raw := page.GetPageToken()
	if raw == "" {
		return storage.Cursor{}, size, nil
	}
	refused := plxerr.New(plxerr.InvalidPageToken, "the page token is not valid for this call; start again without one")
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) <= sha256.Size {
		return storage.Cursor{}, 0, refused
	}
	body, mac := data[:len(data)-sha256.Size], data[len(data)-sha256.Size:]
	if !hmac.Equal(mac, p.mac(body)) {
		return storage.Cursor{}, 0, refused
	}
	var t token
	if err := json.Unmarshal(body, &t); err != nil {
		return storage.Cursor{}, 0, refused
	}
	if t.Binding != binding(procedure, scope, page) || p.now().Sub(time.Unix(t.Issued, 0)) > tokenTTL {
		return storage.Cursor{}, 0, refused
	}
	c := storage.Cursor{Key: t.Key, Sequence: t.Sequence, ID: t.ID}
	if t.Time != 0 {
		c.Time = time.UnixMicro(t.Time).UTC()
	}
	return c, size, nil
}

// Next returns the page token that continues after the last item of a
// full page, or "" when the page was not full and the list has ended.
func (p *Pages) Next(procedure, scope string, page *pluxv1.Page, returned int, size int32, last storage.Cursor) string {
	if returned < int(size) {
		return ""
	}
	t := token{
		Binding: binding(procedure, scope, page), Key: last.Key, Sequence: last.Sequence,
		ID: last.ID, Issued: p.now().Unix(),
	}
	if !last.Time.IsZero() {
		t.Time = last.Time.UnixMicro()
	}
	body, err := json.Marshal(t)
	if err != nil {
		// A struct of strings and integers always encodes.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(append(body, p.mac(body)...))
}

// mac authenticates a token body.
func (p *Pages) mac(body []byte) []byte {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte("plux page token v1\x00"))
	m.Write(body)
	return m.Sum(nil)
}

// Item is one item of a list.
type Item = proto.Message

// Query is what a list call may filter and sort on: the fields its
// filter accepts, and the orderings beyond the default (SRV-004).
type Query struct {
	// Filters maps each filterable field to a function reading it from
	// an item.
	Filters map[string]func(proto.Message) string
	// OrderBy is the one ordering the list is served in; an empty
	// order_by or this value is accepted.
	OrderBy string
}

// clause is one "field = value" condition of a filter.
type clause struct{ field, value string }

// Parse checks a page's filter and ordering against what the call
// supports. The filter language is a conjunction of equalities,
// `key = "value" AND production = "true"`, which every list can serve
// on its keyset without a query planner.
func (q Query) Parse(page *pluxv1.Page) ([]clause, error) {
	if o := strings.TrimSpace(page.GetOrderBy()); o != "" && o != q.OrderBy {
		return nil, plxerr.New(plxerr.InvalidFormat, "this list is ordered by %q only", q.OrderBy)
	}
	filter := strings.TrimSpace(page.GetFilter())
	if filter == "" {
		return nil, nil
	}
	var out []clause
	for _, part := range splitAnd(filter) {
		field, value, ok := strings.Cut(part, "=")
		field, value = strings.TrimSpace(field), strings.TrimSpace(value)
		if !ok || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, plxerr.New(plxerr.InvalidFormat, `a filter is field = "value" joined by AND`)
		}
		if _, known := q.Filters[field]; !known {
			return nil, plxerr.New(plxerr.InvalidFormat, "this list cannot be filtered by %q", field)
		}
		out = append(out, clause{field: field, value: value[1 : len(value)-1]})
	}
	return out, nil
}

// splitAnd splits a filter on the AND keyword.
func splitAnd(filter string) []string {
	var parts []string
	for {
		i := strings.Index(filter, " AND ")
		if i < 0 {
			return append(parts, filter)
		}
		parts = append(parts, filter[:i])
		filter = filter[i+len(" AND "):]
	}
}

// Keep reports whether an item satisfies every clause.
func (q Query) Keep(clauses []clause, item proto.Message) bool {
	for _, c := range clauses {
		if q.Filters[c.field](item) != c.value {
			return false
		}
	}
	return true
}

// Mask limits each item to the fields a read mask names (SRV-004). An
// empty mask leaves items whole; a path the item type does not have is
// refused rather than ignored, so a typo does not return less than the
// caller thinks.
func Mask[T proto.Message](mask *fieldmaskpb.FieldMask, items []T) error {
	if len(mask.GetPaths()) == 0 || len(items) == 0 {
		return nil
	}
	fields := items[0].ProtoReflect().Descriptor().Fields()
	keep := map[protoreflect.Name]bool{}
	for _, path := range mask.GetPaths() {
		name := protoreflect.Name(strings.SplitN(path, ".", 2)[0])
		if fields.ByName(name) == nil {
			return plxerr.New(plxerr.InvalidFormat, "the read mask names %q, which the items do not have", path)
		}
		keep[name] = true
	}
	for _, item := range items {
		m := item.ProtoReflect()
		m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			if !keep[fd.Name()] {
				m.Clear(fd)
			}
			return true
		})
	}
	return nil
}
