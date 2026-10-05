// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"slices"
	"strconv"
	"strings"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Features a bundle raises when it animates (BND-008): implicit, enter
// and exit and hero animation need "anim"; timelines "anim.timelines";
// custom route transitions "anim.transitions". Animation first runs in
// runtime 0.3.0.
const (
	animFeature            = "anim"
	animTimelinesFeature   = "anim.timelines"
	animTransitionsFeature = "anim.transitions"
)

// animRuntimes lists the runtime each revision of the animation features
// first shipped in.
var animRuntimes = []string{"0.3.0"}

// curveIDs are the permanent IDs of the curves (ANI-001); the runtime's
// table lists them in the same order.
var curveIDs = map[schema.Curve]uint32{
	schema.CurveLinear: 1, schema.CurveEaseIn: 2, schema.CurveEaseOut: 3, schema.CurveEaseInOut: 4,
	schema.CurveFastOutSlowIn: 5, schema.CurveDecelerate: 6, schema.CurveBounceIn: 7, schema.CurveBounceOut: 8,
	schema.CurveElasticOut: 9, schema.CurveOvershoot: 10,
}

// transitionKinds are the IDs of the enter and exit transitions (ANI-003).
var transitionKinds = map[schema.AnimTransitionKind]fbs.TransitionKind{
	schema.AnimTransitionKindFade: fbs.TransitionKindFade, schema.AnimTransitionKindScale: fbs.TransitionKindScale, schema.AnimTransitionKindSlideUp: fbs.TransitionKindSlideUp,
	schema.AnimTransitionKindSlideDown: fbs.TransitionKindSlideDown, schema.AnimTransitionKindSlideLeft: fbs.TransitionKindSlideLeft, schema.AnimTransitionKindSlideRight: fbs.TransitionKindSlideRight,
}

// routeProps are the props a route timeline animates, with their IDs
// (NAV-010): opacity, scale, and the slide as a fraction of the page.
var routeProps = map[string]uint32{"opacity": 1, "scale": 2, "slideX": 3, "slideY": 4}

// layoutProps are the props whose change lays the node out again (ANI-008).
var layoutProps = map[string]bool{
	"width": true, "height": true, "padding": true, "margin": true, "spacing": true, "runSpacing": true,
	"flex": true, "constraints": true, "minWidth": true, "minHeight": true, "maxWidth": true, "maxHeight": true,
	"size": true, "left": true, "right": true, "top": true, "bottom": true, "gap": true,
}

// defaultDurationMs is the duration of an enter or exit transition that
// names none.
const defaultDurationMs = 300

// timeline is a checked timeline of a page (ANI-002).
type timeline struct {
	doc    *schema.Timeline
	id     [16]byte
	ptr    string
	tracks []*track
	driver *node
}

// track is a checked track.
type track struct {
	node      *node
	prop      uint32
	keyframes []*keyframe
}

// keyframe is a checked keyframe.
type keyframe struct {
	atUs  int64
	value *value
	curve uint32
}

// nodeAnim is a node's checked animation (ANI-001, ANI-003, ANI-004).
type nodeAnim struct {
	durationUs, delayUs int64
	curve               uint32
	props               []uint32
	reduce              fbs.ReduceMotion
	enter, exit         *transitionSpec
	hero                *value
}

// transitionSpec is an enter or exit transition.
type transitionSpec struct {
	kind       fbs.TransitionKind
	durationUs int64
	curve      uint32
}

// animatable reports whether a prop type can be interpolated.
func animatable(typ string) bool {
	switch strings.TrimSuffix(typ, "?") {
	case "double", "int", "color":
		return true
	}
	return false
}

func curveOf(c schema.Curve) uint32 { return curveIDs[c] }

func reduceOf(r schema.ReduceMotion) fbs.ReduceMotion {
	switch r {
	case schema.ReduceMotionShorten:
		return fbs.ReduceMotionShorten
	case schema.ReduceMotionIgnore:
		return fbs.ReduceMotionIgnore
	}
	return fbs.ReduceMotionSkip
}

