// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites the golden bundle.
var update = flag.Bool("update", false, "rewrite schema/testdata/bundles/sample.pxb")

// goldenPath is read by the Dart container tests too.
var goldenPath = filepath.Join("..", "..", "..", "schema", "testdata", "bundles", "sample.pxb")

// Verifies: BND-001, CMP-002.
// The encoding of the sample sections is pinned byte for byte, so any
// change to the container or the generated builders shows up in review.
func TestGoldenBundle(t *testing.T) {
	t.Parallel()
	data, err := Encode(KindPlugin, sampleSections())
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(goldenPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, golden) {
		t.Errorf("%s differs; run the test with -update and review", goldenPath)
	}
	if _, err := Read(golden, defaultOptions()); err != nil {
		t.Error(err)
	}
}
