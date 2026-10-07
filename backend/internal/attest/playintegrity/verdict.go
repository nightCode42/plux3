// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package playintegrity

import (
	"encoding/json"
	"strconv"
	"time"
)

// DeviceLabel is one value of the device recognition verdict.
type DeviceLabel string

// Device recognition labels understood by this package.
const (
	// LabelNone means no recognised integrity label; it is the zero value.
	LabelNone DeviceLabel = ""
	// LabelBasic is MEETS_BASIC_INTEGRITY.
	LabelBasic DeviceLabel = "MEETS_BASIC_INTEGRITY"
	// LabelDevice is MEETS_DEVICE_INTEGRITY.
	LabelDevice DeviceLabel = "MEETS_DEVICE_INTEGRITY"
	// LabelStrong is MEETS_STRONG_INTEGRITY.
	LabelStrong DeviceLabel = "MEETS_STRONG_INTEGRITY"
	// LabelVirtual is MEETS_VIRTUAL_INTEGRITY, reported for emulators.
	LabelVirtual DeviceLabel = "MEETS_VIRTUAL_INTEGRITY"
)

// Verdict is the verified content of a Play Integrity token.
type Verdict struct {
	// Device lists the recognised device integrity labels; labels this
	// package does not know are dropped.
	Device []DeviceLabel
	// AppRecognised is true when Play recognises the app binary. Verify
	// rejects tokens where it is false, so it is true on every verdict
	// returned without error.
	AppRecognised bool
	// Licensing is the app licensing verdict, for example LICENSED.
	Licensing string
	// VersionCode is the version code of the app that requested the token.
	VersionCode int64
	// Timestamp is when Google produced the verdict.
	Timestamp time.Time
}

// Strongest returns the strongest device label: strong over device over
// basic, otherwise LabelNone. LabelVirtual does not count towards strength.
func (v Verdict) Strongest() DeviceLabel {
	best := LabelNone
	for _, l := range v.Device {
		if labelRank(l) > labelRank(best) {
			best = l
		}
	}
	return best
}

func labelRank(l DeviceLabel) int {
	switch l {
	case LabelStrong:
		return 3
	case LabelDevice:
		return 2
	case LabelBasic:
		return 1
	default:
		return 0
	}
}

func knownLabel(l DeviceLabel) bool {
	switch l {
	case LabelBasic, LabelDevice, LabelStrong, LabelVirtual:
		return true
	default:
		return false
	}
}

// flexInt64 decodes a JSON integer given either as a number or as a string.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt64(n)
	return nil
}

// rawVerdict mirrors the wire form of the verdict payload.
type rawVerdict struct {
	RequestDetails struct {
		PackageName     string    `json:"requestPackageName"`
		RequestHash     string    `json:"requestHash"`
		TimestampMillis flexInt64 `json:"timestampMillis"`
	} `json:"requestDetails"`
	AppIntegrity struct {
		Recognition string    `json:"appRecognitionVerdict"`
		PackageName string    `json:"packageName"`
		CertDigests []string  `json:"certificateSha256Digest"`
		VersionCode flexInt64 `json:"versionCode"`
	} `json:"appIntegrity"`
	DeviceIntegrity struct {
		Labels []string `json:"deviceRecognitionVerdict"`
	} `json:"deviceIntegrity"`
	AccountDetails struct {
		Licensing string `json:"appLicensingVerdict"`
	} `json:"accountDetails"`
}
