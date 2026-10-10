// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Limits on what a key reference may select by.
const (
	maxLabelSize = 128
	maxIDSize    = 64
)

// ErrKeyRef is returned for a key reference that is not in the supported
// subset of RFC 7512.
var ErrKeyRef = errors.New("pkcs11helper: the key reference is not a supported pkcs11 URI")

// Selector picks a key on the token by its label (CKA_LABEL), its
// identifier (CKA_ID), or both.
type Selector struct {
	// Label is the object attribute, or "".
	Label string
	// ID is the id attribute, percent-decoded, or nil.
	ID []byte
}

// ParseKeyRef reads a key reference. The supported subset of RFC 7512 is
// "pkcs11:" followed by the path attributes "object" and "id", separated
// by ";", with percent-encoded values and at least one of the two. A
// query, a repeated attribute or any other attribute is refused, so a
// reference never selects more than it appears to.
func ParseKeyRef(ref string) (Selector, error) {
	rest, ok := strings.CutPrefix(ref, "pkcs11:")
	if !ok || rest == "" || strings.ContainsAny(rest, "?#") {
		return Selector{}, ErrKeyRef
	}
	var sel Selector
	seen := map[string]bool{}
	for part := range strings.SplitSeq(rest, ";") {
		name, raw, found := strings.Cut(part, "=")
		if !found || raw == "" || seen[name] {
			return Selector{}, ErrKeyRef
		}
		seen[name] = true
		value, err := url.PathUnescape(raw)
		if err != nil {
			return Selector{}, ErrKeyRef
		}
		switch name {
		case "object":
			if !utf8.ValidString(value) || len(value) > maxLabelSize {
				return Selector{}, ErrKeyRef
			}
			sel.Label = value
		case "id":
			if len(value) > maxIDSize {
				return Selector{}, ErrKeyRef
			}
			sel.ID = []byte(value)
		default:
			return Selector{}, ErrKeyRef
		}
	}
	return sel, nil
}
