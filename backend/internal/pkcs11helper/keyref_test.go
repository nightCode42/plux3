// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Verifies: SEC-120.
func TestParseKeyRef(t *testing.T) {
	t.Parallel()
	valid := []struct {
		ref  string
		want Selector
	}{
		{"pkcs11:object=plux-targets", Selector{Label: "plux-targets"}},
		{"pkcs11:id=%01%02%ff", Selector{ID: []byte{1, 2, 0xff}}},
		{"pkcs11:object=a%20b;id=%41", Selector{Label: "a b", ID: []byte("A")}},
		{"pkcs11:id=%41;object=k", Selector{Label: "k", ID: []byte("A")}},
	}
	for _, tc := range valid {
		got, err := ParseKeyRef(tc.ref)
		if err != nil || got.Label != tc.want.Label || !bytes.Equal(got.ID, tc.want.ID) {
			t.Errorf("ParseKeyRef(%q) = %+v, %v; want %+v", tc.ref, got, err, tc.want)
		}
	}
	invalid := map[string]string{
		"no scheme":          "object=k",
		"another scheme":     "https://object=k",
		"empty":              "pkcs11:",
		"unknown attribute":  "pkcs11:object=k;token=t",
		"repeated attribute": "pkcs11:object=a;object=b",
		"query":              "pkcs11:object=k?pin-value=1234",
		"fragment":           "pkcs11:object=k#x",
		"no value":           "pkcs11:object=",
		"no equals":          "pkcs11:object",
		"bad escape":         "pkcs11:object=%zz",
		"invalid utf-8":      "pkcs11:object=%ff%fe",
		"long label":         "pkcs11:object=" + strings.Repeat("a", maxLabelSize+1),
		"long id":            "pkcs11:id=" + strings.Repeat("a", maxIDSize+1),
		"stray separator":    "pkcs11:object=k;",
	}
	for name, ref := range invalid {
		if _, err := ParseKeyRef(ref); !errors.Is(err, ErrKeyRef) {
			t.Errorf("%s: ParseKeyRef(%q) err = %v, want ErrKeyRef", name, ref, err)
		}
	}
}
