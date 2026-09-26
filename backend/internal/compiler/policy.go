// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// policy applies the policy tier of validation (SCH-040): per-page
// budgets (CMP-040), accessible names (PLX-1401), secret-like literals,
// insecure URLs, sensitive values sent to analytics, and executable
// content in assets (SEC-054).
func policy(u *unit) {
	for _, pl := range u.plugins {
		for _, pg := range pl.pages {
			if u.inFocus(pg.file) {
				u.budgets(pg)
			}
		}
	}
	for _, n := range u.allNodes() {
		if u.inFocus(n.owner.file()) {
			u.lintNode(n)
		}
	}
	for _, g := range u.allGraphs() {
		if u.graphInFocus(g) {
			u.lintGraph(g)
		}
	}
	if u.focus == "" {
		u.lintEnvironments()
		u.lintAssets()
	}
}

// lintNode checks a node's accessible name and literal props.
func (u *unit) lintNode(n *node) {
	u.accessibleName(n)
	for _, p := range n.props {
		u.lintValue(n.owner.file(), p.ptr, p.value)
	}
}

// lintGraph checks a graph's literal inputs and what it sends to
// analytics.
func (u *unit) lintGraph(g *graph) {
	for _, s := range g.lowered {
		for _, in := range s.inputs {
			u.lintValue(g.file, in.ptr, in.value)
		}
		u.sensitiveToAnalytics(g, s)
	}
}

// lintEnvironments checks the string values of the environments.
func (u *unit) lintEnvironments() {
	for i, env := range u.project.App.Doc.Environments {
		for _, name := range sortedKeys(env.Values) {
			var v any
			if json.Unmarshal(env.Values[name], &v) != nil {
				continue
			}
			if s, ok := v.(string); ok {
				u.lintString("app.json", plxerr.Pointer("environments", strconv.Itoa(i), "values", name), s)
			}
		}
	}
}

// allNodes returns every node of every page and component.
func (u *unit) allNodes() []*node {
	var out []*node
	for _, c := range u.shared {
		out = append(out, c.nodes...)
	}
	for _, pl := range u.plugins {
		for _, c := range pl.components {
			out = append(out, c.nodes...)
		}
		for _, pg := range pl.pages {
			out = append(out, pg.nodes...)
		}
	}
	return out
}

// allGraphs returns every graph.
func (u *unit) allGraphs() []*graph {
	out := append([]*graph(nil), u.appGraphs...)
	for _, pl := range u.plugins {
		out = append(append(out, pl.graphs...), pl.inline...)
	}
	return out
}

// budget is one per-page budget of spec §30.3.
type budget struct {
	key  limits.Key
	code plxerr.Code
	what string
}

// budgets computes a page's node count, depth, build cost, image bytes
// and animations and reports them against the limits (CMP-040): above
// the warning threshold as a warning, above the limit as an error.
func (u *unit) budgets(pg *page) {
	cost, depth := u.cost(pg.root, 1)
	values := []struct {
		b budget
		v int64
	}{
		{budget{limits.PageNodes, plxerr.PageNodeBudget, "nodes"}, int64(len(pg.nodes))},
		{budget{limits.PageDepth, plxerr.PageDepthBudget, "levels deep"}, int64(depth)},
		{budget{limits.PageBuildCost, plxerr.PageBuildCostBudget, "µs of estimated build cost"}, cost},
		{budget{limits.PageImageBytes, plxerr.PageImageBudget, "bytes of images"}, u.imageBytes(pg)},
		// Animations come with timelines (P5); no P1 widget animates.
		{budget{limits.PageAnimations, plxerr.PageAnimationBudget, "animations"}, 0},
	}
	for _, x := range values {
		limit, warn := u.opts.Limits.Get(x.b.key), u.opts.Limits.Warning(x.b.key)
		switch {
		case x.v > limit:
			d := plxerr.NewDiagnostic(x.b.code, plxerr.Location{File: pg.file, Path: "/root"}, "the page has %d %s; the limit %s is %d", x.v, x.b.what, x.b.key, limit)
			u.diags = append(u.diags, d.WithSeverity(plxerr.SeverityError))
		case x.v > warn:
			u.report(x.b.code, pg.file, "/root", "the page has %d %s; the warning threshold of %s is %d", x.v, x.b.what, x.b.key, warn)
		}
	}
}