// subtreeSize counts the nodes below n.
func subtreeSize(n *node) int {
	total := 0
	for _, c := range n.children {
		total += 1 + subtreeSize(c)
	}
	for _, sf := range n.slots {
		for _, c := range sf.nodes {
			total += 1 + subtreeSize(c)
		}
	}
	return total
}

// checkNodeAnimation checks the animation block of a node (ANI-001,
// ANI-003, ANI-004).
func (u *unit) checkNodeAnimation(n *node, sh *shape, c vctx) {
	a := n.doc.Animation
	if a == nil {
		return
	}
	ptr := n.ptr + "/animation"
	u.useRevision(animFeature, animRuntimes, 1, vctx{file: c.file, ptr: ptr, pl: c.pl})
	out := &nodeAnim{curve: curveOf(a.Curve), reduce: reduceOf(a.ReduceMotion)}
	if a.DurationMs != nil {
		out.durationUs = *a.DurationMs * 1000
	}
	if a.DelayMs != nil {
		out.delayUs = *a.DelayMs * 1000
	}
	if a.DurationMs == nil && (len(a.Props) > 0 || a.Curve != "" || a.DelayMs != nil) {
		u.report(plxerr.NodeAnimationInvalid, c.file, ptr, "props, curve and delayMs of an implicit animation need a durationMs")
	}
	for i, name := range a.Props {
		pptr := ptr + "/props/" + strconv.Itoa(i)
		m, ok := findMember(sh.props, name)
		switch {
		case n.widget == nil:
			u.report(plxerr.NodeAnimationInvalid, c.file, pptr, "only widget props animate implicitly")
		case !ok:
			u.report(plxerr.NodeAnimationInvalid, c.file, pptr, "%s has no prop %q", sh.what, name)
		case !animatable(m.typ):
			u.report(plxerr.NodeAnimationInvalid, c.file, pptr, "prop %q of type %s cannot be interpolated; numbers and colours animate", name, m.typ)
		default:
			out.props = append(out.props, m.id)
			u.warnExpensiveProp(n, name, c.file, pptr)
		}
	}
	slices.Sort(out.props)
	out.enter = u.checkTransition(n, a.Enter, "enter", c)
	out.exit = u.checkTransition(n, a.Exit, "exit", c)
	if len(a.Hero) > 0 {
		out.hero = u.checkRaw(vctx{file: c.file, ptr: ptr + "/hero", scope: n.scope, pl: c.pl, code: plxerr.NodeAnimationInvalid, from: c.from}, a.Hero, &texpr{name: "string"})
		if out.hero != nil && out.hero.kind == fbs.ValueKindString && out.hero.s == "" {
			u.report(plxerr.NodeAnimationInvalid, c.file, ptr+"/hero", "a hero tag must not be empty")
		}
	}
	n.anim = out
}

// checkTransition checks an enter or exit transition.
func (u *unit) checkTransition(n *node, t *schema.AnimTransition, what string, c vctx) *transitionSpec {
	if t == nil {
		return nil
	}
	ptr := n.ptr + "/animation/" + what
	if n.parent == nil {
		u.report(plxerr.NodeAnimationInvalid, c.file, ptr, "the root of a page cannot have an %s transition; put it on an inner node", what)
	}
	if t.Kind == schema.AnimTransitionKindFade && subtreeSize(n) > int(u.opts.Limits.Get(limits.AnimCompositedSubtree)) {
		u.report(plxerr.AnimationOpacitySubtree, c.file, ptr, "a fade over %d nodes draws them into an offscreen layer every frame; fade the leaves or use a slide", subtreeSize(n))
	}
	spec := &transitionSpec{kind: transitionKinds[t.Kind], durationUs: defaultDurationMs * 1000, curve: curveOf(t.Curve)}
	if t.DurationMs != nil {
		spec.durationUs = *t.DurationMs * 1000
	}
	return spec
}

