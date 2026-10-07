// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// SettingsSource is the security settings registry, relative to the
// repository root.
const SettingsSource = "schema/security/settings.json"

// Allowed values of the settings registry's enumerations, in canonical order.
var (
	settingProfiles = []string{"standard", "strict", "maximum"}
	settingTypes    = []string{"bool", "seconds", "count", "enum"}
	settingTighter  = []string{"true", "false", "lower", "higher", "order"}
	settingKey      = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	settingEnumVal  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// SettingValue is one default of a setting. Only the field that matches the
// setting's type is meaningful: Bool for bool, Int for seconds and count,
// Text for enum.
type SettingValue struct {
	Bool bool
	Int  int64
	Text string
}

// SettingBounds is the inclusive range of a seconds or count setting.
type SettingBounds struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Setting is one entry of the security settings registry (SEC-182).
type Setting struct {
	Key             string
	Type            string
	Values          []string
	Tighter         string
	Defaults        [3]SettingValue // standard, strict, maximum
	Bounds          *SettingBounds
	TravelsToDevice bool
	Phase           string
	Requirements    []string
	Description     string
}

// settingEntry is the JSON shape of a Setting; defaults are decoded after
// the type is known.
type settingEntry struct {
	Key             string                     `json:"key"`
	Type            string                     `json:"type"`
	Values          []string                   `json:"values"`
	Tighter         string                     `json:"tighter"`
	Defaults        map[string]json.RawMessage `json:"defaults"`
	Bounds          *SettingBounds             `json:"bounds"`
	TravelsToDevice bool                       `json:"travelsToDevice"`
	Phase           string                     `json:"phase"`
	Requirements    []string                   `json:"requirements"`
	Description     string                     `json:"description"`
}

// settingsFile is the shape of schema/security/settings.json.
type settingsFile struct {
	Schema   string         `json:"$schema"`
	Comment  string         `json:"$comment"`
	Profiles []string       `json:"profiles"`
	Settings []settingEntry `json:"settings"`
}

// LoadSettings reads and validates the security settings registry.
func LoadSettings(path string) ([]Setting, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("codegen.LoadSettings: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f settingsFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("codegen.LoadSettings %s: %w", path, err)
	}
	if !slices.Equal(f.Profiles, settingProfiles) {
		return nil, fmt.Errorf("codegen.LoadSettings %s: profiles must be %v", path, settingProfiles)
	}
	if len(f.Settings) == 0 {
		return nil, fmt.Errorf("codegen.LoadSettings %s: no settings", path)
	}
	settings := make([]Setting, len(f.Settings))
	for i, e := range f.Settings {
		if i > 0 && f.Settings[i-1].Key >= e.Key {
			return nil, fmt.Errorf("codegen.LoadSettings: %s: keys must be unique and sorted", e.Key)
		}
		s, err := e.setting()
		if err != nil {
			return nil, fmt.Errorf("codegen.LoadSettings: %s: %w", e.Key, err)
		}
		settings[i] = s
	}
	return settings, nil
}

// setting validates one entry and converts it.
func (e settingEntry) setting() (Setting, error) {
	s := Setting{
		Key: e.Key, Type: e.Type, Values: e.Values, Tighter: e.Tighter, Bounds: e.Bounds,
		TravelsToDevice: e.TravelsToDevice, Phase: e.Phase, Requirements: e.Requirements, Description: e.Description,
	}
	switch {
	case !settingKey.MatchString(e.Key):
		return s, fmt.Errorf("key must be lowerCamelCase")
	case !slices.Contains(settingTypes, e.Type):
		return s, fmt.Errorf("unknown type %q", e.Type)
	case !slices.Contains(settingTighter, e.Tighter):
		return s, fmt.Errorf("unknown tighter %q", e.Tighter)
	case !phaseTag.MatchString(e.Phase):
		return s, fmt.Errorf("invalid phase %q", e.Phase)
	case strings.TrimSpace(e.Description) == "" || !strings.HasSuffix(e.Description, "."):
		return s, fmt.Errorf("description must be a sentence")
	case len(e.Requirements) == 0:
		return s, fmt.Errorf("no requirement IDs")
	}
	for _, id := range e.Requirements {
		if !requirementID.MatchString(id) {
			return s, fmt.Errorf("invalid requirement ID %q", id)
		}
	}
	if err := e.checkShape(); err != nil {
		return s, err
	}
	if len(e.Defaults) != len(settingProfiles) {
		return s, fmt.Errorf("defaults must name exactly %v", settingProfiles)
	}
	for i, p := range settingProfiles {
		raw, ok := e.Defaults[p]
		if !ok {
			return s, fmt.Errorf("no default for profile %q", p)
		}
		v, err := e.parseDefault(raw)
		if err != nil {
			return s, fmt.Errorf("default for profile %q: %w", p, err)
		}
		s.Defaults[i] = v
	}
	for i := 1; i < len(settingProfiles); i++ {
		if s.rank(s.Defaults[i]) < s.rank(s.Defaults[i-1]) {
			return s, fmt.Errorf("profile %q is looser than %q", settingProfiles[i], settingProfiles[i-1])
		}
	}
	return s, nil
}

// checkShape checks that values, bounds and tighter fit the setting's type.
func (e settingEntry) checkShape() error {
	switch e.Type {
	case "bool":
		if e.Tighter != "true" && e.Tighter != "false" {
			return fmt.Errorf("a bool setting needs tighter true or false, not %q", e.Tighter)
		}
	case "seconds", "count":
		if err := e.checkNumeric(); err != nil {
			return err
		}
	case "enum":
		if err := e.checkEnum(); err != nil {
			return err
		}
	}
	if e.Type != "enum" && len(e.Values) > 0 {
		return fmt.Errorf("values are only allowed on an enum setting")
	}
	if e.Type != "seconds" && e.Type != "count" && e.Bounds != nil {
		return fmt.Errorf("bounds are only allowed on a seconds or count setting")
	}
	return nil
}

// checkNumeric checks the tighter direction and bounds of a seconds or count setting.
func (e settingEntry) checkNumeric() error {
	if e.Tighter != "lower" && e.Tighter != "higher" {
		return fmt.Errorf("a %s setting needs tighter lower or higher, not %q", e.Type, e.Tighter)
	}
	if e.Bounds == nil {
		return fmt.Errorf("a %s setting needs bounds", e.Type)
	}
	if e.Bounds.Min < 0 || e.Bounds.Min > e.Bounds.Max {
		return fmt.Errorf("bounds [%d, %d] must satisfy 0 <= min <= max", e.Bounds.Min, e.Bounds.Max)
	}
	return nil
}

// checkEnum checks the tighter direction and the values of an enum setting.
func (e settingEntry) checkEnum() error {
	if e.Tighter != "order" {
		return fmt.Errorf("an enum setting needs tighter order, not %q", e.Tighter)
	}
	if len(e.Values) < 2 {
		return fmt.Errorf("an enum setting needs at least two values")
	}
	for i, v := range e.Values {
		if !settingEnumVal.MatchString(v) {
			return fmt.Errorf("invalid enum value %q", v)
		}
		if slices.Index(e.Values, v) != i {
			return fmt.Errorf("duplicate enum value %q", v)
		}
	}
	return nil
}

// parseDefault decodes one default according to the setting's type and
// checks it against the bounds or the enum values.
func (e settingEntry) parseDefault(raw json.RawMessage) (SettingValue, error) {
	var v SettingValue
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return v, fmt.Errorf("must not be null")
	}
	switch e.Type {
	case "bool":
		if err := json.Unmarshal(raw, &v.Bool); err != nil {
			return v, fmt.Errorf("must be a boolean")
		}
	case "seconds", "count":
		if err := json.Unmarshal(raw, &v.Int); err != nil {
			return v, fmt.Errorf("must be an integer")
		}
		if v.Int < e.Bounds.Min || v.Int > e.Bounds.Max {
			return v, fmt.Errorf("%d is outside the bounds [%d, %d]", v.Int, e.Bounds.Min, e.Bounds.Max)
		}
	case "enum":
		if err := json.Unmarshal(raw, &v.Text); err != nil {
			return v, fmt.Errorf("must be a string")
		}
		if !slices.Contains(e.Values, v.Text) {
			return v, fmt.Errorf("%q is not one of %v", v.Text, e.Values)
		}
	}
	return v, nil
}

