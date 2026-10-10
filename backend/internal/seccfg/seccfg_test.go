// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg

import (
	"crypto/sha256"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// Verifies: SEC-182.
// A change is checked against the registry: the profile is known, every
// key is registered, the value has the setting's type and lies within its
// bounds, and it is not looser than the profile's preset in the setting's
// direction (PLX-6041, PLX-6042).
func TestResolve(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		profile   string
		overrides string
		want      plxerr.Code // 0 for accepted
	}{
		{"no overrides", "standard", ``, 0},
		{"an empty object", "strict", `{}`, 0},
		{"a tighter boolean", "standard", `{"allowSoftwareKeys": false}`, 0},
		{"the preset itself", "standard", `{"allowSoftwareKeys": true}`, 0},
		{"a tighter number", "standard", `{"dpopIatWindow": 30}`, 0},
		{"a tighter enum", "standard", `{"raspRootHookingResponse": "block"}`, 0},
		{"several", "strict", `{"inactivityLockTimeout": 60, "screenshotBlockingDefault": true}`, 0},

		{"an unknown profile", "paranoid", `{}`, plxerr.InvalidEnumValue},
		{"an empty profile", "", `{}`, plxerr.InvalidEnumValue},
		{"an unknown key", "standard", `{"noSuchSetting": 1}`, plxerr.UnknownProperty},
		{"not an object", "standard", `[1]`, plxerr.WrongJSONType},
		{"not JSON", "standard", `{`, plxerr.InvalidJSON},
		{"a number for a boolean", "standard", `{"allowSoftwareKeys": 0}`, plxerr.WrongJSONType},
		{"a string for a number", "standard", `{"dpopIatWindow": "30"}`, plxerr.WrongJSONType},
		{"a fraction for a number", "standard", `{"dpopIatWindow": 30.5}`, plxerr.WrongJSONType},
		{"a number for an enum", "standard", `{"raspRootHookingResponse": 3}`, plxerr.WrongJSONType},
		{"below the minimum", "standard", `{"dpopIatWindow": 4}`, plxerr.SecurityConfigOutOfBounds},
		{"above the maximum", "standard", `{"accessTokenLifetime": 901}`, plxerr.SecurityConfigOutOfBounds},
		{"not in the enum", "standard", `{"raspRootHookingResponse": "ignore"}`, plxerr.SecurityConfigOutOfBounds},

		{"a looser boolean, true is tighter", "strict", `{"inactivityLock": false}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser boolean, false is tighter", "standard", `{"tls12Allowed": true}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser boolean, false is tighter, maximum", "maximum", `{"allowDirectDataSources": true}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser number, lower is tighter", "maximum", `{"dpopIatWindow": 31}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser number at the bound", "standard", `{"dpopIatWindow": 300}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser enum", "maximum", `{"raspRootHookingResponse": "degrade"}`, plxerr.SecurityConfigLoosensPreset},
		{"a looser enum than a higher profile", "strict", `{"confidentialBundles": "off"}`, plxerr.SecurityConfigLoosensPreset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Resolve(tt.profile, []byte(tt.overrides))
			if tt.want == 0 {
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				return
			}
			if code, ok := plxerr.CodeOf(err); !ok || code != tt.want {
				t.Fatalf("Resolve = %v, want code %d", err, tt.want)
			}
		})
	}
}

// Verifies: SEC-182.
// Every direction of tightening compares the right way round.
func TestTighterOrEqual(t *testing.T) {
	t.Parallel()
	enum := settings.Setting{Type: settings.TypeEnum, Tighter: settings.TighterOrder, Values: []string{"a", "b", "c"}}
	tests := []struct {
		name          string
		s             settings.Setting
		value, preset settings.Value
		want          bool
	}{
		{"true is tighter: tighten", settings.Setting{Tighter: settings.TighterTrue}, settings.BoolValue(true), settings.BoolValue(false), true},
		{"true is tighter: same", settings.Setting{Tighter: settings.TighterTrue}, settings.BoolValue(true), settings.BoolValue(true), true},
		{"true is tighter: loosen", settings.Setting{Tighter: settings.TighterTrue}, settings.BoolValue(false), settings.BoolValue(true), false},
		{"false is tighter: tighten", settings.Setting{Tighter: settings.TighterFalse}, settings.BoolValue(false), settings.BoolValue(true), true},
		{"false is tighter: loosen", settings.Setting{Tighter: settings.TighterFalse}, settings.BoolValue(true), settings.BoolValue(false), false},
		{"lower is tighter: tighten", settings.Setting{Tighter: settings.TighterLower}, settings.IntValue(1), settings.IntValue(2), true},
		{"lower is tighter: same", settings.Setting{Tighter: settings.TighterLower}, settings.IntValue(2), settings.IntValue(2), true},
		{"lower is tighter: loosen", settings.Setting{Tighter: settings.TighterLower}, settings.IntValue(3), settings.IntValue(2), false},
		{"higher is tighter: tighten", settings.Setting{Tighter: settings.TighterHigher}, settings.IntValue(3), settings.IntValue(2), true},
		{"higher is tighter: same", settings.Setting{Tighter: settings.TighterHigher}, settings.IntValue(2), settings.IntValue(2), true},
		{"higher is tighter: loosen", settings.Setting{Tighter: settings.TighterHigher}, settings.IntValue(1), settings.IntValue(2), false},
		{"later is tighter: tighten", enum, settings.TextValue("c"), settings.TextValue("b"), true},
		{"later is tighter: same", enum, settings.TextValue("b"), settings.TextValue("b"), true},
		{"later is tighter: loosen", enum, settings.TextValue("a"), settings.TextValue("b"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tighterOrEqual(tt.s, tt.value, tt.preset); got != tt.want {
				t.Errorf("tighterOrEqual = %v, want %v", got, tt.want)
			}
		})
	}
}

