// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"slices"
	"testing"
)

// rank orders a value from the loosest (lowest) to the tightest for the
// setting's direction.
func rank(s Setting, v Value) int64 {
	switch s.Tighter {
	case TighterTrue:
		if v.Bool() {
			return 1
		}
		return 0
	case TighterFalse:
		if v.Bool() {
			return 0
		}
		return 1
	case TighterLower:
		return -v.Int()
	case TighterHigher:
		return v.Int()
	default:
		return int64(slices.Index(s.Values, v.Text()))
	}
}

// TestRegistryProfilesAreMonotonic_SEC_182 checks that no profile is looser
// than the one before it and that every default is valid for its type.
// Verifies: SEC-182.
func TestRegistryProfilesAreMonotonic_SEC_182(t *testing.T) {
	t.Parallel()
	all := All()
	if len(all) == 0 {
		t.Fatal("empty registry")
	}
	for i, s := range all {
		if i > 0 && all[i-1].Key >= s.Key {
			t.Errorf("%s: keys must be unique and sorted", s.Key)
		}
		prev := int64(-1 << 62)
		for _, p := range []Profile{Standard, Strict, Maximum} {
			v, ok := s.Defaults.For(p)
			if !ok {
				t.Fatalf("%s: no default for %s", s.Key, p)
			}
			switch s.Type {
			case TypeSeconds, TypeCount:
				if v.Int() < s.Min || v.Int() > s.Max {
					t.Errorf("%s: %s default %d outside [%d, %d]", s.Key, p, v.Int(), s.Min, s.Max)
				}
			case TypeEnum:
				if !slices.Contains(s.Values, v.Text()) {
					t.Errorf("%s: %s default %q not in %v", s.Key, p, v.Text(), s.Values)
				}
			case TypeBool:
			default:
				t.Fatalf("%s: unknown type %q", s.Key, s.Type)
			}
			if r := rank(s, v); r < prev {
				t.Errorf("%s: %s is looser than the profile before it", s.Key, p)
			} else {
				prev = r
			}
		}
	}
}

// TestDefaultsForRejectsUnknownProfile_SEC_182 checks the profile lookup.
// Verifies: SEC-182.
func TestDefaultsForRejectsUnknownProfile_SEC_182(t *testing.T) {
	t.Parallel()
	s, ok := Lookup(AllowSoftwareKeys)
	if !ok {
		t.Fatal("allowSoftwareKeys missing")
	}
	if v, _ := s.Defaults.For(Standard); !v.Bool() {
		t.Error("standard profile must allow software keys")
	}
	if v, _ := s.Defaults.For(Strict); v.Bool() {
		t.Error("strict profile must not allow software keys")
	}
	if _, ok := s.Defaults.For("paranoid"); ok {
		t.Error("unknown profile accepted")
	}
}

// TestAllReturnsAFreshSlice_SEC_182 checks that callers cannot alter the
// registry.
// Verifies: SEC-182.
func TestAllReturnsAFreshSlice_SEC_182(t *testing.T) {
	t.Parallel()
	first := All()
	first[0].Key = "changed"
	first[0].Description = "changed"
	for i := range first {
		first[i].Values = append(first[i].Values[:0], "x")
	}
	second := All()
	if second[0].Key == "changed" || second[0].Description == "changed" {
		t.Error("All shares state between calls")
	}
	if got, ok := Lookup(MinAssuranceForSync); !ok || !slices.Equal(got.Values, []string{"AL0", "AL1", "AL2", "AL3"}) {
		t.Errorf("Lookup shares enum values: %v", got.Values)
	}
	if _, ok := Lookup("nonexistent"); ok {
		t.Error("unknown key found")
	}
}
