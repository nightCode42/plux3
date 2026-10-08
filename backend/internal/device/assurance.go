// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"fmt"
	"time"

	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
)

// Level is the assurance a device has earned (SEC-007). The server
// computes it from verified evidence; a device never claims one.
type Level string

// The assurance levels, from the lowest.
const (
	// AL0 means nothing was verified: the development provider.
	AL0 Level = "AL0"
	// AL1 means the app was verified, but the key may be a software key.
	AL1 Level = "AL1"
	// AL2 means the app, the device and a hardware-held key were verified.
	AL2 Level = "AL2"
	// AL3 means AL2 plus the strongest integrity the platform offers.
	AL3 Level = "AL3"
)

// Rank orders the levels from AL0 (0) to AL3 (3); a value that is not a
// level ranks -1, so it satisfies nothing.
func (l Level) Rank() int {
	switch l {
	case AL0:
		return 0
	case AL1:
		return 1
	case AL2:
		return 2
	case AL3:
		return 3
	}
	return -1
}

// KeyStorage says where a device key is held (SEC-002). Its values are
// those the devices table accepts.
type KeyStorage string

// The places a key can be held.
const (
	// KeyStorageUnspecified is the state of a device with no key, and of
	// a claim that names no place.
	KeyStorageUnspecified KeyStorage = "unspecified"
	// KeyStorageSoftware is application-readable storage: the lowest
	// assurance.
	KeyStorageSoftware KeyStorage = "software"
	// KeyStorageTEE is an Android trusted execution environment.
	KeyStorageTEE KeyStorage = "tee"
	// KeyStorageStrongBox is an Android dedicated secure element.
	KeyStorageStrongBox KeyStorage = "strongbox"
	// KeyStorageSecureEnclave is the iOS Secure Enclave.
	KeyStorageSecureEnclave KeyStorage = "secure_enclave"
)

// hardware reports whether the key is held in secure hardware.
func (k KeyStorage) hardware() bool {
	return k == KeyStorageTEE || k == KeyStorageStrongBox || k == KeyStorageSecureEnclave
}

// Provider names the attestation that backs a device record (SEC-003).
type Provider string

// The providers a device record can name.
const (
	// ProviderPlayIntegrity is an Android device: a Key Attestation chain
	// and a Play Integrity verdict.
	ProviderPlayIntegrity Provider = "play_integrity"
	// ProviderAppAttest is an iOS device attested by App Attest.
	ProviderAppAttest Provider = "app_attest"
	// ProviderDevelopment is a development build (SEC-008).
	ProviderDevelopment Provider = "development"
)

// Proof is what the server verified about a device, reduced to what the
// assurance level depends on. It is built only from evidence that passed
// verification.
type Proof struct {
	// Provider is the attestation that was verified.
	Provider Provider
	// KeyStorage is where the attestation proved the DPoP key lives.
	KeyStorage KeyStorage
	// AppRecognised is true when Play recognised the app binary
	// (ProviderPlayIntegrity only).
	AppRecognised bool
	// DeviceVerdict is the strongest Play Integrity device label
	// (ProviderPlayIntegrity only).
	DeviceVerdict playintegrity.DeviceLabel
}

// Settings are the security settings registration and assessment depend
// on, resolved for one environment.
type Settings struct {
	// Profile is the environment's security profile.
	Profile settings.Profile
	// AllowSoftwareKeys is the allowSoftwareKeys setting (SEC-001).
	AllowSoftwareKeys bool
	// AndroidDeviceVerdictAL2 is the weakest Play Integrity device
	// verdict that qualifies for AL2 (SEC-003, SEC-007).
	AndroidDeviceVerdictAL2 playintegrity.DeviceLabel
	// AccessTokenLifetime is how long an access token lives (SEC-020).
	AccessTokenLifetime time.Duration
	// RegistrationChallengeTTL is how long a registration challenge is
	// accepted (SEC-005).
	RegistrationChallengeTTL time.Duration
	// ReattestationInterval is how long an attestation stays good before
	// the device must attest again (SEC-006).
	ReattestationInterval time.Duration
	// AndroidRefreshRequiresIntegrity makes every Android refresh carry a
	// Play Integrity token (SEC-025).
	AndroidRefreshRequiresIntegrity bool
	// MinAssuranceForSync is the lowest assurance level a device must hold
	// to fetch a manifest (SEC-007).
	MinAssuranceForSync Level
}

// SettingsFor resolves the settings of a profile from the registry's
// defaults.
func SettingsFor(p settings.Profile) (Settings, error) {
	return resolveSettings(p, func(k settings.Key) (settings.Value, error) { return profileValue(k, p) })
}

