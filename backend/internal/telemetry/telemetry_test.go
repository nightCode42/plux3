// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package telemetry

import (
	"strings"
	"testing"
)

// Verifies: SCH-012.
// Fields are flat, scalar, small and never named like sensitive data.
func TestCheckFields(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{``, `{}`, `{"durationMs": 12, "outcome": "ok", "cached": true, "note": null}`} {
		if _, err := checkFields([]byte(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`[]`, `null`, `{`, `{"a": {"b": 1}}`, `{"a": [1]}`, `{"Bad": 1}`, `{"userPassword": "x"}`, `{"authToken": "x"}`,
		`{"note": "` + strings.Repeat("x", maxText+1) + `"}`, `{"x": "` + strings.Repeat("y", maxFieldsBytes) + `"}`,
		`{` + strings.Repeat(`"a`, 0) + manyFields() + `}`,
	} {
		if _, err := checkFields([]byte(bad)); err == nil {
			t.Errorf("%.60s was accepted", bad)
		}
	}
	for name, want := range map[string]bool{"cardNumber": true, "PIN": true, "email": true, "route": false, "durationMs": false} {
		if Sensitive(name) != want {
			t.Errorf("Sensitive(%q) = %v", name, !want)
		}
	}
}

func manyFields() string {
	parts := make([]string, maxFields+1)
	for i := range parts {
		parts[i] = `"f` + strings.Repeat("a", i) + `": 1`
	}
	return strings.Join(parts, ",")
}
