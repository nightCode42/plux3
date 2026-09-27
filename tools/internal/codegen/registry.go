// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// RegistrySource names the registry sources in generated headers.
const RegistrySource = "schema/widgets and schema/actions"

// Paths of the registry outputs, relative to the repository root.
const (
	registryGoPath   = "backend/internal/schema/registry/registry_gen.go"
	registryDartPath = "packages/plux_flutter/lib/src/schema/registry.g.dart"
	registryTSPath   = "studio/packages/schema/src/registry.gen.ts"
	widgetsDocPath   = "docs/reference/widgets.md"
	actionsDocPath   = "docs/reference/actions.md"
	coveragePath     = "schema/widgets/COVERAGE.md"
)

// RegistryFiles renders the permanent-ID lock, the Go, Dart and TypeScript
// registries, the widget and action references and the coverage table
// (WGT-002, WGT-003, BND-011).
func RegistryFiles(r *registry.Registry) ([]File, error) {
	goSrc, err := goFile(registryGoPath, registryGo(r))
	if err != nil {
		return nil, err
	}
	ts, err := registryTS(r)
	if err != nil {
		return nil, err
	}
	return []File{
		{Path: registry.LockFile, Content: r.Lock.Encode()},
		goSrc,
		{Path: registryDartPath, Content: registryDart(r)},
		{Path: registryTSPath, Content: ts},
		{Path: widgetsDocPath, Content: widgetsMarkdown(r)},
		{Path: actionsDocPath, Content: actionsMarkdown(r)},
		{Path: coveragePath, Content: coverageMarkdown(r)},
	}, nil
}

// compactJSON returns raw JSON without insignificant whitespace, or "" for
// none.
func compactJSON(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		// Unreachable: the registry was decoded from valid JSON.
		return string(raw)
	}
	return b.String()
}

// revisionOr1 returns r, or 1 when unset.
func revisionOr1(r int) int {
	if r == 0 {
		return 1
	}
	return r
}

// runtimes lists the runtime of each revision in order.
func runtimes(revs []registry.Revision) []string {
	out := make([]string, len(revs))
	for i, r := range revs {
		out[i] = r.Runtime
	}
	return out
}

// mdCell escapes text for a Markdown table cell.
func mdCell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

// mdAnchor returns the GitHub anchor of a heading consisting of name.
func mdAnchor(name string) string {
	return strings.ToLower(name)
}

// flutterRef renders a Flutter class and its constructors.
func flutterRef(c registry.FlutterClass) string {
	names := make([]string, len(c.Constructors))
	for i, ctor := range c.Constructors {
		names[i] = "`" + c.Class
		if ctor != "" {
			names[i] += "." + ctor
		}
		names[i] += "`"
	}
	return strings.Join(names, ", ")
}

// goDeprecation renders a *Deprecation literal.
func goDeprecation(d *registry.Deprecation) string {
	if d == nil {
		return "nil"
	}
	return fmt.Sprintf("&Deprecation{Revision: %d, Message: %q}", d.Revision, d.Message)
}