// warnExpensiveProp warns about animating a prop that re-lays out (or
// composites) a large subtree every frame (ANI-008).
func (u *unit) warnExpensiveProp(n *node, prop, file, ptr string) {
	size := subtreeSize(n)
	if size <= int(u.opts.Limits.Get(limits.AnimCompositedSubtree)) {
		return
	}
	switch {
	case layoutProps[prop]:
		u.report(plxerr.AnimationExpensive, file, ptr, "animating %q lays out %d nodes on every frame; animate a transform (scale, slide) or opacity instead", prop, size)
	case prop == "opacity":
		u.report(plxerr.AnimationOpacitySubtree, file, ptr, "animating opacity over %d nodes draws them into an offscreen layer on every frame; fade the leaves or use a transform", size)
	}
}

// checkAnimations checks a page's timelines and the hero tags of its nodes
// (ANI-002, ANI-004, ANI-006, NAV-010, ANI-008).
func (u *unit) checkAnimations(pg *page) {
	doc, file := pg.doc, pg.file
	byID := map[string]*node{}
	for _, n := range pg.nodes {
		byID[n.doc.ID] = n
	}
	names := u.newKeys("timeline")
	for i := range doc.Animations {
		t := &doc.Animations[i]
		ptr := plxerr.Pointer("animations", strconv.Itoa(i))
		names.claim(t.Name, file, ptr+"/name")
		u.useRevision(animTimelinesFeature, animRuntimes, 1, vctx{file: file, ptr: ptr, pl: pg.plugin})
		if tl := u.checkTimeline(pg, t, ptr, byID); tl != nil {
			pg.timelines = append(pg.timelines, tl)
		}
	}
	u.checkTimelineLoad(pg)
	u.checkRouteTimeline(pg)
	u.checkHeroTags(pg)
}

// checkTimeline checks one timeline.
func (u *unit) checkTimeline(pg *page, t *schema.Timeline, ptr string, byID map[string]*node) *timeline {
	file := pg.file
	tl := &timeline{doc: t, id: uuidBytes(t.ID), ptr: ptr}
	if max := u.opts.Limits.Get(limits.AnimTimelineDuration); t.DurationMs > max {
		u.report(plxerr.AnimationInvalid, file, ptr+"/durationMs", "the timeline lasts %d ms; the limit %s is %d", t.DurationMs, limits.AnimTimelineDuration, max)
	}
	route := t.Scope == schema.TimelineScopeRoute
	if route && (t.Driver != nil || t.Autoplay != nil && *t.Autoplay) {
		u.report(plxerr.TransitionInvalid, file, ptr, "a route timeline plays with its route; it has no driver and no autoplay")
	}
	if t.Driver != nil {
		tl.driver = byID[t.Driver.Node]
		if tl.driver == nil {
			u.report(plxerr.AnimationInvalid, file, ptr+"/driver/node", "the driver names no node of the page")
		}
		if t.Autoplay != nil && *t.Autoplay {
			u.report(plxerr.AnimationInvalid, file, ptr, "a driven timeline follows its driver and cannot autoplay")
		}
	}
	for i := range t.Tracks {
		tptr := ptr + "/tracks/" + strconv.Itoa(i)
		if tr := u.checkTrack(pg, t, &t.Tracks[i], tptr, route, byID); tr != nil {
			tl.tracks = append(tl.tracks, tr)
		}
	}
	return tl
}

