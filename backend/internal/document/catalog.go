// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Component describes a component document.
type Component struct {
	ID        string
	AppID     string
	PluginID  string
	Key       string
	Name      string
	Path      string
	Revision  int64
	UpdatedAt time.Time
}

// Template describes a template document (SCH-031).
type Template struct {
	ID          string
	AppID       string
	Key         string
	Name        string
	Description string
	Kind        string
	Parameters  []schema.TemplateParameter
	UpdatedAt   time.Time
}

// Usage is one reference to an entity, from the reference graph (SCH-041).
type Usage struct {
	PluginKey string
	File      string
	Path      string
	Kind      string
}

// named reads the "name" of a document, which may be a plain string or a
// translatable text.
func named(tree map[string]any) string {
	switch n := tree["name"].(type) {
	case string:
		return n
	case map[string]any:
		if s, ok := n["value"].(string); ok {
			return s
		}
	}
	key, _ := tree["key"].(string)
	return key
}

// entityOf reads a component or template document by its entity ID and
// checks that the principal may read its app.
func (s *Service) entityOf(ctx context.Context, q *dbgen.Queries, p auth.Principal, id string, kind schema.DocumentKind) (dbgen.Document, draft, map[string]any, error) {
	eid, err := parseID(id, string(kind))
	if err != nil {
		return dbgen.Document{}, draft{}, nil, err
	}
	row, err := q.GetDocumentByEntity(ctx, dbgen.GetDocumentByEntityParams{EntityID: eid, Kind: string(kind)})
	if err != nil {
		return dbgen.Document{}, draft{}, nil, failure(err, string(kind))
	}
	dr, err := q.GetDraftByID(ctx, row.DraftID)
	if err != nil {
		return dbgen.Document{}, draft{}, nil, failure(err, "draft")
	}
	d, err := s.resolve(ctx, q, p, storage.ID(dr.AppID), storage.ID(dr.PluginID))
	if err != nil {
		return dbgen.Document{}, draft{}, nil, err
	}
	if err := authorize(p, auth.PluginRead, d.app); err != nil {
		return dbgen.Document{}, draft{}, nil, err
	}
	content, err := s.blob(ctx, q, row.OrganizationID, row.Sha256)
	if err != nil {
		return dbgen.Document{}, draft{}, nil, err
	}
	tree, err := jcs.Parse(content, int(s.o.Limits.Get(limits.DocumentJSONDepth)))
	if err != nil {
		return dbgen.Document{}, draft{}, nil, fmt.Errorf("document: read %s: %w", row.Path, err)
	}
	m, _ := tree.(map[string]any)
	return row, d, m, nil
}

// ListComponents lists an app's components, shared and per plugin, or one
// plugin's, in path order.
func (s *Service) ListComponents(ctx context.Context, p auth.Principal, appID, pluginID, afterPath string, size int32) ([]Component, error) {
	var out []Component
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		var plugin pgtype.UUID
		if d.plugin != nil {
			plugin = d.plugin.ID
		}
		rows, err := q.ListDocumentsOfKind(ctx, dbgen.ListDocumentsOfKindParams{
			AppID: d.row.AppID, Kind: string(schema.KindComponent), PluginID: plugin, AfterPath: afterPath, PageSize: size,
		})
		if err != nil {
			return failure(err, "component")
		}
		for _, row := range rows {
			content, err := s.blob(ctx, q, row.OrganizationID, row.Sha256)
			if err != nil {
				return err
			}
			var head map[string]any
			if err := json.Unmarshal(content, &head); err != nil {
				return fmt.Errorf("document: read %s: %w", row.Path, err)
			}
			out = append(out, Component{
				ID: storage.ID(row.EntityID), AppID: appID, PluginID: storage.ID(row.PluginID), Key: row.EntityKey,
				Name: named(head), Path: row.Path, Revision: row.Revision, UpdatedAt: storage.Time(row.UpdatedAt),
			})
		}
		return nil
	})
	return out, err
}

// GetComponent returns a component with its content.
func (s *Service) GetComponent(ctx context.Context, p auth.Principal, id string) (Component, []byte, error) {
	var (
		out     Component
		content []byte
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, d, tree, err := s.entityOf(ctx, q, p, id, schema.KindComponent)
		if err != nil {
			return err
		}
		out = Component{
			ID: id, AppID: d.app, PluginID: d.pluginID(), Key: row.EntityKey, Name: named(tree),
			Path: row.Path, Revision: row.Revision, UpdatedAt: storage.Time(row.UpdatedAt),
		}
		content, err = s.blob(ctx, q, row.OrganizationID, row.Sha256)
		return err
	})
	return out, content, err
}

// usageEdges are the reference-graph edges that use each kind of entity.
var usageEdges = map[string][]compiler.EdgeKind{
	"component":      {compiler.EdgeUsesComponent},
	"asset":          {compiler.EdgeUsesAsset},
	"translationKey": {compiler.EdgeUsesTranslation},
	"token":          {compiler.EdgeUsesToken},
	"state":          {compiler.EdgeUsesState},
	"actionGraph":    {compiler.EdgeUsesGraph},
	"page":           {compiler.EdgeNavigates, compiler.EdgeNavigatesPlugin},
	// A template is copied when it is inserted, so nothing refers to it.
	"template": nil,
}

