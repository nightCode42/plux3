// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage/idempotency"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

func store(t *testing.T, now func() time.Time) *idempotency.Store {
	t.Helper()
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := idempotency.NewStore(storagetest.Open(t), backend, now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func code(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// Verifies: SRV-005.
// The first call with a key acts; a repeat gets the kept result, even an
// empty one; a key reused for another request, or repeated while the
// first call runs, is refused; a failed call releases its key.
func TestIdempotentCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store(t, nil)
	call := idempotency.Call{Subject: "user:u", Key: "k1", Procedure: "/p", Request: []byte("req")}
	if kept, done, err := s.Begin(ctx, call); err != nil || done || kept != nil {
		t.Fatalf("first Begin = %q, %v, %v", kept, done, err)
	}
	if _, _, err := s.Begin(ctx, call); code(err) != plxerr.IdempotencyConflict {
		t.Errorf("a repeat while in progress: %v", err)
	}
	if err := s.Complete(ctx, call, []byte("result with a secret")); err != nil {
		t.Fatal(err)
	}
	kept, done, err := s.Begin(ctx, call)
	if err != nil || !done || string(kept) != "result with a secret" {
		t.Errorf("a repeat = %q, %v, %v", kept, done, err)
	}
	changed := call
	changed.Request = []byte("other")
	if _, _, err := s.Begin(ctx, changed); code(err) != plxerr.IdempotencyConflict {
		t.Errorf("a key reused for another request: %v", err)
	}
	// Another subject's key of the same name is its own.
	theirs := call
	theirs.Subject = "user:v"
	if _, done, err := s.Begin(ctx, theirs); err != nil || done {
		t.Errorf("another subject's key: %v, %v", done, err)
	}
	// An empty result is still a result.
	empty := idempotency.Call{Subject: "user:u", Key: "k2", Procedure: "/p"}
	if _, _, err := s.Begin(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, empty, nil); err != nil {
		t.Fatal(err)
	}
	if kept, done, err := s.Begin(ctx, empty); err != nil || !done || len(kept) != 0 {
		t.Errorf("an empty result = %q, %v, %v", kept, done, err)
	}
	// A failure releases the key.
	failing := idempotency.Call{Subject: "user:u", Key: "k3", Procedure: "/p"}
	if _, _, err := s.Begin(ctx, failing); err != nil {
		t.Fatal(err)
	}
	if err := s.Abandon(ctx, failing); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.Begin(ctx, failing); err != nil || done {
		t.Errorf("after a failure the key was not released: %v, %v", done, err)
	}
	for _, bad := range []idempotency.Call{
		{Key: "k"}, {Subject: "s"}, {Subject: "s", Key: strings.Repeat("k", 200)}, {Subject: "s", Key: "has space"},
	} {
		if _, _, err := s.Begin(ctx, bad); err == nil {
			t.Errorf("Begin(%+v) was accepted", bad)
		}
	}
}

// Verifies: SRV-005, SEC-106.
// Results are sealed at rest and forgotten after a day.
func TestIdempotentResultsAreSealedAndExpire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	db := storagetest.Open(t)
	backend, _ := signing.NewFile(t.TempDir())
	s, err := idempotency.NewStore(db, backend, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	call := idempotency.Call{Subject: "token:t", Key: "k", Procedure: "/p", Request: []byte("r")}
	if _, _, err := s.Begin(ctx, call); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, call, []byte("plux_pat_secretvalue")); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := db.Pool().QueryRow(ctx, `SELECT response FROM idempotency_keys`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("plux_pat_secretvalue")) {
		t.Error("a result is stored in the clear")
	}
	if n, err := s.Expire(ctx); err != nil || n != 0 {
		t.Errorf("Expire before a day = %d, %v", n, err)
	}
	// Keys written as if a day ago have expired: a new call with the
	// same key acts again.
	past, _ := idempotency.NewStore(db, backend, func() time.Time { return now.Add(-25 * time.Hour) })
	old := idempotency.Call{Subject: "token:t", Key: "old", Procedure: "/p"}
	if _, _, err := past.Begin(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.Begin(ctx, old); err != nil || done {
		t.Errorf("an expired key: %v, %v", done, err)
	}
	if _, err := s.Expire(ctx); err != nil {
		t.Error(err)
	}
	if _, err := idempotency.NewStore(nil, nil, nil); err == nil {
		t.Error("a store with no database was built")
	}
}