// rank orders values from loosest to tightest for the setting's direction.
func (s Setting) rank(v SettingValue) int64 {
	switch s.Tighter {
	case "true":
		if v.Bool {
			return 1
		}
		return 0
	case "false":
		if v.Bool {
			return 0
		}
		return 1
	case "lower":
		return -v.Int
	case "higher":
		return v.Int
	default:
		return int64(slices.Index(s.Values, v.Text))
	}
}

// SettingsFiles renders the registry for Go and Dart. The Dart output holds
// only the settings that travel to the device.
func SettingsFiles(settings []Setting) ([]File, error) {
	goSrc, err := goFile("backend/internal/security/settings/settings_gen.go", settingsGo(settings))
	if err != nil {
		return nil, err
	}
	return []File{
		goSrc,
		{Path: "packages/plux_flutter/lib/src/security/settings.g.dart", Content: settingsDart(settings)},
	}, nil
}

// goValue renders a default as a Go expression.
func (s Setting) goValue(v SettingValue) string {
	switch s.Type {
	case "bool":
		return fmt.Sprintf("BoolValue(%t)", v.Bool)
	case "enum":
		return fmt.Sprintf("TextValue(%q)", v.Text)
	default:
		return fmt.Sprintf("IntValue(%d)", v.Int)
	}
}

