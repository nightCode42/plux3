// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NoticeSeparator separates the licences of one LICENSE file: a line of
// 80 hyphens, which Flutter's licence collector splits on.
const NoticeSeparator = "\n--------------------------------------------------------------------------------\n"

// notice is one licence in a package's LICENSE file: the names it covers,
// an optional copyright line and the file holding its text.
type notice struct {
	names     []string
	copyright string
	text      string
}

// packageNotices lists the licences each Dart package carries. The
// runtime also carries the third-party code and fonts it puts on a
// device: the vendored zstd decoder (ADR-0030) and the icon fonts the
// server subsets into release bundles (ADR-0032 § Icons), whose notices
// must reach the host app's licence page.
func packageNotices() map[string][]notice {
	// Plux's own licence of the Dart packages (ADR-0022).
	own := func(name string) notice {
		return notice{names: []string{name}, copyright: "Copyright 2026 Plux contributors", text: "LICENSES/Apache-2.0.txt"}
	}
	return map[string][]notice{
		"plux_flutter": {
			own("plux_flutter"),
			{names: []string{"zstd"}, text: "packages/plux_flutter/native/zstd/LICENSE"},
			{names: []string{"Material Symbols"}, copyright: "Copyright Google LLC", text: "backend/internal/icons/fonts/LICENSE-material-design-icons"},
			{names: []string{"Cupertino Icons"}, text: "backend/internal/icons/fonts/LICENSE-cupertino-icons"},
		},
		"plux_devtools":   {own("plux_devtools")},
		"plux_svgc":       {own("plux_svgc")},
		"plux_widget_api": {own("plux_widget_api")},
	}
}

// NoticeFiles returns each Dart package's LICENSE, which pub.dev shows
// and from which Flutter builds a host app's licence page. A file with
// one licence is its text; one with several gives each its names, a
// blank line and its text, separated by NoticeSeparator, the format
// Flutter's licence collector reads.
func NoticeFiles(root string) ([]File, error) {
	all := packageNotices()
	files := make([]File, 0, len(all))
	for _, pkg := range slices.Sorted(maps.Keys(all)) {
		notices := all[pkg]
		sections := make([]string, 0, len(notices))
		for _, n := range notices {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n.text)))
			if err != nil {
				return nil, fmt.Errorf("codegen: notices: %w", err)
			}
			text := strings.Trim(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			text = strings.TrimRight(text, " \n")
			if n.copyright != "" {
				text = n.copyright + "\n\n" + text
			}
			if len(notices) > 1 {
				text = strings.Join(n.names, "\n") + "\n\n" + text
			}
			sections = append(sections, text)
		}
		files = append(files, File{Path: "packages/" + pkg + "/LICENSE", Content: []byte(strings.Join(sections, NoticeSeparator) + "\n")})
	}
	return files, nil
}
