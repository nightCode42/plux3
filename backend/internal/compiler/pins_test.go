// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	// Two SHA-256 hashes in canonical base64.
	pinA = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	pinB = "n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg="
)

// appPins sets the app's declared domains and pins.
func appPins(t *testing.T, m fstest.MapFS, domains []any, pins map[string]any) {
	t.Helper()
	edit(t, m, "app.json", func(doc map[string]any) {
		caps, _ := doc["capabilities"].(map[string]any)
		if caps == nil {
			caps = map[string]any{}
		}
		if domains != nil {
			caps["networkDomains"] = domains
		}
		caps["networkPins"] = pins
		doc["capabilities"] = caps
	})
}

// TestNetworkPinsReachTheAppBundle checks that the pins an app sets for its
// customer domains are carried into the app bundle, sorted by domain and
// pin, and that a plugin bundle carries none.
// Verifies: SEC-042.
func TestNetworkPinsReachTheAppBundle(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	appPins(t, m, []any{"example.com", "api.example.com", "*.shop.example"}, map[string]any{
		"api.example.com": []any{pinB, pinA},
		"eu.shop.example": []any{pinA, pinB},
	})
	res := compileFS(m)
	clean(t, res)
	bundles := readAll(t, res)
	caps := metaOf(t, bundles[0]).Capabilities(nil)
	if caps == nil {
		t.Fatal("the app bundle has no capabilities")
	}
	got := map[string][]string{}
	var order []string
	var dp fbs.DomainPins
	for i := range caps.NetworkPinsLength() {
		caps.NetworkPins(&dp, i)
		h := string(dp.Host())
		order = append(order, h)
		for j := range dp.PinsLength() {
			got[h] = append(got[h], string(dp.Pins(j)))
		}
	}
	want := map[string][]string{
		"api.example.com": {pinA, pinB},
		"eu.shop.example": {pinA, pinB},
	}
	if !reflect.DeepEqual(got, want) || !slices.IsSorted(order) {
		t.Errorf("pins %v in order %v, want %v sorted", got, order, want)
	}
	for _, b := range bundles[1:] {
		if c := metaOf(t, b).Capabilities(nil); c != nil && c.NetworkPinsLength() != 0 {
			t.Errorf("a plugin bundle carries %d pinned domains", c.NetworkPinsLength())
		}
	}
	// Equal documents give equal bytes (CMP-002).
	again := compileFS(m)
	if !reflect.DeepEqual(res.App.Data, again.App.Data) {
		t.Error("the app bundle differs between two compilations")
	}
}

// TestAppCapabilitiesReachTheAppBundle checks that the device APIs, the
// network domains and the pins an app approves are all in the one
// capabilities table of the app bundle's meta, which the runtime reads
// for the app's own triggers (deny by default otherwise).
// Verifies: SEC-080, SEC-042.
func TestAppCapabilitiesReachTheAppBundle(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	appPins(t, m, []any{"example.com", "api.example.com"}, map[string]any{
		"api.example.com": []any{pinA, pinB},
	})
	edit(t, m, "app.json", func(doc map[string]any) {
		doc["capabilities"].(map[string]any)["deviceApis"] = []any{"location", "haptics"}
	})
	res := compileFS(m)
	clean(t, res)
	caps := metaOf(t, readAll(t, res)[0]).Capabilities(nil)
	if caps == nil {
		t.Fatal("the app bundle has no capabilities")
	}
	strs := func(n int, at func(int) []byte) []string {
		var out []string
		for i := range n {
			out = append(out, string(at(i)))
		}
		return out
	}
	if got, want := strs(caps.NetworkDomainsLength(), caps.NetworkDomains), []string{"example.com", "api.example.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("network domains %v, want %v", got, want)
	}
	if got, want := strs(caps.DeviceApisLength(), caps.DeviceApis), []string{"location", "haptics"}; !reflect.DeepEqual(got, want) {
		t.Errorf("device APIs %v, want %v", got, want)
	}
	if caps.NetworkPinsLength() != 1 {
		t.Errorf("%d pinned domains, want 1", caps.NetworkPinsLength())
	}
}

// TestNetworkPinsAreChecked checks that a domain needs two distinct pins
// and has to be one the app declares, and that a pin is a SHA-256 hash.
// Verifies: SEC-042.
func TestNetworkPinsAreChecked(t *testing.T) {
	t.Parallel()
	domains := []any{"example.com", "api.example.com"}
	cases := []struct {
		name    string
		domains []any
		pins    map[string]any
		code    plxerr.Code
		ptr     string
	}{
		{"one pin", domains, map[string]any{"api.example.com": []any{pinA}}, 0, "/capabilities/networkPins/api.example.com"},
		{"a pin twice", domains, map[string]any{"api.example.com": []any{pinA, pinA}}, 0, "/capabilities/networkPins/api.example.com"},
		{"not a hash", domains, map[string]any{"api.example.com": []any{pinA, "AAAA"}}, 0, "/capabilities/networkPins/api.example.com/1"},
		{"not canonical base64", domains, map[string]any{"api.example.com": []any{pinA, pinB[:42] + "B="}}, plxerr.InvalidFormat, "/capabilities/networkPins/api.example.com/1"},
		{"undeclared domain", domains, map[string]any{"other.example.com": []any{pinA, pinB}}, plxerr.CapabilityNotApproved, "/capabilities/networkPins/other.example.com"},
		{"no declared domains", nil, map[string]any{"api.example.com": []any{pinA, pinB}}, plxerr.CapabilityNotApproved, "/capabilities/networkPins/api.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, widgetsDir)
			appPins(t, m, c.domains, c.pins)
			res := compileFS(m)
			if !res.Diagnostics.HasErrors() {
				t.Fatal("compiled without errors")
			}
			if c.code != 0 {
				wantDiag(t, res, c.code, "app.json", c.ptr)
				return
			}
			// Rejected by the document schema.
			found := false
			for _, d := range res.Diagnostics {
				found = found || (d.File == "app.json" && len(d.Path) >= len(c.ptr) && d.Path[:len(c.ptr)] == c.ptr)
			}
			if !found {
				t.Errorf("nothing at %s; got:\n%s", c.ptr, list(res.Diagnostics))
			}
		})
	}
}
