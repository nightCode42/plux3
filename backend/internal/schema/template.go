// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// IDSource returns fresh identifiers, e.g. from uuid7.Generator.
type IDSource func() (string, error)

// InstantiateTemplate returns a deep copy of the template's subtree in which
// every node has a fresh identifier (SCH-031), and the mapping from old to
// new identifiers so callers can rewrite references to the copied nodes.
// References to other entities — components, translations, assets, graphs —
// are kept: they name things outside the subtree.
func InstantiateTemplate(t *TemplateDocument, newID IDSource) (Node, map[string]string, error) {
	data, err := json.Marshal(t.Root)
	if err != nil {
		return Node{}, nil, fmt.Errorf("schema.InstantiateTemplate %s: %w", t.Key, err)
	}
	var root Node
	if err := json.Unmarshal(data, &root); err != nil {
		return Node{}, nil, fmt.Errorf("schema.InstantiateTemplate %s: %w", t.Key, err)
	}
	ids := map[string]string{}
	if err := renumber(&root, newID, ids); err != nil {
		return Node{}, nil, fmt.Errorf("schema.InstantiateTemplate %s: %w", t.Key, err)
	}
	return root, ids, nil
}

// renumber gives n and its descendants fresh identifiers, in document order
// with slots visited by name, so the result is deterministic for a
// deterministic source.
func renumber(n *Node, newID IDSource, ids map[string]string) error {
	if _, dup := ids[n.ID]; dup {
		return fmt.Errorf("node %s appears twice in the template", n.ID)
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("new identifier: %w", err)
	}
	ids[n.ID] = id
	n.ID = id
	for i := range n.Children {
		if err := renumber(&n.Children[i], newID, ids); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(n.Slots)) {
		fill := n.Slots[name]
		if fill.One != nil {
			if err := renumber(fill.One, newID, ids); err != nil {
				return err
			}
		}
		for i := range fill.Many {
			if err := renumber(&fill.Many[i], newID, ids); err != nil {
				return err
			}
		}
	}
	return nil
}
