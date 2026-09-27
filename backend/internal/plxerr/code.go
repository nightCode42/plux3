// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import (
	"fmt"
	"strconv"
	"strings"
)

// Code is a stable Plux error code. Its text form is "PLX-NNNN".
type Code uint16

// String returns the code in its published form, e.g. "PLX-1001".
func (c Code) String() string { return fmt.Sprintf("PLX-%04d", uint16(c)) }

// MarshalText encodes the code as "PLX-NNNN".
func (c Code) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// UnmarshalText decodes a code from "PLX-NNNN".
func (c *Code) UnmarshalText(text []byte) error {
	parsed, err := ParseCode(string(text))
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// ParseCode parses the published form "PLX-NNNN" with exactly four digits.
func ParseCode(s string) (Code, error) {
	digits, ok := strings.CutPrefix(s, "PLX-")
	if !ok || len(digits) != 4 {
		return 0, fmt.Errorf("plxerr.ParseCode: %q is not of the form PLX-NNNN", s)
	}
	n, err := strconv.ParseUint(digits, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("plxerr.ParseCode: %q is not of the form PLX-NNNN", s)
	}
	return Code(n), nil
}

// Anchor returns the fragment identifier of the code's entry in the
// published catalogue, e.g. "plx-1001".
func (c Code) Anchor() string { return strings.ToLower(c.String()) }

// DocURL returns the link to the code's entry in the published catalogue
// (DX-003).
func (c Code) DocURL() string { return CatalogueURL + "#" + c.Anchor() }

// CatalogueURL is the published location of docs/reference/errors.md.
const CatalogueURL = "https://github.com/nightCode42/plux3/blob/main/docs/reference/errors.md"

// Reason is the stable UPPER_SNAKE_CASE identifier paired with a code.
type Reason string

// Severity grades a diagnostic. The zero value is invalid.
type Severity uint8

// Severities in increasing order of gravity.
const (
	SeverityInfo Severity = iota + 1
	SeverityWarning
	SeverityError
)

// String returns "info", "warning" or "error".
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return "severity(" + strconv.Itoa(int(s)) + ")"
	}
}

// MarshalText encodes the severity as "info", "warning" or "error".
func (s Severity) MarshalText() ([]byte, error) {
	if s < SeverityInfo || s > SeverityError {
		return nil, fmt.Errorf("plxerr.Severity: invalid value %d", uint8(s))
	}
	return []byte(s.String()), nil
}

// UnmarshalText decodes "info", "warning" or "error".
func (s *Severity) UnmarshalText(text []byte) error {
	switch string(text) {
	case "info":
		*s = SeverityInfo
	case "warning":
		*s = SeverityWarning
	case "error":
		*s = SeverityError
	default:
		return fmt.Errorf("plxerr.Severity: unknown severity %q", text)
	}
	return nil
}

// Area is a range of codes owned by one part of Plux (spec Appendix F).
type Area struct {
	// Name is the area's title in the catalogue.
	Name string
	// First and Last bound the range, inclusive.
	First, Last Code
}

// Areas returns the code ranges of spec Appendix F, in ascending order.
func Areas() []Area {
	return []Area{
		{"Schema and validation", 1000, 1999},
		{"Compiler and PXL", 2000, 2999},
		{"Release and sync", 3000, 3999},
		{"Runtime rendering and navigation", 4000, 4999},
		{"Actions, data and local database", 5000, 5999},
		{"Security", 6000, 6999},
		{"Functions", 7000, 7999},
		{"Governance", 8000, 8999},
		{"Studio, CLI and AI", 9000, 9999},
	}
}
