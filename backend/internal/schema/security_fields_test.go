// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: SEC-181, SCH-000.
func TestAppSecuritySettingsSchema_SEC_181(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		wantCode plxerr.Code
		path     string
	}{
		{"all settings", map[string]any{
			"allowDirectDataSources": false, "minAssuranceForSync": "AL2", "screenshotBlockingDefault": true,
			"inactivityLock": true, "inactivityLockTimeout": 300, "raspRootHookingResponse": "block",
		}, 0, ""},
		{"timeout too short", map[string]any{"inactivityLockTimeout": 29}, plxerr.OutOfRange, "/security/settings/inactivityLockTimeout"},
		{"timeout too long", map[string]any{"inactivityLockTimeout": 3601}, plxerr.OutOfRange, "/security/settings/inactivityLockTimeout"},
		{"unknown assurance level", map[string]any{"minAssuranceForSync": "AL9"}, plxerr.InvalidEnumValue, "/security/settings/minAssuranceForSync"},
		{"unknown response", map[string]any{"raspRootHookingResponse": "ignore"}, plxerr.InvalidEnumValue, "/security/settings/raspRootHookingResponse"},
		{"unknown setting", map[string]any{"allowEverything": true}, plxerr.UnknownProperty, "/security/settings/allowEverything"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := exampleFS(t)
			edit(t, fsys, "app.json", func(doc map[string]any) {
				doc["security"] = map[string]any{"settings": tt.settings}
			})
			_, diags := newLoader(t).Load(fstest.MapFS(fsys))
			if tt.wantCode == 0 {
				for _, d := range diags {
					t.Errorf("unexpected diagnostic %+v", d)
				}
				return
			}
			wantDiagnostic(t, diags, tt.wantCode, "app.json", tt.path)
		})
	}
}
