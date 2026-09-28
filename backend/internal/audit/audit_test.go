// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// chain returns n linked entries.
func chain(n int) []Entry {
	out := make([]Entry, n)
	previous := ""
	for i := range out {
		e := Entry{
			ID:             "01a0c450-6c00-7000-8000-00000000000" + string(rune('0'+i)),
			OrganizationID: "01a0c450-6c00-7000-8000-00000000000a",
			Sequence:       int64(i + 1),
			At:             time.Unix(1_700_000_000+int64(i), 0).UTC(),
			Actor:          Actor{Kind: "user", ID: "u1", Display: "Ada"},
			Action:         AppCreated,
			TargetKind:     "app",
			TargetID:       "demo",
			PreviousHash:   previous,
		}
		e.EntryHash = e.Hash()
		previous = e.EntryHash
		out[i] = e
	}
	return out
}

// Verifies: SEC-140.
func TestChainDetectsEveryTampering(t *testing.T) {
	t.Parallel()
	entries := chain(4)
	if err := Verify(entries, ""); err != nil {
		t.Fatalf("an untouched chain failed: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func([]Entry) []Entry
		want string
	}{
		{"an entry is edited", func(e []Entry) []Entry {
			e[2].Action = AppDeleted
			return e
		}, "was changed"},
		{"an entry is removed", func(e []Entry) []Entry {
			return slices.Delete(e, 2, 3)
		}, "missing"},
		{"an entry is re-hashed after an edit", func(e []Entry) []Entry {
			e[2].Action = AppDeleted
			e[2].EntryHash = e[2].Hash()
			return e
		}, "does not follow"},
		{"the chain is started elsewhere", func(e []Entry) []Entry {
			e[0].PreviousHash = "deadbeef"
			e[0].EntryHash = e[0].Hash()
			return e
		}, "does not follow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Verify(tc.edit(chain(4)), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Verify = %v; want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// Verifies: SEC-140.
// The hash is unambiguous: moving characters between fields changes it.
func TestHashIsUnambiguous(t *testing.T) {
	t.Parallel()
	a := Entry{Actor: Actor{Kind: "user", ID: "ab"}, Action: AppCreated}
	b := Entry{Actor: Actor{Kind: "usera", ID: "b"}, Action: AppCreated}
	if a.Hash() == b.Hash() {
		t.Error("two different entries hash the same")
	}
	c := a
	c.RequestID = "x"
	if a.Hash() == c.Hash() {
		t.Error("the request ID is not covered by the hash")
	}
}

// Verifies: SEC-140.
func TestActionsAreRegistered(t *testing.T) {
	t.Parallel()
	if !Registered(AppCreated) || !Registered(UserSignedIn) {
		t.Error("a registered action was not recognised")
	}
	if Registered("app.exploded") {
		t.Error("an unregistered action was accepted")
	}
	got := Actions()
	if !slices.IsSorted(got) {
		t.Error("Actions() must be sorted")
	}
	seen := map[Action]bool{}
	for _, a := range got {
		if seen[a] {
			t.Errorf("%q is registered twice", a)
		}
		seen[a] = true
		if !strings.Contains(string(a), ".") {
			t.Errorf("%q should be of the form subject.verb", a)
		}
	}
	if len(got) != len(actions) {
		t.Errorf("Actions() returned %d of %d", len(got), len(actions))
	}
}

// Verifies: SEC-140.
// A partial run verifies against the hash of the entry before it, which
// is what paging through the log does.
func TestVerifyAPartialRun(t *testing.T) {
	t.Parallel()
	entries := chain(5)
	if err := Verify(entries[2:], entries[1].EntryHash); err != nil {
		t.Errorf("a later page failed to verify: %v", err)
	}
	if err := Verify(entries[2:], ""); err == nil {
		t.Error("a later page verified without its predecessor")
	}
	if err := Verify(nil, ""); err != nil {
		t.Errorf("an empty run failed: %v", err)
	}
}
