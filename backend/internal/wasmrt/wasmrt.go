// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package wasmrt creates the wazero runtimes Plux runs WebAssembly on: the
// image codecs now, Plux Functions from P7. Every runtime is created here.
//
// wazero 1.12.0 reads its own module version from the build information
// the first time a runtime is created and caches it in a package variable
// without synchronisation (internal/version.GetWazeroVersion), so runtimes
// first created at the same moment race on it. NewRuntime creates one
// throwaway runtime, once per process, before any other; after that the
// cache is only read. Remove the warm-up when wazero synchronises it.
package wasmrt

import (
	"context"
	"sync"

	"github.com/tetratelabs/wazero"
)

// warmUp fills wazero's version cache exactly once.
var warmUp = sync.OnceFunc(func() {
	ctx := context.Background()
	_ = wazero.NewRuntime(ctx).Close(ctx) // closing an empty runtime cannot fail
})

// NewRuntime returns a runtime with the given configuration; it is safe
// to call from several goroutines at once.
func NewRuntime(ctx context.Context, cfg wazero.RuntimeConfig) wazero.Runtime {
	warmUp()
	return wazero.NewRuntimeWithConfig(ctx, cfg)
}
