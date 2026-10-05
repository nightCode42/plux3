// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// animationDir is the conformance project of animation (P5 R7), whose
// bundles the runtime's animation tests run.
var animationDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "animation")

const stagePage = "plugins/stage/pages/stage.page.json"

// stageProject returns the animation project in memory.
func stageProject(t testing.TB) fstest.MapFS { return project(t, animationDir) }

// onlyFeatureWarnings fails on any diagnostic but the raised features.
func onlyFeatureWarnings(t *testing.T, res *Result) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Code != plxerr.RequiredFeaturesRaised {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	}
}

// Verifies: CMP-002, QA-003, ANI-001, ANI-002, ANI-003, ANI-004, ANI-006, NAV-010, BND-008.
// A bundle that animates needs anim.v1, anim.timelines.v1 and
// anim.transitions.v1, first in runtime 0.3.0, and is byte-stable.
func TestAnimationGoldenBundles(t *testing.T) {
	t.Parallel()
	res := Compile(stageProject(t), DefaultOptions())
	onlyFeatureWarnings(t, res)
	readAll(t, res)
	for _, f := range []string{"anim.v1", "anim.timelines.v1", "anim.transitions.v1"} {
		if !slices.Contains(res.Plugins[0].Features, f) {
			t.Errorf("plugin features %v lack %s", res.Plugins[0].Features, f)
		}
	}
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "animation", b.Key+".pxb"), b.Data)
	}
}

// timelinesOf returns the decoded timelines section of the plugin.
func timelinesOf(t *testing.T, res *Result) (*fbs.Timelines, func(uint32) string) {
	t.Helper()
	b := readAll(t, res)[1]
	for i := range b.Sections {
		if b.Sections[i].Kind == bundle.SectionTimelines {
			return fbs.GetRootAsTimelines(b.Sections[i].Data, 0), stringsOf(t, b)
		}
	}
	t.Fatal("no timelines section")
	return nil, nil
}

// Verifies: ANI-002, ANI-006, NAV-010, BND-017.
func TestTimelinesSection(t *testing.T) {
	t.Parallel()
	res := Compile(stageProject(t), DefaultOptions())
	onlyFeatureWarnings(t, res)
	tls, str := timelinesOf(t, res)
	if tls.TimelinesLength() != 6 {
		t.Fatalf("%d timelines, want 6", tls.TimelinesLength())
	}
	byName := map[string]*fbs.Timeline{}
	for i := range tls.TimelinesLength() {
		var tl fbs.Timeline
		tls.Timelines(&tl, i)
		cp := tl
		byName[str(tl.Name())] = &cp
	}
	pulse := byName["pulse"]
	if !pulse.Autoplay() || !pulse.Forever() || !pulse.Reverse() || pulse.DurationUs() != 800_000 {
		t.Errorf("pulse = autoplay %v forever %v reverse %v %d us", pulse.Autoplay(), pulse.Forever(), pulse.Reverse(), pulse.DurationUs())
	}
	if byName["rows"].StaggerUs() != 100_000 {
		t.Errorf("rows stagger = %d", byName["rows"].StaggerUs())
	}
	if d := byName["parallax"]; fbs.Driver(d.Driver()) != fbs.DriverScroll || d.DriverExtent() != 200 {
		t.Errorf("parallax driver = %d extent %v", d.Driver(), d.DriverExtent())
	}
	drag := byName["drag"]
	if fbs.Driver(drag.Driver()) != fbs.DriverDrag || drag.Spring(nil) == nil || drag.Spring(nil).Stiffness() != 200 {
		t.Errorf("drag timeline lacks its spring or driver")
	}
	if !byName["slideIn"].Route() {
		t.Errorf("slideIn is not a route timeline")
	}
	var tr fbs.Track
	byName["pulse"].Tracks(&tr, 0)
	if tr.KeyframesLength() != 2 || tr.Node(nil) == nil {
		t.Errorf("pulse track has %d keyframes", tr.KeyframesLength())
	}
}

