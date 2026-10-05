// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

const (
	galleryFile = "plugins/gallery/plugin.json"
	galleryPage = "plugins/gallery/pages/material.page.json"
	stepPath    = "root/slots/body/slots/child/children/6/events/onPressed/steps/0"
	stepPtr     = "/" + stepPath
)

// onStep replaces the first haptic step of the gallery's material page.
func onStep(t *testing.T, m fstest.MapFS, action string, input map[string]any) {
	t.Helper()
	edit(t, m, galleryPage, func(doc map[string]any) {
		step := at(t, doc, stepPath)
		step["action"] = action
		step["input"] = input
	})
}

// capabilities edits the gallery plugin's capabilities and the app's
// approved set.
func capabilities(t *testing.T, m fstest.MapFS, plugin, app map[string]any) {
	t.Helper()
	edit(t, m, galleryFile, func(doc map[string]any) { doc["capabilities"] = plugin })
	edit(t, m, "app.json", func(doc map[string]any) {
		if app == nil {
			delete(doc, "capabilities")
			return
		}
		doc["capabilities"] = app
	})
}

// TestCapabilitiesMustBeApproved checks that a plugin may only request
// what its app approves: an app without capabilities approves no device
// API, and a listed set of domains or functions narrows what plugins
// declare.
// Verifies: SEC-080.
func TestCapabilitiesMustBeApproved(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	if res := compileFS(m); res.Diagnostics.HasErrors() {
		t.Fatalf("the approved gallery:\n%s", list(res.Diagnostics))
	}

	cases := []struct {
		name        string
		plugin, app map[string]any
		ptr         string
	}{
		{"app without capabilities", map[string]any{"deviceApis": []any{"haptics"}}, nil, "/capabilities/deviceApis/0"},
		{"other device API", map[string]any{"deviceApis": []any{"haptics", "camera"}}, map[string]any{"deviceApis": []any{"haptics"}}, "/capabilities/deviceApis/1"},
		{
			"unlisted domain",
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"api.example.com", "evil.test"}},
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"*.example.com"}},
			"/capabilities/networkDomains/1",
		},
		{
			"wildcard under a plain name",
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"*.example.com"}},
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"api.example.com"}},
			"/capabilities/networkDomains/0",
		},
		{
			"unlisted function",
			map[string]any{"deviceApis": []any{"haptics"}, "functions": []any{map[string]any{"id": "01c0c450-6c00-7000-8000-0000000fff01", "function": "gallery.sync"}}},
			map[string]any{"deviceApis": []any{"haptics"}, "functions": []any{"gallery.other"}},
			"/capabilities/functions/0/function",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, widgetsDir)
			capabilities(t, m, c.plugin, c.app)
			wantDiag(t, compileFS(m), plxerr.CapabilityNotApproved, galleryFile, c.ptr)
		})
	}

	t.Run("a domain under an approved wildcard", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		capabilities(t, m,
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"api.example.com", "*.example.com"}},
			map[string]any{"deviceApis": []any{"haptics"}, "networkDomains": []any{"*.example.com"}})
		for _, d := range compileFS(m).Diagnostics {
			if d.Code == plxerr.CapabilityNotApproved && d.Path != "/capabilities/networkDomains/1" {
				t.Errorf("unexpected: %s", d.Message)
			}
		}
	})
}

// TestDeviceActionsNeedTheirCapability checks that a step runs a device
// action only if its plugin declares the device API, and that
// requestPermission asks only for a permission the plugin declares.
// Verifies: SEC-080.
func TestDeviceActionsNeedTheirCapability(t *testing.T) {
	t.Parallel()
	t.Run("haptic without haptics", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		capabilities(t, m, map[string]any{"networkDomains": []any{"example.com"}}, map[string]any{"deviceApis": []any{"haptics"}})
		wantDiag(t, compileFS(m), plxerr.DeviceCapabilityUndeclared, galleryPage, stepPtr+"/action")
	})
	t.Run("share declared and approved", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		onStep(t, m, "share", map[string]any{"text": "hi"})
		capabilities(t, m, map[string]any{"deviceApis": []any{"share", "haptics"}}, map[string]any{"deviceApis": []any{"share", "haptics"}})
		res := compileFS(m)
		for _, d := range res.Diagnostics {
			if d.Code == plxerr.DeviceCapabilityUndeclared {
				t.Errorf("unexpected: %s", d.Message)
			}
		}
	})
	t.Run("permission undeclared", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		onStep(t, m, "requestPermission", map[string]any{"permission": "location"})
		wantDiag(t, compileFS(m), plxerr.DeviceCapabilityUndeclared, galleryPage, stepPtr+"/action")
	})
	t.Run("permission declared", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		onStep(t, m, "requestPermission", map[string]any{"permission": "location"})
		capabilities(t, m, map[string]any{"deviceApis": []any{"location", "haptics"}}, map[string]any{"deviceApis": []any{"location", "haptics"}})
		if res := compileFS(m); res.Diagnostics.HasErrors() {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	})
	t.Run("not a permission", func(t *testing.T) {
		t.Parallel()
		m := project(t, widgetsDir)
		onStep(t, m, "requestPermission", map[string]any{"permission": "haptics"})
		wantDiag(t, compileFS(m), plxerr.UnresolvedReference, galleryPage, stepPtr+"/input/permission")
	})
}

