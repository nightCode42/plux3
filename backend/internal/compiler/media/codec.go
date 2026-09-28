// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"context"
	_ "embed" // the codec modules
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// The codecs are libwebp and libavif with libaom compiled to WebAssembly
// by codecs/build.sh (ADR-0027). They run on wazero, so the server and
// the CLI stay free of cgo and reproducible (CI-006), and a codec bug is
// confined to the module's own memory. Each call instantiates a fresh
// module: nothing one image leaves behind reaches the next, and calls may
// run concurrently. The modules see no files, no environment and a clock
// that does not move, so their output depends on their input alone.

//go:embed codecs/webp.wasm
var webpModule []byte

//go:embed codecs/avif.wasm
var avifModule []byte

// maxMemoryPages bounds each module's memory: 1 GiB of 64 KiB pages,
// room for the largest image asset.imagePixels allows at its hard
// maximum while encoding.
const maxMemoryPages = 16384

// Codecs runs the WebAssembly image codecs. It is safe for concurrent
// use; Close releases it.
type Codecs struct {
	rt   wazero.Runtime
	webp wazero.CompiledModule
	avif wazero.CompiledModule
}

// NewCodecs compiles the codec modules.
func NewCodecs(ctx context.Context) (*Codecs, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(maxMemoryPages).WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("media: %w", err)
	}
	webp, err := rt.CompileModule(ctx, webpModule)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("media: compile the WebP codec: %w", err)
	}
	avif, err := rt.CompileModule(ctx, avifModule)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("media: compile the AVIF codec: %w", err)
	}
	return &Codecs{rt: rt, webp: webp, avif: avif}, nil
}

// Close releases the runtime.
func (c *Codecs) Close(ctx context.Context) error {
	if err := c.rt.Close(ctx); err != nil {
		return fmt.Errorf("media: %w", err)
	}
	return nil
}

// errCodec is returned when a codec refuses or fails on an input.
var errCodec = errors.New("media: the codec failed")

// instance is one module instance for one call.
type instance struct {
	mod api.Module
	ctx context.Context
}

// instantiate starts a fresh instance of a codec module.
func (c *Codecs) instantiate(ctx context.Context, m wazero.CompiledModule) (*instance, error) {
	mod, err := c.rt.InstantiateModule(ctx, m, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize")) //nolint:misspell // the WASI reactor's entry point
	if err != nil {
		return nil, fmt.Errorf("media: start a codec: %w", err)
	}
	return &instance{mod: mod, ctx: ctx}, nil
}

func (i *instance) close() { _ = i.mod.Close(i.ctx) }

// call runs an exported function and returns its single result.
func (i *instance) call(name string, args ...uint64) (uint64, error) {
	f := i.mod.ExportedFunction(name)
	if f == nil {
		return 0, fmt.Errorf("%w: the module has no %s", errCodec, name)
	}
	out, err := f.Call(i.ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", errCodec, name, err)
	}
	if len(out) == 0 {
		return 0, nil
	}
	return out[0], nil
}

// put copies bytes into the module's memory and returns their address.
func (i *instance) put(b []byte) (uint32, error) {
	p, err := i.call("plux_alloc", uint64(len(b)))
	if err != nil || p == 0 {
		return 0, fmt.Errorf("%w: out of memory", errCodec)
	}
	ptr := uint32(p) //nolint:gosec // a wasm32 address
	if !i.mod.Memory().Write(ptr, b) {
		return 0, fmt.Errorf("%w: write out of range", errCodec)
	}
	return ptr, nil
}

// get copies n bytes out of the module's memory.
func (i *instance) get(ptr, n uint32) ([]byte, error) {
	b, ok := i.mod.Memory().Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("%w: read out of range", errCodec)
	}
	return append([]byte(nil), b...), nil
}

// u32 reads a little-endian 32-bit value from the module's memory.
func (i *instance) u32(ptr uint32) (uint32, error) {
	v, ok := i.mod.Memory().ReadUint32Le(ptr)
	if !ok {
		return 0, fmt.Errorf("%w: read out of range", errCodec)
	}
	return v, nil
}

// encodeWebP encodes RGBA pixels as WebP; a negative quality is lossless.
func (c *Codecs) encodeWebP(ctx context.Context, px *RGBA, quality float32) ([]byte, error) {
	in, err := c.instantiate(ctx, c.webp)
	if err != nil {
		return nil, err
	}
	defer in.close()
	pixels, err := in.put(px.Pix)
	if err != nil {
		return nil, err
	}
	outPtr, err := in.put(make([]byte, 4))
	if err != nil {
		return nil, err
	}
	n, err := in.call("plux_webp_encode", uint64(pixels), uint64(px.W), uint64(px.H), api.EncodeF32(quality), uint64(outPtr)) //nolint:gosec // positive sizes
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: WebP encoding produced nothing", errCodec)
	}
	buf, err := in.u32(outPtr)
	if err != nil {
		return nil, err
	}
	return in.get(buf, uint32(n)) //nolint:gosec // a wasm32 size
}

// decodeWebP decodes a WebP file to RGBA pixels.
func (c *Codecs) decodeWebP(ctx context.Context, data []byte) (*RGBA, error) {
	in, err := c.instantiate(ctx, c.webp)
	if err != nil {
		return nil, err
	}
	defer in.close()
	src, err := in.put(data)
	if err != nil {
		return nil, err
	}
	dims, err := in.put(make([]byte, 8))
	if err != nil {
		return nil, err
	}
	p, err := in.call("plux_webp_decode", uint64(src), uint64(len(data)), uint64(dims), uint64(dims+4))
	if err != nil {
		return nil, err
	}
	if p == 0 {
		return nil, fmt.Errorf("%w: not a WebP image", errCodec)
	}
	w, err := in.u32(dims)
	if err != nil {
		return nil, err
	}
	h, err := in.u32(dims + 4)
	if err != nil {
		return nil, err
	}
	pix, err := in.get(uint32(p), w*h*4) //nolint:gosec // a wasm32 address
	if err != nil {
		return nil, err
	}
	return &RGBA{Pix: pix, W: int(w), H: int(h)}, nil
}

// encodeAVIF encodes RGBA pixels as AVIF at a quality of 0 to 100.
func (c *Codecs) encodeAVIF(ctx context.Context, px *RGBA, quality, speed int) ([]byte, error) {
	in, err := c.instantiate(ctx, c.avif)
	if err != nil {
		return nil, err
	}
	defer in.close()
	pixels, err := in.put(px.Pix)
	if err != nil {
		return nil, err
	}
	outPtr, err := in.put(make([]byte, 4))
	if err != nil {
		return nil, err
	}
	n, err := in.call("plux_avif_encode", uint64(pixels), uint64(px.W), uint64(px.H), uint64(quality), uint64(speed), uint64(outPtr)) //nolint:gosec // positive sizes and settings
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: AVIF encoding produced nothing", errCodec)
	}
	buf, err := in.u32(outPtr)
	if err != nil {
		return nil, err
	}
	return in.get(buf, uint32(n)) //nolint:gosec // a wasm32 size
}
