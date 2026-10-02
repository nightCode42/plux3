// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"cmp"
	"slices"
)

// EdgeKind classifies a reference between entities (SCH-041).
type EdgeKind string

// Edge kinds.
const (
	// EdgeNavigates: a page or graph navigates to a page of its plugin.
	EdgeNavigates EdgeKind = "navigates"
	// EdgeNavigatesPlugin: to a page of another plugin.
	EdgeNavigatesPlugin EdgeKind = "navigatesPlugin"
	// EdgeNavigatesNative: to a native route of the host.
	EdgeNavigatesNative EdgeKind = "navigatesNative"
	// EdgeUsesSlot: a node is a native slot of the host (WGT-033).
	EdgeUsesSlot EdgeKind = "usesSlot"
	// EdgeUsesAction: a step calls a custom action of the host (ACT-060).
	EdgeUsesAction      EdgeKind = "usesAction"
	EdgeUsesComponent   EdgeKind = "usesComponent"
	EdgeUsesGraph       EdgeKind = "usesGraph"
	EdgeUsesState       EdgeKind = "usesState"
	EdgeUsesTranslation EdgeKind = "usesTranslation"
	EdgeUsesDataSource  EdgeKind = "usesDataSource"
	EdgeUsesCollection  EdgeKind = "usesCollection"
	EdgeUsesFunction    EdgeKind = "usesFunction"
	EdgeUsesToken       EdgeKind = "usesToken"
	EdgeUsesAsset       EdgeKind = "usesAsset"
)

// Edge is a reference from an entity to another.
type Edge struct {
	// From is the ID of the page, component or graph that refers.
	From string   `json:"from"`
	Kind EdgeKind `json:"kind"`
	// To is the ID of the target, or a route name, token path or state
	// path where the target has no ID of its own.
	To string `json:"to"`
	// File and Path locate the reference.
	File string `json:"file"`
	Path string `json:"path"`
}

// Graph is the reference graph of a project (SCH-041): it powers Studio's
// arrows, "where used" and deletion protection.
type Graph struct {
	// Edges are sorted and unique.
	Edges []Edge `json:"edges"`
}

// add records an edge.
func (g *Graph) add(e Edge) { g.Edges = append(g.Edges, e) }

// finish sorts the edges and removes duplicates, so the graph is a
// function of the project only.
func (g *Graph) finish() {
	slices.SortFunc(g.Edges, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.To, b.To),
			cmp.Compare(a.File, b.File), cmp.Compare(a.Path, b.Path))
	})
	g.Edges = slices.Compact(g.Edges)
}

// UsesOf returns the edges pointing at target.
func (g *Graph) UsesOf(target string) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.To == target {
			out = append(out, e)
		}
	}
	return out
}