// ListUsages reports every place that uses an entity, from the reference
// graph the compiler builds over the app's drafts (SCH-041), in file and
// path order; after is the file and path to continue after.
func (s *Service) ListUsages(ctx context.Context, p auth.Principal, appID, entityKind, entityID, after string, size int32) ([]Usage, error) {
	kinds, ok := usageEdges[entityKind]
	if !ok {
		return nil, plxerr.New(plxerr.InvalidEnumValue, "%q is not an entity kind with usages", entityKind)
	}
	var out []Usage
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, "")
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		lim, err := s.limitsFor(ctx, tx, d)
		if err != nil {
			return err
		}
		fsys, err := s.project(ctx, q, d)
		if err != nil {
			return err
		}
		res := compiler.Compile(fsys, s.compileOptions(lim))
		if res.Graph == nil {
			return nil
		}
		for _, e := range res.Graph.UsesOf(entityID) {
			if !slices.Contains(kinds, e.Kind) || e.File+"\x00"+e.Path <= after {
				continue
			}
			out = append(out, Usage{PluginKey: pluginKeyOf(e.File), File: e.File, Path: e.Path, Kind: string(e.Kind)})
		}
		slices.SortFunc(out, func(a, b Usage) int { return strings.Compare(a.File+"\x00"+a.Path, b.File+"\x00"+b.Path) })
		if int32(len(out)) > size { //nolint:gosec // a page size
			out = out[:size]
		}
		return nil
	})
	return out, err
}

// pluginKeyOf returns the plugin key of a path under plugins/<key>/.
func pluginKeyOf(file string) string {
	if place, ok := schema.PlaceOf(file); ok {
		return place.Plugin
	}
	return ""
}

// ListTemplates lists an app's templates in path order. Every template of
// this phase inserts a node subtree as a new page, so its kind is "page".
func (s *Service) ListTemplates(ctx context.Context, p auth.Principal, appID, kind, afterPath string, size int32) ([]Template, error) {
	if kind != "" && kind != "page" && kind != "flow" {
		return nil, plxerr.New(plxerr.InvalidEnumValue, "a template's kind is \"page\" or \"flow\"")
	}
	var out []Template
	if kind == "flow" {
		return out, nil
	}
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, "")
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		rows, err := q.ListDocumentsOfKind(ctx, dbgen.ListDocumentsOfKindParams{
			AppID: d.row.AppID, Kind: string(schema.KindTemplate), AfterPath: afterPath, PageSize: size,
		})
		if err != nil {
			return failure(err, "template")
		}
		for _, row := range rows {
			content, err := s.blob(ctx, q, row.OrganizationID, row.Sha256)
			if err != nil {
				return err
			}
			t, err := templateOf(content)
			if err != nil {
				return err
			}
			out = append(out, Template{
				ID: t.ID, AppID: appID, Key: t.Key, Name: t.Name, Description: t.Description, Kind: "page",
				Parameters: t.Parameters, UpdatedAt: storage.Time(row.UpdatedAt),
			})
		}
		return nil
	})
	return out, err
}

// templateOf decodes a stored template.
func templateOf(content []byte) (schema.TemplateDocument, error) {
	var t schema.TemplateDocument
	if err := json.Unmarshal(content, &t); err != nil {
		return schema.TemplateDocument{}, fmt.Errorf("document: read a template: %w", err)
	}
	return t, nil
}

// GetTemplate returns a template with its content.
func (s *Service) GetTemplate(ctx context.Context, p auth.Principal, id string) (Template, []byte, error) {
	var (
		out     Template
		content []byte
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, d, _, err := s.entityOf(ctx, q, p, id, schema.KindTemplate)
		if err != nil {
			return err
		}
		if content, err = s.blob(ctx, q, row.OrganizationID, row.Sha256); err != nil {
			return err
		}
		t, err := templateOf(content)
		if err != nil {
			return err
		}
		out = Template{
			ID: id, AppID: d.app, Key: t.Key, Name: t.Name, Description: t.Description, Kind: "page",
			Parameters: t.Parameters, UpdatedAt: storage.Time(row.UpdatedAt),
		}
		return nil
	})
	return out, content, err
}

