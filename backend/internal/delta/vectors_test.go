// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package delta

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// update rewrites the conformance vectors.
var update = flag.Bool("update", false, "rewrite schema/testdata/delta/vectors.json")

// vectorsPath is read by the Dart delta applier's tests.
var vectorsPath = filepath.Join("..", "..", "..", "schema", "testdata", "delta", "vectors.json")

// vector is one conformance case: applying delta to old must give new, or
// fail with error. When exact is false any of PLX-3011 and PLX-3012 is
// acceptable: a corrupted zstd frame may be caught by the frame decoder in
// one implementation and by the hash check in another.
type vector struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	Delta   string `json:"delta"`
	MaxSize int64  `json:"maxSize"`
	New     string `json:"new,omitempty"`
	Error   string `json:"error,omitempty"`
	Exact   bool   `json:"exact,omitempty"`
}

type vectorFile struct {
	Comment string   `json:"$comment"`
	Vectors []vector `json:"vectors"`
}

// Verifies: QA-002, REL-025.
// The vectors pin this package's deltas and outcomes, so the device's
// applier is checked against the server's encoder byte for byte (ADR-0030).
func TestConformanceVectors(t *testing.T) {
	t.Parallel()
	vs := conformanceVectors(t)
	data, err := json.MarshalIndent(vectorFile{
		Comment: "Section-delta conformance vectors (ADR-0003, ADR-0030), written by backend/internal/delta (go test -update). Bytes are base64. Applying delta to old gives new, or fails with error; exact=false accepts PLX-3011 or PLX-3012.",
		Vectors: vs,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if *update {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, golden) {
		t.Errorf("%s differs; run the test with -update and review", vectorsPath)
	}
}

// conformanceVectors builds the vectors deterministically: generated
// bundle pairs, a pair of real compiled bundles, and damaged deltas.
func conformanceVectors(t *testing.T) []vector {
	t.Helper()
	rng := rand.New(rand.NewPCG(3011, 3012)) //nolint:gosec // G404: reproducible test data, not security.
	var vs []vector
	add := func(name string, old, d []byte, maxSize int64) {
		v := vector{Name: name, Old: b64(old), Delta: b64(d), MaxSize: maxSize}
		out, err := Apply(old, d, maxSize)
		if err == nil {
			v.New = b64(out)
		} else {
			var pe *plxerr.Error
			if !errors.As(err, &pe) {
				t.Fatalf("%s: untyped error %v", name, err)
			}
			v.Error = pe.Code.String()
			v.Exact = !bytes.HasPrefix([]byte(name), []byte("corrupt-"))
		}
		vs = append(vs, v)
	}
	for i := range 12 {
		oldSecs := randomSections(rng, 10)
		newSecs := mutateSections(rng, oldSecs)
		old, d := pair(t, oldSecs, newSecs)
		add(fmt.Sprintf("generated-%02d", i), old, d, maxSize)
	}
	// A real compiled plugin bundle with one page section edited.
	loans, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "testdata", "bundles", "loan-calculator", "loans.pxb"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := bundle.ReadStructure(loans)
	if err != nil {
		t.Fatal(err)
	}
	var edited []bundle.Section
	for _, s := range base.Sections {
		data := bytes.Clone(s.Data)
		if s.Kind == bundle.SectionPage {
			data[len(data)/2] ^= 0x20
		}
		edited = append(edited, bundle.Section{Kind: s.Kind, ID: s.ID, Data: data})
	}
	newLoans, err := bundle.Encode(base.Kind, edited)
	if err != nil {
		t.Fatal(err)
	}
	loansDelta, err := Diff(loans, newLoans)
	if err != nil {
		t.Fatal(err)
	}
	add("compiled-plugin-page-edited", loans, loansDelta, maxSize)
	add("compiled-plugin-identity", loans, mustDiff(t, loans, loans), maxSize)

	// Damage one generated delta in every structural way.
	oldSecs := randomSections(rng, 8)
	old, d := pair(t, oldSecs, mutateSections(rng, oldSecs))
	add("wrong-base", mustEncode(t, randomSections(rng, 3)), d, maxSize)
	add("bad-magic", old, patched(d, 0, 'X'), maxSize)
	add("bad-version", old, patched(d, 4, 9), maxSize)
	add("unknown-flags", old, patched(d, 12, 1), maxSize)
	add("truncated-header", old, d[:40], maxSize)
	add("trailing-bytes", old, append(bytes.Clone(d), 0), maxSize)
	add("too-large", old, d, 16)
	if len(d) > headerSize+instrSize {
		add("unknown-operation", old, patched(d, headerSize+18, 7), maxSize)
		add("reserved-byte", old, patched(d, headerSize+19, 1), maxSize)
		add("truncated-instruction", old, d[:headerSize+instrSize-1], maxSize)
		add("truncated-payload", old, d[:len(d)-1], maxSize)
		size := binary.LittleEndian.Uint64(d[headerSize+20:])
		add("wrong-section-size", old, patchedU64(d, headerSize+20, size+1), maxSize)
	}
	// Flip a byte inside every payload of a patch-heavy delta.
	for i, p := range payloads(d) {
		at := p[0] + p[1] - 1 - (p[1]-1)/3
		add(fmt.Sprintf("corrupt-payload-%02d", i), old, patched(d, at, d[at]^0xff), maxSize)
		if i == 5 {
			break
		}
	}
	return vs
}

func pair(t *testing.T, a, b []bundle.Section) (old, d []byte) {
	t.Helper()
	old = mustEncode(t, a)
	return old, mustDiff(t, old, mustEncode(t, b))
}

func mustEncode(t *testing.T, s []bundle.Section) []byte {
	t.Helper()
	b, err := bundle.Encode(bundle.KindPlugin, s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustDiff(t *testing.T, a, b []byte) []byte {
	t.Helper()
	d, err := Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// randomSections draws up to n sections with distinct (kind, ID); the data
// is text-like, so zstd patches are smaller than whole sections.
func randomSections(rng *rand.Rand, n int) []bundle.Section {
	const alphabet = "abcdefghij klmnop,.;"
	seen := map[[17]byte]bool{}
	var out []bundle.Section
	for range rng.IntN(n) + 1 {
		s := bundle.Section{Kind: bundle.SectionKind(rng.IntN(11) + 1)} //nolint:gosec // G115: 1–11.
		s.ID[0] = byte(rng.IntN(4))                                     //nolint:gosec // G115: 0–3.
		key := [17]byte{byte(s.Kind)}                                   //nolint:gosec // G115: 1–11.
		copy(key[1:], s.ID[:])
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Data = make([]byte, rng.IntN(300))
		for i := range s.Data {
			s.Data[i] = alphabet[rng.IntN(len(alphabet))]
		}
		out = append(out, s)
	}
	return out
}

// mutateSections keeps, edits, grows, shrinks, removes and adds sections.
func mutateSections(rng *rand.Rand, old []bundle.Section) []bundle.Section {
	var out []bundle.Section
	for _, s := range old {
		d := bytes.Clone(s.Data)
		switch rng.IntN(5) {
		case 0:
			continue
		case 2:
			if len(d) > 0 {
				d[rng.IntN(len(d))] = 'Z'
			}
		case 3:
			d = append(d, []byte("appended text")...)
		case 4:
			d = d[:len(d)/2]
		}
		out = append(out, bundle.Section{Kind: s.Kind, ID: s.ID, Data: d})
	}
	extra := bundle.Section{Kind: bundle.SectionL10n}
	extra.ID[15] = 0xee
	extra.Data = []byte("a new section")
	return append(out, extra)
}

// payloads returns the start and length of each whole or patch payload.
func payloads(d []byte) [][2]int {
	var out [][2]int
	for at := headerSize; at+instrSize <= len(d); {
		n := int(binary.LittleEndian.Uint32(d[at+28:]))
		if d[at+18] != opReuse && n > hashSize {
			out = append(out, [2]int{at + instrSize, n})
		}
		at += instrSize + n
	}
	return out
}

func patched(d []byte, at int, b byte) []byte {
	c := bytes.Clone(d)
	c[at] = b
	return c
}

func patchedU64(d []byte, at int, v uint64) []byte {
	c := bytes.Clone(d)
	binary.LittleEndian.PutUint64(c[at:], v)
	return c
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
