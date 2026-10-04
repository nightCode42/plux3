// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// PhoneSource is the vendored phone metadata (pxl.phone.v1).
const PhoneSource = "schema/pxl/phone.json"

// Where the phone tables are written.
const (
	phoneGoPath   = "backend/internal/pxl/phone/table_gen.go"
	phoneDartPath = "packages/plux_flutter/lib/src/pxl/phone.g.dart"
)

// Phone is schema/pxl/phone.json, as tools/cmd/phonemeta derives it.
type Phone struct {
	Schema      string `json:"$schema"`
	Description string `json:"description"`
	Source      struct {
		Project string `json:"project"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		File    string `json:"file"`
		SHA256  string `json:"sha256"`
	} `json:"source"`
	Territories []PhoneTerritory `json:"territories"`
}

// PhoneTerritory is one territory's validation metadata.
type PhoneTerritory struct {
	ID                          string      `json:"id"`
	CountryCode                 int         `json:"countryCode"`
	MainCountryForCode          bool        `json:"mainCountryForCode"`
	LeadingDigits               string      `json:"leadingDigits"`
	InternationalPrefix         string      `json:"internationalPrefix"`
	NationalPrefixForParsing    string      `json:"nationalPrefixForParsing"`
	NationalPrefixTransformRule string      `json:"nationalPrefixTransformRule"`
	General                     PhoneDesc   `json:"generalDesc"`
	Types                       []PhoneDesc `json:"types"`
}

// PhoneDesc is a pattern with its national lengths.
type PhoneDesc struct {
	Type      string `json:"type,omitempty"`
	Pattern   string `json:"pattern"`
	Lengths   []int  `json:"lengths"`
	LocalOnly []int  `json:"localOnly,omitempty"`
	Example   string `json:"example,omitempty"`
}

// phoneField is what a table field may hold: the pattern alphabet of the
// metadata, so the space and "~" separate fields unambiguously.
var phoneField = regexp.MustCompile(`^[0-9\\d\[\]()|?:{},$-]+$`)

// LoadPhone reads and checks the phone metadata under root.
func LoadPhone(root string) (*Phone, error) {
	var p Phone
	if err := decodeStrictFile(filepath.Join(root, filepath.FromSlash(PhoneSource)), &p); err != nil {
		return nil, err
	}
	var errs []string
	seen := map[string]bool{}
	for _, t := range p.Territories {
		key := t.ID + "/" + strconv.Itoa(t.CountryCode)
		if seen[key] {
			errs = append(errs, "duplicate territory "+key)
		}
		seen[key] = true
		fields := []string{t.LeadingDigits, t.InternationalPrefix, t.NationalPrefixForParsing, t.NationalPrefixTransformRule, t.General.Pattern}
		for _, d := range t.Types {
			fields = append(fields, d.Pattern)
		}
		for _, f := range fields {
			if f != "" && !phoneField.MatchString(f) {
				errs = append(errs, fmt.Sprintf("%s: field %q has characters outside the table alphabet", key, f))
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("codegen.LoadPhone: %s", strings.Join(errs, "; "))
	}
	return &p, nil
}

// PhoneFiles renders the Go and Dart tables: one text, read the same way
// by both runtimes (schema/pxl/phone.md §2).
func PhoneFiles(p *Phone) ([]File, error) {
	table := phoneTable(p)
	var g bytes.Buffer
	g.WriteString(header(LangGo, PhoneSource))
	fmt.Fprintf(&g, "package phone\n\n// Version is the libphonenumber release the table is derived from.\nconst Version = %q\n\n", p.Source.Version)
	g.WriteString("// table holds one territory per line (schema/pxl/phone.md §2).\nconst table = `" + table + "`\n")
	goSrc, err := goFile(phoneGoPath, g.Bytes())
	if err != nil {
		return nil, err
	}
	var d bytes.Buffer
	d.WriteString(header(LangDart, PhoneSource))
	d.WriteString("/// The phone validation table of `pxl.phone.v1`, derived from libphonenumber\n")
	fmt.Fprintf(&d, "/// %s (Apache-2.0, The Libphonenumber Authors); schema/pxl/phone.md.\nlibrary;\n\n", p.Source.Version)
	fmt.Fprintf(&d, "/// The libphonenumber release the table is derived from.\nconst String phoneMetadataVersion = '%s';\n\n", p.Source.Version)
	d.WriteString("/// One territory per line (schema/pxl/phone.md §2).\nconst String phoneTable = r'''" + table + "''';\n")
	return []File{goSrc, {Path: phoneDartPath, Content: d.Bytes()}}, nil
}

// phoneTable renders the territories as lines of space-separated fields;
// number types with the same lengths are joined into one alternation.
func phoneTable(p *Phone) string {
	var b strings.Builder
	field := func(s string) {
		if s == "" {
			s = "~"
		}
		b.WriteByte(' ')
		b.WriteString(s)
	}
	for _, t := range p.Territories {
		b.WriteString(t.ID)
		field(strconv.Itoa(t.CountryCode))
		main := "0"
		if t.MainCountryForCode {
			main = "1"
		}
		field(main)
		field(t.LeadingDigits)
		field(t.InternationalPrefix)
		field(t.NationalPrefixForParsing)
		field(t.NationalPrefixTransformRule)
		field(t.General.Pattern)
		field(lengthList(t.General.Lengths))
		field(lengthList(t.General.LocalOnly))
		var order []string
		groups := map[string][]string{}
		for _, d := range t.Types {
			k := lengthList(d.Lengths)
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], d.Pattern)
		}
		for _, k := range order {
			field(k)
			field(strings.Join(groups[k], "|"))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func lengthList(ls []int) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = strconv.Itoa(l)
	}
	return strings.Join(parts, ",")
}
