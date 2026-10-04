// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command phonemeta derives schema/pxl/phone.json, the phone-validation
// metadata of pxl.phone.v1, from libphonenumber's PhoneNumberMetadata.xml
// at a pinned release (make phone-metadata). It keeps what validation needs
// — calling codes, prefixes and each number type's pattern and lengths —
// resolved the way libphonenumber's BuildMetadataFromXml resolves them, and
// one example number per type for tests; formats, carriers and comments
// are dropped. The output is deterministic: the same XML gives the same
// bytes.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run returns 0 on success and 2 on usage, input or output errors.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("phonemeta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("xml", "", "libphonenumber's resources/PhoneNumberMetadata.xml")
	version := fs.String("version", "", "the libphonenumber release tag, e.g. v9.0.40")
	commit := fs.String("commit", "", "the commit of that tag")
	out := fs.String("out", "", "the phone.json to write")
	if err := fs.Parse(args); err != nil || *in == "" || *version == "" || *commit == "" || *out == "" {
		_, _ = fmt.Fprintln(stderr, "usage: phonemeta -xml <PhoneNumberMetadata.xml> -version <tag> -commit <sha> -out <phone.json>")
		return 2
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "phonemeta:", err)
		return 2
	}
	meta, err := derive(data, source{Project: "libphonenumber", Version: *version, Commit: *commit, File: "resources/PhoneNumberMetadata.xml"})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if err := os.WriteFile(*out, meta, 0o600); err != nil {
		_, _ = fmt.Fprintln(stderr, "phonemeta:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "✓ wrote %s (%d bytes)\n", *out, len(meta))
	return 0
}

// The derived document (schema/json/pxl/phone.schema.json).
type (
	document struct {
		Schema      string      `json:"$schema"`
		Description string      `json:"description"`
		Source      source      `json:"source"`
		Territories []territory `json:"territories"`
	}
	source struct {
		Project string `json:"project"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		File    string `json:"file"`
		SHA256  string `json:"sha256"`
	}
	territory struct {
		ID                          string     `json:"id"`
		CountryCode                 int        `json:"countryCode"`
		MainCountryForCode          bool       `json:"mainCountryForCode,omitempty"`
		LeadingDigits               string     `json:"leadingDigits,omitempty"`
		InternationalPrefix         string     `json:"internationalPrefix,omitempty"`
		NationalPrefixForParsing    string     `json:"nationalPrefixForParsing,omitempty"`
		NationalPrefixTransformRule string     `json:"nationalPrefixTransformRule,omitempty"`
		General                     general    `json:"generalDesc"`
		Types                       []numberTy `json:"types"`
	}
	general struct {
		Pattern   string `json:"pattern"`
		Lengths   []int  `json:"lengths"`
		LocalOnly []int  `json:"localOnly,omitempty"`
	}
	numberTy struct {
		Type    string `json:"type"`
		Pattern string `json:"pattern"`
		Lengths []int  `json:"lengths"`
		Example string `json:"example,omitempty"`
	}
)

// typeNames are the number types validity checks, in libphonenumber's
// PhoneNumberDesc order; noInternationalDialling is not a type.
var typeNames = []string{"fixedLine", "mobile", "tollFree", "premiumRate", "sharedCost", "personalNumber", "voip", "pager", "uan", "voicemail"}

// The XML elements read.
type (
	xmlRoot struct {
		Territories []xmlTerritory `xml:"territories>territory"`
	}
	xmlTerritory struct {
		ID                          string   `xml:"id,attr"`
		CountryCode                 string   `xml:"countryCode,attr"`
		MainCountryForCode          string   `xml:"mainCountryForCode,attr"`
		LeadingDigits               string   `xml:"leadingDigits,attr"`
		InternationalPrefix         string   `xml:"internationalPrefix,attr"`
		NationalPrefix              string   `xml:"nationalPrefix,attr"`
		NationalPrefixForParsing    string   `xml:"nationalPrefixForParsing,attr"`
		NationalPrefixTransformRule string   `xml:"nationalPrefixTransformRule,attr"`
		General                     *xmlDesc `xml:"generalDesc"`
		Descs                       []xmlAny `xml:",any"`
	}
	xmlAny struct {
		XMLName xml.Name
		xmlDesc
	}
	xmlDesc struct {
		Pattern string       `xml:"nationalNumberPattern"`
		Lengths []xmlLengths `xml:"possibleLengths"`
		Example string       `xml:"exampleNumber"`
	}
	xmlLengths struct {
		National  string `xml:"national,attr"`
		LocalOnly string `xml:"localOnly,attr"`
	}
)

var (
	space      = regexp.MustCompile(`\s`)
	regionID   = regexp.MustCompile(`^([A-Z]{2}|001)$`)
	transform  = regexp.MustCompile(`^(\d|\$[1-9])*$`)
	digitsOnly = regexp.MustCompile(`^\d+$`)
)

// derive converts the XML into the document's bytes.
func derive(data []byte, src source) ([]byte, error) {
	var root xmlRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("phonemeta: %w", err)
	}
	sum := sha256.Sum256(data)
	src.SHA256 = hex.EncodeToString(sum[:])
	doc := document{
		Schema: "../json/pxl/phone.schema.json",
		Description: "Phone-number validation metadata of pxl.phone.v1 (schema/pxl/phone.md), derived by tools/cmd/phonemeta (make phone-metadata) from libphonenumber's PhoneNumberMetadata.xml at the release in source; " +
			"Copyright The Libphonenumber Authors, Apache-2.0. Patterns are libphonenumber's with white space removed; lengths are resolved as its metadata builder resolves them. Do not edit: re-derive.",
		Source: src,
	}
	var errs []error
	for _, t := range root.Territories {
		out, err := convert(t)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		doc.Territories = append(doc.Territories, out)
	}
	if len(doc.Territories) == 0 {
		errs = append(errs, errors.New("phonemeta: no territories"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("phonemeta: %w", err)
	}
	return b.Bytes(), nil
}

// convert resolves one territory.
func convert(t xmlTerritory) (territory, error) {
	fail := func(format string, args ...any) (territory, error) {
		return territory{}, fmt.Errorf("phonemeta: territory %s: %s", t.ID, fmt.Sprintf(format, args...))
	}
	cc, err := strconv.Atoi(t.CountryCode)
	if !regionID.MatchString(t.ID) || err != nil || cc < 1 || cc > 999 {
		return fail("invalid id or country code %q", t.CountryCode)
	}
	out := territory{
		ID: t.ID, CountryCode: cc, MainCountryForCode: t.MainCountryForCode == "true",
		LeadingDigits: t.LeadingDigits, InternationalPrefix: t.InternationalPrefix,
		NationalPrefixForParsing:    space.ReplaceAllString(t.NationalPrefixForParsing, ""),
		NationalPrefixTransformRule: t.NationalPrefixTransformRule,
	}
	if out.NationalPrefixForParsing == "" {
		out.NationalPrefixTransformRule = "" // ignored without a parsing prefix
		out.NationalPrefixForParsing = t.NationalPrefix
	}
	for _, p := range []string{out.LeadingDigits, out.InternationalPrefix, out.NationalPrefixForParsing} {
		if space.MatchString(p) || p != "" && !compiles(p) {
			return fail("pattern %q", p)
		}
	}
	if !transform.MatchString(out.NationalPrefixTransformRule) {
		return fail("transform rule %q is not digits and $1–$9", out.NationalPrefixTransformRule)
	}
	if t.General == nil || strings.TrimSpace(t.General.Pattern) == "" {
		return fail("no generalDesc pattern")
	}
	out.General.Pattern = space.ReplaceAllString(t.General.Pattern, "")
	if len(t.General.Lengths) > 0 {
		return fail("possibleLengths on generalDesc")
	}
	national, local := map[int]bool{}, map[int]bool{}
	descs := map[string]xmlDesc{}
	for _, d := range t.Descs {
		name := d.XMLName.Local
		if name != "noInternationalDialling" && !slices.Contains(typeNames, name) {
			continue
		}
		if _, dup := descs[name]; dup {
			return fail("two %s elements", name)
		}
		descs[name] = d.xmlDesc
		if name == "noInternationalDialling" {
			continue
		}
		n, l, err := lengths(d.Lengths)
		if err != nil {
			return fail("%s: %v", name, err)
		}
		for _, x := range n {
			national[x] = true
		}
		for _, x := range l {
			local[x] = true
		}
	}
	out.General.Lengths = sortedKeys(national, nil)
	out.General.LocalOnly = sortedKeys(local, national)
	for _, name := range typeNames {
		d, ok := descs[name]
		if !ok {
			continue
		}
		p := space.ReplaceAllString(d.Pattern, "")
		if p == "" || !compiles(p) {
			return fail("%s has no valid pattern", name)
		}
		n, _, _ := lengths(d.Lengths)
		if len(n) == 0 {
			n = out.General.Lengths
		}
		ex := strings.TrimSpace(d.Example)
		if ex != "" && !digitsOnly.MatchString(ex) {
			return fail("%s example %q", name, ex)
		}
		out.Types = append(out.Types, numberTy{Type: name, Pattern: p, Lengths: n, Example: ex})
	}
	return out, nil
}

// compiles checks a pattern against Go's RE2 syntax; the backend's tests
// check every pattern against the pxl.regex.v1 subset.
func compiles(p string) bool {
	_, err := regexp.Compile(p)
	return err == nil && !strings.Contains(p, "|)")
}

// lengths parses possibleLengths elements: "9,10" or "[4-8]" lists.
func lengths(els []xmlLengths) (national, local []int, err error) {
	for _, e := range els {
		n, err := parseLengths(e.National)
		if err != nil {
			return nil, nil, err
		}
		national = append(national, n...)
		if e.LocalOnly != "" {
			l, err := parseLengths(e.LocalOnly)
			if err != nil {
				return nil, nil, err
			}
			local = append(local, l...)
		}
	}
	return national, local, nil
}

func parseLengths(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		lo, hi := part, part
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			var ok bool
			if lo, hi, ok = strings.Cut(part[1:len(part)-1], "-"); !ok {
				return nil, fmt.Errorf("length range %q", part)
			}
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || a < 1 || b < a || b > 17 {
			return nil, fmt.Errorf("lengths %q", s)
		}
		for x := a; x <= b; x++ {
			out = append(out, x)
		}
	}
	return out, nil
}

// sortedKeys returns the keys of set not in except, ascending.
func sortedKeys(set, except map[int]bool) []int {
	out := []int{}
	for k := range set {
		if !except[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
