// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSettings writes a settings registry file with the given entries.
func writeSettings(t *testing.T, profiles, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"$schema":"x","profiles":`+profiles+`,"settings":[`+entries+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const standardProfiles = `["standard","strict","maximum"]`

// settingEntryJSON builds one settings entry of type bool, overriding fields
// in JSON; an override of the form key=- removes the field.
func settingEntryJSON(key string, overrides ...string) string {
	fields := map[string]string{
		"key": `"` + key + `"`, "type": `"bool"`, "tighter": `"true"`,
		"defaults": `{"standard":false,"strict":true,"maximum":true}`, "travelsToDevice": "true",
		"phase": `"P6"`, "requirements": `["SEC-001"]`, "description": `"Things."`,
	}
	for _, o := range overrides {
		k, v, _ := strings.Cut(o, "=")
		if v == "-" {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	parts := make([]string, 0, len(fields))
	for _, k := range []string{"key", "type", "values", "tighter", "defaults", "bounds", "travelsToDevice", "phase", "requirements", "description"} {
		if v, ok := fields[k]; ok {
			parts = append(parts, `"`+k+`":`+v)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// numberEntry builds a seconds setting that is tighter when lower.
func numberEntry(key string, overrides ...string) string {
	base := []string{`type="seconds"`, `tighter="lower"`, `defaults={"standard":60,"strict":30,"maximum":10}`, `bounds={"min":5,"max":100}`}
	return settingEntryJSON(key, append(base, overrides...)...)
}

// enumEntry builds an enum setting whose later values are tighter.
func enumEntry(key string, overrides ...string) string {
	base := []string{`type="enum"`, `tighter="order"`, `values=["off","on","strict"]`, `defaults={"standard":"off","strict":"on","maximum":"strict"}`}
	return settingEntryJSON(key, append(base, overrides...)...)
}

// TestLoadSettingsValidatesEveryField_SEC_182 checks the registry rules.
// Verifies: SEC-182.
func TestLoadSettingsValidatesEveryField_SEC_182(t *testing.T) {
	t.Parallel()
	valid := settingEntryJSON("a") + "," + numberEntry("b") + "," + enumEntry("c") + "," +
		settingEntryJSON("d", `tighter="false"`, `defaults={"standard":true,"strict":false,"maximum":false}`) + "," +
		numberEntry("e", `tighter="higher"`, `defaults={"standard":10,"strict":20,"maximum":20}`)
	settings, err := LoadSettings(writeSettings(t, standardProfiles, valid))
	if err != nil {
		t.Fatalf("valid registry rejected: %v", err)
	}
	if len(settings) != 5 || settings[1].Defaults[2].Int != 10 || settings[2].Defaults[1].Text != "on" || !settings[0].Defaults[2].Bool {
		t.Errorf("settings decoded wrongly: %+v", settings)
	}
	bad := map[string]string{
		"unsorted":                settingEntryJSON("b") + "," + settingEntryJSON("a"),
		"duplicate":               settingEntryJSON("a") + "," + settingEntryJSON("a"),
		"bad key":                 settingEntryJSON("A_one"),
		"unknown type":            settingEntryJSON("a", `type="float"`),
		"unknown tighter":         settingEntryJSON("a", `tighter="sideways"`),
		"bool tighter lower":      settingEntryJSON("a", `tighter="lower"`),
		"bad phase":               settingEntryJSON("a", `phase="P16"`),
		"no sentence":             settingEntryJSON("a", `description="things"`),
		"no requirements":         settingEntryJSON("a", `requirements=[]`),
		"bad requirement":         settingEntryJSON("a", `requirements=["x"]`),
		"unknown field":           strings.TrimSuffix(settingEntryJSON("a"), "}") + `,"extra":1}`,
		"missing profile default": settingEntryJSON("a", `defaults={"standard":false,"strict":true}`),
		"extra profile default":   settingEntryJSON("a", `defaults={"standard":false,"strict":true,"maximum":true,"extreme":true}`),
		"default null":            settingEntryJSON("a", `defaults={"standard":null,"strict":true,"maximum":true}`),
		"bool default is number":  settingEntryJSON("a", `defaults={"standard":0,"strict":true,"maximum":true}`),
		"bool looser in strict":   settingEntryJSON("a", `defaults={"standard":true,"strict":false,"maximum":true}`),
		"false-tighter looser":    settingEntryJSON("a", `tighter="false"`),
		"bool with values":        settingEntryJSON("a", `values=["x","y"]`),
		"bool with bounds":        settingEntryJSON("a", `bounds={"min":1,"max":2}`),
		"number without bounds":   numberEntry("a", "bounds=-"),
		"number bounds inverted":  numberEntry("a", `bounds={"min":50,"max":5}`),
		"number bounds negative":  numberEntry("a", `bounds={"min":-1,"max":100}`, `defaults={"standard":60,"strict":30,"maximum":0}`),
		"number default too low":  numberEntry("a", `defaults={"standard":60,"strict":30,"maximum":1}`),
		"number default too high": numberEntry("a", `defaults={"standard":101,"strict":30,"maximum":10}`),
		"number is fractional":    numberEntry("a", `defaults={"standard":60.5,"strict":30,"maximum":10}`),
		"number is string":        numberEntry("a", `defaults={"standard":"60","strict":30,"maximum":10}`),
		"number tighter order":    numberEntry("a", `tighter="order"`),
		"number looser in max":    numberEntry("a", `defaults={"standard":60,"strict":30,"maximum":40}`),
		"higher looser in strict": numberEntry("a", `tighter="higher"`),
		"number with values":      numberEntry("a", `values=["x","y"]`),
		"enum without values":     enumEntry("a", "values=-"),
		"enum one value":          enumEntry("a", `values=["off"]`, `defaults={"standard":"off","strict":"off","maximum":"off"}`),
		"enum duplicate value":    enumEntry("a", `values=["off","on","on"]`),
		"enum bad value":          enumEntry("a", `values=["off","on-ish","strict"]`),
		"enum default not listed": enumEntry("a", `defaults={"standard":"off","strict":"on","maximum":"extreme"}`),
		"enum default is bool":    enumEntry("a", `defaults={"standard":"off","strict":"on","maximum":true}`),
		"enum looser in strict":   enumEntry("a", `defaults={"standard":"on","strict":"off","maximum":"strict"}`),
		"enum tighter lower":      enumEntry("a", `tighter="lower"`),
		"enum with bounds":        enumEntry("a", `bounds={"min":1,"max":2}`),
	}
	for name, entries := range bad {
		if _, err := LoadSettings(writeSettings(t, standardProfiles, entries)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadSettings(writeSettings(t, standardProfiles, "")); err == nil {
		t.Error("empty registry accepted")
	}
	for _, profiles := range []string{`["standard","maximum","strict"]`, `["standard","strict"]`, `[]`} {
		if _, err := LoadSettings(writeSettings(t, profiles, settingEntryJSON("a"))); err == nil {
			t.Errorf("profiles %s accepted", profiles)
		}
	}
	if _, err := LoadSettings(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
}

// TestLoadSettingsNamesTheKey_SEC_182 checks that an error identifies the
// offending setting and profile.
// Verifies: SEC-182.
func TestLoadSettingsNamesTheKey_SEC_182(t *testing.T) {
	t.Parallel()
	_, err := LoadSettings(writeSettings(t, standardProfiles, settingEntryJSON("fine")+","+numberEntry("tooLow", `defaults={"standard":60,"strict":30,"maximum":1}`)))
	if err == nil || !strings.Contains(err.Error(), "tooLow") || !strings.Contains(err.Error(), "maximum") {
		t.Errorf("error does not name the key and profile: %v", err)
	}
}

// TestLoadSettingsAcceptsTheRegistry_SEC_182 loads the committed registry.
// Verifies: SEC-182.
func TestLoadSettingsAcceptsTheRegistry_SEC_182(t *testing.T) {
	t.Parallel()
	settings, err := LoadSettings(filepath.Join("..", "..", "..", filepath.FromSlash(SettingsSource)))
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) == 0 {
		t.Fatal("empty registry")
	}
}

// TestSettingsFilesRenderGoAndDart_SEC_182 checks that each output names the
// settings it should and that Dart omits server-only settings; gofmt aligns
// fields, so whitespace is collapsed before matching.
// Verifies: SEC-182.
func TestSettingsFilesRenderGoAndDart_SEC_182(t *testing.T) {
	t.Parallel()
	entries := settingEntryJSON("a") + "," + numberEntry("b", `travelsToDevice=false`) + "," + enumEntry("c")
	settings, err := LoadSettings(writeSettings(t, standardProfiles, entries))
	if err != nil {
		t.Fatal(err)
	}
	files, err := SettingsFiles(settings)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ has, lacks []string }{
		"backend/internal/security/settings/settings_gen.go": {
			has: []string{
				`A Key = "a"`, `B Key = "b"`, `C Key = "c"`,
				"Defaults: Defaults{Standard: BoolValue(false), Strict: BoolValue(true), Maximum: BoolValue(true)}",
				"Type: TypeSeconds, Tighter: TighterLower,", "Min: 5, Max: 100,", "IntValue(60)",
				`Values: []string{"off", "on", "strict"}`, `TextValue("strict")`, "TravelsToDevice: false", "func All() []Setting",
			},
		},
		"packages/plux_flutter/lib/src/security/settings.g.dart": {
			has: []string{
				"a('a', SecuritySettingType.boolean, false, true, true),",
				"c('c', SecuritySettingType.choice, 'off', 'on', 'strict');", "enum SecurityProfile",
			},
			lacks: []string{"b('b'"},
		},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files", len(files))
	}
	for _, f := range files {
		w, ok := want[f.Path]
		if !ok {
			t.Fatalf("unexpected file %s", f.Path)
		}
		for _, fragment := range append(w.has, "Code generated by schemagen from schema/security/settings.json. DO NOT EDIT.", "Apache-2.0") {
			if !strings.Contains(strings.Join(strings.Fields(string(f.Content)), " "), fragment) {
				t.Errorf("%s lacks %q", f.Path, fragment)
			}
		}
		for _, fragment := range w.lacks {
			if strings.Contains(strings.Join(strings.Fields(string(f.Content)), " "), fragment) {
				t.Errorf("%s contains %q", f.Path, fragment)
			}
		}
	}
	again, err := SettingsFiles(settings)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if string(files[i].Content) != string(again[i].Content) {
			t.Errorf("%s is not deterministic", files[i].Path)
		}
	}
}