// checkTrack checks one track of a timeline.
func (u *unit) checkTrack(pg *page, t *schema.Timeline, tk *schema.Track, ptr string, route bool, byID map[string]*node) *track {
	file := pg.file
	tr := &track{}
	var typ string
	cons := vctx{file: file, ptr: ptr, pl: pg.plugin, code: plxerr.AnimationValueInvalid}
	if route {
		id, ok := routeProps[tk.Prop]
		switch {
		case tk.Node != "":
			u.report(plxerr.TransitionInvalid, file, ptr+"/node", "a route timeline animates the page, not a node")
			return nil
		case !ok:
			u.report(plxerr.TransitionInvalid, file, ptr+"/prop", "a route timeline animates opacity, scale, slideX or slideY, not %q", tk.Prop)
			return nil
		}
		tr.prop, typ = id, "double"
	} else {
		n := byID[tk.Node]
		if tk.Node == "" || n == nil || n.widget == nil {
			u.report(plxerr.AnimationTargetInvalid, file, ptr+"/node", "the track names no widget node of the page")
			return nil
		}
		m, ok := findMember(widgetShape(n.widget).props, tk.Prop)
		switch {
		case !ok:
			u.report(plxerr.AnimationTargetInvalid, file, ptr+"/prop", "%s has no prop %q", n.widget.Type, tk.Prop)
			return nil
		case !animatable(m.typ):
			u.report(plxerr.AnimationTargetInvalid, file, ptr+"/prop", "prop %q of type %s cannot be interpolated; numbers and colours animate", tk.Prop, m.typ)
			return nil
		}
		tr.node, tr.prop, typ = n, m.id, strings.TrimSuffix(m.typ, "?")
		cons.constraints = m.cons
		n.animated = true
		u.warnExpensiveProp(n, tk.Prop, file, ptr+"/prop")
	}
	te := &texpr{name: typ}
	if max := int(u.opts.Limits.Get(limits.AnimKeyframesPerTrack)); len(tk.Keyframes) > max {
		u.report(plxerr.AnimationInvalid, file, ptr+"/keyframes", "the track has %d keyframes; the limit %s is %d", len(tk.Keyframes), limits.AnimKeyframesPerTrack, max)
		return nil
	}
	last := int64(-1)
	for i, kf := range tk.Keyframes {
		kptr := ptr + "/keyframes/" + strconv.Itoa(i)
		if kf.AtMs <= last || kf.AtMs > t.DurationMs {
			u.report(plxerr.AnimationInvalid, file, kptr+"/atMs", "keyframes must be in increasing time within the timeline's %d ms", t.DurationMs)
			return nil
		}
		last = kf.AtMs
		c := cons
		c.ptr = kptr + "/value"
		v := u.checkRaw(c, kf.Value, te)
		if v == nil {
			return nil
		}
		if isBindingRaw(kf.Value) || v.kind == fbs.ValueKindExpr || v.kind == fbs.ValueKindToken {
			u.report(plxerr.AnimationValueInvalid, file, c.ptr, "a keyframe is a literal; bindings do not animate")
			return nil
		}
		tr.keyframes = append(tr.keyframes, &keyframe{atUs: kf.AtMs * 1000, value: v, curve: curveOf(kf.Curve)})
	}
	return tr
}

// checkTimelineLoad enforces the page's animation budget (CMP-040) and
// warns about timelines that run together (ANI-008).
func (u *unit) checkTimelineLoad(pg *page) {
	running := 0
	for _, tl := range pg.timelines {
		if tl.doc.Driver != nil || tl.doc.Autoplay != nil && *tl.doc.Autoplay {
			running++
		}
	}
	if max := int(u.opts.Limits.Get(limits.AnimSimultaneousTimelines)); running > max {
		u.report(plxerr.AnimationTooManyTimelines, pg.file, "/animations", "%d timelines autoplay or follow a driver; above %d they all tick on every frame", running, max)
	}
}

// animationCount counts the animations a page can run at once: its
// timelines and the nodes that animate implicitly.
func animationCount(pg *page) int64 {
	total := int64(len(pg.timelines))
	for _, n := range pg.nodes {
		if n.anim != nil && n.anim.durationUs > 0 {
			total++
		}
	}
	return total
}

// checkRouteTimeline checks the custom transition of a page (NAV-010).
func (u *unit) checkRouteTimeline(pg *page) {
	ro := pg.doc.RouteOptions
	file := pg.file
	custom := ro != nil && ro.Transition == schema.TransitionCustom
	switch {
	case custom && ro.Timeline == "":
		u.report(plxerr.TransitionInvalid, file, "/routeOptions/transition", "a custom transition names its route timeline in routeOptions.timeline")
	case !custom && ro != nil && ro.Timeline != "":
		u.report(plxerr.TransitionInvalid, file, "/routeOptions/timeline", "routeOptions.timeline belongs to the custom transition")
	case custom:
		u.useRevision(animTransitionsFeature, animRuntimes, 1, vctx{file: file, ptr: "/routeOptions/transition", pl: pg.plugin})
		found := false
		for _, tl := range pg.timelines {
			if tl.doc.Name == ro.Timeline {
				found = tl.doc.Scope == schema.TimelineScopeRoute
			}
		}
		if !found {
			u.report(plxerr.TransitionInvalid, file, "/routeOptions/timeline", "the page has no route timeline named %q", ro.Timeline)
		}
	}
}

