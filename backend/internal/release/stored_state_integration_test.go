// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
)

// Verifies: STA-040.
// A publish compares the stored state with the bundle's latest version: a
// session, persisted or secure entry whose type changed fails the publish
// with PLX-1143 until it declares a migration from the previous type
// (PLX-1144 for another type) or a reset.
func TestPublishChecksStoredState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if job := f.publish(t, "", false); job.State != release.StateSucceeded {
		t.Fatalf("the app publish: %+v", job)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "edit", false); err != nil {
		t.Fatal(err)
	}
	const path = "plugins/loans/plugin.json"
	put := func(entry string) {
		t.Helper()
		doc, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(doc.Content, &m); err != nil {
			t.Fatal(err)
		}
		var e any
		if err := json.Unmarshal([]byte(entry), &e); err != nil {
			t.Fatal(err)
		}
		m["state"] = append(m["state"].([]any)[:1], e)
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", path, data, doc.Revision); err != nil {
			t.Fatal(err)
		}
	}
	const head = `{"id": "01a0c450-6c00-7014-8000-0000000300aa", "name": "seen", "persistence": "persisted", `
	put(head + `"type": "int", "default": 0}`)
	if job := f.publish(t, f.loans, false); job.State != release.StateSucceeded || job.Version != 1 {
		t.Fatalf("the first version: %+v", job)
	}
	put(head + `"type": "string", "default": ""}`)
	if job := f.publish(t, f.loans, false); job.State != release.StateFailed || !hasCode(job.Diagnostics, plxerr.StateMigrationRequired) {
		t.Errorf("a type change without a migration: %+v", job)
	}
	put(head + `"type": "string", "default": "", "migration": {"from": "bool", "value": {"$expr": "string(previous)"}}}`)
	if job := f.publish(t, f.loans, false); job.State != release.StateFailed || !hasCode(job.Diagnostics, plxerr.StateMigrationMismatch) {
		t.Errorf("a migration from another type: %+v", job)
	}
	put(head + `"type": "string", "default": "", "migration": {"from": "int", "value": {"$expr": "string(previous)"}}}`)
	if job := f.publish(t, f.loans, false); job.State != release.StateSucceeded || job.Version != 2 {
		t.Errorf("a type change with a migration: %+v", job)
	}
}
