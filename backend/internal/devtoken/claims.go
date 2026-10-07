// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package devtoken

import (
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	// MaxLifetime is the longest a device access token may live
	// (SEC-020).
	MaxLifetime = 15 * time.Minute
	// MaxTokenSize is the longest token the verifier looks at.
	MaxTokenSize = 4 << 10
	// tokenType is the JOSE typ of an RFC 9068 access token.
	tokenType = "at+jwt"
	// maxNumericDate bounds iat and exp so that conversions cannot
	// overflow.
	maxNumericDate = 1e11
	// jtiBytes is the number of random bytes in a token identifier.
	jtiBytes = 16
)

// Claims is what a device access token says.
type Claims struct {
	// DeviceID is the device the token was issued to (sub).
	DeviceID string
	// AppID is the application the device runs (client_id).
	AppID string
	// Environment is the identifier of the environment the token is
	// valid in (env).
	Environment string
	// OrganizationID is the organisation that owns the app (org), so the
	// request path needs no lookup to scope a call.
	OrganizationID string
	// HostBuild is the host app build the device last reported (hb); it
	// may be empty.
	HostBuild string
	// Production is true for a production environment. It selects the
	// signing key class when issuing; Verify sets it from its argument.
	Production bool
	// Assurance is the device's assurance level, "AL0" to "AL3" (al).
	Assurance string
	// JKT is the thumbprint of the device's DPoP key (cnf.jkt).
	JKT string
	// Lifetime, when more than zero, replaces the issuer's lifetime for
	// this token: the lifetime is a security setting that differs per
	// environment. It is bounded like the issuer's. Issue only; Verify
	// leaves it zero.
	Lifetime time.Duration
	// IssuedAt, ExpiresAt and ID are set by Issue from its clock,
	// lifetime and random source; Issue ignores what a caller puts here.
	// Verify fills them from the token.
	IssuedAt, ExpiresAt time.Time
	ID                  string
}

// wire is the JSON form of the payload.
type wire struct {
	Iss      string  `json:"iss"`
	Aud      string  `json:"aud"`
	Sub      string  `json:"sub"`
	ClientID string  `json:"client_id"`
	Env      string  `json:"env"`
	Org      string  `json:"org"`
	HB       string  `json:"hb,omitempty"`
	AL       string  `json:"al"`
	Cnf      wireCnf `json:"cnf"`
	IAT      int64   `json:"iat"`
	EXP      int64   `json:"exp"`
	JTI      string  `json:"jti"`
}

// wireCnf is the confirmation claim (RFC 7800).
type wireCnf struct {
	JKT string `json:"jkt"`
}

// validAssurance reports whether s names an assurance level.
func validAssurance(s string) bool {
	switch s {
	case "AL0", "AL1", "AL2", "AL3":
		return true
	default:
		return false
	}
}

// invalid is the error of a failed check; it names the check, never the
// value (SEC-092).
func invalid(check string) error {
	return plxerr.New(plxerr.AccessTokenInvalid, "access token failed check %q", check)
}
