// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The WebP module's interface: allocation, encoding RGBA pixels, and
// decoding a WebP file to RGBA. Built by build.sh against libwebp.

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include "webp/decode.h"
#include "webp/encode.h"

__attribute__((export_name("plux_alloc"))) void* plux_alloc(size_t n) { return malloc(n); }
__attribute__((export_name("plux_free"))) void plux_free(void* p) { free(p); }
__attribute__((export_name("plux_webp_free"))) void plux_webp_free(void* p) { WebPFree(p); }

// plux_webp_encode encodes RGBA pixels; a negative quality means
// lossless. It returns the size and stores the buffer, which the caller
// frees with plux_webp_free, in *out; 0 on failure.
__attribute__((export_name("plux_webp_encode")))
size_t plux_webp_encode(const uint8_t* rgba, int width, int height, float quality, uint8_t** out) {
  if (quality < 0) return WebPEncodeLosslessRGBA(rgba, width, height, width * 4, out);
  return WebPEncodeRGBA(rgba, width, height, width * 4, quality, out);
}

// plux_webp_decode decodes a WebP file to RGBA; the caller frees the
// pixels with plux_webp_free. It returns 0 on failure.
__attribute__((export_name("plux_webp_decode")))
uint8_t* plux_webp_decode(const uint8_t* data, size_t size, int* width, int* height) {
  return WebPDecodeRGBA(data, size, width, height);
}
