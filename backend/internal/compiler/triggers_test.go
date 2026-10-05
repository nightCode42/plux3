// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

// triggersDir is the conformance project of the app's and the plugins'
// triggers, error handlers and component events, whose bundles the
// runtime's integration tests run.
var triggersDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "triggers")

// TestTriggersGoldenBundles pins the bundles of the triggers project byte
// for byte; they carry app-, plugin- and page-level triggers, the plugin's
// and the app's error handlers and a component that emits events.
// Verifies: ACT-002, ACT-020, SCH-030, CMP-002, QA-003.
func TestTriggersGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(os.DirFS(triggersDir), DefaultOptions())
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	read := readAll(t, res)
	if len(metaTriggers(metaOf(t, read[0]))) != 2 || len(metaTriggers(metaOf(t, read[1]))) != 2 {
		t.Error("the app's and the plugin's triggers are not encoded")
	}
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "triggers", b.Key+".pxb"), b.Data)
	}
}
