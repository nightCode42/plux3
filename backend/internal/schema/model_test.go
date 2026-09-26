// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"encoding/json"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// TestSlotFillRoundTripsSingleAndList_SCH_023 checks the one union of the
// generated types: a slot holds one node or a list of nodes.
// Verifies: SCH-023, SCH-001.
func TestSlotFillRoundTripsSingleAndList_SCH_023(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`{"id":"a","type":"Text"}`, `[{"id":"a","type":"Text"},{"id":"b","type":"Icon"}]`} {
		var s SlotFill
		if err := json.Unmarshal([]byte(in), &s); err != nil {
			t.Fatal(err)
		}
		if (s.One == nil) == (s.Many == nil) {
			t.Fatalf("%s: exactly one of One and Many must be set", in)
		}
		out, err := json.Marshal(s)
		if err != nil || string(out) != in {
			t.Errorf("round trip of %s gave %s (%v)", in, out, err)
		}
	}
	var s SlotFill
	if err := json.Unmarshal([]byte(`[1]`), &s); err == nil {
		t.Error("invalid list accepted")
	}
}

// TestGeneratedEnumsValidate checks the generated Valid methods.
func TestGeneratedEnumsValidate(t *testing.T) {
	t.Parallel()
	if !PageKindBottomSheet.Valid() || PageKind("popup").Valid() || !MediaTypeImageSvgXml.Valid() {
		t.Error("enum validation wrong")
	}
}

// TestValidatorRejectsUnknownKinds checks direct use of the validator.
func TestValidatorRejectsUnknownKinds(t *testing.T) {
	t.Parallel()
	v, err := sharedValidator()
	if err != nil {
		t.Fatal(err)
	}
	if v.Kinds("widget") || !v.Kinds(KindPage) {
		t.Error("Kinds wrong")
	}
	diags := v.Validate("widget", map[string]any{}, "x.json")
	if len(diags) != 1 || diags[0].Code != plxerr.InvalidEnumValue {
		t.Errorf("got %v", diags)
	}
}
