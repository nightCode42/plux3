// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// u64 reads a little-endian uint64 directory field as an int.
func u64(data []byte, off int) int {
	return int(binary.LittleEndian.Uint64(data[off:])) //nolint:gosec // G115: test data is small.
}

// le32 and le64 encode test values.
func le32(v int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(v)) } //nolint:gosec // G115: small test values.
func le64(v int) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(v)) } //nolint:gosec // G115: small test values.

// mustEncode encodes or fails the test.
func mustEncode(t *testing.T, k Kind, sections []Section) []byte {
	t.Helper()
	data, err := Encode(k, sections)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, name string, err error, code plxerr.Code) {
	t.Helper()
	if got, ok := plxerr.CodeOf(err); !ok || got != code {
		t.Errorf("%s: got %v, want %s", name, err, code)
	}
}

// Verifies: BND-003, BND-005, BND-012, CMP-002.
func TestEncodeReadRoundTrip(t *testing.T) {
	t.Parallel()
	sections := sampleSections()
	data := mustEncode(t, KindPlugin, sections)
	if string(data[:4]) != "PLUX" || binary.LittleEndian.Uint16(data[4:]) != 1 || binary.LittleEndian.Uint32(data[12:]) != 4 {
		t.Fatalf("header % x", data[:16])
	}
	// The same sections in another order give the same bytes.
	reversed := []Section{sections[3], sections[2], sections[1], sections[0]}
	if !bytes.Equal(data, mustEncode(t, KindPlugin, reversed)) {
		t.Error("the encoding depends on the order of the input")
	}
	b, err := Read(data, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != KindPlugin || b.Flags != 0 || len(b.Sections) != 4 {
		t.Fatalf("read %+v", b)
	}
	if b.Hash != sha256.Sum256(append(append([]byte{}, data[:16]...), data[48:48+72*4]...)) {
		t.Error("bundle hash is not SHA-256 of the header and directory")
	}
	for i, want := range []SectionKind{SectionMeta, SectionPage, SectionPage, SectionStrings} {
		s := b.Sections[i]
		if s.Kind != want {
			t.Errorf("section %d is %s, want %s", i, s.Kind, want)
		}
		off := u64(data, 48+72*i+20)
		if off%8 != 0 || &data[off] != &s.Data[0] {
			t.Errorf("section %d is not an aligned view of the input", i)
		}
	}
	if s, ok := b.Section(SectionPage, ID{2}); !ok || !bytes.Equal(s.Data, pageSection()) {
		t.Error("Section(page, 2) not found")
	}
	if _, ok := b.Section(SectionL10n, ID{}); ok {
		t.Error("found a missing section")
	}
	if string(b.Meta.Key()) != "loans" || b.Meta.RequiredFeaturesLength() != 1 {
		t.Error("meta not read")
	}
}

// Verifies: CMP-041.
func TestSourceMapOnlyInDevelopmentBundles(t *testing.T) {
	t.Parallel()
	sm := Section{Kind: SectionSourceMap, ID: ID{1}, Data: stringsSection()}
	if _, err := Encode(KindPlugin, append(sampleSections(), sm)); err == nil {
		t.Error("a release bundle accepted a source map")
	}
	sections := sampleSections()
	sections[3].Data = metaSection(fbs.BundleKindDevelopment)
	data := mustEncode(t, KindDevelopment, append(sections, sm))
	if binary.LittleEndian.Uint32(data[8:])&FlagSourceMap == 0 {
		t.Error("the source-map flag is not set")
	}
	// The source map is not a SourceMap buffer, so reading fails in the verifier.
	_, err := Read(data, defaultOptions())
	wantCode(t, "wrong source map", err, plxerr.SectionVerificationFailed)
}

func TestEncodeRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		kind     Kind
		sections []Section
	}{
		"bundle kind":  {0, sampleSections()},
		"section kind": {KindPlugin, []Section{{Kind: 99}}},
		"duplicate":    {KindPlugin, []Section{{Kind: SectionPage}, {Kind: SectionPage}}},
	}
	for name, tt := range tests {
		if _, err := Encode(tt.kind, tt.sections); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// rehash recomputes the header hash after a directory edit.
func rehash(data []byte) []byte {
	h := headerHash(data, int(binary.LittleEndian.Uint32(data[12:])))
	copy(data[16:48], h[:])
	return data
}

// Verifies: BND-003, BND-005, SEC-054.
func TestReadRejectsMalformedContainers(t *testing.T) {
	t.Parallel()
	good := mustEncode(t, KindPlugin, sampleSections())
	edit := func(f func(d []byte) []byte) []byte { return f(bytes.Clone(good)) }
	entry := func(i int) int { return 48 + 72*i }
	tests := []struct {
		name string
		data []byte
		code plxerr.Code
	}{
		{"empty", nil, plxerr.BundleMalformed},
		{"magic", edit(func(d []byte) []byte { d[0] = 'X'; return d }), plxerr.BundleMalformed},
		{"version", edit(func(d []byte) []byte { d[4] = 2; return rehash(d) }), plxerr.BundleMalformed},
		{"bundle kind", edit(func(d []byte) []byte { d[6] = 9; return rehash(d) }), plxerr.BundleMalformed},
		{"unknown flag", edit(func(d []byte) []byte { d[8] = 4; return rehash(d) }), plxerr.BundleMalformed},
		{"encrypted", edit(func(d []byte) []byte { d[8] = 1; return rehash(d) }), plxerr.BundleEncryptedUnsupported},
		{"section count", edit(func(d []byte) []byte { d[12] = 200; return d }), plxerr.BundleMalformed},
		{"header hash", edit(func(d []byte) []byte { d[20] ^= 1; return d }), plxerr.BundleMalformed},
		{"reserved", edit(func(d []byte) []byte { d[entry(0)+18] = 1; return rehash(d) }), plxerr.BundleMalformed},
		{"kind zero", edit(func(d []byte) []byte { d[entry(0)+16] = 0; return rehash(d) }), plxerr.BundleMalformed},
		{"order", edit(func(d []byte) []byte { d[entry(1)] = 9; return rehash(d) }), plxerr.BundleMalformed},
		{"misaligned", edit(func(d []byte) []byte { d[entry(0)+20]++; return rehash(d) }), plxerr.BundleMalformed},
		{"overlap", edit(func(d []byte) []byte {
			binary.LittleEndian.PutUint64(d[entry(1)+20:], binary.LittleEndian.Uint64(d[entry(0)+20:]))
			return rehash(d)
		}), plxerr.BundleMalformed},
		{"length", edit(func(d []byte) []byte { binary.LittleEndian.PutUint64(d[entry(3)+28:], 1<<40); return rehash(d) }), plxerr.BundleMalformed},
		{"gap bytes", func() []byte {
			d := mustEncode(t, KindPlugin, append(sampleSections(), Section{Kind: SectionActions, Data: []byte("odd")}))
			d[firstGap(t, d)] = 1
			return d
		}(), plxerr.BundleMalformed},
		{"trailing", append(bytes.Clone(good), 0), plxerr.BundleMalformed},
		{"section hash", edit(func(d []byte) []byte { d[len(d)-1] ^= 1; return d }), plxerr.SectionHashMismatch},
	}
	for _, tt := range tests {
		_, err := Read(tt.data, defaultOptions())
		wantCode(t, tt.name, err, tt.code)
	}
}

// firstGap returns the offset of the first padding byte between sections.
func firstGap(t *testing.T, data []byte) int {
	t.Helper()
	n := int(binary.LittleEndian.Uint32(data[12:]))
	for i := range n - 1 {
		e := data[48+72*i:]
		end := u64(e, 20) + u64(e, 28)
		if next := u64(data, 48+72*(i+1)+20); end < next {
			return end
		}
	}
	t.Fatal("no gap between sections")
	return 0
}

// Verifies: BND-008, BND-018.
func TestRequiredFeaturesAndUnknownSections(t *testing.T) {
	t.Parallel()
	data := mustEncode(t, KindPlugin, sampleSections())
	opts := defaultOptions()
	opts.Supports = func(f string) bool { return f != "pxl.v1" }
	_, err := Read(data, opts)
	wantCode(t, "unsupported feature", err, plxerr.UnsupportedRequiredFeature)
	var pe *plxerr.Error
	if !errors.As(err, &pe) || pe.Details["feature"] != "pxl.v1" {
		t.Errorf("the error does not name the feature: %v", err)
	}
	opts.Supports = nil
	_, err = Read(data, opts)
	wantCode(t, "no feature support", err, plxerr.UnsupportedRequiredFeature)

	// An unknown section kind is skipped, whatever it holds.
	unknown := addRawSection(t, data, 200, []byte("future data"))
	b, err := Read(unknown, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if last := b.Sections[len(b.Sections)-1]; last.Kind.Known() || last.Kind.String() != "section(200)" {
		t.Errorf("unknown section read as %s", last.Kind)
	}
}

// addRawSection appends a section of any kind, bypassing Encode's checks.
func addRawSection(t *testing.T, data []byte, kind SectionKind, payload []byte) []byte {
	t.Helper()
	b, err := Read(data, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	sections := append(slices.Clone(b.Sections), Section{Kind: kind, ID: ID{7}, Data: payload})
	var out bytes.Buffer
	n := len(sections)
	out.Write(data[:12])
	out.Write(le32(n))
	out.Write(make([]byte, 32))
	offset := alignUp(48 + 72*n)
	for _, s := range sections {
		out.Write(s.ID[:])
		out.Write(binary.LittleEndian.AppendUint16(nil, uint16(s.Kind)))
		out.Write([]byte{0, 0})
		out.Write(le64(offset))
		out.Write(le64(len(s.Data)))
		sum := sha256.Sum256(s.Data)
		out.Write(sum[:])
		out.Write([]byte{0, 0, 0, 0})
		offset = alignUp(offset + len(s.Data))
	}
	for _, s := range sections {
		out.Write(make([]byte, alignUp(out.Len())-out.Len()))
		out.Write(s.Data)
	}
	return rehash(out.Bytes())
}

// Verifies: SEC-054, BND-009.
func TestExecutablePayloadsAreRejected(t *testing.T) {
	t.Parallel()
	data := mustEncode(t, KindPlugin, sampleSections())
	for _, f := range executableFormats {
		_, err := Read(addRawSection(t, data, 300, []byte(f.prefix+"payload")), defaultOptions())
		wantCode(t, f.name, err, plxerr.ExecutableContent)
	}
	// A known kind holding code fails verification: it is not a FlatBuffers buffer.
	sections := append(sampleSections(), Section{Kind: SectionAssetsIndex, ID: ID{1}, Data: []byte("\x7fELF\x02\x01\x01\x00 and more")})
	_, err := Read(mustEncode(t, KindPlugin, sections), defaultOptions())
	wantCode(t, "ELF as assets-index", err, plxerr.SectionVerificationFailed)
	// A wasm section needs its feature.
	sections = append(sampleSections(), Section{Kind: SectionWasm, ID: ID{1}, Data: wasmSection()})
	_, err = Read(mustEncode(t, KindPlugin, sections), defaultOptions())
	wantCode(t, "undeclared wasm", err, plxerr.BundleMalformed)
	sections[3].Data = metaSection(fbs.BundleKindPlugin, FeatureWasm)
	if _, err := Read(mustEncode(t, KindPlugin, sections), defaultOptions()); err != nil {
		t.Errorf("declared wasm: %v", err)
	}
}

// Verifies: BND-010, LIM-001.
func TestSizeLimits(t *testing.T) {
	t.Parallel()
	data := mustEncode(t, KindPlugin, sampleSections())
	set := func(k limits.Key, v int64) ReadOptions {
		opts := defaultOptions()
		var err error
		if opts.Limits, err = opts.Limits.Tighten(k, limits.ScopePlugin, v); err != nil {
			t.Fatal(err)
		}
		return opts
	}
	_, err := Read(data, set(limits.BundlePluginSize, int64(len(data)-1)))
	wantCode(t, "plugin size", err, plxerr.LimitExceeded)
	_, err = Read(data, set(limits.BundlePageSectionSize, 64))
	wantCode(t, "page section size", err, plxerr.LimitExceeded)
}

func TestMetaChecks(t *testing.T) {
	t.Parallel()
	noMeta := sampleSections()[:3]
	_, err := Read(mustEncode(t, KindPlugin, noMeta), defaultOptions())
	wantCode(t, "no meta", err, plxerr.BundleMalformed)
	_, err = Read(mustEncode(t, KindApp, sampleSections()), defaultOptions())
	wantCode(t, "kind mismatch", err, plxerr.BundleMalformed)
}

func TestLocaleID(t *testing.T) {
	t.Parallel()
	id, err := LocaleID("am-ET")
	if err != nil || string(bytes.TrimRight(id[:], "\x00")) != "am-ET" {
		t.Errorf("LocaleID = %q, %v", id, err)
	}
	for _, bad := range []string{"", "a-very-long-language-tag", "ü"} {
		if _, err := LocaleID(bad); err == nil {
			t.Errorf("LocaleID(%q) accepted", bad)
		}
	}
}
