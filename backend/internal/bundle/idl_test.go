// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Verifies: BND-013.
// Every scalar field of the IDL narrower than 32 bits is a flag, a byte
// payload or a value that is not an index or a count, so the format caps
// nothing below the limits of spec §30.4.
func TestIndicesAndCountsAre32Bit(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "fbs", "bundle.fbs"))
	if err != nil {
		t.Fatal(err)
	}
	// Fields reviewed as not being indices or counts.
	notCounts := map[string]bool{
		"offset":   true, // a UTC offset in minutes
		"unscaled": true, // decimal digits
		"code":     true, // PXL bytecode
		"hash":     true, // SHA-256
		"module":   true, // WebAssembly bytes
		"hints":    true, // NodeHints bits
	}
	field := regexp.MustCompile(`^\s*([a-z_]+)\s*:\s*\[?(byte|ubyte|int8|uint8|short|ushort|int16|uint16)\]?\s*[;(]`)
	found := 0
	for i, line := range strings.Split(string(data), "\n") {
		m := field.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		if !notCounts[m[1]] {
			t.Errorf("bundle.fbs:%d: %s is a %s; indices and counts must be 32-bit", i+1, m[1], m[2])
		}
	}
	if found == 0 {
		t.Fatal("the pattern matched no field; the check is broken")
	}
}
