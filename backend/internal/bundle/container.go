// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Kind is the kind of a bundle (Appendix B.1).
type Kind uint16

// Bundle kinds.
const (
	KindPlugin      Kind = 1
	KindApp         Kind = 2
	KindDevelopment Kind = 3
)

// SectionKind is the kind of a section (Appendix B.2).
type SectionKind uint16

// Section kinds; the codes are permanent (ADR-0002).
const (
	SectionMeta        SectionKind = 1
	SectionPage        SectionKind = 2
	SectionComponent   SectionKind = 3
	SectionActions     SectionKind = 4
	SectionPXL         SectionKind = 5
	SectionStyles      SectionKind = 6
	SectionStrings     SectionKind = 7
	SectionL10n        SectionKind = 8
	SectionTimelines   SectionKind = 9
	SectionSchemas     SectionKind = 10
	SectionAssetsIndex SectionKind = 11
	SectionWasm        SectionKind = 12
	SectionSourceMap   SectionKind = 13
)

// sectionInfo is the name and FlatBuffers file identifier of a known kind.
var sectionInfo = map[SectionKind]struct{ name, ident string }{
	SectionMeta: {"meta", "PXMT"}, SectionPage: {"page", "PXPG"}, SectionComponent: {"component", "PXCO"},
	SectionActions: {"actions", "PXAC"}, SectionPXL: {"pxl", "PXEX"}, SectionStyles: {"styles", "PXST"},
	SectionStrings: {"strings", "PXSG"}, SectionL10n: {"l10n", "PXLN"}, SectionTimelines: {"timelines", "PXTL"},
	SectionSchemas: {"schemas", "PXSC"}, SectionAssetsIndex: {"assets-index", "PXAS"}, SectionWasm: {"wasm", "PXWM"},
	SectionSourceMap: {"sourcemap", "PXSM"},
}

// String returns the kind's name, or its number when unknown.
func (k SectionKind) String() string {
	if info, ok := sectionInfo[k]; ok {
		return info.name
	}
	return fmt.Sprintf("section(%d)", uint16(k))
}

// Known reports whether this reader understands the kind.
func (k SectionKind) Known() bool {
	_, ok := sectionInfo[k]
	return ok
}

// Finish completes a section built with builder, writing the file
// identifier of kind k after the root offset (BND-012).
func Finish(builder *flatbuffers.Builder, root flatbuffers.UOffsetT, k SectionKind) {
	builder.FinishWithFileIdentifier(root, []byte(sectionInfo[k].ident))
}

// ID identifies a section within its kind: a document UUID, or a BCP 47
// tag padded with NUL bytes for l10n sections.
type ID [16]byte

// LocaleID returns the ID of a locale's l10n section.
func LocaleID(tag string) (ID, error) {
	var id ID
	if tag == "" || len(tag) > len(id) {
		return id, fmt.Errorf("bundle.LocaleID: tag %q must have 1 to 16 bytes", tag)
	}
	for i := range len(tag) {
		if c := tag[i]; c < 0x21 || c > 0x7E {
			return id, fmt.Errorf("bundle.LocaleID: tag %q is not printable ASCII", tag)
		}
	}
	copy(id[:], tag)
	return id, nil
}

// Section is one section. In a bundle returned by Read, Data aliases the
// input: sections are read in place, never copied (BND-003).
type Section struct {
	Kind SectionKind
	ID   ID
	Data []byte
	// Hash is the SHA-256 of Data (BND-005).
	Hash [sha256.Size]byte
}

// Header flags.
const (
	FlagEncrypted uint32 = 1 << 0
	FlagSourceMap uint32 = 1 << 1
	knownFlags           = FlagEncrypted | FlagSourceMap
)

// Layout constants of Appendix B.1.
const (
	magic            = "PLUX"
	containerVersion = 1
	headerSize       = 48
	entrySize        = 72
	sectionAlign     = 8
)

