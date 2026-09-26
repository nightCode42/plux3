// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// sequence returns an ID source producing id-1, id-2 and so on.
func sequence() IDSource {
	n := 0
	return func() (string, error) {
		n++
		return fmt.Sprintf("id-%d", n), nil
	}
}

// TestInstantiateTemplateAssignsFreshIDs_SCH_031 checks that every node of
// the copy has a new identifier, deterministically, and that the template
// itself is not modified.
// Verifies: SCH-031.
func TestInstantiateTemplateAssignsFreshIDs_SCH_031(t *testing.T) {
	t.Parallel()
	p, diags := newLoader(t).Load(os.DirFS(exampleDir))
	if len(diags) != 0 {
		t.Fatal(list(diags))
	}
	tpl := p.Templates[0].Doc
	originalRoot := tpl.Root.ID

	copied, ids, err := InstantiateTemplate(tpl, sequence())
	if err != nil {
		t.Fatal(err)
	}

	if copied.ID != "id-1" || copied.Children[0].ID != "id-2" || copied.Children[1].ID != "id-3" {
		t.Errorf("IDs not assigned in document order: %s %s %s", copied.ID, copied.Children[0].ID, copied.Children[1].ID)
	}
	if len(ids) != 3 || ids[originalRoot] != "id-1" {
		t.Errorf("mapping = %v", ids)
	}
	if tpl.Root.ID != originalRoot || string(copied.Children[0].Props["data"]) != string(tpl.Root.Children[0].Props["data"]) {
		t.Error("template modified or props lost")
	}
	copied.Children[0].Props["data"][1] = 'X'
	if tpl.Root.Children[0].Props["data"][1] == 'X' {
		t.Error("copy shares prop storage with the template")
	}
}

// TestInstantiateTemplateCopiesSlotsAndRejectsDuplicates checks slot
// traversal, duplicate detection and ID-source failures.
func TestInstantiateTemplateCopiesSlotsAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	leaf := Node{ID: "leaf", Type: "Text"}
	tpl := &TemplateDocument{Key: "t", Root: Node{ID: "root", Type: "Card", Slots: map[string]SlotFill{
		"b": {Many: []Node{{ID: "b1", Type: "Text"}}}, "a": {One: &leaf},
	}}}
	copied, _, err := InstantiateTemplate(tpl, sequence())
	if err != nil {
		t.Fatal(err)
	}
	if copied.Slots["a"].One.ID != "id-2" || copied.Slots["b"].Many[0].ID != "id-3" {
		t.Errorf("slots not visited by name: %+v", copied.Slots)
	}
	dup := &TemplateDocument{Key: "d", Root: Node{ID: "x", Type: "Column", Children: []Node{{ID: "x", Type: "Text"}}}}
	if _, _, err := InstantiateTemplate(dup, sequence()); err == nil {
		t.Error("duplicate IDs accepted")
	}
	failing := func() (string, error) { return "", errors.New("entropy") }
	if _, _, err := InstantiateTemplate(tpl, failing); err == nil {
		t.Error("ID source failure ignored")
	}
}