// Instantiate inserts a template into a plugin's draft as a new page with
// a fresh identifier for every node (SCH-031), applies the arguments to
// the template's parameters, and lists the page in plugin.json. It needs
// the plugin's lock, and is one snapshot.
func (s *Service) Instantiate(ctx context.Context, p auth.Principal, templateID, appID, pluginID, session string, arguments []byte, key string) (Written, error) {
	if pluginID == "" {
		return Written{}, plxerr.New(plxerr.MissingProperty, "a template is inserted into a plugin")
	}
	if !schema.ValidKey(key) {
		return Written{}, plxerr.New(plxerr.InvalidFormat, "a page key is a lower-kebab slug of at most 64 characters")
	}
	var tmpl schema.TemplateDocument
	if err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, d, _, err := s.entityOf(ctx, q, p, templateID, schema.KindTemplate)
		if err != nil {
			return err
		}
		if d.app != appID {
			return plxerr.New(plxerr.ResourceNotFound, "no such template in this app")
		}
		content, err := s.blob(ctx, q, row.OrganizationID, row.Sha256)
		if err != nil {
			return err
		}
		tmpl, err = templateOf(content)
		return err
	}); err != nil {
		return Written{}, err
	}
	args, err := parseArguments(arguments)
	if err != nil {
		return Written{}, err
	}
	out, err := s.write(ctx, p, appID, pluginID, session, reasonWrite,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			return s.instantiate(ctx, q, d, lim, tmpl, args, key)
		})
	if err != nil {
		return out, err
	}
	return out, s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.record(ctx, tx, p, audit.TemplateInstantiated, "template", templateID, "", "")
	})
}

// parseArguments reads a template's arguments: a JSON object, or nothing.
func parseArguments(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	v, err := jcs.Parse(data, 64)
	if err != nil {
		return nil, plxerr.Wrap(plxerr.InvalidJSON, err, "the arguments are not JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, plxerr.New(plxerr.WrongJSONType, "the arguments are a JSON object of parameter names to values")
	}
	return m, nil
}

// instantiate builds the page and the updated plugin.json.
func (s *Service) instantiate(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set,
	tmpl schema.TemplateDocument, args map[string]any, key string,
) ([]change, plxerr.Diagnostics, error) {
	root, _, err := schema.InstantiateTemplate(&tmpl, s.o.IDs.New)
	if err != nil {
		return nil, nil, plxerr.Wrap(plxerr.InvalidStructure, err, "the template cannot be instantiated")
	}
	rootJSON, err := json.Marshal(root)
	if err != nil {
		return nil, nil, fmt.Errorf("document: %w", err)
	}
	rootTree, err := jcs.Parse(rootJSON, int(lim.Get(limits.DocumentJSONDepth)))
	if err != nil {
		return nil, nil, fmt.Errorf("document: %w", err)
	}
	for name := range args {
		if !slices.ContainsFunc(tmpl.Parameters, func(tp schema.TemplateParameter) bool { return tp.Name == name }) {
			return nil, nil, plxerr.New(plxerr.UnknownProperty, "the template has no parameter %q", name)
		}
	}
	var ops []Op
	for _, tp := range tmpl.Parameters {
		v, given := args[tp.Name]
		if !given {
			continue // the template's own value stays, which is its default
		}
		ops = append(ops, Op{Op: "replace", Path: tp.Path, Value: v})
	}
	if rootTree, err = Apply(rootTree, ops); err != nil {
		return nil, nil, err
	}
	pageID, err := s.newID()
	if err != nil {
		return nil, nil, err
	}
	page := map[string]any{
		"schemaVersion": schema.CurrentVersion, "kind": string(schema.KindPage), "id": pageID, "key": key,
		"pageKind": "screen", "title": tmpl.Name, "root": rootTree,
	}
	pageJSON, err := jcs.Marshal(page)
	if err != nil {
		return nil, nil, fmt.Errorf("document: %w", err)
	}
	pagePath := path.Join(d.prefix(), "pages", key+".page.json")
	pageChange, diags, err := s.prepare(d, lim, pagePath, pageJSON, 0)
	if err != nil {
		return nil, diags, err
	}
	pluginChange, dg, found, err := s.listPage(ctx, q, d, lim, pageID)
	diags = append(diags, dg...)
	if err != nil || !found {
		return []change{pageChange}, diags, err
	}
	return []change{pageChange, pluginChange}, diags, nil
}

// listPage adds a page to the plugin.json of a draft, when it has one.
func (s *Service) listPage(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set, pageID string) (change, plxerr.Diagnostics, bool, error) {
	pluginPath := d.prefix() + "plugin.json"
	row, err := q.GetDocument(ctx, dbgen.GetDocumentParams{DraftID: d.row.ID, Path: pluginPath})
	if errors.Is(err, pgx.ErrNoRows) {
		return change{}, nil, false, nil // no plugin.json yet: the page stands alone until one lists it
	}
	if err != nil {
		return change{}, nil, false, failure(err, "document")
	}
	current, err := s.blob(ctx, q, d.row.OrganizationID, row.Sha256)
	if err != nil {
		return change{}, nil, false, err
	}
	tree, err := jcs.Parse(current, int(lim.Get(limits.DocumentJSONDepth)))
	if err != nil {
		return change{}, nil, false, fmt.Errorf("document: read plugin.json: %w", err)
	}
	plugin, _ := tree.(map[string]any)
	pages, _ := plugin["pages"].([]any)
	plugin["pages"] = append(pages, pageID)
	updated, err := jcs.Marshal(plugin)
	if err != nil {
		return change{}, nil, false, fmt.Errorf("document: %w", err)
	}
	c, diags, err := s.prepare(d, lim, pluginPath, updated, row.Revision)
	return c, diags, err == nil, err
}
