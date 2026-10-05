// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"io/fs"
	"runtime/debug"
	"sync"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/icons"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Mode selects release or development bundles (CMP-041).
type Mode int

// Modes.
const (
	// Release bundles carry no source map; it is returned separately.
	Release Mode = iota
	// Development bundles embed their source map.
	Development
)

// Options configure a compilation.
type Options struct {
	// Limits are the effective limits (LIM-001); use limits.Defaults()
	// unless an installation, organisation, app or plugin tightens them.
	Limits limits.Set
	// Mode selects release or development bundles.
	Mode Mode
	// Version is the compiler version recorded in every bundle (CMP-005).
	Version string
	// AssetVariants returns the transcoded forms of an asset file by the
	// file's SHA-256 (CMP-030); nil, or no variants, lists none. The
	// server supplies the variants its asset pipeline made.
	AssetVariants func(sum [sha256.Size]byte) []AssetVariant
	// IconFont returns the font of an icon set subset to the named icons,
	// which the compiler has checked are the set's (THM-005); nil adds no
	// icon fonts. The server supplies it (package icons/fonts).
	IconFont func(set icons.Set, names []string) ([]byte, error)
}

// AssetVariant is a transcoded form of an asset file.
type AssetVariant struct {
	MediaType string
	Density   int
	Width     int
	Height    int
	Hash      [sha256.Size]byte
	Size      int64
}

// DefaultOptions compiles release bundles with the registry defaults.
func DefaultOptions() Options {
	return Options{Limits: limits.Defaults(), Mode: Release, Version: "dev"}
}

// Bundle is one compiled bundle.
type Bundle struct {
	// Kind is the container's bundle kind.
	Kind bundle.Kind
	// ID is the app or plugin UUID; Key its key.
	ID  [16]byte
	Key string
	// Data is the bundle (Appendix B).
	Data []byte
	// Hash is the bundle hash (BND-005).
	Hash [sha256.Size]byte
	// Features are the bundle's required features, sorted (BND-008).
	Features []string
	// SourceMap is the source-map section of a release bundle, kept by
	// the server for symbolication (CMP-041); nil in development bundles,
	// which embed it.
	SourceMap []byte
}

// Result is the outcome of a compilation. Bundles are nil when the
// diagnostics contain an error.
type Result struct {
	// App is the app bundle; Plugins one bundle per plugin, by key
	// (BND-002).
	App     *Bundle
	Plugins []*Bundle
	// Graph is the reference graph (SCH-041), available whenever
	// resolution ran.
	Graph *Graph
	// Diagnostics are sorted by file, path and range.
	Diagnostics plxerr.Diagnostics
	// Files are the asset files the compilation made, the bundles' icon
	// fonts, by SHA-256; the server stores them with the uploaded ones.
	Files map[[sha256.Size]byte][]byte
	// Natives is the native catalogue the project is compiled against, or
	// nil; HostBuildIncompatibilities checks host builds against it.
	Natives *schema.NativeCatalogueDocument
	// Project is the loaded project, or nil when no app document was
	// found; plux codegen reads its documents (HST-030).
	Project *schema.Project
	// StoredState are the session, persisted and secure state entries of
	// each bundle, by bundle key ("" for the app): publishing compares
	// them with the previous release's (STA-040, CheckStoredState).
	StoredState map[string][]StoredEntry
	// DeviceUses are the steps that run an action of an optional package,
	// sorted; HostBuildLacksPackages checks host builds against them.
	DeviceUses []DeviceUse
}

// stage is one step of the pipeline (CMP-003). Checking stages run even
// after errors, so one compilation reports every problem; producing
// stages run only on a project without errors.
type stage struct {
	name     string
	run      func(*unit)
	checking bool
}

// pipeline lists the stages after loading, in the order of CMP-003.
var pipeline = []stage{
	{"resolve", resolve, true},
	{"typecheck", typecheck, true},
	{"semantic", semantic, true},
	{"policy", policy, true},
	{"optimise", optimise, false},
	{"lower", lower, false},
	{"encode", encode, false},
	{"assets", assets, false},
	{"hash", hash, false},
}

// Compile compiles the project in fsys, laid out as in SCH-006. It never
// panics: an unexpected failure is reported as PLX-2201 (CMP-052).
func Compile(fsys fs.FS, opts Options) (res *Result) {
	res = &Result{}
	defer func() {
		if r := recover(); r != nil {
			res.App, res.Plugins = nil, nil
			res.Diagnostics = append(res.Diagnostics, plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{},
				"the compiler failed unexpectedly: %v\n%s", r, debug.Stack()))
			res.Diagnostics.Sort()
		}
	}()
	u := newUnit(opts)
	v, err := sharedValidator()
	if err != nil {
		u.internalError("the embedded JSON Schemas do not compile: %v", err)
		res.Diagnostics = u.diags
		return res
	}
	loader := schema.NewLoader(v, schema.DefaultMigrator(), opts.Limits)
	project, diags := loader.Load(fsys)
	u.project = project
	u.diags = append(u.diags, diags...)
	run(u, pipeline)
	u.diags.Sort()
	res.Diagnostics = u.diags
	res.Graph = u.graph
	if project != nil && project.NativeCatalogue != nil {
		res.Natives = project.NativeCatalogue.Doc
	}
	if project != nil && project.App.Doc != nil {
		res.Project = project
	}
	if !u.diags.HasErrors() {
		res.App, res.Plugins, res.Files = u.app, u.outputs, u.files
		res.StoredState = u.storedOf()
		res.DeviceUses = u.sortedDeviceUses()
	}
	return res
}

// run executes stages in order. Nothing runs without an app document, and
// producing stages stop at the first error.
func run(u *unit, stages []stage) {
	if u.project == nil || u.project.App.Doc == nil {
		return
	}
	for _, s := range stages {
		if !s.checking && u.diags.HasErrors() {
			return
		}
		s.run(u)
	}
}

// internalError reports a condition the compiler must never meet.
func (u *unit) internalError(format string, args ...any) {
	u.diags = append(u.diags, plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{}, format, args...))
}

// sharedValidator builds the structural validator once: it compiles the
// embedded JSON Schemas, which never change while the process runs.
var sharedValidator = sync.OnceValues(schema.NewValidator)
