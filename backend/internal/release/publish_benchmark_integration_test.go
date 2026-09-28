// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// uuidPattern matches the identifiers of a document.
var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// fiftyPages returns the loan calculator with 48 copies of its result
// page, each with fresh identifiers: a 50-page plugin.
func fiftyPages(t *testing.T) []document.File {
	t.Helper()
	files := project(t)
	const source = "plugins/loans/pages/result.page.json"
	var page []byte
	shared := map[string]bool{}
	for _, f := range files {
		switch f.Path {
		case source:
			page = f.Content
		case "plugins/loans/plugin.json":
		default:
			for _, id := range uuidPattern.FindAllString(string(f.Content), -1) {
				shared[id] = true
			}
		}
	}
	var own []string
	for _, id := range uuidPattern.FindAllString(string(page), -1) {
		if !shared[id] && !slices.Contains(own, id) {
			own = append(own, id)
		}
	}
	var ids []string
	for j := range 48 {
		copied := string(page)
		for n, id := range own {
			copied = strings.ReplaceAll(copied, id, fmt.Sprintf("01a0c450-6c00-7%03x-8000-%012x", 0x100+j, n+1))
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(copied), &doc); err != nil {
			t.Fatal(err)
		}
		doc["key"] = fmt.Sprintf("result-%d", j)
		data, _ := json.Marshal(doc)
		files = append(files, document.File{Path: fmt.Sprintf("plugins/loans/pages/result-%d.page.json", j), Content: data})
		ids = append(ids, doc["id"].(string))
	}
	for i, f := range files {
		if f.Path == "plugins/loans/plugin.json" {
			var plugin map[string]any
			if err := json.Unmarshal(f.Content, &plugin); err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				plugin["pages"] = append(plugin["pages"].([]any), id)
			}
			files[i].Content, _ = json.Marshal(plugin)
		}
	}
	return files
}

// Verifies: SRV-053, NFR-021.
// A publish of a 50-page plugin, with the deltas from its last ten
// versions, completes within 15 s: timed from the request to the last
// delta stored, over five publishes, of which the slowest must meet the
// budget (so the p95 does). Results are recorded in
// docs/benchmarks/p2-backend.md.
func TestPublishFiftyPagePluginWithDeltas(t *testing.T) {
	if testing.Short() {
		t.Skip("a benchmark")
	}
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.docs.Import(ctx, f.owner, f.app, "", "bench", fiftyPages(t)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	f.publish(t, "", false)
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "bench", false); err != nil {
		t.Fatal(err)
	}
	edit := func(n int) {
		path := fmt.Sprintf("plugins/loans/pages/result-%d.page.json", n%48)
		doc, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, path)
		if err != nil {
			t.Fatal(err)
		}
		changed := bytes.Replace(doc.Content, []byte(`string(params.schedule.monthlyPayment)`), []byte(fmt.Sprintf(`string(params.schedule.monthlyPayment) + \" (%d)\"`, n)), 1)
		if bytes.Equal(changed, doc.Content) {
			changed = bytes.Replace(doc.Content, []byte(fmt.Sprintf(`(%d)`, n-48)), []byte(fmt.Sprintf(`(%d)`, n)), 1)
		}
		if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "bench", path, changed, doc.Revision); err != nil {
			t.Fatalf("PutDocument: %v", err)
		}
	}
	publish := func() release.PublishJob {
		j := f.publish(t, f.loans, true)
		if j.State != release.StateSucceeded {
			t.Fatalf("publish: %+v", j)
		}
		return j
	}
	publish()
	for n := range 10 {
		edit(n)
		publish()
	}
	var took []time.Duration
	for n := 10; n < 15; n++ {
		edit(n)
		start := time.Now()
		j := publish() // f.publish runs the publish job and the delta job it enqueues
		took = append(took, time.Since(start))
		if j.Version != int64(n+2) {
			t.Fatalf("version %d, want %d", j.Version, n+2)
		}
	}
	var deltas int
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM deltas d JOIN plugin_versions v ON v.bundle_sha256 = d.to_sha256
			WHERE v.plugin_key = 'loans' AND v.version = (SELECT max(version) FROM plugin_versions WHERE plugin_key = 'loans')`).Scan(&deltas)
	}); err != nil {
		t.Fatal(err)
	}
	if deltas < release.RecentDeltas {
		t.Errorf("the last publish stored %d deltas, want at least %d", deltas, release.RecentDeltas)
	}
	slices.Sort(took)
	t.Logf("SRV-053: 50-page plugin, publish with deltas from the last 10 versions: min %v, median %v, max %v", took[0], took[2], took[4])
	if took[4] > 15*time.Second {
		t.Errorf("the slowest publish took %v, more than 15 s", took[4])
	}
}
