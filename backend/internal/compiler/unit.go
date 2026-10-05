// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/nightCode42/plux3/backend/internal/icons"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// unit is the state of one compilation: the loaded project, the index the
// resolve stage builds, the intermediate representation the later stages
// refine, and the outputs.
type unit struct {
	opts    Options
	project *schema.Project
	diags   plxerr.Diagnostics
	// focus is the page file ValidatePage checks: the checking stages
	// then skip every other page, component and flow. Empty in Compile.
	focus string

	// Built by resolve.
	ids         map[string]plxerr.Location       // every entity ID, for duplicates
	plugins     []*plugin                        // sorted by key
	pages       map[string]*page                 // by ID
	routes      map[string]*route                // by route name (SCH-025)
	hostEvents  map[string]*schema.HostEventDecl // by name (HST-013)
	tabValues   map[string][2]*value             // a shell tab's label and icon by pointer (NAV-006)
	graphs      map[string]*graph                // document graphs by ID
	components  map[string]*component            // by ID
	shared      []*component                     // app-level components, by file
	appGraphs   []*graph                         // inline graphs of shared components and app triggers
	appTriggers []*trigger                       // the app's triggers (ACT-002)
	tkeys       map[string]*schema.TranslationKey
	tokens      map[string]*token
	assetIDs    map[string]*schema.AssetEntry
	natives     natives
	types       *universe
	graph       *Graph

	// features are the required features raised per bundle (nil: the app
	// bundle) by WGT-004.
	features map[*plugin]map[string]bool
	// stored are the session, persisted and secure state entries per
	// bundle (nil: the app bundle), for STA-040.
	stored map[*plugin][]StoredEntry
	// icons are the icons used per bundle (nil: the app bundle), by set,
	// for the bundle's icon fonts (THM-005).
	icons map[*plugin]map[icons.Set]map[string]bool
	// files are the asset files the compilation made, by SHA-256.
	files map[[sha256.Size]byte][]byte

	// Built by typecheck and semantic.
	appScope   *scope
	appState   []*stateEntry
	appSources []*dataSource
	envs       map[string]*pxl.Env
	exprs      map[string]*expr // by file + "#" + pointer
	// dataConfigs are the decoded configurations of REST and GraphQL
	// sources, by source ID (ADR-0048).
	dataConfigs map[string]*parsedConfig

	// Encoding state and outputs.
	appOut     *out
	pluginOuts []*out
	app        *Bundle
	outputs    []*Bundle // plugins, by key
}

// newUnit returns an empty unit.
func newUnit(opts Options) *unit {
	return &unit{
		opts: opts, ids: map[string]plxerr.Location{}, pages: map[string]*page{}, routes: map[string]*route{},
		graphs: map[string]*graph{}, components: map[string]*component{}, tkeys: map[string]*schema.TranslationKey{},
		tokens: map[string]*token{}, assetIDs: map[string]*schema.AssetEntry{}, envs: map[string]*pxl.Env{},
		exprs: map[string]*expr{}, graph: &Graph{}, features: map[*plugin]map[string]bool{}, stored: map[*plugin][]StoredEntry{},
		icons: map[*plugin]map[icons.Set]map[string]bool{}, files: map[[sha256.Size]byte][]byte{},
		dataConfigs: map[string]*parsedConfig{},
	}
}

// inFocus reports whether the checking stages check a document of a
// file: every file in Compile, the edited page in ValidatePage.
func (u *unit) inFocus(file string) bool { return u.focus == "" || u.focus == file }

// graphInFocus reports whether the checking stages check a graph: in
// ValidatePage, only the graphs of the edited page, inline or not.
func (u *unit) graphInFocus(g *graph) bool {
	return u.inFocus(g.file) || g.page != nil && g.page.file == u.focus
}

// finishGraph sorts the reference graph; ValidatePage does not return it.
func (u *unit) finishGraph() {
	if u.focus == "" {
		u.graph.finish()
	}
}

// report adds a diagnostic at a JSON Pointer of a file.
func (u *unit) report(code plxerr.Code, file, ptr, format string, args ...any) {
	u.diags = append(u.diags, plxerr.NewDiagnostic(code, plxerr.Location{File: file, Path: ptr}, format, args...))
}

// natives is the native catalogue (SCH-032).
type natives struct {
	routes  map[string]*schema.NativeRoute
	actions map[string]*schema.NativeAction
	slots   map[string]*schema.NativeSlot
}

// plugin is a plugin and what it holds.
type plugin struct {
	doc        *schema.PluginDocument
	file       string
	key        string
	pages      []*page      // in the order of plugin.json
	components []*component // private components, by file
	graphs     []*graph     // document graphs, by file
	inline     []*graph     // inline graphs, in document order
	lowered    []*graph     // every graph of the bundle, by ID
	functions  map[string]bool
	scope      *scope
	state      []*stateEntry
	sources    []*dataSource
	triggers   []*trigger // the plugin's triggers (ACT-002)
}

// route is a route name's target: a page or a native route.
type route struct {
	name   string
	page   *page
	native *schema.NativeRoute
	file   string
	ptr    string
}

// owner is what a node tree belongs to: a page or a component.
type owner struct {
	page      *page
	component *component
}

// file returns the owner's document path.
func (o owner) file() string {
	if o.page != nil {
		return o.page.file
	}
	return o.component.file
}

// plugin returns the owner's plugin, or nil for a shared component.
func (o owner) plugin() *plugin {
	if o.page != nil {
		return o.page.plugin
	}
	return o.component.plugin
}