// Verifies: ANI-001, ANI-003, ANI-004, NAV-010.
func TestNodeAnimationsAreEncoded(t *testing.T) {
	t.Parallel()
	res := Compile(stageProject(t), DefaultOptions())
	onlyFeatureWarnings(t, res)
	pb := readAll(t, res)[1]
	var page *fbs.Page
	for i := range pb.Sections {
		if pb.Sections[i].Kind == bundle.SectionPage {
			page = fbs.GetRootAsPage(pb.Sections[i].Data, 0)
		}
	}
	if page == nil {
		t.Fatal("no page section")
	}
	if page.TransitionTimeline() == 0 {
		t.Error("the page lacks its transition timeline")
	}
	var implicit, enterExit, hero int
	var n fbs.Node
	for i := range page.NodesLength() {
		page.Nodes(&n, i)
		a := n.Animation(nil)
		if a == nil {
			continue
		}
		if a.DurationUs() == 200_000 && a.PropsLength() == 1 {
			implicit++
		}
		if a.Enter(nil) != nil && a.Exit(nil) != nil {
			enterExit++
		}
		if a.Hero(nil) != nil {
			hero++
		}
	}
	if implicit != 1 || enterExit != 1 || hero != 1 {
		t.Errorf("implicit %d, enter/exit %d, hero %d; want one each", implicit, enterExit, hero)
	}
}

// Verifies: CMP-002, ANI-002.
func TestAnimationCompilationIsDeterministic(t *testing.T) {
	t.Parallel()
	a := Compile(stageProject(t), DefaultOptions())
	b := Compile(stageProject(t), DefaultOptions())
	if a.Plugins[0].Hash != b.Plugins[0].Hash {
		t.Error("two compilations differ")
	}
}

// edited compiles the project with the page edited.
func edited(t *testing.T, f func(page map[string]any)) *Result {
	t.Helper()
	m := stageProject(t)
	edit(t, m, stagePage, f)
	return compileFS(m)
}

func timelineDoc(t *testing.T, page map[string]any, i int) map[string]any {
	t.Helper()
	return page["animations"].([]any)[i].(map[string]any)
}

func trackDoc(t *testing.T, page map[string]any, i int) map[string]any {
	t.Helper()
	return timelineDoc(t, page, i)["tracks"].([]any)[0].(map[string]any)
}

// Verifies: ANI-002.
func TestTimelineErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code plxerr.Code
		ptr  string
		do   func(t *testing.T, p map[string]any)
	}{
		{"duplicate name", plxerr.DuplicateKey, "/animations/1/name", func(t *testing.T, p map[string]any) { timelineDoc(t, p, 1)["name"] = "pulse" }},
		{"unknown node", plxerr.AnimationTargetInvalid, "/animations/0/tracks/0/node", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 0)["node"] = "01d0c450-6c00-7000-8000-0000000fffff"
		}},
		{"unknown prop", plxerr.AnimationTargetInvalid, "/animations/0/tracks/0/prop", func(t *testing.T, p map[string]any) { trackDoc(t, p, 0)["prop"] = "nope" }},
		{"not interpolable", plxerr.AnimationTargetInvalid, "/animations/0/tracks/0/prop", func(t *testing.T, p map[string]any) { trackDoc(t, p, 0)["prop"] = "alwaysIncludeSemantics" }},
		{"keyframes out of order", plxerr.AnimationInvalid, "/animations/0/tracks/0/keyframes/1/atMs", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 0)["keyframes"].([]any)[1].(map[string]any)["atMs"] = 0
		}},
		{"keyframe past the end", plxerr.AnimationInvalid, "/animations/0/tracks/0/keyframes/1/atMs", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 0)["keyframes"].([]any)[1].(map[string]any)["atMs"] = 900
		}},
		{"keyframe type", plxerr.AnimationValueInvalid, "/animations/0/tracks/0/keyframes/1/value", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 0)["keyframes"].([]any)[1].(map[string]any)["value"] = "text"
		}},
		{"keyframe binding", plxerr.AnimationValueInvalid, "/animations/0/tracks/0/keyframes/1/value", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 0)["keyframes"].([]any)[1].(map[string]any)["value"] = raw(t, `{"$expr": "1.0"}`)
		}},
		{"driver without node", plxerr.AnimationInvalid, "/animations/3/driver/node", func(t *testing.T, p map[string]any) {
			timelineDoc(t, p, 3)["driver"].(map[string]any)["node"] = "01d0c450-6c00-7000-8000-0000000fffff"
		}},
		{"driven autoplay", plxerr.AnimationInvalid, "/animations/3", func(t *testing.T, p map[string]any) { timelineDoc(t, p, 3)["autoplay"] = true }},
		{"duration limit", plxerr.AnimationInvalid, "/animations/0/durationMs", func(t *testing.T, p map[string]any) {
			timelineDoc(t, p, 0)["durationMs"] = 600000
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := edited(t, func(p map[string]any) { c.do(t, p) })
			wantDiag(t, res, c.code, stagePage, c.ptr)
			if res.App != nil {
				t.Error("a bundle was produced")
			}
		})
	}
}