// Encode lays out sections in a container of kind k and returns its bytes.
// Sections are ordered by (kind, id), so the output depends on content
// only (CMP-002). Only development bundles may carry a source map
// (CMP-041).
func Encode(k Kind, sections []Section) ([]byte, error) {
	if k < KindPlugin || k > KindDevelopment {
		return nil, fmt.Errorf("bundle.Encode: unknown bundle kind %d", k)
	}
	sorted := slices.Clone(sections)
	slices.SortFunc(sorted, compareSections)
	var flags uint32
	for i, s := range sorted {
		if !s.Kind.Known() {
			return nil, fmt.Errorf("bundle.Encode: unknown section kind %d", s.Kind)
		}
		if i > 0 && compareSections(sorted[i-1], s) == 0 {
			return nil, fmt.Errorf("bundle.Encode: duplicate %s section %x", s.Kind, s.ID)
		}
		if s.Kind == SectionSourceMap {
			if k != KindDevelopment {
				return nil, fmt.Errorf("bundle.Encode: a %s section is allowed only in development bundles", s.Kind)
			}
			flags |= FlagSourceMap
		}
	}
	dirEnd := headerSize + entrySize*len(sorted)
	var out bytes.Buffer
	out.Grow(dirEnd + len(sorted)*sectionAlign + totalSize(sorted))
	out.WriteString(magic)
	out.Write(binary.LittleEndian.AppendUint16(nil, containerVersion))
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(k)))
	out.Write(binary.LittleEndian.AppendUint32(nil, flags))
	out.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(sorted)))) //nolint:gosec // G115: bounded by memory.
	out.Write(make([]byte, sha256.Size))                                  // header hash, filled below
	offset := alignUp(dirEnd)
	for _, s := range sorted {
		out.Write(s.ID[:])
		out.Write(binary.LittleEndian.AppendUint16(nil, uint16(s.Kind)))
		out.Write([]byte{0, 0})
		out.Write(binary.LittleEndian.AppendUint64(nil, uint64(offset)))      //nolint:gosec // G115: offsets are non-negative.
		out.Write(binary.LittleEndian.AppendUint64(nil, uint64(len(s.Data)))) //nolint:gosec // G115: lengths are non-negative.
		sum := sha256.Sum256(s.Data)
		out.Write(sum[:])
		out.Write([]byte{0, 0, 0, 0})
		offset = alignUp(offset + len(s.Data))
	}
	for _, s := range sorted {
		out.Write(make([]byte, alignUp(out.Len())-out.Len()))
		out.Write(s.Data)
	}
	data := out.Bytes()
	h := headerHash(data, len(sorted))
	copy(data[16:headerSize], h[:])
	return data, nil
}

// compareSections orders sections by kind, then ID.
func compareSections(a, b Section) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), bytes.Compare(a.ID[:], b.ID[:]))
}

func totalSize(sections []Section) int {
	n := 0
	for _, s := range sections {
		n += len(s.Data)
	}
	return n
}

func alignUp(n int) int { return (n + sectionAlign - 1) &^ (sectionAlign - 1) }

// headerHash is the bundle hash: SHA-256 of bytes 0–15 followed by the
// section directory (BND-005).
func headerHash(data []byte, count int) [sha256.Size]byte {
	h := sha256.New()
	h.Write(data[:16])
	h.Write(data[headerSize : headerSize+entrySize*count])
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return sum
}

// Bundle is a read, fully checked bundle.
type Bundle struct {
	Kind  Kind
	Flags uint32
	// Hash is the bundle hash (BND-005); signing it signs every section.
	Hash [sha256.Size]byte
	// Sections are in directory order, unknown kinds included.
	Sections []Section
	// Meta is the meta section's content.
	Meta *fbs.Meta
}

// Section returns the section of kind k with ID id.
func (b *Bundle) Section(k SectionKind, id ID) (Section, bool) {
	i, found := slices.BinarySearchFunc(b.Sections, Section{Kind: k, ID: id}, compareSections)
	if !found {
		return Section{}, false
	}
	return b.Sections[i], true
}

// ReadOptions configures Read.
type ReadOptions struct {
	// Limits bounds sizes and the verifier (LIM-001).
	Limits limits.Set
	// Supports reports whether the reader supports a required feature;
	// a bundle requiring an unsupported feature is refused (BND-008).
	Supports func(feature string) bool
}

// Read checks a bundle and returns it; data must not be modified while
// the bundle is in use. Errors carry the codes PLX-3040–3043, PLX-3010,
// PLX-1320 or PLX-1503.
func Read(data []byte, opts ReadOptions) (*Bundle, error) {
	b, err := readHeader(data, &opts.Limits)
	if err != nil {
		return nil, err
	}
	if err := readDirectory(data, b); err != nil {
		return nil, err
	}
	for _, s := range b.Sections {
		if err := checkSection(s, opts.Limits); err != nil {
			return nil, err
		}
	}
	if err := checkMeta(b, opts.Supports); err != nil {
		return nil, err
	}
	return b, nil
}

