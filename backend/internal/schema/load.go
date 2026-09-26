// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Source is one document file of a project after parsing, migration and
// canonicalisation.
type Source struct {
	// File is the path in the project layout, with forward slashes.
	File string
	// Kind is the document's kind.
	Kind DocumentKind
	// Canonical is the RFC 8785 form of the migrated document (SCH-003).
	Canonical []byte
	// Hash is the SHA-256 of Canonical.
	Hash [sha256.Size]byte
	// Tree is the parsed document, for JSON Pointer lookups.
	Tree map[string]any
}

// Loaded is a decoded document and its source. Doc is nil when the document
// failed structural validation; its diagnostics say why.
type Loaded[T any] struct {
	Doc    *T
	Source *Source
}

// Plugin is a plugin directory: plugins/<key>/.
type Plugin struct {
	Loaded[PluginDocument]
	// Dir is "plugins/<key>".
	Dir        string
	Pages      []Loaded[PageDocument]
	Components []Loaded[ComponentDocument]
	Graphs     []Loaded[ActionGraphDocument]
}

// Project is a project loaded from the Git layout (SCH-006). Optional
// documents are nil when absent. Slices are sorted by file path.
type Project struct {
	App             Loaded[AppDocument]
	Theme           Loaded[ThemeDocument]
	NativeCatalogue *Loaded[NativeCatalogueDocument]
	TranslationKeys *Loaded[TranslationKeysDocument]
	Translations    []Loaded[TranslationsDocument]
	Assets          *Loaded[AssetIndexDocument]
	// AssetFiles holds the content of every file the asset index lists,
	// keyed by its path under assets/.
	AssetFiles map[string][]byte
	Components []Loaded[ComponentDocument]
	Templates  []Loaded[TemplateDocument]
	Plugins    []Plugin
}

// Loader reads projects. It is safe for concurrent use.
type Loader struct {
	validator *Validator
	migrator  *Migrator
	limits    limits.Set
}

// NewLoader returns a loader enforcing the given limits.
func NewLoader(v *Validator, m *Migrator, lim limits.Set) *Loader {
	return &Loader{validator: v, migrator: m, limits: lim}
}

// Load reads the project rooted at fsys. It reports every problem it finds
// and returns as much of the project as it could decode.
func (l *Loader) Load(fsys fs.FS) (*Project, plxerr.Diagnostics) {
	r := &run{l: l, fsys: fsys}
	p := &Project{AssetFiles: map[string][]byte{}}
	if _, err := fs.Stat(fsys, "app.json"); err != nil {
		r.report(plxerr.ProjectNotFound, "app.json", "", "no app.json at the project root")
		return p, r.diags
	}
	p.App = load[AppDocument](r, "app.json", KindApp)
	p.Theme = load[ThemeDocument](r, "theme.json", KindTheme)
	p.NativeCatalogue = loadOptional[NativeCatalogueDocument](r, "native-catalogue.json", KindNativeCatalogue)
	p.TranslationKeys = loadOptional[TranslationKeysDocument](r, "translations/keys.json", KindTranslationKeys)
	p.Assets = loadOptional[AssetIndexDocument](r, "assets/index.json", KindAssetIndex)
	for _, f := range r.files("translations", ".json", "keys.json") {
		p.Translations = append(p.Translations, load[TranslationsDocument](r, f, KindTranslations))
	}
	p.Components = loadAll[ComponentDocument](r, "components", ".component.json", KindComponent)
	p.Templates = loadAll[TemplateDocument](r, "templates", ".template.json", KindTemplate)
	r.loadAssets(p)
	r.loadPlugins(p)
	r.checkTranslations(p)
	r.diags.Sort()
	return p, r.diags
}

// run holds the state of one Load call.
type run struct {
	l     *Loader
	fsys  fs.FS
	diags plxerr.Diagnostics
}

// report adds a diagnostic.
func (r *run) report(code plxerr.Code, file, pointer, format string, args ...any) {
	r.diags = append(r.diags, plxerr.NewDiagnostic(code, plxerr.Location{File: file, Path: pointer}, format, args...))
}

// files lists the files in dir with the given suffix, in name order,
// skipping exclude. Other JSON files in dir are layout errors.
func (r *run) files(dir, suffix, exclude string) []string {
	entries, err := fs.ReadDir(r.fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.report(plxerr.FileSystemError, dir, "", "read directory: %v", err)
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == exclude || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if !strings.HasSuffix(name, suffix) {
			r.report(plxerr.InvalidProjectLayout, path.Join(dir, name), "", "files in %s/ must be named <key>%s", dir, suffix)
			continue
		}
		out = append(out, path.Join(dir, name))
	}
	return out
}

// loadAll loads every document with the given suffix in dir.
func loadAll[T any](r *run, dir, suffix string, want DocumentKind) []Loaded[T] {
	var out []Loaded[T]
	for _, f := range r.files(dir, suffix, "") {
		out = append(out, load[T](r, f, want))
	}
	return out
}