// Verifies: SEC-182.
// The effective configuration is the preset with the overrides on top.
func TestValuesGet(t *testing.T) {
	t.Parallel()
	v, err := Resolve("strict", []byte(`{"dpopIatWindow": 20, "allowSoftwareKeys": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Get(settings.DPOPIatWindow).Int(); got != 20 || !v.Overridden(settings.DPOPIatWindow) {
		t.Errorf("an override: %d", got)
	}
	if got := v.Get(settings.InactivityLock).Bool(); !got || v.Overridden(settings.InactivityLock) {
		t.Errorf("the strict preset: %v", got)
	}
	if v.Get("noSuchSetting") != (settings.Value{}) {
		t.Error("an unregistered key has a value")
	}
	if (Values{}).Profile() != settings.Standard {
		t.Error("the zero Values is not the standard profile")
	}
	effective, err := v.EffectiveJSON()
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]any
	if err := json.Unmarshal(effective, &all); err != nil || len(all) != len(settings.All()) || all["dpopIatWindow"] != float64(20) {
		t.Errorf("EffectiveJSON = %s, %v", effective, err)
	}
}

// Verifies: SEC-182.
// The device document holds the profile and the overrides of the settings
// that travel to the device, canonical and hashed; version 0 is the empty
// document.
func TestDeviceDocument(t *testing.T) {
	t.Parallel()
	v, err := Resolve("strict", []byte(`{"allowSoftwareKeys": false, "inactivityLockTimeout": 60, "dpopIatWindow": 20, "confidentialBundles": "required"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, sum, err := Config{Version: 4, Values: v}.DeviceDocument()
	if err != nil {
		t.Fatal(err)
	}
	// dpopIatWindow and confidentialBundles stay on the server.
	const want = `{"overrides":{"allowSoftwareKeys":false,"inactivityLockTimeout":60},"profile":"strict"}`
	if string(raw) != want || sum != sha256.Sum256([]byte(want)) {
		t.Errorf("D = %s, hash %x", raw, sum)
	}
	raw, sum, err = Config{}.DeviceDocument()
	if err != nil || string(raw) != `{}` || sum != sha256.Sum256([]byte(`{}`)) {
		t.Errorf("D0 = %s, %x, %v", raw, sum, err)
	}
	if again, _, _ := (Config{Version: 4, Values: v}).DeviceDocument(); !reflect.DeepEqual(again, []byte(want)) {
		t.Error("the document is not deterministic")
	}
}

// Verifies: SEC-182.
// A stored version stays readable when a preset has moved since it was
// written: only the types and bounds are checked on the way out.
func TestStoredIsLenient(t *testing.T) {
	t.Parallel()
	if _, err := stored("maximum", []byte(`{"dpopIatWindow": 300}`)); err != nil {
		t.Errorf("a stored override looser than today's preset: %v", err)
	}
	if _, err := stored("maximum", []byte(`{"dpopIatWindow": 1}`)); err == nil {
		t.Error("a stored override out of bounds was read")
	}
	if _, err := stored("paranoid", nil); err == nil {
		t.Error("a stored unknown profile was read")
	}
}

// Verifies: SEC-182.
// The request and the device document are bounded by securityConfig.bytes.
func TestCheckSize(t *testing.T) {
	t.Parallel()
	v, err := Resolve("standard", []byte(`{"allowSoftwareKeys": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSize(v, 10, 65536); err != nil {
		t.Errorf("within the limit: %v", err)
	}
	for name, args := range map[string][2]int64{"the request": {100, 50}, "the document": {10, 20}} {
		err := checkSize(v, int(args[0]), args[1])
		if code, _ := plxerr.CodeOf(err); code != plxerr.LimitExceeded {
			t.Errorf("%s over the limit: %v", name, err)
		}
	}
}

// applyMergePatch applies an RFC 7396 merge patch to a target.
func applyMergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	out := make(map[string]any, len(t))
	for k, v := range t {
		out[k] = v
	}
	for k, v := range p {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = applyMergePatch(out[k], v)
	}
	return out
}

// Verifies: SEC-182.
// The patch generator follows RFC 7396: objects recurse, a removed key is
// null, arrays and scalars replace.
func TestDiff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, from, to, want string
	}{
		{"equal", `{"a":1}`, `{"a":1}`, `{}`},
		{"added", `{}`, `{"a":1}`, `{"a":1}`},
		{"changed", `{"a":1}`, `{"a":2}`, `{"a":2}`},
		{"removed", `{"a":1,"b":2}`, `{"a":1}`, `{"b":null}`},
		{"nested change", `{"o":{"x":1,"y":2}}`, `{"o":{"x":1,"y":3}}`, `{"o":{"y":3}}`},
		{"nested removal", `{"o":{"x":1,"y":2}}`, `{"o":{"x":1}}`, `{"o":{"y":null}}`},
		{"nested addition", `{"o":{}}`, `{"o":{"x":true}}`, `{"o":{"x":true}}`},
		{"an array is replaced", `{"a":[1,2]}`, `{"a":[1,2,3]}`, `{"a":[1,2,3]}`},
		{"a scalar becomes an object", `{"a":1}`, `{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{"an object becomes a scalar", `{"a":{"b":2}}`, `{"a":"s"}`, `{"a":"s"}`},
		{"the whole document", `{"profile":"standard","overrides":{"x":1}}`, `{"profile":"strict","overrides":{}}`, `{"overrides":{"x":null},"profile":"strict"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			from, to := parseTree(t, tt.from), parseTree(t, tt.to)
			patch, _ := diff(from, to)
			got, err := jcs.Marshal(patch)
			if err != nil || string(got) != tt.want {
				t.Fatalf("diff = %s, %v; want %s", got, err, tt.want)
			}
			if applied := applyMergePatch(from, patch); !reflect.DeepEqual(applied, to) {
				t.Errorf("applying the patch gives %v, want %v", applied, to)
			}
		})
	}
	if _, changed := diff(parseTree(t, `{"a":1}`), parseTree(t, `{"a":1}`)); changed {
		t.Error("equal documents differ")
	}
}

// parseTree reads a JSON text into the tree the generator works on, with
// numbers as float64 like the device document.
func parseTree(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// randomDocument builds a document of the shape of the device document,
// with nesting, arrays and scalars, and no null.
func randomDocument(r *rand.Rand, depth int) map[string]any {
	keys := []string{"a", "b", "c", "d", "profile", "overrides"}
	out := map[string]any{}
	for _, k := range keys {
		if r.IntN(2) == 0 {
			continue
		}
		switch r.IntN(5) {
		case 0:
			out[k] = r.IntN(4) == 0
		case 1:
			out[k] = float64(r.IntN(5))
		case 2:
			out[k] = keys[r.IntN(len(keys))]
		case 3:
			out[k] = []any{float64(r.IntN(3)), "x"}
		default:
			if depth > 0 {
				out[k] = randomDocument(r, depth-1)
			}
		}
	}
	return out
}

// Verifies: SEC-182.
// For any two documents, applying the patch from one to the other gives
// the other, and the patch of equal documents is empty.
func TestDiffRoundTrip(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a seeded generator makes the property test repeatable
	for range 2000 {
		from, to := randomDocument(r, 3), randomDocument(r, 3)
		patch, changed := diff(from, to)
		if applied := applyMergePatch(from, patch); !reflect.DeepEqual(applied, any(to)) {
			t.Fatalf("from %v to %v: patch %v applies to %v", from, to, patch, applied)
		}
		if changed != !reflect.DeepEqual(from, to) {
			t.Fatalf("from %v to %v: changed = %v", from, to, changed)
		}
		if _, sameChanged := diff(to, to); sameChanged {
			t.Fatalf("%v differs from itself", to)
		}
	}
}

// Verifies: SEC-182.
// A patch within securityConfig.patchBytes is sent as it is; a larger one
// is replaced by the patch from nothing and marked as the full document.
func TestDeliver(t *testing.T) {
	t.Parallel()
	from := map[string]any{"profile": "standard", "overrides": map[string]any{}}
	to := map[string]any{"profile": "strict", "overrides": map[string]any{"allowSoftwareKeys": false}}
	small, err := deliver(from, to, 16384)
	if err != nil || small.FullRequired || string(small.Patch) != `{"overrides":{"allowSoftwareKeys":false},"profile":"strict"}` {
		t.Fatalf("a small patch: %+v %v", small, err)
	}
	full, err := deliver(from, to, 10)
	if err != nil || !full.FullRequired || !strings.Contains(string(full.Patch), `"profile":"strict"`) {
		t.Fatalf("a patch beyond the limit: %+v %v", full, err)
	}
	if applied := applyMergePatch(map[string]any{}, parseTree(t, string(full.Patch))); !reflect.DeepEqual(applied, any(to)) {
		t.Errorf("the full patch gives %v", applied)
	}
	same, err := deliver(to, to, 16384)
	if err != nil || string(same.Patch) != `{}` || same.FullRequired {
		t.Errorf("equal documents: %+v %v", same, err)
	}
}
