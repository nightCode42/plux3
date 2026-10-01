// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

// flutterLicences reads a LICENSE file the way Flutter's licence
// collector does (flutter_tools/lib/src/license_collector.dart): split on
// NoticeSeparator; with several parts, each is its names, a blank line
// and its text.
func flutterLicences(pkg, file string) map[string]string {
	parts := strings.Split(file, NoticeSeparator)
	out := map[string]string{}
	for _, p := range parts {
		if len(parts) == 1 {
			out[pkg] = p
			continue
		}
		names, text, ok := strings.Cut(p, "\n\n")
		if !ok {
			out[pkg] = p
			continue
		}
		for _, n := range strings.Split(names, "\n") {
			out[n] = text
		}
	}
	return out
}

// The runtime's LICENSE gives the host app's licence page Plux's own
// licence and the notices of what it puts on a device: the zstd decoder
// and the icon fonts release bundles carry.
func TestNoticeFiles(t *testing.T) {
	files, err := NoticeFiles(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Content)
	}
	runtime := flutterLicences("plux_flutter", got["packages/plux_flutter/LICENSE"])
	for name, want := range map[string]string{
		"plux_flutter":     "Apache License",
		"zstd":             "BSD License",
		"Material Symbols": "Apache License",
		"Cupertino Icons":  "Copyright (c) 2016 Vladimir Kharlampidi",
	} {
		if !strings.Contains(runtime[name], want) {
			t.Errorf("plux_flutter's LICENSE: %s = %.80q, want it to contain %q", name, runtime[name], want)
		}
	}
	if !strings.HasPrefix(runtime["Material Symbols"], "Copyright Google LLC\n\n") {
		t.Errorf("the Material Symbols notice has no copyright line: %.80q", runtime["Material Symbols"])
	}
	for _, pkg := range []string{"plux_devtools", "plux_svgc", "plux_widget_api"} {
		l := flutterLicences(pkg, got["packages/"+pkg+"/LICENSE"])
		if len(l) != 1 || !strings.HasPrefix(l[pkg], "Copyright 2026 Plux contributors\n\n") {
			t.Errorf("%s's LICENSE: %v", pkg, l)
		}
	}
	for path, content := range got {
		if strings.Contains(content, "\r") || !strings.HasSuffix(content, "\n") || strings.HasSuffix(content, "\n\n") {
			t.Errorf("%s: CR line endings or a missing or doubled final newline", path)
		}
	}
}