// cost returns the estimated build cost of a subtree, a component
// instance counting its definition, and the subtree's depth.
func (u *unit) cost(n *node, level int) (int64, int) {
	var total int64
	switch {
	case n.widget != nil:
		total = int64(n.widget.Cost)
	case n.target != nil && n.target.root != nil:
		total, _ = u.cost(n.target.root, 1)
	}
	depth := level
	kids := append([]*node(nil), n.children...)
	for _, sf := range n.slots {
		kids = append(kids, sf.nodes...)
	}
	for _, k := range kids {
		c, d := u.cost(k, level+1)
		total += c
		depth = max(depth, d)
	}
	return total, depth
}

// imageBytes sums the files of the distinct image assets a page uses.
func (u *unit) imageBytes(pg *page) int64 {
	seen := map[[16]byte]bool{}
	var total int64
	var walk func(v *value)
	walk = func(v *value) {
		if v == nil {
			return
		}
		if v.kind == fbs.ValueKindAsset && !seen[v.uuid] {
			seen[v.uuid] = true
			if a, ok := u.assetIDs[uuidString(v.uuid)]; ok && strings.HasPrefix(string(a.MediaType), "image/") {
				total += int64(len(u.project.AssetFiles[a.File]))
			}
		}
		for _, it := range v.items {
			walk(it)
		}
		for _, e := range v.entries {
			walk(e.value)
		}
	}
	for _, n := range pg.nodes {
		for _, p := range n.props {
			walk(p.value)
		}
	}
	return total
}

// textNames are the props and value-type fields from which an accessible
// name is derived, such as Text.data or InputDecoration.labelText.
var textNames = map[string]bool{
	"data": true, "label": true, "tooltip": true, "semanticsLabel": true, "semanticLabel": true, "text": true,
	"labelText": true, "hintText": true, "helperText": true,
}

// accessibleName warns when an interactive node has no semantics label
// and no text in its subtree (A11Y-002).
func (u *unit) accessibleName(n *node) {
	if n.widget == nil || !n.widget.Interactive {
		return
	}
	if n.semantics != nil && n.semantics.label != nil {
		return
	}
	if hasText(n) {
		return
	}
	u.report(plxerr.AccessibleNameMissing, n.owner.file(), n.ptr, "%s has no semantics label and no text to announce", n.widget.Type)
}

// hasText reports whether a subtree sets a text prop or field.
func hasText(n *node) bool {
	for _, p := range n.props {
		if textNames[p.name] || valueHasText(p.value) {
			return true
		}
	}
	for _, c := range n.children {
		if hasText(c) {
			return true
		}
	}
	for _, sf := range n.slots {
		for _, c := range sf.nodes {
			if hasText(c) {
				return true
			}
		}
	}
	return false
}

// valueHasText reports whether a value-type object sets a text field.
func valueHasText(v *value) bool {
	if v == nil || v.kind != fbs.ValueKindObject || v.valueType == 0 {
		return false
	}
	for _, vt := range registry.ValueTypes() {
		if vt.ID != v.valueType {
			continue
		}
		for _, e := range v.entries {
			if textNames[fieldName(vt, e.id)] || valueHasText(e.value) {
				return true
			}
		}
	}
	return false
}

// secretPatterns match literals that look like credentials (DAT-003).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN ([A-Z]+ )*PRIVATE KEY-----`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`[sr]k_live_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|secret|password|passwd|access[_-]?token)\s*[:=]\s*\S{8,}`),
}

// lintValue checks the string literals of a value.
func (u *unit) lintValue(file, ptr string, v *value) {
	if v == nil {
		return
	}
	if v.kind == fbs.ValueKindString {
		u.lintString(file, ptr, v.s)
	}
	for i, it := range v.items {
		u.lintValue(file, ptr+"/"+strconv.Itoa(i), it)
	}
	for _, e := range v.entries {
		key := e.key
		if key == "" {
			key = strconv.FormatUint(uint64(e.id), 10)
		}
		u.lintValue(file, ptr+plxerr.Pointer(key), e.value)
	}
}