// ReadStructure checks a bundle's header, directory and section hashes,
// but neither its size limit nor its sections' contents. It serves code
// that handles bundles as opaque sections — the delta encoder and applier
// — which never interprets a section.
func ReadStructure(data []byte) (*Bundle, error) {
	b, err := readHeader(data, nil)
	if err != nil {
		return nil, err
	}
	if err := readDirectory(data, b); err != nil {
		return nil, err
	}
	for _, s := range b.Sections {
		if sha256.Sum256(s.Data) != s.Hash {
			return nil, newErr(plxerr.SectionHashMismatch, "%s section %x", s.Kind, s.ID)
		}
	}
	return b, nil
}

// newErr returns a coded error (ADR-0018).
func newErr(code plxerr.Code, format string, args ...any) error {
	return &plxerr.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrapErr returns a coded error wrapping its cause.
func wrapErr(code plxerr.Code, cause error, format string, args ...any) error {
	return &plxerr.Error{Code: code, Message: fmt.Sprintf(format, args...), Err: cause}
}

func malformed(format string, args ...any) error {
	return newErr(plxerr.BundleMalformed, format, args...)
}

// readHeader checks the fixed header and the header hash.
func readHeader(data []byte, lim *limits.Set) (*Bundle, error) {
	if len(data) < headerSize || string(data[:4]) != magic {
		return nil, malformed("not a Plux bundle")
	}
	if v := binary.LittleEndian.Uint16(data[4:]); v != containerVersion {
		return nil, malformed("container version %d", v)
	}
	b := &Bundle{Kind: Kind(binary.LittleEndian.Uint16(data[6:])), Flags: binary.LittleEndian.Uint32(data[8:])}
	if b.Kind < KindPlugin || b.Kind > KindDevelopment {
		return nil, malformed("bundle kind %d", b.Kind)
	}
	if b.Flags&^knownFlags != 0 {
		return nil, malformed("unknown flags %#x", b.Flags)
	}
	if b.Flags&FlagEncrypted != 0 {
		return nil, newErr(plxerr.BundleEncryptedUnsupported, "the bundle is encrypted")
	}
	if lim != nil {
		key := limits.BundlePluginSize
		if b.Kind == KindApp {
			key = limits.ReleaseAppSize
		}
		if max := lim.Get(key); int64(len(data)) > max {
			return nil, newErr(plxerr.LimitExceeded, "the bundle has %d bytes, more than %s = %d", len(data), key, max)
		}
	}
	count := int64(binary.LittleEndian.Uint32(data[12:]))
	if count > int64(len(data)-headerSize)/entrySize {
		return nil, malformed("%d sections do not fit in %d bytes", count, len(data))
	}
	b.Sections = make([]Section, count)
	copy(b.Hash[:], data[16:headerSize])
	if headerHash(data, int(count)) != b.Hash {
		return nil, malformed("the header hash does not match")
	}
	return b, nil
}

// readDirectory reads the entries: ordered, aligned, in bounds, without
// overlap, with zero-filled gaps and nothing after the last section.
func readDirectory(data []byte, b *Bundle) error {
	end := headerSize + entrySize*len(b.Sections)
	for i := range b.Sections {
		e := data[headerSize+entrySize*i : headerSize+entrySize*(i+1)]
		s := &b.Sections[i]
		copy(s.ID[:], e[:16])
		s.Kind = SectionKind(binary.LittleEndian.Uint16(e[16:]))
		offset, length := binary.LittleEndian.Uint64(e[20:]), binary.LittleEndian.Uint64(e[28:])
		copy(s.Hash[:], e[36:68])
		switch {
		case s.Kind == 0 || !allZero(e[18:20]) || !allZero(e[68:72]):
			return malformed("directory entry %d: invalid kind or reserved bytes", i)
		case i > 0 && compareSections(b.Sections[i-1], *s) >= 0:
			return malformed("directory entry %d is out of order or duplicated", i)
		case offset%sectionAlign != 0 || offset < uint64(end) || offset > uint64(len(data)) || length > uint64(len(data))-offset: //nolint:gosec // G115: len is non-negative.
			return malformed("directory entry %d: offset %d and length %d do not fit", i, offset, length)
		case !allZero(data[end:offset]):
			return malformed("non-zero bytes before section %d", i)
		}
		s.Data = data[offset : offset+length : offset+length]
		end = int(offset + length) //nolint:gosec // G115: bounded by len(data).
	}
	if end != len(data) {
		return malformed("%d bytes after the last section", len(data)-end)
	}
	return nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// checkSection checks a section's hash, content and structure.
func checkSection(s Section, lim limits.Set) error {
	if sha256.Sum256(s.Data) != s.Hash {
		return newErr(plxerr.SectionHashMismatch, "%s section %x", s.Kind, s.ID)
	}
	if !s.Kind.Known() {
		if format := ExecutableFormat(s.Data); format != "" {
			return newErr(plxerr.ExecutableContent, "%s section %x holds %s code", s.Kind, s.ID, format)
		}
		return nil // skipped (BND-018); a needed one is named by a required feature
	}
	if key, bounded := sectionLimits[s.Kind]; bounded {
		if max := lim.Get(key); int64(len(s.Data)) > max {
			return newErr(plxerr.LimitExceeded, "%s section %x has %d bytes, more than %s = %d", s.Kind, s.ID, len(s.Data), key, max)
		}
	}
	if err := verify(s.Kind, s.Data, lim); err != nil {
		return wrapErr(plxerr.SectionVerificationFailed, err, "%s section %x", s.Kind, s.ID)
	}
	return nil
}

// sectionLimits bounds the size of section kinds (BND-010).
var sectionLimits = map[SectionKind]limits.Key{
	SectionPage: limits.BundlePageSectionSize,
	SectionWasm: limits.BundleDeviceFunctionModuleSize,
}

// executableFormats are the signatures of code that runs outside the PXL
// VM and the action interpreter (SEC-054, BND-009). A known section must
// pass the verifier as a FlatBuffers buffer of its kind, so it cannot be
// such code; a section of an unknown kind is never read, and is refused
// when it carries one of these formats.
var executableFormats = []struct{ name, prefix string }{
	{"ELF", "\x7fELF"},
	{"Mach-O", "\xfe\xed\xfa\xce"},
	{"Mach-O", "\xfe\xed\xfa\xcf"},
	{"Mach-O", "\xce\xfa\xed\xfe"},
	{"Mach-O", "\xcf\xfa\xed\xfe"},
	{"Mach-O universal", "\xca\xfe\xba\xbe"},
	{"PE", "MZ"},
	{"Dalvik", "dex\n"},
	{"Dart kernel", "\x90\xab\xcd\xef"},
	{"Dart snapshot", "\xf5\xf5\xdc\xdc"},
	{"WebAssembly", "\x00asm"},
	{"script", "#!"},
}

// ExecutableFormat names the executable format data starts with, or ""
// when it starts with none. The compiler uses it on asset files too.
func ExecutableFormat(data []byte) string {
	for _, f := range executableFormats {
		if bytes.HasPrefix(data, []byte(f.prefix)) {
			return f.name
		}
	}
	return ""
}

// FeatureWasm is the required feature of bundles with a wasm section.
const FeatureWasm = "section.wasm.v1"

// checkMeta reads the meta section: exactly one, of the bundle's kind,
// with only supported required features (BND-008); a source map only in
// development bundles, matching the flag (CMP-041); and a wasm section
// only when FeatureWasm is required.
func checkMeta(b *Bundle, supports func(string) bool) error {
	var meta []Section
	hasSourceMap, hasWasm := false, false
	for _, s := range b.Sections {
		switch s.Kind {
		case SectionMeta:
			meta = append(meta, s)
		case SectionSourceMap:
			hasSourceMap = true
		case SectionWasm:
			hasWasm = true
		}
	}
	if len(meta) != 1 {
		return malformed("%d meta sections", len(meta))
	}
	if hasSourceMap != (b.Flags&FlagSourceMap != 0) || (hasSourceMap && b.Kind != KindDevelopment) {
		return malformed("a source map is allowed only in development bundles, with the source-map flag")
	}
	b.Meta = fbs.GetRootAsMeta(meta[0].Data, 0)
	if kind := b.Meta.Kind(); Kind(kind) != b.Kind {
		return malformed("the meta section says bundle kind %d, the header %d", kind, b.Kind)
	}
	wasmDeclared := false
	for i := range b.Meta.RequiredFeaturesLength() {
		feature := string(b.Meta.RequiredFeatures(i))
		wasmDeclared = wasmDeclared || feature == FeatureWasm
		if supports == nil || !supports(feature) {
			e := &plxerr.Error{Code: plxerr.UnsupportedRequiredFeature, Message: fmt.Sprintf("the bundle requires %q", feature)}
			return e.WithDetail("feature", feature)
		}
	}
	if hasWasm && !wasmDeclared {
		return malformed("a wasm section without the required feature %s", FeatureWasm)
	}
	return nil
}

// MediaType is the media type bundles are stored and served with.
const MediaType = "application/vnd.plux.bundle"