// checkHeroTags reports literal hero tags a page uses twice: Flutter
// refuses two heroes with one tag in a route.
func (u *unit) checkHeroTags(pg *page) {
	seen := map[string]bool{}
	for _, n := range pg.nodes {
		if n.anim == nil || n.anim.hero == nil || n.anim.hero.kind != fbs.ValueKindString {
			continue
		}
		tag := n.anim.hero.s
		if seen[tag] {
			u.report(plxerr.NodeAnimationInvalid, pg.file, n.ptr+"/animation/hero", "the hero tag %q is used twice on the page; bind it to the item to tell repeated nodes apart", tag)
		}
		seen[tag] = true
	}
}

// timelinesSection writes the timelines of a plugin's pages (BND-017).
func (u *unit) timelinesSection(o *out, pages []*page) {
	var all []*timeline
	var owners []*page
	for _, pg := range pages {
		for _, tl := range pg.timelines {
			all = append(all, tl)
			owners = append(owners, pg)
		}
	}
	if len(all) == 0 {
		return
	}
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return bytes.Compare(all[a].id[:], all[b].id[:]) })
	b := flatbuffers.NewBuilder(2048)
	e := &valueEnc{u: u, o: o, b: b, strs: o.shared}
	offs := make([]flatbuffers.UOffsetT, len(all))
	for i, k := range order {
		offs[i] = timelineTable(e, all[k], owners[k])
	}
	tv := offsetVector(b, offs)
	fbs.TimelinesStart(b)
	fbs.TimelinesAddTimelines(b, tv)
	o.add(bundle.SectionTimelines, o.id, finish(b, fbs.TimelinesEnd(b), bundle.SectionTimelines))
}

// timelineTable writes one timeline.
func timelineTable(e *valueEnc, tl *timeline, pg *page) flatbuffers.UOffsetT {
	b, t := e.b, tl.doc
	tracks := make([]flatbuffers.UOffsetT, len(tl.tracks))
	for i, tr := range tl.tracks {
		tracks[i] = trackTable(e, tr)
	}
	tv := offsetVector(b, tracks)
	name := e.strs.of(t.Name)
	var spring flatbuffers.UOffsetT
	if t.Spring != nil {
		fbs.SpringStart(b)
		fbs.SpringAddStiffness(b, floatOr(t.Spring.Stiffness, 180))
		fbs.SpringAddDamping(b, floatOr(t.Spring.Damping, 20))
		fbs.SpringAddMass(b, floatOr(t.Spring.Mass, 1))
		spring = fbs.SpringEnd(b)
	}
	fbs.TimelineStart(b)
	hi, lo := uuidHalves(tl.id)
	fbs.TimelineAddId(b, fbs.CreateUuid(b, hi, lo))
	fbs.TimelineAddDurationUs(b, t.DurationMs*1000)
	if t.Repeat != nil {
		fbs.TimelineAddRepeat(b, uint32(*t.Repeat)) //nolint:gosec // G115: at most 1000 by the schema.
	}
	fbs.TimelineAddTracks(b, tv)
	fbs.TimelineAddName(b, name)
	phi, plo := uuidHalves(uuidBytes(pg.doc.ID))
	fbs.TimelineAddPage(b, fbs.CreateUuid(b, phi, plo))
	if t.DelayMs != nil {
		fbs.TimelineAddDelayUs(b, *t.DelayMs*1000)
	}
	fbs.TimelineAddForever(b, t.RepeatForever != nil && *t.RepeatForever)
	fbs.TimelineAddReverse(b, t.Reverse != nil && *t.Reverse)
	if t.StaggerMs != nil {
		fbs.TimelineAddStaggerUs(b, *t.StaggerMs*1000)
	}
	fbs.TimelineAddReduceMotion(b, reduceOf(t.ReduceMotion))
	fbs.TimelineAddRoute(b, t.Scope == schema.TimelineScopeRoute)
	fbs.TimelineAddAutoplay(b, t.Autoplay != nil && *t.Autoplay)
	if t.Driver != nil {
		fbs.TimelineAddDriver(b, driverOf(t.Driver.Kind))
		if tl.driver != nil {
			dhi, dlo := uuidHalves(uuidBytes(tl.driver.doc.ID))
			fbs.TimelineAddDriverNode(b, fbs.CreateUuid(b, dhi, dlo))
		}
		fbs.TimelineAddDriverExtent(b, t.Driver.Extent)
	}
	if spring != 0 {
		fbs.TimelineAddSpring(b, spring)
	}
	return fbs.TimelineEnd(b)
}

func floatOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func driverOf(k schema.TimelineDriverKind) fbs.Driver {
	if k == schema.TimelineDriverKindDrag {
		return fbs.DriverDrag
	}
	return fbs.DriverScroll
}

// trackTable writes one track.
func trackTable(e *valueEnc, tr *track) flatbuffers.UOffsetT {
	b := e.b
	kfs := make([]flatbuffers.UOffsetT, len(tr.keyframes))
	for i, kf := range tr.keyframes {
		v := e.value(kf.value)
		fbs.KeyframeStart(b)
		fbs.KeyframeAddAtUs(b, kf.atUs)
		fbs.KeyframeAddValue(b, v)
		if kf.curve != 0 {
			fbs.KeyframeAddCurve(b, kf.curve)
		}
		kfs[i] = fbs.KeyframeEnd(b)
	}
	kv := offsetVector(b, kfs)
	fbs.TrackStart(b)
	fbs.TrackAddProp(b, tr.prop)
	fbs.TrackAddKeyframes(b, kv)
	if tr.node != nil {
		hi, lo := uuidHalves(uuidBytes(tr.node.doc.ID))
		fbs.TrackAddNode(b, fbs.CreateUuid(b, hi, lo))
	}
	return fbs.TrackEnd(b)
}

// nodeAnimationTable writes a node's animation, or returns 0.
func nodeAnimationTable(e *valueEnc, n *node) flatbuffers.UOffsetT {
	a := n.anim
	if a == nil {
		return 0
	}
	b := e.b
	props := u32Vector(b, a.props)
	hero := e.value(a.hero)
	enter, exit := transitionTable(b, a.enter), transitionTable(b, a.exit)
	fbs.NodeAnimationStart(b)
	fbs.NodeAnimationAddDurationUs(b, a.durationUs)
	if a.curve != 0 {
		fbs.NodeAnimationAddCurve(b, a.curve)
	}
	fbs.NodeAnimationAddDelayUs(b, a.delayUs)
	fbs.NodeAnimationAddProps(b, props)
	fbs.NodeAnimationAddReduceMotion(b, a.reduce)
	if enter != 0 {
		fbs.NodeAnimationAddEnter(b, enter)
	}
	if exit != 0 {
		fbs.NodeAnimationAddExit(b, exit)
	}
	if hero != 0 {
		fbs.NodeAnimationAddHero(b, hero)
	}
	return fbs.NodeAnimationEnd(b)
}

// transitionTable writes an enter or exit transition, or returns 0.
func transitionTable(b *flatbuffers.Builder, t *transitionSpec) flatbuffers.UOffsetT {
	if t == nil {
		return 0
	}
	fbs.TransitionStart(b)
	fbs.TransitionAddKind(b, t.kind)
	fbs.TransitionAddDurationUs(b, t.durationUs)
	if t.curve != 0 {
		fbs.TransitionAddCurve(b, t.curve)
	}
	return fbs.TransitionEnd(b)
}