// lintString reports secret-like values (PLX-1500) and insecure URLs
// (PLX-1502).
func (u *unit) lintString(file, ptr, s string) {
	for _, re := range secretPatterns {
		if re.MatchString(s) {
			u.report(plxerr.SecretLikeValue, file, ptr, "the value looks like a credential; bundles are readable on devices")
			return
		}
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "http://") {
		u.report(plxerr.InsecureURL, file, ptr, "%q uses http:", s)
	}
}

// sensitiveToAnalytics reports trackEvent props that read sensitive
// values (SCH-012, ACT-031).
func (u *unit) sensitiveToAnalytics(g *graph, s *step) {
	if a, _ := registry.LookupAction("trackEvent"); s.action != a.ID {
		return
	}
	sensitive := u.sensitivePaths(g)
	for _, in := range s.inputs {
		if in.name == "props" {
			u.sensitiveReads(g.file, in.ptr, in.value, sensitive)
		}
	}
}

// sensitiveReads reports the expressions of a value that read sensitive
// paths.
func (u *unit) sensitiveReads(file, ptr string, v *value, sensitive []string) {
	if v == nil {
		return
	}
	if v.prog != nil {
		for _, r := range v.prog.Reads {
			if p := sensitiveRead(r, sensitive); p != "" {
				u.report(plxerr.SensitiveValueExposed, file, ptr, "analytics must not receive the sensitive value %s", p)
			}
		}
	}
	for _, e := range v.entries {
		u.sensitiveReads(file, ptr+plxerr.Pointer(e.key), e.value, sensitive)
	}
}

// sensitivePaths lists the root paths of sensitive values a graph sees.
func (u *unit) sensitivePaths(g *graph) []string {
	var out []string
	add := func(root string, entries []schema.StateEntry) {
		for _, e := range entries {
			if e.Sensitive != nil && *e.Sensitive {
				out = append(out, root+"."+e.Name)
			}
		}
	}
	app := u.project.App.Doc
	add("app", app.State)
	for _, f := range app.UserContext {
		if f.Sensitive != nil && *f.Sensitive {
			out = append(out, "user."+f.Name)
		}
	}
	if g.plugin != nil {
		add("plugin", g.plugin.doc.State)
	}
	if g.page != nil {
		add("page", g.page.doc.State)
		for _, p := range g.page.doc.Params {
			if p.Sensitive != nil && *p.Sensitive {
				out = append(out, "params."+p.Name)
			}
		}
	}
	return out
}

// sensitiveRead returns the sensitive path a read covers, or "".
func sensitiveRead(read string, sensitive []string) string {
	for _, s := range sensitive {
		if read == s || strings.HasPrefix(read, s+".") || strings.HasPrefix(s, read+".") {
			return s
		}
	}
	return ""
}

// lintAssets refuses asset files that hold executable code, including
// scripts in SVG images (SEC-054, PLX-1503).
func (u *unit) lintAssets() {
	a := u.project.Assets
	if a == nil || a.Doc == nil {
		return
	}
	for i, e := range a.Doc.Assets {
		data := u.project.AssetFiles[e.File]
		ptr := plxerr.Pointer("assets", strconv.Itoa(i), "file")
		if format := bundle.ExecutableFormat(data); format != "" {
			u.report(plxerr.ExecutableContent, a.Source.File, ptr, "%s holds %s code", e.File, format)
			continue
		}
		if e.MediaType == schema.MediaTypeImageSvgXml && svgScript(data) {
			u.report(plxerr.ExecutableContent, a.Source.File, ptr, "%s contains a script or an event handler", e.File)
		}
	}
}

// svgEvent matches an SVG event-handler attribute such as onload=.
var svgEvent = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)

// svgScript reports scripts, event handlers and javascript: URLs in SVG.
func svgScript(data []byte) bool {
	lower := bytes.ToLower(data)
	return bytes.Contains(lower, []byte("<script")) || bytes.Contains(lower, []byte("javascript:")) || svgEvent.Match(data)
}
