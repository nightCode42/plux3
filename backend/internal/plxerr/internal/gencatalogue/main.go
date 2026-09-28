// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command gencatalogue writes the published error catalogue from the plxerr
// registry. It runs through go generate in backend/internal/plxerr.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// main delegates to run so that the logic is testable.
func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run writes docs/reference/errors.md, schema/errors.json and the Dart
// error codes under -root.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("gencatalogue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := write(*root); err != nil {
		_, _ = fmt.Fprintln(stderr, "gencatalogue:", err)
		return 1
	}
	return 0
}

// write renders the catalogue files.
func write(root string) error {
	js, err := plxerr.RenderJSON()
	if err != nil {
		return fmt.Errorf("render JSON: %w", err)
	}
	files := map[string][]byte{
		plxerr.CatalogueMarkdownPath: plxerr.RenderMarkdown(),
		plxerr.CatalogueJSONPath:     js,
		plxerr.CatalogueDartPath:     plxerr.RenderDart(),
	}
	for rel, data := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // G306: published documentation is world-readable.
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}
