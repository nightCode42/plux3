// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
)

// The generated code speaks pgx's own types. These converters are the
// only place that translates between them and the domain's: identifiers
// are UUIDv7 strings (SCH-002) and times are time.Time.

// UUID converts a UUIDv7 string for the database.
func UUID(s string) (pgtype.UUID, error) {
	id, err := uuid7.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("storage: %w", err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// MustUUID converts an identifier the caller has already validated.
// Passing an invalid one is a programming error and panics, so it
// cannot reach the database as a silent zero.
func MustUUID(s string) pgtype.UUID {
	v, err := UUID(s)
	if err != nil {
		panic(err)
	}
	return v
}

// NullUUID converts an optional identifier; "" becomes NULL.
func NullUUID(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	return UUID(s)
}

// ID renders a database identifier as a UUID string, or "" for NULL.
func ID(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid7.UUID(v.Bytes).String()
}

// Timestamp converts a time for the database; the zero time becomes
// NULL.
func Timestamp(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// Time reads a database timestamp; NULL becomes the zero time.
func Time(v pgtype.Timestamptz) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time.UTC()
}

// Cursor is the position a page of a list continues from (SRV-004). A
// list orders by a key and breaks ties by ID; the zero Cursor starts at
// the beginning. The API edge carries it in an opaque, authenticated
// page token; the domain only ever sees this.
type Cursor struct {
	// Key is the last item's sort key, for lists ordered by a key.
	Key string
	// Time is the last item's sort time, for lists ordered by time.
	Time time.Time
	// Sequence is the last item's sequence, for lists ordered by one.
	Sequence int64
	// ID is the last item's identifier.
	ID string
}

// AfterID is the tie-breaking identifier to continue after; the zero
// UUID, which sorts first, at the beginning.
func (c Cursor) AfterID() pgtype.UUID {
	if c.ID == "" {
		return pgtype.UUID{Valid: true}
	}
	id, err := UUID(c.ID)
	if err != nil {
		return pgtype.UUID{Valid: true}
	}
	return id
}

// AfterTime is the time to continue after; minus infinity at the
// beginning.
func (c Cursor) AfterTime() pgtype.Timestamptz {
	if c.Time.IsZero() {
		return pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true}
	}
	return pgtype.Timestamptz{Time: c.Time.UTC(), Valid: true}
}