// page is a page and its compiled parts.
type page struct {
	doc    *schema.PageDocument
	file   string
	plugin *plugin
	route  string
	root   *node
	nodes  []*node // pre-order
	// graphs are the lifecycle handlers' graphs by event name.
	graphs map[string]*graph
	// lifecycle handlers, in LifecycleEvent order.
	lifecycle []*handler
	title     *value
	params    []*param
	state     []*stateEntry
	sources   []*dataSource
	guards    []*graph
	scope     *scope
	triggers  []*trigger // the page's triggers (ACT-002)
	forms     []*form    // the page's forms (STA-020)
}

// component is a component definition (SCH-030).
type component struct {
	doc    *schema.ComponentDocument
	file   string
	plugin *plugin // nil for a shared component
	root   *node
	nodes  []*node
	props  []*param // sorted by name
	state  []*stateEntry
	forms  []*form // the component's forms (STA-020)
}

// node is a node of a page or component tree.
type node struct {
	doc    *schema.Node
	owner  owner
	ptr    string
	parent *node
	// slot is the parent's slot holding the node; "" for children.
	slot string
	// widget is the descriptor of a widget node; target the component of
	// an instance. Both are nil when the type is unknown.
	widget   *registry.Widget
	target   *component
	children []*node
	slots    []*slotFill
	// graphs are the event handlers' graphs by event name.
	graphs map[string]*graph
	scope  *scope
	// typeArgs binds the descriptor's type parameters (e.g. ForEach T).
	typeArgs map[string]*pxl.Type
	// Filled by semantic.
	props     []*prop
	handlers  []*handler
	visible   *value
	semantics *semantics
	overrides []*override
	// Filled by optimise and encode.
	hints   fbs.NodeHints
	removed bool
	index   uint32
}

// slotFill is a filled slot.
type slotFill struct {
	name  string
	id    uint32
	ptr   string
	nodes []*node
}

// prop is a checked prop or input value.
type prop struct {
	name  string
	id    uint32
	ptr   string
	value *value
}

// override is a responsive override layer (WGT-010, BND-016).
type override struct {
	kind  fbs.OverrideKind
	key   string
	props []*prop
}

// semantics are a node's accessibility semantics.
type semantics struct {
	label, hint, value                   *value
	header, button, liveRegion, excluded bool
}

// handler is an event or lifecycle handler bound to a graph.
type handler struct {
	event       uint32
	graph       *graph
	concurrency fbs.Concurrency
	interval    uint32
	detached    bool
}

// graph is an action graph: a document, or inline in a handler.
type graph struct {
	id     [16]byte
	key    string
	file   string
	ptr    string // the steps' parent; "" for documents
	doc    *schema.ActionGraphDocument
	steps  []schema.Step
	plugin *plugin
	page   *page // page-scoped graphs
	// component is the component whose node handler runs an inline graph;
	// emitEvent emits its events (D11).
	component *component
	// eventTypes declares the type `event` names when it is synthesised,
	// such as a host event's payload (ACT-002).
	eventTypes map[string]pxl.TypeSpec
	inline     bool
	// eventType is the payload type every handler running the graph agrees
	// on; "" when they carry none.
	eventType string
	eventSet  bool
	scope     *scope
	lowered   []*step
	navs      []navigation
	output    string
	used      bool
	// state are the run's variables (STA-001).
	state []*stateEntry
}

// step is a lowered step.
type step struct {
	ptr       string
	id        string
	action    uint32
	inputs    []*prop
	next      int32
	onSuccess int32
	onError   int32
	branches  []branch
	retry     *schema.Retry
	timeoutMs uint32
	// redact lists the inputs that read sensitive values (ACT-031).
	redact []uint32
}

// branch is a named successor.
type branch struct {
	name string
	step int32
}

// param is a declared parameter, input or component prop.
type param struct {
	id        [16]byte
	name      string
	typ       string
	required  bool
	def       *value
	sensitive bool
}

// stateEntry is a state entry (STA-*).
type stateEntry struct {
	id          [16]byte
	name        string
	typ         string
	def         *value
	computed    *expr
	persistence fbs.Persistence
	sensitive   bool
	exposed     bool
	// fingerprint versions a stored entry's type; migrationFrom and
	// migration, or migrationReset, say how a value of the previous
	// type is read (STA-040).
	fingerprint    string
	migrationFrom  string
	migrationType  string
	migration      *expr
	migrationReset bool
}

// dataSource is a data source.
type dataSource struct {
	id     [16]byte
	name   string
	kind   fbs.DataSourceKind
	typ    string
	config *value
}

// token is a design token of the theme.
type token struct {
	path  string
	typ   string // $type
	light json.RawMessage
	dark  json.RawMessage
}

// expr is a compiled PXL expression site.
type expr struct {
	src   string
	file  string
	ptr   string
	scope *scope
	prog  *pxl.Program
	typ   *pxl.Type
}

// value is a checked value in the form the encoder writes (BND-015).
type value struct {
	kind     fbs.ValueKind
	i        int64
	offset   int16
	d        float64
	s        string
	unscaled []byte
	scale    uint32
	uuid     [16]byte
	items    []*value
	entries  []entry
	prog     *pxl.Program
	// valueType is the registry value type of an object literal, which
	// the encoder stores as a deduplicated style (CMP-020).
	valueType uint32
}

// entry is a map entry (key) or object field (id for value types, key
// for declared types).
type entry struct {
	key   string
	id    uint32
	value *value
}
