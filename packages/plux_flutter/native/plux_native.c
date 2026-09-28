// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The native part of the Plux runtime (ADR-0030): read-only memory maps of
// verified bundle files, and one-shot zstd decompression with an optional
// raw-content dictionary for section deltas (ADR-0003). The functions keep
// no state, take bytes and return bytes; every bound is checked in Dart
// before a call, and again here.

#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include "zstd/zstd.h"

#define PLUX_EXPORT __attribute__((visibility("default"))) __attribute__((used))

// A mapped file; Dart reads the fields and passes the handle back to
// plux_release, directly or through a NativeFinalizer.
typedef struct {
  void* address;
  uint64_t length;
  int32_t error;
} plux_mapping;

// Maps the file at the NUL-terminated path read-only. Returns a handle,
// or NULL when out of memory. On failure the handle's error is the errno
// value and its address is NULL; an empty file maps to a NULL address with
// length zero and no error.
PLUX_EXPORT plux_mapping* plux_map(const char* path) {
  plux_mapping* m = (plux_mapping*)calloc(1, sizeof(plux_mapping));
  if (m == NULL) return NULL;
  int fd = open(path, O_RDONLY | O_CLOEXEC);
  if (fd < 0) {
    m->error = errno;
    return m;
  }
  struct stat st;
  if (fstat(fd, &st) != 0) {
    m->error = errno;
    close(fd);
    return m;
  }
  if (!S_ISREG(st.st_mode)) {
    m->error = EINVAL;
    close(fd);
    return m;
  }
  if (st.st_size > 0) {
    void* a = mmap(NULL, (size_t)st.st_size, PROT_READ, MAP_PRIVATE, fd, 0);
    if (a == MAP_FAILED) {
      m->error = errno;
    } else {
      m->address = a;
      m->length = (uint64_t)st.st_size;
    }
  }
  close(fd);
  return m;
}

// Unmaps a mapping and frees its handle. Safe to call with NULL.
PLUX_EXPORT void plux_release(plux_mapping* m) {
  if (m == NULL) return;
  if (m->address != NULL) munmap(m->address, (size_t)m->length);
  free(m);
}

// Error results of the zstd functions; zstd's own error codes are
// reported negated below these.
enum {
  PLUX_ZSTD_UNKNOWN_SIZE = -1,
  PLUX_ZSTD_BAD_FRAME = -2,
  PLUX_ZSTD_NO_MEMORY = -3,
};

// Returns the content size a zstd frame declares, PLUX_ZSTD_UNKNOWN_SIZE
// when it declares none, or PLUX_ZSTD_BAD_FRAME when the header is not a
// zstd frame header.
PLUX_EXPORT int64_t plux_zstd_content_size(const uint8_t* src, uint64_t length) {
  unsigned long long n = ZSTD_getFrameContentSize(src, (size_t)length);
  if (n == ZSTD_CONTENTSIZE_UNKNOWN) return PLUX_ZSTD_UNKNOWN_SIZE;
  if (n == ZSTD_CONTENTSIZE_ERROR || n > (unsigned long long)INT64_MAX) return PLUX_ZSTD_BAD_FRAME;
  return (int64_t)n;
}

// Decodes exactly one zstd frame from src into dst, which holds capacity
// bytes, with dict as a raw-content dictionary when dict_length is not
// zero (zstd's --patch-from). Returns the number of bytes written, or a
// negative error: the frame is invalid, its checksum fails, it has
// trailing data, or it decodes to more than capacity.
PLUX_EXPORT int64_t plux_zstd_decompress(uint8_t* dst, uint64_t capacity, const uint8_t* src,
                                         uint64_t length, const uint8_t* dict,
                                         uint64_t dict_length) {
  if (length == 0) return PLUX_ZSTD_BAD_FRAME;
  unsigned long long frame = ZSTD_findFrameCompressedSize(src, (size_t)length);
  if (ZSTD_isError(frame) || frame != length) return PLUX_ZSTD_BAD_FRAME;
  ZSTD_DCtx* ctx = ZSTD_createDCtx();
  if (ctx == NULL) return PLUX_ZSTD_NO_MEMORY;
  size_t r = 0;
  if (dict_length > 0) r = ZSTD_DCtx_refPrefix(ctx, dict, (size_t)dict_length);
  if (!ZSTD_isError(r)) r = ZSTD_decompressDCtx(ctx, dst, (size_t)capacity, src, (size_t)length);
  ZSTD_freeDCtx(ctx);
  if (ZSTD_isError(r)) return PLUX_ZSTD_NO_MEMORY - (int64_t)ZSTD_getErrorCode(r);
  return (int64_t)r;
}