// Verifies: NAV-010.
func TestRouteTransitionErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ptr  string
		do   func(t *testing.T, p map[string]any)
	}{
		{"custom without timeline", "/routeOptions/transition", func(t *testing.T, p map[string]any) { delete(p["routeOptions"].(map[string]any), "timeline") }},
		{"unknown timeline", "/routeOptions/timeline", func(t *testing.T, p map[string]any) { p["routeOptions"].(map[string]any)["timeline"] = "nope" }},
		{"page timeline", "/routeOptions/timeline", func(t *testing.T, p map[string]any) { p["routeOptions"].(map[string]any)["timeline"] = "pulse" }},
		{"timeline without custom", "/routeOptions/timeline", func(t *testing.T, p map[string]any) { p["routeOptions"].(map[string]any)["transition"] = "fade" }},
		{"node in route timeline", "/animations/5/tracks/0/node", func(t *testing.T, p map[string]any) {
			trackDoc(t, p, 5)["node"] = "01d0c450-6c00-7000-8000-000000000134"
		}},
		{"route prop", "/animations/5/tracks/0/prop", func(t *testing.T, p map[string]any) { trackDoc(t, p, 5)["prop"] = "rotate" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := edited(t, func(p map[string]any) { c.do(t, p) })
			wantDiag(t, res, plxerr.TransitionInvalid, stagePage, c.ptr)
		})
	}
}

// findNode finds a node of a document by test ID.
func findNode(cur any, testID string) map[string]any {
	switch x := cur.(type) {
	case map[string]any:
		if x["testId"] == testID {
			return x
		}
		for _, v := range x {
			if n := findNode(v, testID); n != nil {
				return n
			}
		}
	case []any:
		for _, v := range x {
			if n := findNode(v, testID); n != nil {
				return n
			}
		}
	}
	return nil
}

// Verifies: ANI-001, ANI-003, ANI-004.
func TestNodeAnimationErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		testID string
		ptrEnd string
		do     func(a map[string]any, n map[string]any)
	}{
		{"unknown prop", "implicit", "/animation/props/0", func(a, _ map[string]any) { a["props"] = []any{"nope"} }},
		{"prop not interpolable", "implicit", "/animation/props/0", func(a, _ map[string]any) { a["props"] = []any{"alwaysIncludeSemantics"} }},
		{"props without duration", "implicit", "/animation", func(a, _ map[string]any) { delete(a, "durationMs") }},
		{"empty hero", "hero", "/animation/hero", func(a, _ map[string]any) { a["hero"] = "" }},
		{"duplicate hero", "presence", "/animation/hero", func(a, _ map[string]any) { a["hero"] = "banner" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := edited(t, func(p map[string]any) {
				n := findNode(p["root"], c.testID)
				a, ok := n["animation"].(map[string]any)
				if !ok {
					a = map[string]any{}
					n["animation"] = a
				}
				c.do(a, n)
			})
			found := false
			for _, d := range res.Diagnostics {
				if d.Code == plxerr.NodeAnimationInvalid && d.File == stagePage {
					found = true
				}
			}
			if !found {
				t.Errorf("no PLX-1217; got:\n%s", list(res.Diagnostics))
			}
		})
	}
}

// Verifies: ANI-003.
func TestRootCannotEnter(t *testing.T) {
	t.Parallel()
	res := edited(t, func(p map[string]any) {
		p["root"].(map[string]any)["animation"] = raw(t, `{"enter": {"kind": "fade"}}`)
	})
	wantDiag(t, res, plxerr.NodeAnimationInvalid, stagePage, "/root/animation/enter")
}

// tightened compiles the project with one limit lowered.
func tightened(t *testing.T, key limits.Key, v int64, f func(page map[string]any)) *Result {
	t.Helper()
	m := stageProject(t)
	if f != nil {
		edit(t, m, stagePage, f)
	}
	opts := DefaultOptions()
	set, err := opts.Limits.Tighten(key, limits.ScopeApp, v)
	if err != nil {
		t.Fatal(err)
	}
	opts.Limits = set
	return Compile(m, opts)
}