// dartValue renders a default as a Dart literal.
func (s Setting) dartValue(v SettingValue) string {
	switch s.Type {
	case "bool":
		return fmt.Sprintf("%t", v.Bool)
	case "enum":
		return quoteDart(v.Text)
	default:
		return fmt.Sprint(v.Int)
	}
}

// settingsGoPrelude is the fixed part of the generated Go file: the types
// the registry table is made of.
const settingsGoPrelude = `package settings

// Profile names a security profile; each setting has one default per profile.
type Profile string

// Profiles, from the loosest to the tightest.
const (
	// Standard is the default profile.
	Standard Profile = "standard"
	// Strict tightens the settings that cost little usability.
	Strict Profile = "strict"
	// Maximum sets every setting to its tightest practical value.
	Maximum Profile = "maximum"
)

// Type is the type of a setting's value.
type Type string

// Types of settings.
const (
	// TypeBool is a boolean.
	TypeBool Type = "bool"
	// TypeSeconds is a duration in whole seconds.
	TypeSeconds Type = "seconds"
	// TypeCount is a non-negative count.
	TypeCount Type = "count"
	// TypeEnum is one of an ordered list of strings.
	TypeEnum Type = "enum"
)

// Tighter names the direction in which a setting becomes tighter.
type Tighter string

// Directions of tightening.
const (
	// TighterTrue means true is the tighter boolean.
	TighterTrue Tighter = "true"
	// TighterFalse means false is the tighter boolean.
	TighterFalse Tighter = "false"
	// TighterLower means a lower number is tighter.
	TighterLower Tighter = "lower"
	// TighterHigher means a higher number is tighter.
	TighterHigher Tighter = "higher"
	// TighterOrder means a later entry of Values is tighter.
	TighterOrder Tighter = "order"
)

// Value is a setting's value: a boolean, a number or a string, according to
// the setting's Type. The zero Value is false, 0 and "". Values are
// comparable.
type Value struct {
	b bool
	n int64
	s string
}

// BoolValue returns the value of a bool setting.
func BoolValue(v bool) Value { return Value{b: v} }

// IntValue returns the value of a seconds or count setting.
func IntValue(v int64) Value { return Value{n: v} }

// TextValue returns the value of an enum setting.
func TextValue(v string) Value { return Value{s: v} }

// Bool returns the value of a bool setting.
func (v Value) Bool() bool { return v.b }

// Int returns the value of a seconds or count setting.
func (v Value) Int() int64 { return v.n }

// Text returns the value of an enum setting.
func (v Value) Text() string { return v.s }

// Defaults holds the value a setting takes in each profile.
type Defaults struct {
	Standard Value
	Strict   Value
	Maximum  Value
}

// For returns the default of the profile, or false for an unknown profile.
func (d Defaults) For(p Profile) (Value, bool) {
	switch p {
	case Standard:
		return d.Standard, true
	case Strict:
		return d.Strict, true
	case Maximum:
		return d.Maximum, true
	default:
		return Value{}, false
	}
}

// Setting describes one security setting.
type Setting struct {
	// Key is the stable name of the setting.
	Key Key
	// Type is the type of the setting's value.
	Type Type
	// Values lists the values of an enum setting, loosest first.
	Values []string
	// Tighter is the direction in which the setting becomes tighter.
	Tighter Tighter
	// Min and Max are the inclusive bounds of a seconds or count setting.
	Min, Max int64
	// Defaults holds the value of each profile.
	Defaults Defaults
	// TravelsToDevice reports whether the signed configuration delivers the
	// setting to the device.
	TravelsToDevice bool
	// Phase is the phase that introduced the setting.
	Phase string
	// Requirements lists the requirement IDs the setting implements.
	Requirements []string
	// Description says what the setting controls.
	Description string
}

// Lookup returns the setting with the key.
func Lookup(k Key) (Setting, bool) {
	for _, s := range All() {
		if s.Key == k {
			return s, true
		}
	}
	return Setting{}, false
}
`

