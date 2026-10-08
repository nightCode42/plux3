// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// maxDepth bounds the nesting of the JSON a caller sends. Overrides are
// one flat object, so a deeper document is never valid.
const maxDepth = 4

// Values is a resolved security configuration: a profile and the settings
// an operator moved away from the profile's preset (SEC-182). Its zero
// value is the standard profile with no overrides, which is also what an
// environment with no stored configuration has (version 0). Values are
// immutable once built.
type Values struct {
	profile   settings.Profile
	overrides map[settings.Key]settings.Value
}

// Profile returns the profile; it is never empty.
func (v Values) Profile() settings.Profile {
	if v.profile == "" {
		return settings.Standard
	}
	return v.profile
}

// Get returns the value of a setting: the override when there is one,
// else the profile's preset. A key that is not in the registry has the
// zero Value.
func (v Values) Get(k settings.Key) settings.Value {
	if o, ok := v.overrides[k]; ok {
		return o
	}
	s, ok := settings.Lookup(k)
	if !ok {
		return settings.Value{}
	}
	d, _ := s.Defaults.For(v.Profile())
	return d
}

// Overridden reports whether an operator set the setting, rather than the
// profile.
func (v Values) Overridden(k settings.Key) bool {
	_, ok := v.overrides[k]
	return ok
}

// Overrides returns the keys an operator set, in order.
func (v Values) Overrides() []settings.Key {
	return slices.Sorted(maps.Keys(v.overrides))
}

// jsonValue renders a value as the JSON tree of its setting's type.
func jsonValue(s settings.Setting, v settings.Value) any {
	switch s.Type {
	case settings.TypeBool:
		return v.Bool()
	case settings.TypeSeconds, settings.TypeCount:
		return float64(v.Int())
	default:
		return v.Text()
	}
}

// OverridesJSON returns the overrides as canonical JSON, "{}" for none.
func (v Values) OverridesJSON() ([]byte, error) {
	tree := make(map[string]any, len(v.overrides))
	for k, o := range v.overrides {
		s, _ := settings.Lookup(k)
		tree[string(k)] = jsonValue(s, o)
	}
	return marshal(tree)
}

// EffectiveJSON returns every setting with its value, as canonical JSON:
// the preset of the profile, then the overrides (SEC-180).
func (v Values) EffectiveJSON() ([]byte, error) {
	all := settings.All()
	tree := make(map[string]any, len(all))
	for _, s := range all {
		tree[string(s.Key)] = jsonValue(s, v.Get(s.Key))
	}
	return marshal(tree)
}

// document is the device document D: the profile and the overrides of the
// settings that travel to the device (ADR-0053). Settings that stay on the
// server never appear in it.
func (v Values) document() map[string]any {
	overrides := map[string]any{}
	for k, o := range v.overrides {
		if s, _ := settings.Lookup(k); s.TravelsToDevice {
			overrides[string(k)] = jsonValue(s, o)
		}
	}
	return map[string]any{"profile": string(v.Profile()), "overrides": overrides}
}

// DeviceDocument returns the canonical JSON of the device document and its
// SHA-256, which the signed manifest pins (SEC-182).
func (v Values) DeviceDocument() ([]byte, [sha256.Size]byte, error) {
	return documentJSON(v.document())
}

// documentJSON canonicalises a document tree and hashes it.
func documentJSON(tree map[string]any) ([]byte, [sha256.Size]byte, error) {
	raw, err := marshal(tree)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

// marshal writes the canonical form of a tree.
func marshal(tree any) ([]byte, error) {
	raw, err := jcs.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("seccfg: %w", err)
	}
	return raw, nil
}

// emptyDocument is D0, the document of version 0: the built-in defaults a
// device carries.
func emptyDocument() map[string]any { return map[string]any{} }

// parseProfile names a profile or refuses it.
func parseProfile(name string) (settings.Profile, error) {
	p := settings.Profile(name)
	if _, ok := (settings.Defaults{}).For(p); !ok {
		return "", plxerr.New(plxerr.InvalidEnumValue, "%q is not a security profile; use standard, strict or maximum", name)
	}
	return p, nil
}