// SettingsOf resolves the settings of an environment's configuration:
// each setting takes the operator's override or else the profile's preset
// (SEC-182).
func SettingsOf(v Values) (Settings, error) {
	return resolveSettings(v.Profile(), func(k settings.Key) (settings.Value, error) {
		if _, ok := settings.Lookup(k); !ok {
			return settings.Value{}, fmt.Errorf("device: the setting %s is not in the registry", k)
		}
		return v.Get(k), nil
	})
}

// resolveSettings reads the settings the device service acts on.
func resolveSettings(p settings.Profile, read func(settings.Key) (settings.Value, error)) (Settings, error) {
	allow, err := read(settings.AllowSoftwareKeys)
	if err != nil {
		return Settings{}, err
	}
	verdict, err := read(settings.AndroidDeviceVerdictAL2)
	if err != nil {
		return Settings{}, err
	}
	label := playintegrity.DeviceLabel(verdict.Text())
	if verdictRank(label) == 0 {
		return Settings{}, fmt.Errorf("device: the setting %s names no device verdict", settings.AndroidDeviceVerdictAL2)
	}
	minimum, err := read(settings.MinAssuranceForSync)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Profile: p, AllowSoftwareKeys: allow.Bool(), AndroidDeviceVerdictAL2: label, MinAssuranceForSync: Level(minimum.Text())}
	if out.MinAssuranceForSync.Rank() < 0 {
		return Settings{}, fmt.Errorf("device: the setting %s names no assurance level", settings.MinAssuranceForSync)
	}
	if err := out.resolveTokens(read); err != nil {
		return Settings{}, err
	}
	return out, nil
}

// resolveTokens reads the settings that govern access tokens, their
// refresh and the registration challenge.
func (s *Settings) resolveTokens(read func(settings.Key) (settings.Value, error)) error {
	lifetime, err := read(settings.AccessTokenLifetime)
	if err != nil {
		return err
	}
	interval, err := read(settings.ReattestationInterval)
	if err != nil {
		return err
	}
	integrity, err := read(settings.AndroidRefreshRequiresIntegrity)
	if err != nil {
		return err
	}
	challenge, err := read(settings.RegistrationChallengeTtl)
	if err != nil {
		return err
	}
	s.AccessTokenLifetime = time.Duration(lifetime.Int()) * time.Second
	s.RegistrationChallengeTTL = time.Duration(challenge.Int()) * time.Second
	s.ReattestationInterval = time.Duration(interval.Int()) * time.Second
	s.AndroidRefreshRequiresIntegrity = integrity.Bool()
	return nil
}

// profileValue reads a setting's default for a profile.
func profileValue(k settings.Key, p settings.Profile) (settings.Value, error) {
	s, ok := settings.Lookup(k)
	if !ok {
		return settings.Value{}, fmt.Errorf("device: the setting %s is not in the registry", k)
	}
	v, ok := s.Defaults.For(p)
	if !ok {
		return settings.Value{}, fmt.Errorf("device: %q is not a security profile", p)
	}
	return v, nil
}

// requireHardware reports whether a software key is refused: under strict
// and maximum always, under standard when allowSoftwareKeys is off
// (device-trust.md §2).
func (s Settings) requireHardware() bool {
	return s.Profile != settings.Standard || !s.AllowSoftwareKeys
}

// verdictRank orders the Play Integrity device labels; an unknown label
// ranks zero.
func verdictRank(l playintegrity.DeviceLabel) int {
	switch l {
	case playintegrity.LabelStrong:
		return 3
	case playintegrity.LabelDevice:
		return 2
	case playintegrity.LabelBasic:
		return 1
	default:
		return 0
	}
}

// Assess computes the assurance level a verified proof earns under the
// settings (SEC-007, device-trust.md §1). A software key caps the level
// at AL1 whatever else is proven. No RASP finding is known until the
// runtime reports them, and Apple's fraud-metric receipt is not checked
// yet, so an iOS device never reaches AL3.
func Assess(p Proof, s Settings) Level {
	switch p.Provider {
	case ProviderPlayIntegrity:
		return assessAndroid(p, s)
	case ProviderAppAttest:
		if p.KeyStorage == KeyStorageSecureEnclave {
			return AL2
		}
		return AL1
	default:
		return AL0
	}
}

func assessAndroid(p Proof, s Settings) Level {
	if !p.AppRecognised {
		return AL0
	}
	need := max(verdictRank(s.AndroidDeviceVerdictAL2), 1)
	if !p.KeyStorage.hardware() || verdictRank(p.DeviceVerdict) < need {
		return AL1
	}
	if p.DeviceVerdict == playintegrity.LabelStrong {
		return AL3
	}
	return AL2
}
