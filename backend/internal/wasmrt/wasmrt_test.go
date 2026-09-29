// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package wasmrt

import (
	"context"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero"
)

// Runtimes created at the same moment do not race; `go test -race` (make
// go-cover in CI) fails this test without the warm-up.
func TestNewRuntimeConcurrently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			rt := NewRuntime(ctx, wazero.NewRuntimeConfig())
			if err := rt.Close(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
