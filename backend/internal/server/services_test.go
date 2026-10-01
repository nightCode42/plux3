// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/nightCode42/plux3/backend/internal/release"
)

// Verifies: CMP-030.
// A publish waiting for assets is snoozed for the time it asks, which the
// job queue does not count as a failed attempt; other errors pass
// through.
func TestSnoozePending(t *testing.T) {
	t.Parallel()
	err := snoozePending(&release.AssetsPendingError{Pending: 2, RetryAfter: 1500 * time.Millisecond})
	snooze, ok := errors.AsType[*rivertype.JobSnoozeError](err)
	if !ok || snooze.Duration != 1500*time.Millisecond {
		t.Errorf("snoozePending: %v", err)
	}
	other := errors.New("boom")
	if !errors.Is(snoozePending(other), other) || snoozePending(nil) != nil {
		t.Error("other errors must pass through")
	}
}