// settingsGo renders the Go registry.
func settingsGo(settings []Setting) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangGo, SettingsSource))
	b.WriteString(settingsGoPrelude)
	b.WriteString("\n// Key names a setting.\ntype Key string\n\n// Keys of the security settings registry.\nconst (\n")
	for _, s := range settings {
		b.WriteString(wrapComment("\t// ", fmt.Sprintf("%s: %s (%s)", GoName(s.Key), s.Description, strings.Join(s.Requirements, ", ")), 78))
		fmt.Fprintf(&b, "\t%s Key = %q\n", GoName(s.Key), s.Key)
	}
	b.WriteString(")\n\n// All returns every setting in key order. The result is a fresh slice that\n// the caller may modify.\nfunc All() []Setting {\n\treturn []Setting{\n")
	for _, s := range settings {
		fmt.Fprintf(&b, "\t\t{\n\t\t\tKey: %s, Type: Type%s, Tighter: Tighter%s,\n", GoName(s.Key), GoName(s.Type), GoName(s.Tighter))
		if len(s.Values) > 0 {
			quoted := make([]string, len(s.Values))
			for i, v := range s.Values {
				quoted[i] = fmt.Sprintf("%q", v)
			}
			fmt.Fprintf(&b, "\t\t\tValues: []string{%s},\n", strings.Join(quoted, ", "))
		}
		if s.Bounds != nil {
			fmt.Fprintf(&b, "\t\t\tMin: %d, Max: %d,\n", s.Bounds.Min, s.Bounds.Max)
		}
		fmt.Fprintf(&b, "\t\t\tDefaults: Defaults{Standard: %s, Strict: %s, Maximum: %s},\n",
			s.goValue(s.Defaults[0]), s.goValue(s.Defaults[1]), s.goValue(s.Defaults[2]))
		reqs := make([]string, len(s.Requirements))
		for i, r := range s.Requirements {
			reqs[i] = fmt.Sprintf("%q", r)
		}
		fmt.Fprintf(&b, "\t\t\tTravelsToDevice: %t, Phase: %q, Requirements: []string{%s},\n\t\t\tDescription: %q,\n\t\t},\n",
			s.TravelsToDevice, s.Phase, strings.Join(reqs, ", "), s.Description)
	}
	b.WriteString("\t}\n}\n")
	return b.Bytes()
}

// settingsDart renders the Dart registry of the settings that travel to the
// device.
func settingsDart(settings []Setting) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangDart, SettingsSource))
	b.WriteString("/// The security settings that travel to the device (SEC-182).\nlibrary;\n\n")
	b.WriteString("/// Security profiles, from the loosest to the tightest.\nenum SecurityProfile {\n")
	for i, p := range settingProfiles {
		sep := ","
		if i == len(settingProfiles)-1 {
			sep = ";"
		}
		fmt.Fprintf(&b, "  /// The %s profile.\n  %s%s\n", p, p, sep)
	}
	b.WriteString("}\n\n/// Types of setting values.\nenum SecuritySettingType {\n")
	dartTypes := []string{"boolean", "seconds", "count", "choice"}
	for i, t := range dartTypes {
		sep := ","
		if i == len(dartTypes)-1 {
			sep = ";"
		}
		fmt.Fprintf(&b, "  /// %s.\n  %s%s\n", map[string]string{"boolean": "A bool", "seconds": "A duration in seconds, an int", "count": "A count, an int", "choice": "One of an ordered list of strings"}[t], t, sep)
	}
	b.WriteString("}\n\n/// Every setting the device receives, with its default in each profile.\n/// The values are a bool, an int or a String according to the type.\nenum SecuritySetting {\n")
	var device []Setting
	for _, s := range settings {
		if s.TravelsToDevice {
			device = append(device, s)
		}
	}
	for i, s := range device {
		b.WriteString(wrapComment("  /// ", s.Description, 80))
		sep := ","
		if i == len(device)-1 {
			sep = ";"
		}
		dartType := dartTypes[slices.Index(settingTypes, s.Type)]
		fmt.Fprintf(&b, "  %s(%s, SecuritySettingType.%s, %s, %s, %s)%s\n", LowerCamel(s.Key), quoteDart(s.Key), dartType,
			s.dartValue(s.Defaults[0]), s.dartValue(s.Defaults[1]), s.dartValue(s.Defaults[2]), sep)
	}
	b.WriteString(`
  const SecuritySetting(this.key, this.type, this.standard, this.strict, this.maximum);

  /// The stable registry key.
  final String key;

  /// The type of the setting's value.
  final SecuritySettingType type;

  /// The default in the standard profile.
  final Object standard;

  /// The default in the strict profile.
  final Object strict;

  /// The default in the maximum profile.
  final Object maximum;

  /// The default in [profile].
  Object defaultFor(SecurityProfile profile) => switch (profile) {
    SecurityProfile.standard => standard,
    SecurityProfile.strict => strict,
    SecurityProfile.maximum => maximum,
  };
}
`)
	return b.Bytes()
}
