// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package benchproject

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// update rewrites the benchmark app's baseline.
var update = flag.Bool("update", false, "rewrite test/bench/runtime/assets/plux")

// baselineDir is the runtime benchmark app's embedded release.
var baselineDir = filepath.Join("..", "..", "..", "test", "bench", "runtime", "assets", "plux")

// BaselinePlugins is how many plugins the runtime benchmark's release
// holds: the fifty of NFR-008.
const BaselinePlugins = 50

func compile(t *testing.T, plugins, revision int) *compiler.Result {
	t.Helper()
	res := compiler.Compile(Project(plugins, revision), Options())
	if len(res.Diagnostics) > 0 || res.App == nil {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	return res
}

// countSlots counts the widget nodes of a page tree.
func countSlots(n any) int {
	v, ok := n.(map[string]any)
	if !ok {
		return 0
	}
	c := 0
	if _, ok := v["type"]; ok {
		c = 1
	}
	if slots, ok := v["slots"].(map[string]any); ok {
		for _, x := range slots {
			c += countSlots(x)
		}
	}
	if children, ok := v["children"].([]any); ok {
		for _, x := range children {
			c += countSlots(x)
		}
	}
	return c
}

// Verifies: QA-007.
// The project compiles without a diagnostic, the catalog page has the
// node count NFR-002 and NFR-003 measure, and a revision changes exactly
// the first three plugins' bundles.
func TestProject(t *testing.T) {
	t.Parallel()
	var page struct{ Root any }
	if err := json.Unmarshal(Project(5, 1)["plugins/catalog/pages/catalog.page.json"].Data, &page); err != nil {
		t.Fatal(err)
	}
	if n := countSlots(page.Root); n != CatalogNodes {
		t.Errorf("the catalog page has %d nodes, want %d", n, CatalogNodes)
	}
	one, two := compile(t, 5, 1), compile(t, 5, 2)
	if one.App.Hash != two.App.Hash {
		t.Error("a revision changed the app bundle")
	}
	var changed []string
	// The compiler orders plugins by key.
	for i, p := range one.Plugins {
		if p.Key != two.Plugins[i].Key {
			t.Fatalf("plugin %d is %s and %s", i, p.Key, two.Plugins[i].Key)
		}
		if p.Hash != two.Plugins[i].Hash {
			changed = append(changed, p.Key)
		}
	}
	if !slices.Equal(changed, []string{"catalog", "extra01", "feed"}) {
		t.Errorf("revision 2 changed %v", changed)
	}
	if got := Project(1, 1); len(got) != 2+2*Changed {
		t.Errorf("a project of one plugin has %d files, want the minimum of %d plugins", len(got), Changed)
	}
}

// baseline, entry, asset and key mirror what `plux pull` writes
// (backend/cmd/plux), which the runtime reads (SYN-007).
type baseline struct {
	App         string  `json:"app"`
	Environment string  `json:"environment"`
	Channel     string  `json:"channel"`
	Sequence    int64   `json:"releaseSequence"`
	Bundles     []entry `json:"bundles"`
	Assets      []asset `json:"assets"`
	Keys        []key   `json:"keys"`
}

type entry struct {
	Plugin    string `json:"plugin"`
	Version   int64  `json:"version"`
	SHA256    string `json:"sha256"`
	File      string `json:"file"`
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

type asset struct {
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

type key struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Role      string `json:"role"`
	PublicKey string `json:"publicKey"`
}

// Verifies: QA-007, NFR-008.
// The runtime benchmark embeds the fifty-plugin project as `plux pull`
// would write it, signed with a key that exists only for this benchmark
// (its seed is in this file): the app trusts it through the baseline's
// keys.json and never talks to a server. Ed25519 and the compiler are
// deterministic, so the files are pinned; -update rewrites them.
func TestRuntimeBaseline(t *testing.T) {
	t.Parallel()
	res := compile(t, BaselinePlugins, 1)
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte("bench"), 7)[:ed25519.SeedSize])
	pub, _ := priv.Public().(ed25519.PublicKey)
	keyID := signing.KeyID(pub)
	files := map[string][]byte{}
	b := baseline{
		App: AppID, Environment: "production", Channel: "production", Sequence: 1,
		Keys: []key{{KeyID: keyID, Algorithm: signing.Algorithm, Role: "targets", PublicKey: hex.EncodeToString(pub)}},
	}
	for _, c := range append([]*compiler.Bundle{res.App}, res.Plugins...) {
		name := "bundles/" + c.Key + ".pxb"
		if c == res.App {
			name = "bundles/_app.pxb"
		}
		files[name] = c.Data
		plugin := c.Key
		if c == res.App {
			plugin = ""
		}
		b.Bundles = append(b.Bundles, entry{
			Plugin: plugin, Version: 1, SHA256: hex.EncodeToString(c.Hash[:]), File: name,
			KeyID: keyID, Algorithm: signing.Algorithm, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, c.Hash[:])),
		})
	}
	for _, sum := range slices.SortedFunc(maps.Keys(res.Files), func(a, b [sha256.Size]byte) int { return bytes.Compare(a[:], b[:]) }) {
		name := "assets/" + hex.EncodeToString(sum[:])
		files[name] = res.Files[sum]
		b.Assets = append(b.Assets, asset{SHA256: hex.EncodeToString(sum[:]), File: name})
	}
	for name, v := range map[string]any{"baseline.json": b, "keys.json": b.Keys} {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		files[name] = append(data, '\n')
	}

	if *update {
		for _, sub := range []string{"bundles", "assets"} {
			if err := os.RemoveAll(filepath.Join(baselineDir, sub)); err != nil {
				t.Fatal(err)
			}
		}
		for name, data := range files {
			path := filepath.Join(baselineDir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var onDisk []string
	for _, sub := range []string{"bundles", "assets"} {
		entries, err := os.ReadDir(filepath.Join(baselineDir, sub))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != ".gitkeep" {
				onDisk = append(onDisk, sub+"/"+e.Name())
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		got, err := os.ReadFile(filepath.Join(baselineDir, name)) //nolint:gosec // G304: a path under the repository.
		if err != nil || !bytes.Equal(got, files[name]) {
			t.Errorf("%s differs from what the generator writes; run the test with -update and review", name)
		}
	}
	if want := len(files) - 2; len(onDisk) != want {
		t.Errorf("%s holds %d bundle and asset files, the generator writes %d; run the test with -update", baselineDir, len(onDisk), want)
	}
}
