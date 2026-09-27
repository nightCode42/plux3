// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package iso4217 compares schema/pxl/currencies.json with ISO 4217 list
// one as published by its maintenance agency, SIX.
package iso4217

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// ReadList reads list one and returns the minor units of every currency
// that has them; funds and metals marked "N.A." are left out, as in
// currencies.json.
func ReadList(r io.Reader) (map[string]int, error) {
	var doc struct {
		Entries []struct {
			Code  string `xml:"Ccy"`
			Minor string `xml:"CcyMnrUnts"`
		} `xml:"CcyTbl>CcyNtry"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("iso4217: read list one: %w", err)
	}
	out := map[string]int{}
	for _, e := range doc.Entries {
		if e.Code == "" || e.Minor == "N.A." {
			continue
		}
		n, err := strconv.Atoi(e.Minor)
		if err != nil {
			return nil, fmt.Errorf("iso4217: %s: minor units %q: %w", e.Code, e.Minor, err)
		}
		out[e.Code] = n
	}
	return out, nil
}

// ReadCurrencies reads schema/pxl/currencies.json.
func ReadCurrencies(r io.Reader) (map[string]int, error) {
	var doc struct {
		Currencies []struct {
			Code  string `json:"code"`
			Minor int    `json:"minorUnits"`
		} `json:"currencies"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("iso4217: read currencies.json: %w", err)
	}
	out := map[string]int{}
	for _, c := range doc.Currencies {
		out[c.Code] = c.Minor
	}
	return out, nil
}

// Diff lists every difference between list one and currencies.json,
// sorted by code; it is empty when they agree.
func Diff(list, ours map[string]int) []string {
	var out []string
	for code, n := range list {
		m, ok := ours[code]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: in ISO 4217 (%d minor units), missing from currencies.json", code, n))
		case m != n:
			out = append(out, fmt.Sprintf("%s: ISO 4217 has %d minor units, currencies.json %d", code, n, m))
		}
	}
	for code := range ours {
		if _, ok := list[code]; !ok {
			out = append(out, code+": in currencies.json, not in ISO 4217 list one")
		}
	}
	slices.Sort(out)
	return out
}
