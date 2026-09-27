// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Registry locations, relative to the repository root.
const (
	WidgetsDir = "schema/widgets"
	ActionsDir = "schema/actions"
	APIFile    = "schema/widgets/flutter-api.json"
	LockFile   = "schema/widgets/ids.lock.json"
)

// schemaRefs is the `$schema` each kind of registry file must declare, so
// editors validate it; the path is relative to the file.
var schemaRefs = map[string]string{
	"layer1":  "../../json/registry/widget-descriptor.schema.json",
	"layer2":  "../../json/registry/widget-descriptor.schema.json",
	"types":   "../../json/registry/value-type.schema.json",
	"enums":   "../../json/registry/enum.schema.json",
	"actions": "../json/registry/action.schema.json",
}

// Load reads the registry under root, checks it, computes the coverage of
// the Flutter snapshot and merges its IDs into the committed lock. The
// returned error lists every problem found, one per line.
func Load(root string) (*Registry, error) {
	var p problems
	r := &Registry{}
	for _, layer := range []string{"layer1", "layer2"} {
		r.Widgets = append(r.Widgets, loadDir[Widget](root, WidgetsDir+"/"+layer, layer, &p)...)
	}
	r.Types = loadDir[ValueType](root, WidgetsDir+"/types", "types", &p)
	r.Enums = loadDir[Enum](root, WidgetsDir+"/enums", "enums", &p)
	r.Actions = loadDir[Action](root, ActionsDir, "actions", &p)
	slices.SortFunc(r.Widgets, func(a, b *Widget) int { return strings.Compare(a.Type, b.Type) })

	api, err := readAPI(filepath.Join(root, filepath.FromSlash(APIFile)))
	if err != nil {
		p.add(APIFile, "%v", err)
		api = &FlutterAPI{}
	}
	r.API = api
	lock, err := ReadLock(filepath.Join(root, filepath.FromSlash(LockFile)))
	if err != nil {
		p.add(LockFile, "%v", err)
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	check(r, &p)
	if err := p.err(); err != nil {
		return nil, err
	}
	r.Lock, err = lock.Merge(r.Entries())
	if err != nil {
		return nil, err
	}
	return r, nil
}

// entry is implemented by the registry file types.
type entry interface {
	Widget | ValueType | Enum | Action
}

// loadDir decodes every *.json file of dir, sorted by name. A missing
// directory is empty.
func loadDir[T entry](root, dir, kind string, p *problems) []*T {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		p.add(dir, "%v", err)
		return nil
	}
	var out []*T
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		file := dir + "/" + e.Name()
		v, err := decodeFile[T](filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			p.add(file, "%v", err)
			continue
		}
		name, schema := identify(v, file)
		if want := strings.TrimSuffix(e.Name(), ".json"); name != want {
			p.add(file, "the file must be named after its entry: %s.json", name)
		}
		if schema != schemaRefs[kind] {
			p.add(file, "$schema must be %q", schemaRefs[kind])
		}
		out = append(out, v)
	}
	return out
}

// identify records the file of v and returns its name and `$schema`.
func identify[T entry](v *T, file string) (name, schema string) {
	var raw json.RawMessage
	switch e := any(v).(type) {
	case *Widget:
		e.File, name, raw = file, e.Type, e.Schema
	case *ValueType:
		e.File, name, raw = file, e.Name, e.Schema
	case *Enum:
		e.File, name, raw = file, e.Name, e.Schema
	case *Action:
		e.File, name, raw = file, e.Name, e.Schema
	}
	_ = json.Unmarshal(raw, &schema)
	return name, schema
}

// decodeFile decodes one JSON document strictly: unknown fields and
// trailing data are errors.
func decodeFile[T any](file string) (*T, error) {
	data, err := os.ReadFile(file) //nolint:gosec // G304: registry paths are fixed under the repository root.
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return decodeStrict[T](data)
}

// decodeStrict decodes data into a new T, rejecting unknown fields and
// trailing data.
func decodeStrict[T any](data []byte) (*T, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode: trailing data after the document")
	}
	return &v, nil
}

// problems collects every problem of a check, prefixed by its location.
type problems struct {
	list []string
}

// add records a problem at loc.
func (p *problems) add(loc, format string, args ...any) {
	p.list = append(p.list, loc+": "+fmt.Sprintf(format, args...))
}

// err returns the problems as one error, or nil.
func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	return fmt.Errorf("%d registry problems:\n  %s", len(p.list), strings.Join(p.list, "\n  "))
}
