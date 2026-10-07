// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type updateCase struct {
	Name     string `json:"name"`
	Document any    `json:"document"`
}

// Verifies: SEC-122.
func TestUpdateMetadataSchemasAgainstVectors_SEC_122(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "..", "schema")
	raw, err := os.ReadFile(filepath.Join(dir, "testdata", "update", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors map[string]json.RawMessage
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	commonRaw, err := os.ReadFile(filepath.Join(dir, "update", "common.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	common, err := jsonschema.UnmarshalJSON(bytes.NewReader(commonRaw))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"root", "snapshot", "timestamp"} {
		var set struct {
			Valid   []updateCase `json:"valid"`
			Invalid []updateCase `json:"invalid"`
		}
		if err := json.Unmarshal(vectors[kind], &set); err != nil {
			t.Fatal(err)
		}
		if len(set.Valid) == 0 || len(set.Invalid) == 0 {
			t.Fatalf("%s: vectors need valid and invalid cases", kind)
		}
		abs, err := filepath.Abs(filepath.Join(dir, "update", kind+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		if err := c.AddResource("https://plux.dev/schema/v1/update/common.schema.json", common); err != nil {
			t.Fatal(err)
		}
		s, err := c.Compile(abs)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for _, tc := range set.Valid {
			if err := s.Validate(tc.Document); err != nil {
				t.Errorf("%s/%s rejected: %v", kind, tc.Name, err)
			}
		}
		for _, tc := range set.Invalid {
			if err := s.Validate(tc.Document); err == nil {
				t.Errorf("%s/%s accepted", kind, tc.Name)
			}
		}
	}
}
