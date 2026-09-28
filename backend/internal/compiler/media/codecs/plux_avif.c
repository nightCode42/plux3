// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The AVIF module's interface: allocation and encoding RGBA pixels.
// Built by build.sh against libavif and libaom.

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "avif/avif.h"

__attribute__((export_name("plux_alloc"))) void* plux_alloc(size_t n) { return malloc(n); }
__attribute__((export_name("plux_free"))) void plux_free(void* p) { free(p); }

// plux_avif_encode encodes RGBA pixels at a quality of 0 to 100 and a
// speed of 0 (slowest) to 10. It returns the size and stores a buffer
// the caller frees with plux_free in *out; 0 on failure.
__attribute__((export_name("plux_avif_encode")))
size_t plux_avif_encode(const uint8_t* rgba, int width, int height, int quality, int speed, uint8_t** out) {
  size_t size = 0;
  avifImage* image = avifImageCreate(width, height, 8, AVIF_PIXEL_FORMAT_YUV420);
  avifEncoder* encoder = avifEncoderCreate();
  avifRWData data = AVIF_DATA_EMPTY;
  avifRGBImage rgb;
  if (!image || !encoder) goto done;
  image->yuvRange = AVIF_RANGE_FULL;
  avifRGBImageSetDefaults(&rgb, image);
  rgb.format = AVIF_RGB_FORMAT_RGBA;
  rgb.depth = 8;
  rgb.pixels = (uint8_t*)rgba;
  rgb.rowBytes = (uint32_t)width * 4;
  if (avifImageRGBToYUV(image, &rgb) != AVIF_RESULT_OK) goto done;
  encoder->maxThreads = 1;
  encoder->quality = quality;
  encoder->qualityAlpha = quality;
  encoder->speed = speed;
  if (avifEncoderWrite(encoder, image, &data) != AVIF_RESULT_OK) goto done;
  *out = malloc(data.size);
  if (!*out) goto done;
  memcpy(*out, data.data, data.size);
  size = data.size;
done:
  avifRWDataFree(&data);
  if (encoder) avifEncoderDestroy(encoder);
  if (image) avifImageDestroy(image);
  return size;
}