// TestOpenURLDomains checks that a literal openUrl address is an HTTPS URL
// on a domain the plugin declares, or a link the app answers.
// Verifies: SEC-080, NAV-008.
func TestOpenURLDomains(t *testing.T) {
	t.Parallel()
	declare := func(t *testing.T, m fstest.MapFS, url string) {
		t.Helper()
		onStep(t, m, "openUrl", map[string]any{"url": url})
		capabilities(t, m, map[string]any{"networkDomains": []any{"example.com"}}, map[string]any{})
		edit(t, m, "app.json", func(doc map[string]any) {
			doc["navigation"] = map[string]any{"deepLinks": map[string]any{
				"hosts": []any{"links.example.com"}, "schemes": []any{"acme"},
			}}
		})
	}
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://example.com/terms", true},
		{"https://links.example.com/p/home", true},
		{"acme://items/42", true},
		{"https://other.test/", false},
		{"http://example.com/", false},
		{"mailto:a@example.com", false},
		{"/relative", false},
	} {
		t.Run(c.url, func(t *testing.T) {
			t.Parallel()
			m := project(t, widgetsDir)
			declare(t, m, c.url)
			res := compileFS(m)
			got := slices.ContainsFunc(res.Diagnostics, func(d plxerr.Diagnostic) bool { return d.Code == plxerr.OpenURLDomainUndeclared })
			if got == c.want {
				t.Errorf("openUrl %s: PLX-1232 reported = %v\n%s", c.url, got, list(res.Diagnostics))
			}
		})
	}
}

// TestDeviceFeature checks that a bundle with a device action requires
// device.v1, first in runtime 0.3.0.
// Verifies: BND-008, SEC-080.
func TestDeviceFeature(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	edit(t, m, "app.json", func(doc map[string]any) { doc["minRuntimeVersion"] = "0.2.0" })
	wantDiag(t, compileFS(m), plxerr.RuntimeTooOld, galleryPage, stepPtr)

	m = project(t, widgetsDir)
	edit(t, m, "app.json", func(doc map[string]any) {
		doc["minRuntimeVersion"] = "0.2.0"
		doc["requiredFeatures"] = "raise"
	})
	res := compileFS(m)
	if !slices.Contains(res.Plugins[0].Features, "device.v1") {
		t.Errorf("plugin features %v lack device.v1\n%s", res.Plugins[0].Features, list(res.Diagnostics))
	}
}

// TestHostBuildLacksPackages checks the packages of the device actions a
// project uses against those a host build records, like the native
// catalogue check (WGT-032).
// Verifies: RT-060, REL-080.
func TestHostBuildLacksPackages(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	onStep(t, m, "pickImage", map[string]any{"multiple": false})
	capabilities(t, m, map[string]any{"deviceApis": []any{"photos", "haptics"}}, map[string]any{"deviceApis": []any{"photos", "haptics"}})
	res := compileFS(m)
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	if uses := DeviceUses(res, nil); len(uses) != 1 || uses[0].Package != "plux_media" || uses[0].Action != "pickImage" {
		t.Fatalf("uses = %+v", uses)
	}

	with := schema.NativeCatalogueDocument{Packages: []string{"plux_media"}}
	if got := HostBuildLacksPackages(res, nil, "1.0.0+1", &with); len(got) != 0 {
		t.Errorf("a build with plux_media: %v", got)
	}
	without := schema.NativeCatalogueDocument{Packages: []string{"plux_scanner"}}
	got := HostBuildLacksPackages(res, nil, "1.0.0+2", &without)
	if len(got) != 1 || got[0].Code != plxerr.HostBuildLacksPackage || got[0].File != galleryPage || got[0].Path != stepPtr+"/action" {
		t.Errorf("a build without it: %v", got)
	}
	if got := HostBuildLacksPackages(res, func(string) bool { return false }, "1.0.0+2", &without); len(got) != 0 {
		t.Errorf("files outside the draft: %v", got)
	}
	if got := HostBuildLacksPackages(res, nil, "0.9.0+1", nil); len(got) != 0 {
		t.Errorf("a build with no catalogue is not judged: %v", got)
	}
}
