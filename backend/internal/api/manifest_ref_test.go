// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"testing"

	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/release"
)

// Verifies: SEC-050.
// A manifest of an environment with a root names the metadata versions and
// the environment whose files hold them, so that a device needs nothing
// else to fetch the chain; one without a root names none.
func TestManifestNamesItsMetadata_SEC_050(t *testing.T) {
	t.Parallel()
	d := device.Identity{AppID: "app", EnvironmentID: "env-id"}
	enrolled := manifestProto(d, release.ServedManifest{Metadata: release.MetadataRef{Root: 2, Snapshot: 3, Timestamp: 9}})
	ref := enrolled.GetMetadata()
	if ref.GetRootVersion() != 2 || ref.GetSnapshotVersion() != 3 || ref.GetTimestampVersion() != 9 || ref.GetEnvironmentId() != "env-id" {
		t.Errorf("the reference: %v", ref)
	}
	if got := manifestProto(d, release.ServedManifest{}).GetMetadata(); got != nil {
		t.Errorf("an environment without a root names metadata: %v", got)
	}
}