// parseOverrides reads a JSON object of overrides against the registry:
// every key is registered and every value has its setting's type and lies
// within its bounds (PLX-6042). An empty input means no overrides.
func parseOverrides(raw []byte) (map[settings.Key]settings.Value, error) {
	if len(raw) == 0 {
		return map[settings.Key]settings.Value{}, nil
	}
	tree, err := jcs.Parse(raw, maxDepth)
	if err != nil {
		return nil, plxerr.Wrap(plxerr.InvalidJSON, err, "the overrides are not valid JSON")
	}
	object, ok := tree.(map[string]any)
	if !ok {
		return nil, plxerr.New(plxerr.WrongJSONType, "the overrides must be a JSON object of setting keys and values")
	}
	out := make(map[settings.Key]settings.Value, len(object))
	for _, name := range slices.Sorted(maps.Keys(object)) {
		s, ok := settings.Lookup(settings.Key(name))
		if !ok {
			return nil, plxerr.New(plxerr.UnknownProperty, "%q is not a security setting", name)
		}
		v, err := parseValue(s, object[name])
		if err != nil {
			return nil, err
		}
		out[s.Key] = v
	}
	return out, nil
}

// parseValue reads the value of one setting and checks its bounds.
func parseValue(s settings.Setting, raw any) (settings.Value, error) {
	switch s.Type {
	case settings.TypeBool:
		b, ok := raw.(bool)
		if !ok {
			return settings.Value{}, wrongType(s, "true or false")
		}
		return settings.BoolValue(b), nil
	case settings.TypeSeconds, settings.TypeCount:
		n, ok := raw.(json.Number)
		if !ok {
			return settings.Value{}, wrongType(s, "a whole number")
		}
		i, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return settings.Value{}, wrongType(s, "a whole number")
		}
		if i < s.Min || i > s.Max {
			return settings.Value{}, plxerr.New(plxerr.SecurityConfigOutOfBounds,
				"%s is %d; it must be between %d and %d", s.Key, i, s.Min, s.Max)
		}
		return settings.IntValue(i), nil
	default:
		t, ok := raw.(string)
		if !ok {
			return settings.Value{}, wrongType(s, "a string")
		}
		if !slices.Contains(s.Values, t) {
			return settings.Value{}, plxerr.New(plxerr.SecurityConfigOutOfBounds,
				"%s is %q; it must be one of %v", s.Key, t, s.Values)
		}
		return settings.TextValue(t), nil
	}
}

// wrongType refuses a value of the wrong JSON type.
func wrongType(s settings.Setting, want string) error {
	return plxerr.New(plxerr.WrongJSONType, "%s must be %s", s.Key, want)
}

// checkTightens refuses a value that is looser than the profile's preset
// in the direction the setting tightens (SEC-182, PLX-6041). Loosening
// means choosing a lower profile.
func checkTightens(s settings.Setting, profile settings.Profile, v settings.Value) error {
	preset, _ := s.Defaults.For(profile)
	if !tighterOrEqual(s, v, preset) {
		return plxerr.New(plxerr.SecurityConfigLoosensPreset,
			"%s may only be tightened from the %s profile's preset; choose a lower profile to loosen it", s.Key, profile)
	}
	return nil
}

// tighterOrEqual reports whether v is at least as tight as preset.
func tighterOrEqual(s settings.Setting, v, preset settings.Value) bool {
	switch s.Tighter {
	case settings.TighterTrue:
		return v.Bool() || !preset.Bool()
	case settings.TighterFalse:
		return !v.Bool() || preset.Bool()
	case settings.TighterLower:
		return v.Int() <= preset.Int()
	case settings.TighterHigher:
		return v.Int() >= preset.Int()
	default:
		return slices.Index(s.Values, v.Text()) >= slices.Index(s.Values, preset.Text())
	}
}

// resolve builds the values of a profile and its overrides.
func resolve(profile settings.Profile, overrides map[settings.Key]settings.Value) Values {
	return Values{profile: profile, overrides: overrides}
}

// stored builds the values of a stored version. It checks types and bounds
// but not the direction of tightening: the registry's presets may move in
// a later release, and a stored version must stay readable.
func stored(profile string, overrides []byte) (Values, error) {
	p, err := parseProfile(profile)
	if err != nil {
		return Values{}, fmt.Errorf("seccfg: a stored configuration: %w", err)
	}
	o, err := parseOverrides(overrides)
	if err != nil {
		return Values{}, fmt.Errorf("seccfg: a stored configuration: %w", err)
	}
	return resolve(p, o), nil
}

// Resolve checks a profile and a JSON object of overrides against the
// registry and returns the values they make (SEC-182). The errors are
// plxerr ones: an unknown profile or key, a value of the wrong type, out
// of bounds (PLX-6042) or looser than the preset (PLX-6041).
func Resolve(profile string, overrides []byte) (Values, error) {
	p, err := parseProfile(profile)
	if err != nil {
		return Values{}, err
	}
	o, err := parseOverrides(overrides)
	if err != nil {
		return Values{}, err
	}
	for _, k := range slices.Sorted(maps.Keys(o)) {
		s, _ := settings.Lookup(k)
		if err := checkTightens(s, p, o[k]); err != nil {
			return Values{}, err
		}
	}
	return resolve(p, o), nil
}