// Verifies: ANI-008.
func TestExpensiveAnimationWarnings(t *testing.T) {
	t.Parallel()
	t.Run("opacity over a subtree", func(t *testing.T) {
		t.Parallel()
		res := tightened(t, limits.AnimCompositedSubtree, 0+1, func(p map[string]any) {
			n := findNode(p["root"], "implicit")
			n["animation"] = raw(t, `{"durationMs": 200, "props": ["opacity"]}`)
			n["slots"].(map[string]any)["child"] = raw(t, `{"id": "01d0c450-6c00-7000-8000-000000000170", "type": "Column", "children": [
				{"id": "01d0c450-6c00-7000-8000-000000000171", "type": "Text", "props": {"data": "a"}},
				{"id": "01d0c450-6c00-7000-8000-000000000172", "type": "Text", "props": {"data": "b"}}]}`)
		})
		wantDiag(t, res, plxerr.AnimationOpacitySubtree, stagePage, "/root")
	})
	t.Run("fade over a subtree", func(t *testing.T) {
		t.Parallel()
		res := tightened(t, limits.AnimCompositedSubtree, 1, func(p map[string]any) {
			n := findNode(p["root"], "implicit")
			n["animation"] = raw(t, `{"enter": {"kind": "fade"}}`)
			n["slots"].(map[string]any)["child"] = raw(t, `{"id": "01d0c450-6c00-7000-8000-000000000170", "type": "Column", "children": [
				{"id": "01d0c450-6c00-7000-8000-000000000171", "type": "Text", "props": {"data": "a"}},
				{"id": "01d0c450-6c00-7000-8000-000000000172", "type": "Text", "props": {"data": "b"}}]}`)
		})
		wantDiag(t, res, plxerr.AnimationOpacitySubtree, stagePage, "/root")
	})
	t.Run("layout prop of a container", func(t *testing.T) {
		t.Parallel()
		res := tightened(t, limits.AnimCompositedSubtree, 1, func(p map[string]any) {
			n := findNode(p["root"], "dragged")
			n["slots"].(map[string]any)["child"] = raw(t, `{"id": "01d0c450-6c00-7000-8000-00000000013c", "type": "SizedBox", "props": {"width": 80.0}, "animation": {"durationMs": 100, "props": ["width"]},
				"slots": {"child": {"id": "01d0c450-6c00-7000-8000-000000000173", "type": "Column", "children": [
					{"id": "01d0c450-6c00-7000-8000-000000000174", "type": "Text", "props": {"data": "a"}},
					{"id": "01d0c450-6c00-7000-8000-000000000175", "type": "Text", "props": {"data": "b"}}]}}}`)
		})
		wantDiag(t, res, plxerr.AnimationExpensive, stagePage, "/root")
	})
	t.Run("too many timelines", func(t *testing.T) {
		t.Parallel()
		res := tightened(t, limits.AnimSimultaneousTimelines, 2, nil)
		wantDiag(t, res, plxerr.AnimationTooManyTimelines, stagePage, "/animations")
	})
}

// Verifies: ANI-008, CMP-040.
func TestAnimationBudget(t *testing.T) {
	t.Parallel()
	// The page runs six timelines and one implicit animation.
	res := tightened(t, limits.PageAnimations, 5, nil)
	wantDiag(t, res, plxerr.PageAnimationBudget, stagePage, "/root")
	if res.App != nil {
		t.Error("a bundle was produced above the animation budget")
	}
	ok := tightened(t, limits.PageAnimations, 7, nil)
	for _, d := range ok.Diagnostics {
		if d.Code == plxerr.PageAnimationBudget {
			t.Errorf("reported within the budget: %s", d)
		}
	}
}

// Verifies: ANI-001, BND-008.
// A project whose minRuntimeVersion forbids the features refuses to use
// animation instead of raising them.
func TestAnimationNeedsRuntime(t *testing.T) {
	t.Parallel()
	m := stageProject(t)
	edit(t, m, "app.json", func(app map[string]any) { app["requiredFeatures"] = "reject" })
	res := compileFS(m)
	wantDiag(t, res, plxerr.RuntimeTooOld, stagePage, "/animations")
}
