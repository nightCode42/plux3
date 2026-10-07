// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"testing"

	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

func mustSettings(t *testing.T, p settings.Profile) Settings {
	t.Helper()
	s, err := SettingsFor(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Verifies: SEC-007, SEC-001.
// Every level rule: Android AL1 is a recognised app, AL2 adds a
// hardware key and the profile's device verdict, AL3 adds strong
// integrity; iOS AL1 is App Attest with a software key and AL2 adds the
// Secure Enclave; a software key caps either at AL1; the development
// provider is AL0.
func TestAssess(t *testing.T) {
	t.Parallel()
	android := func(storage KeyStorage, recognised bool, verdict playintegrity.DeviceLabel) Proof {
		return Proof{Provider: ProviderPlayIntegrity, KeyStorage: storage, AppRecognised: recognised, DeviceVerdict: verdict}
	}
	ios := func(storage KeyStorage) Proof { return Proof{Provider: ProviderAppAttest, KeyStorage: storage} }
	standard, strict := mustSettings(t, settings.Standard), mustSettings(t, settings.Strict)
	maximum := mustSettings(t, settings.Maximum)
	for _, c := range []struct {
		name  string
		proof Proof
		conf  Settings
		want  Level
	}{
		{"development", Proof{Provider: ProviderDevelopment, KeyStorage: KeyStorageSoftware}, standard, AL0},
		{"unknown provider", Proof{}, standard, AL0},
		{"android, app not recognised", android(KeyStorageTEE, false, playintegrity.LabelStrong), standard, AL0},
		{"android, software key, no verdict", android(KeyStorageSoftware, true, playintegrity.LabelNone), standard, AL1},
		{"android, TEE key, no device verdict", android(KeyStorageTEE, true, playintegrity.LabelNone), standard, AL1},
		{"android, TEE key, basic, standard", android(KeyStorageTEE, true, playintegrity.LabelBasic), standard, AL2},
		{"android, StrongBox key, basic, standard", android(KeyStorageStrongBox, true, playintegrity.LabelBasic), standard, AL2},
		{"android, TEE key, basic, strict", android(KeyStorageTEE, true, playintegrity.LabelBasic), strict, AL1},
		{"android, TEE key, device, strict", android(KeyStorageTEE, true, playintegrity.LabelDevice), strict, AL2},
		{"android, TEE key, device, maximum", android(KeyStorageTEE, true, playintegrity.LabelDevice), maximum, AL2},
		{"android, TEE key, strong, standard", android(KeyStorageTEE, true, playintegrity.LabelStrong), standard, AL3},
		{"android, TEE key, strong, maximum", android(KeyStorageStrongBox, true, playintegrity.LabelStrong), maximum, AL3},
		{"android, software key caps strong integrity", android(KeyStorageSoftware, true, playintegrity.LabelStrong), standard, AL1},
		{"android, unspecified key caps strong integrity", android(KeyStorageUnspecified, true, playintegrity.LabelStrong), standard, AL1},
		{"ios, software key", ios(KeyStorageSoftware), standard, AL1},
		{"ios, secure enclave", ios(KeyStorageSecureEnclave), standard, AL2},
		{"ios, secure enclave, maximum", ios(KeyStorageSecureEnclave), maximum, AL2},
	} {
		if got := Assess(c.proof, c.conf); got != c.want {
			t.Errorf("%s: Assess = %s, want %s", c.name, got, c.want)
		}
	}
}

// Verifies: SEC-001, SEC-007.
// A profile's defaults decide whether a software key is refused and which
// device verdict AL2 needs.
func TestSettingsFor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		profile  settings.Profile
		hardware bool
		verdict  playintegrity.DeviceLabel
	}{
		{settings.Standard, false, playintegrity.LabelBasic},
		{settings.Strict, true, playintegrity.LabelDevice},
		{settings.Maximum, true, playintegrity.LabelDevice},
	} {
		s := mustSettings(t, c.profile)
		if s.requireHardware() != c.hardware || s.AndroidDeviceVerdictAL2 != c.verdict {
			t.Errorf("%s: %+v", c.profile, s)
		}
	}
	if _, err := SettingsFor("paranoid"); err == nil {
		t.Error("an unknown profile was accepted")
	}
	loose := mustSettings(t, settings.Standard)
	loose.AllowSoftwareKeys = false
	if !loose.requireHardware() {
		t.Error("standard without allowSoftwareKeys accepts a software key")
	}
}

// Verifies: SEC-002.
// A proven storage supports the claims at or below it, never above.
func TestCovers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		proven, claim KeyStorage
		want          bool
	}{
		{KeyStorageSoftware, KeyStorageUnspecified, true},
		{KeyStorageSoftware, KeyStorageSoftware, true},
		{KeyStorageSoftware, KeyStorageTEE, false},
		{KeyStorageTEE, KeyStorageTEE, true},
		{KeyStorageTEE, KeyStorageStrongBox, false},
		{KeyStorageStrongBox, KeyStorageTEE, true},
		{KeyStorageStrongBox, KeyStorageStrongBox, true},
		{KeyStorageStrongBox, KeyStorageSecureEnclave, false},
		{KeyStorageSoftware, KeyStorageSecureEnclave, false},
		{KeyStorageSecureEnclave, KeyStorageSecureEnclave, true},
	} {
		if got := c.proven.covers(c.claim); got != c.want {
			t.Errorf("%s covers %s = %v", c.proven, c.claim, got)
		}
	}
}
