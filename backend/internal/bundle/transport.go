// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"github.com/klauspost/compress/zstd"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Compress encodes a bundle for transport as one zstd frame that declares
// its content size (BND-007). The output depends only on the input.
func Compress(data []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
		zstd.WithZeroFrames(true))
	if err != nil {
		return nil, wrapErr(plxerr.TransportDecodingFailed, err, "zstd encoder")
	}
	defer func() { _ = enc.Close() }()
	return enc.EncodeAll(data, nil), nil
}

// Decompress decodes a transport-encoded bundle. The frame must declare
// its content size, which must not exceed maxSize; the decoder never
// allocates more, so a compression bomb is rejected before allocation.
func Decompress(data []byte, maxSize int64) ([]byte, error) {
	var h zstd.Header
	if err := h.Decode(data); err != nil {
		return nil, wrapErr(plxerr.TransportDecodingFailed, err, "zstd frame header")
	}
	if !h.HasFCS || maxSize < 0 || h.FrameContentSize > uint64(maxSize) {
		return nil, newErr(plxerr.TransportDecodingFailed, "the frame declares no size or more than %d bytes", maxSize)
	}
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(uint64(maxSize)),
		zstd.WithDecodeAllCapLimit(true))
	if err != nil {
		return nil, wrapErr(plxerr.TransportDecodingFailed, err, "zstd decoder")
	}
	defer dec.Close()
	out, err := dec.DecodeAll(data, make([]byte, 0, h.FrameContentSize))
	if err != nil {
		return nil, wrapErr(plxerr.TransportDecodingFailed, err, "zstd")
	}
	if uint64(len(out)) != h.FrameContentSize {
		return nil, newErr(plxerr.TransportDecodingFailed, "decoded %d bytes, the frame declares %d", len(out), h.FrameContentSize)
	}
	return out, nil
}
