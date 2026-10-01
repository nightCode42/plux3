// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// verifierVectorsPath is read by the runtime's verifier tests (ADR-0029).
var verifierVectorsPath = filepath.Join("..", "..", "..", "schema", "testdata", "verifier", "vectors.json")

// verifierCase is one mutated section of a golden bundle and this
// verifier's verdict on it. Edits are [offset, byte] pairs applied to the
// section's bytes, so the file stays small.
type verifierCase struct {
	Bundle  string   `json:"bundle"`
	Section int      `json:"section"`
	Kind    int      `json:"kind"`
	Edits   [][2]int `json:"edits"`
	Valid   bool     `json:"valid"`
}

type verifierFile struct {
	Comment string         `json:"$comment"`
	Cases   []verifierCase `json:"cases"`
}

// Verifies: BND-006, QA-004.
// The corpus pins this verifier's verdict on mutated sections, so the
// runtime's verifier is checked to accept and reject exactly the same
// buffers (ADR-0029).
func TestVerifierVectors(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "schema", "testdata", "bundles")
	var names []string
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err == nil && filepath.Ext(path) == ".pxb" && !bytes.Contains([]byte(path), []byte(".dev.")) {
			rel, _ := filepath.Rel(root, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	rng := rand.New(rand.NewPCG(3042, 6)) //nolint:gosec // G404: reproducible test data, not security.
	lim := limits.Defaults()
	var cases []verifierCase
	valid := 0
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		b, err := ReadStructure(data)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range b.Sections {
			if !s.Kind.Known() || len(s.Data) < 8 {
				continue
			}
			cases = append(cases, verifierCase{Bundle: name, Section: i, Kind: int(s.Kind), Edits: [][2]int{}, Valid: verify(s.Kind, s.Data, lim) == nil})
			for range 12 {
				c := verifierCase{Bundle: name, Section: i, Kind: int(s.Kind)}
				mutated := bytes.Clone(s.Data)
				for range rng.IntN(3) + 1 {
					at := rng.IntN(len(mutated))
					if at < 8 && rng.IntN(4) != 0 {
						at += 8 // mostly past the root offset and identifier
						at %= len(mutated)
					}
					v := rng.IntN(256)
					mutated[at] = byte(v) //nolint:gosec // G115: 0–255.
					c.Edits = append(c.Edits, [2]int{at, v})
				}
				c.Valid = verify(s.Kind, mutated, lim) == nil
				if c.Valid {
					valid++
				}
				cases = append(cases, c)
			}
		}
	}
	if valid == 0 {
		t.Error("no mutation was accepted; the corpus would not test acceptance")
	}
	out, err := json.MarshalIndent(verifierFile{
		Comment: "FlatBuffers verifier conformance corpus (BND-006, ADR-0029), written by backend/internal/bundle (go test -update). Each case applies [offset, byte] edits to one section of a golden bundle; valid is the Go verifier's verdict under the default limits.",
		Cases:   cases,
	}, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if *update {
		if err := os.MkdirAll(filepath.Dir(verifierVectorsPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(verifierVectorsPath, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(verifierVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, golden) {
		t.Errorf("%s differs; run the test with -update and review", verifierVectorsPath)
	}
}
