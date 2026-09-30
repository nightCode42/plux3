// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
)

// Verifies: ANL-003.
// The app document's telemetry sampling rates reach the signed app
// bundle in thousandths, sorted by event; plugin bundles carry none.
func TestTelemetrySamplingInTheAppBundle(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.AssetVariants = galleryVariants(t)
	res := Compile(os.DirFS(widgetsDir), opts)
	bundles := readAll(t, res)
	rates := func(b *bundle.Bundle) map[string]uint32 {
		out := map[string]uint32{}
		for _, s := range b.Sections {
			if s.Kind != bundle.SectionMeta {
				continue
			}
			m := fbs.GetRootAsMeta(s.Data, 0)
			var e fbs.Sampling
			for i := range m.TelemetrySamplingLength() {
				if m.TelemetrySampling(&e, i) {
					out[string(e.Event())] = e.Rate()
				}
			}
		}
		return out
	}
	if got := rates(bundles[0]); len(got) != 2 || got["render_perf"] != 250 || got["screen_view"] != 500 {
		t.Errorf("the app bundle's sampling: %v", got)
	}
	for _, b := range bundles[1:] {
		if got := rates(b); len(got) != 0 {
			t.Errorf("a plugin bundle has sampling: %v", got)
		}
	}
}