// loadOptional loads a document if its file exists.
func loadOptional[T any](r *run, file string, want DocumentKind) *Loaded[T] {
	if _, err := fs.Stat(r.fsys, file); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	d := load[T](r, file, want)
	return &d
}

// load reads, checks and decodes one document.
func load[T any](r *run, file string, want DocumentKind) Loaded[T] {
	src, ok := r.source(file, want)
	if !ok {
		return Loaded[T]{Source: src}
	}
	var doc T
	if err := json.Unmarshal(src.Canonical, &doc); err != nil {
		r.report(plxerr.InternalCompilerError, file, "", "decode validated document: %v", err)
		return Loaded[T]{Source: src}
	}
	return Loaded[T]{Doc: &doc, Source: src}
}

// source reads a file and runs the document pipeline up to validation. It
// returns false when the document has errors and must not be decoded.
func (r *run) source(file string, want DocumentKind) (*Source, bool) {
	data, ok := r.read(file)
	if !ok {
		return nil, false
	}
	src, diags := r.l.parse(file, data, want)
	r.diags = append(r.diags, diags...)
	if src == nil || diags.HasErrors() {
		return src, false
	}
	r.checkKey(src)
	return src, true
}

// read returns a file's content within the document size limit.
func (r *run) read(file string) ([]byte, bool) {
	info, err := fs.Stat(r.fsys, file)
	if err != nil {
		r.report(plxerr.InvalidProjectLayout, file, "", "required file is missing")
		return nil, false
	}
	if max := r.l.limits.Get(limits.DocumentFileSize); info.Size() > max {
		r.report(plxerr.LimitExceeded, file, "", "file is %d bytes, above the limit document.fileSize = %d", info.Size(), max)
		return nil, false
	}
	data, err := fs.ReadFile(r.fsys, file)
	if err != nil {
		r.report(plxerr.FileSystemError, file, "", "read: %v", err)
		return nil, false
	}
	return data, true
}

// checkKey verifies that a keyed document's file is named after its key,
// so that the Git layout stays navigable (SCH-006).
func (r *run) checkKey(src *Source) {
	key, ok := src.Tree["key"].(string)
	if !ok || src.Kind == KindApp || src.Kind == KindTheme || src.Kind == KindPlugin {
		return
	}
	base := path.Base(src.File)
	if !strings.HasPrefix(base, key+".") || strings.Count(base[len(key)+1:], ".") != 1 {
		r.report(plxerr.InvalidProjectLayout, src.File, "/key", "file must be named after its key %q", key)
	}
}

// ParseDocument runs the document pipeline on one file's content: strict
// parsing, kind check, migration, structural validation and
// canonicalisation. It serves incremental validation of a single edited
// document (SCH-042). want may be empty to accept any kind.
func (l *Loader) ParseDocument(file string, data []byte, want DocumentKind) (*Source, plxerr.Diagnostics) {
	src, diags := l.parse(file, data, want)
	diags.Sort()
	return src, diags
}

// parse implements ParseDocument.
func (l *Loader) parse(file string, data []byte, want DocumentKind) (*Source, plxerr.Diagnostics) {
	tree, err := jcs.Parse(data, int(l.limits.Get(limits.DocumentJSONDepth)))
	if err != nil {
		var syn *jcs.SyntaxError
		msg := err.Error()
		if errors.As(err, &syn) {
			line, col := position(data, syn.Offset)
			msg = fmt.Sprintf("line %d, column %d: %s", line, col, syn.Msg)
		}
		return nil, plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InvalidJSON, plxerr.Location{File: file}, "%s", msg)}
	}
	doc, ok := tree.(map[string]any)
	if !ok {
		return nil, plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.WrongJSONType, plxerr.Location{File: file}, "a document must be a JSON object")}
	}
	got, _ := doc["kind"].(string)
	if want != "" && DocumentKind(got) != want {
		return nil, plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InvalidProjectLayout, plxerr.Location{File: file, Path: "/kind"},
			"file contains a %q document; this location holds %q documents", got, want)}
	}
	if d := l.migrator.Migrate(doc, file); d != nil {
		return nil, plxerr.Diagnostics{*d}
	}
	diags := l.validator.Validate(DocumentKind(got), doc, file)
	canonical, err := jcs.Marshal(doc)
	if err != nil {
		return nil, append(diags, plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{File: file}, "canonicalise: %v", err))
	}
	return &Source{File: file, Kind: DocumentKind(got), Canonical: canonical, Hash: sha256.Sum256(canonical), Tree: doc}, diags
}

// position converts a byte offset into a 1-based line and column.
func position(data []byte, offset int) (line, col int) {
	offset = min(offset, len(data))
	before := data[:offset]
	line = bytes.Count(before, []byte("\n")) + 1
	col = offset - bytes.LastIndexByte(before, '\n')
	return line, col
}
